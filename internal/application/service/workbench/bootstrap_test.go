package workbench

import (
	"context"
	"errors"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bootstrapRepo 只实现 Bootstrap 需要的三个计数方法与未读通知数。
type bootstrapRepo struct {
	interfaces.WorkbenchRepository

	items   int64
	folders int64
	parts   int64
	unread  int
	err     error
}

func (b *bootstrapRepo) CountItems(context.Context, uint64, string) (int64, error) {
	return b.items, b.err
}

func (b *bootstrapRepo) CountFolders(context.Context, uint64, string) (int64, error) {
	return b.folders, b.err
}

func (b *bootstrapRepo) CountArchiveParts(context.Context, uint64, string) (int64, error) {
	return b.parts, b.err
}

// CountUnread 未读通知数；Bootstrap 会读它来填 unread_notices。
func (b *bootstrapRepo) CountUnread(context.Context, string) (int, error) {
	return b.unread, b.err
}

// bootstrapKB 只提供 EnsureWorkKnowledgeBase，用来验证 Bootstrap 的 KB 投影。
type bootstrapKB struct {
	kb  *types.KnowledgeBase
	err error
}

func (b bootstrapKB) EnsureWorkKnowledgeBase(
	context.Context, uint64, string,
) (*types.KnowledgeBase, error) {
	return b.kb, b.err
}

func newBootstrapKB() *types.KnowledgeBase {
	kb := &types.KnowledgeBase{
		ID: "kb-work", TenantID: 1, CreatorID: "user-a",
		Name: types.KBNameWork, IsSystem: true, SystemKey: types.KBSystemKeyWork,
		Visibility: types.KBVisibilityPrivate, EmbeddingModelID: "model-1",
	}
	kb.IndexingStrategy = types.IndexingStrategy{KeywordEnabled: true, VectorEnabled: true}
	return kb
}

// Bootstrap 返回工作 KB 与三类计数，配额取常量上限。
func TestBootstrapAggregatesCounts(t *testing.T) {
	svc := NewService(&bootstrapRepo{items: 4, folders: 2, parts: 3, unread: 2}).
		WithArchiveServices(bootstrapKB{kb: newBootstrapKB()}, nil)

	result, err := svc.Bootstrap(newTestContext())
	require.NoError(t, err)

	assert.Equal(t, "kb-work", result.WorkKB.ID)
	assert.Equal(t, types.KBNameWork, result.WorkKB.Name)
	assert.True(t, result.WorkKB.IsSystem)
	assert.True(t, result.WorkKB.Capabilities.Vector)
	assert.True(t, result.WorkKB.Capabilities.Keyword)
	assert.Equal(t, int64(4), result.Counts.Items)
	assert.Equal(t, int64(2), result.Counts.Folders)
	assert.Equal(t, int64(3), result.Counts.ArchiveParts)
	assert.Equal(t, 2, result.UnreadNotices, "未读通知数来自 CountUnread")
	assert.Equal(t, int64(types.WorkbenchMaxYDocBytes), result.Limits.ItemContentBytes)
	assert.Equal(t, types.WorkbenchMaxArchiveParts, result.Limits.ArchiveParts)
	assert.False(t, result.NeedsSemanticIndex, "已有向量模型时不需要建议语义索引")
	assert.False(t, result.ServerTime.IsZero())
}

// 章节文档数量来自注入的 docTasks；未注入时保持 0 且不让首屏失败。
func TestBootstrapCountsDocumentsWhenDocTasksInjected(t *testing.T) {
	svc := NewService(&bootstrapRepo{}).
		WithArchiveServices(bootstrapKB{kb: newBootstrapKB()}, nil).
		WithDocTasks(fakeDocTasks{count: 5})

	result, err := svc.Bootstrap(newTestContext())
	require.NoError(t, err)
	assert.Equal(t, int64(5), result.Counts.Documents)

	// 未注入时按 0 处理。
	plain, err := NewService(&bootstrapRepo{}).
		WithArchiveServices(bootstrapKB{kb: newBootstrapKB()}, nil).
		Bootstrap(newTestContext())
	require.NoError(t, err)
	assert.Zero(t, plain.Counts.Documents)
}

// 发布门禁随 bootstrap 下发；未注入时全部为 false，前端据此隐藏入口（§9.1）。
func TestBootstrapCarriesFeatureToggles(t *testing.T) {
	svc := NewService(&bootstrapRepo{}).
		WithArchiveServices(bootstrapKB{kb: newBootstrapKB()}, nil).
		WithFeatures(types.WorkbenchFeatureToggles{
			Templates: true, Journals: true, AI: true, DualMode: false,
		})

	result, err := svc.Bootstrap(newTestContext())
	require.NoError(t, err)
	assert.True(t, result.Features.Templates)
	assert.True(t, result.Features.Journals)
	assert.True(t, result.Features.AI)
	assert.False(t, result.Features.DualMode)

	// 未注入时是零值：发布门禁默认关闭。
	plain, err := NewService(&bootstrapRepo{}).
		WithArchiveServices(bootstrapKB{kb: newBootstrapKB()}, nil).
		Bootstrap(newTestContext())
	require.NoError(t, err)
	assert.False(t, plain.Features.Templates, "未注入时模板必须视为关闭")
	assert.False(t, plain.Features.AI)
}

// fakeDocTasks 只提供 ListTasks。
type fakeDocTasks struct {
	count int
	err   error
}

func (f fakeDocTasks) ListTasks(context.Context) ([]*types.DocGenerationTask, error) {
	if f.err != nil {
		return nil, f.err
	}
	rows := make([]*types.DocGenerationTask, f.count)
	for i := range rows {
		rows[i] = &types.DocGenerationTask{ID: "task"}
	}
	return rows, nil
}

// 只有关键词索引时提示需要语义索引。
func TestBootstrapSuggestsSemanticIndexWithoutVector(t *testing.T) {
	kb := newBootstrapKB()
	kb.IndexingStrategy = types.IndexingStrategy{KeywordEnabled: true, VectorEnabled: false}

	svc := NewService(&bootstrapRepo{}).WithArchiveServices(bootstrapKB{kb: kb}, nil)
	result, err := svc.Bootstrap(newTestContext())
	require.NoError(t, err)
	assert.True(t, result.NeedsSemanticIndex)
}

// 没注入 KB 服务时返回 503，而不是 panic。
func TestBootstrapWithoutKBServiceFails(t *testing.T) {
	svc := NewService(&bootstrapRepo{})
	_, err := svc.Bootstrap(newTestContext())
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok, "必须是 AppError，got %v", err)
	assert.Equal(t, int(apperrors.ErrServiceUnavailable), int(appErr.Code))
}

// 计数失败时把错误原样抛出，不返回半份数据。
func TestBootstrapPropagatesCountError(t *testing.T) {
	svc := NewService(&bootstrapRepo{err: errors.New("db down")}).
		WithArchiveServices(bootstrapKB{kb: newBootstrapKB()}, nil)

	_, err := svc.Bootstrap(newTestContext())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

// 未登录（无 caller）时返回 401，不查库。
func TestBootstrapRequiresCaller(t *testing.T) {
	svc := NewService(&bootstrapRepo{}).WithArchiveServices(bootstrapKB{kb: newBootstrapKB()}, nil)
	_, err := svc.Bootstrap(context.Background())
	require.Error(t, err)
}

// 合成用户（API Key）一律拒绝。
func TestBootstrapRejectsSyntheticUser(t *testing.T) {
	ctx := types.WithCaller(context.Background(), types.Caller{
		TenantID: 1, UserID: "system-1", Role: types.TenantRoleOwner,
	})
	svc := NewService(&bootstrapRepo{}).WithArchiveServices(bootstrapKB{kb: newBootstrapKB()}, nil)
	_, err := svc.Bootstrap(ctx)
	require.Error(t, err)
}
