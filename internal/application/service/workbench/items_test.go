package workbench

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// stubWorkbenchRepo 用内存状态模拟仓储的乐观锁语义，只覆盖服务层需要的行为。
// 内嵌组合接口，未实现的方法保持 nil（调用会 panic，正好暴露越界使用）。
type stubWorkbenchRepo struct {
	interfaces.WorkbenchRepository

	item    *types.WorkbenchItem
	missing bool
	// versions 记录本次测试里写入的检查点，供 shouldCheckpoint 断言使用。
	versions []*types.WorkbenchItemVersion
}

func (s *stubWorkbenchRepo) UpdateItemContent(
	_ context.Context, _ uint64, _ string, _ string,
	baseVersion int, force bool, ydoc []byte, text string, schemaVersion int,
) (int, time.Time, error) {
	if s.missing {
		return 0, time.Time{}, gorm.ErrRecordNotFound
	}
	if !force && s.item.SnapshotVersion != baseVersion {
		return s.item.SnapshotVersion, s.item.UpdatedAt, types.ErrWorkbenchVersionConflict
	}
	s.item.SnapshotVersion++
	s.item.YDoc = ydoc
	s.item.TextContent = text
	s.item.EditorSchemaVersion = schemaVersion
	s.item.UpdatedAt = time.Now().UTC()
	return s.item.SnapshotVersion, s.item.UpdatedAt, nil
}

func (s *stubWorkbenchRepo) GetItem(
	_ context.Context, _ uint64, _ string, _ string,
) (*types.WorkbenchItem, error) {
	if s.missing {
		return nil, gorm.ErrRecordNotFound
	}
	return s.item, nil
}

// InsertVersion 记录检查点调用，供断言“首版/显式 checkpoint 是否写了版本行”。
func (s *stubWorkbenchRepo) InsertVersion(_ context.Context, v *types.WorkbenchItemVersion) error {
	s.versions = append(s.versions, v)
	return nil
}

// ListVersions 按版本倒序返回已记录的检查点；shouldCheckpoint 用它判断 10 分钟间隔。
func (s *stubWorkbenchRepo) ListVersions(
	_ context.Context, _ uint64, _ string, _ string, limit int,
) ([]*types.WorkbenchVersionSummary, error) {
	out := make([]*types.WorkbenchVersionSummary, 0, len(s.versions))
	for i := len(s.versions) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, &types.WorkbenchVersionSummary{
			Version:   s.versions[i].Version,
			CreatedAt: s.versions[i].CreatedAt,
		})
	}
	return out, nil
}

// ListItemIDsMentioning / ReplaceItemLinks 是保存后重算反向链接的派生写入。
// 这里给内存仓储补上空实现：反向链接不是保存链路的断言对象，
// 缺方法会让「保存」相关用例在内嵌 nil 接口上 panic。
func (s *stubWorkbenchRepo) ListItemIDsMentioning(
	_ context.Context, _ uint64, _, _, _ string, _ int,
) ([]string, error) {
	return nil, nil
}

func (s *stubWorkbenchRepo) ReplaceItemLinks(
	_ context.Context, _ uint64, _, _ string, _ []types.WorkbenchLinkTarget,
) error {
	return nil
}

func newTestContext() context.Context {
	return types.WithCaller(context.Background(), types.Caller{
		TenantID: 1,
		UserID:   "user-a",
		Role:     types.TenantRoleOwner,
	})
}

func newStubRepo() *stubWorkbenchRepo {
	return &stubWorkbenchRepo{item: &types.WorkbenchItem{
		ID:                  "item-1",
		TenantID:            1,
		OwnerID:             "user-a",
		Kind:                types.WorkbenchKindCanvas,
		Title:               "t",
		SnapshotVersion:     1,
		EditorSchemaVersion: 1,
		Visibility:          types.WorkbenchVisibilityPrivate,
		UpdatedAt:           time.Now().UTC(),
	}}
}

func validSaveRequest(baseVersion int) types.SaveWorkbenchContentRequest {
	return types.SaveWorkbenchContentRequest{
		BaseVersion:         baseVersion,
		EditorSchemaVersion: 1,
		YDoc:                "AAECAw==", // 合法 base64
		TextContent:         "hello",
		ClientID:            "tab-a",
	}
}

// 服务层必须把仓储的版本冲突翻译成 2401，并在 details 里带上 current_version。
func TestWorkbenchSaveContent_MapsConflictTo2401(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	// 仓储当前 version=1，用过一个明显过期的基线保存 → 冲突。
	_, err := svc.SaveContent(newTestContext(), "item-1", validSaveRequest(7))
	require.Error(t, err)
	appErr, ok := err.(*apperrors.AppError)
	require.True(t, ok, "服务层必须返回 AppError，got %T", err)
	assert.Equal(t, 2401, int(appErr.Code))
	assert.Equal(t, 409, appErr.HTTPCode)

	details, ok := appErr.Details.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 1, details["current_version"], "details 必须带当前版本供前端恢复")
}

// 冲突后前端拿 current_version 重试必须成功 —— 409 可恢复在服务层的落点。
func TestWorkbenchSaveContent_RetryWithCurrentVersionSucceeds(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	ctx := newTestContext()

	_, err := svc.SaveContent(ctx, "item-1", validSaveRequest(7))
	require.Error(t, err)

	result, err := svc.SaveContent(ctx, "item-1", validSaveRequest(repo.item.SnapshotVersion))
	require.NoError(t, err)
	assert.Equal(t, 2, result.SnapshotVersion)
}

// 条目不存在/越权 → 2400；不能退化成 500 或 409。
func TestWorkbenchSaveContent_MissingItemIsNotFound(t *testing.T) {
	repo := newStubRepo()
	repo.missing = true
	svc := NewService(repo)

	_, err := svc.SaveContent(newTestContext(), "item-1", validSaveRequest(1))
	require.Error(t, err)
	appErr, ok := err.(*apperrors.AppError)
	require.True(t, ok)
	assert.Equal(t, 2400, int(appErr.Code))
	assert.Equal(t, 404, appErr.HTTPCode)
}

// 体积超限 → 2402；schema 越界 → 2409。
func TestWorkbenchSaveContent_SizeAndSchemaGuards(t *testing.T) {
	svc := NewService(newStubRepo())
	ctx := newTestContext()

	t.Run("ydoc too large", func(t *testing.T) {
		req := validSaveRequest(1)
		// 构造一份解码后刚好超出 8MB 的合法 base64，验证按解码字节数判定。
		req.YDoc = base64.StdEncoding.EncodeToString(make([]byte, types.WorkbenchMaxYDocBytes+1))
		_, err := svc.SaveContent(ctx, "item-1", req)
		appErr, ok := err.(*apperrors.AppError)
		require.True(t, ok)
		assert.Equal(t, 2402, int(appErr.Code))
		assert.Equal(t, 413, appErr.HTTPCode)
	})

	t.Run("schema version out of range", func(t *testing.T) {
		req := validSaveRequest(1)
		// 边界值：高出一档就必须 2409，而不是只挡住明显伪造的大数。
		req.EditorSchemaVersion = types.SupportedEditorSchemaVersion + 1
		_, err := svc.SaveContent(ctx, "item-1", req)
		appErr, ok := err.(*apperrors.AppError)
		require.True(t, ok)
		assert.Equal(t, 2409, int(appErr.Code))
		assert.Equal(t, 422, appErr.HTTPCode)
	})

	t.Run("invalid base64", func(t *testing.T) {
		req := validSaveRequest(1)
		req.YDoc = "not-base64!!"
		_, err := svc.SaveContent(ctx, "item-1", req)
		appErr, ok := err.(*apperrors.AppError)
		require.True(t, ok)
		assert.Equal(t, 2409, int(appErr.Code))
	})
}

// 缺身份上下文必须是 401 语义，不能静默按 tenant 0 查询。
func TestWorkbenchSaveContent_RequiresCallerScope(t *testing.T) {
	svc := NewService(newStubRepo())
	_, err := svc.SaveContent(context.Background(), "item-1", validSaveRequest(1))
	require.Error(t, err)
	appErr, ok := err.(*apperrors.AppError)
	require.True(t, ok)
	assert.Equal(t, 1001, int(appErr.Code), "缺失身份应是 Unauthorized")
}

// 读取内容时 YDoc 以 base64 返回，与保存入参同构。
func TestWorkbenchGetContent_ReturnsBase64YDoc(t *testing.T) {
	repo := newStubRepo()
	repo.item.YDoc = []byte{1, 2, 3}
	repo.item.TextContent = "hi"
	svc := NewService(repo)

	content, err := svc.GetContent(newTestContext(), "item-1")
	require.NoError(t, err)
	assert.Equal(t, "AQID", content.YDoc)
	assert.Equal(t, "hi", content.TextContent)
	assert.Equal(t, 1, content.SnapshotVersion)
}

// 仓储返回非业务错误时不得被吞成 2401。
func TestWorkbenchSaveContent_UnmappedErrorStaysRaw(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(&failingRepo{stubWorkbenchRepo: repo, err: errors.New("db down")})

	_, err := svc.SaveContent(newTestContext(), "item-1", validSaveRequest(1))
	require.Error(t, err)
	var appErr *apperrors.AppError
	assert.False(t, errors.As(err, &appErr), "未知错误应原样上抛，交给 handler 记 500")
}

type failingRepo struct {
	*stubWorkbenchRepo
	err error
}

func (f *failingRepo) UpdateItemContent(
	context.Context, uint64, string, string, int, bool, []byte, string, int,
) (int, time.Time, error) {
	return 0, time.Time{}, f.err
}
