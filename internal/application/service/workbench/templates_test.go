package workbench

import (
	"context"
	"fmt"
	"testing"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memTemplateRepo 是模板仓储替身：按 tenant+owner 隔离，硬删。
type memTemplateRepo struct {
	interfaces.WorkbenchRepository
	rows map[string]*types.WorkbenchTemplate
}

func newMemTemplateRepo() *memTemplateRepo {
	return &memTemplateRepo{rows: map[string]*types.WorkbenchTemplate{}}
}

func (m *memTemplateRepo) ListTemplates(
	_ context.Context, tenantID uint64, ownerID string,
) ([]*types.WorkbenchTemplateSummary, error) {
	out := make([]*types.WorkbenchTemplateSummary, 0, len(m.rows))
	// 只按 updated_at 倒序；数量少，直接排序即可
	all := make([]*types.WorkbenchTemplate, 0, len(m.rows))
	for _, row := range m.rows {
		if row.TenantID == tenantID && row.OwnerID == ownerID {
			all = append(all, row)
		}
	}
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].UpdatedAt.After(all[i].UpdatedAt) {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
	for _, row := range all {
		out = append(out, row.ToSummary())
	}
	return out, nil
}

func (m *memTemplateRepo) CountTemplates(
	_ context.Context, tenantID uint64, ownerID string,
) (int64, error) {
	var n int64
	for _, row := range m.rows {
		if row.TenantID == tenantID && row.OwnerID == ownerID {
			n++
		}
	}
	return n, nil
}

func (m *memTemplateRepo) CreateTemplate(
	_ context.Context, tpl *types.WorkbenchTemplate,
) error {
	m.rows[tpl.ID] = tpl
	return nil
}

func (m *memTemplateRepo) GetTemplate(
	_ context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchTemplate, error) {
	row, ok := m.rows[id]
	if !ok || row.TenantID != tenantID || row.OwnerID != ownerID {
		return nil, nil
	}
	return row, nil
}

func (m *memTemplateRepo) GetTemplateWithContent(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchTemplate, error) {
	return m.GetTemplate(ctx, tenantID, ownerID, id)
}

func (m *memTemplateRepo) UpdateTemplate(
	_ context.Context, tenantID uint64, ownerID, id string, fields map[string]any,
) (*types.WorkbenchTemplate, error) {
	row, ok := m.rows[id]
	if !ok || row.TenantID != tenantID || row.OwnerID != ownerID {
		return nil, nil
	}
	if name, ok := fields["name"].(string); ok {
		row.Name = name
	}
	if desc, ok := fields["description"].(string); ok {
		row.Description = desc
	}
	row.UpdatedAt = time.Now().UTC()
	return row, nil
}

func (m *memTemplateRepo) DeleteTemplate(
	_ context.Context, tenantID uint64, ownerID, id string,
) (int64, error) {
	row, ok := m.rows[id]
	if !ok || row.TenantID != tenantID || row.OwnerID != ownerID {
		return 0, nil
	}
	delete(m.rows, id)
	return 1, nil
}

func (m *memTemplateRepo) DeleteByOwner(
	_ context.Context, tenantID uint64, ownerID string,
) (int64, error) {
	var n int64
	for id, row := range m.rows {
		if row.TenantID == tenantID && row.OwnerID == ownerID {
			delete(m.rows, id)
			n++
		}
	}
	return n, nil
}

// templateTestService 组合条目与模板两个内存仓储。
type templateTestService struct {
	*Service
	items     *memWorkbenchRepo
	templates *memTemplateRepo
}

func newTemplateTestService() *templateTestService {
	items := newMemWorkbenchRepo()
	templates := newMemTemplateRepo()
	return &templateTestService{
		Service:   NewService(&combinedRepo{items: items, templates: templates}),
		items:     items,
		templates: templates,
	}
}

// combinedRepo 把两个替身拼成一个满足 WorkbenchRepository 的对象。
//
// 只显式实现模板服务真正用到的方法（GetItem + 模板 7 个），
// 其余交给内嵌的 nil 接口兜底：这样不必为了一个测试实现整个仓储面。
type combinedRepo struct {
	interfaces.WorkbenchRepository
	items     *memWorkbenchRepo
	templates *memTemplateRepo
}

func (c *combinedRepo) GetItem(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchItem, error) {
	return c.items.GetItem(ctx, tenantID, ownerID, id)
}

func (c *combinedRepo) ListTemplates(
	ctx context.Context, tenantID uint64, ownerID string,
) ([]*types.WorkbenchTemplateSummary, error) {
	return c.templates.ListTemplates(ctx, tenantID, ownerID)
}

func (c *combinedRepo) CountTemplates(
	ctx context.Context, tenantID uint64, ownerID string,
) (int64, error) {
	return c.templates.CountTemplates(ctx, tenantID, ownerID)
}

func (c *combinedRepo) CreateTemplate(ctx context.Context, tpl *types.WorkbenchTemplate) error {
	return c.templates.CreateTemplate(ctx, tpl)
}

func (c *combinedRepo) GetTemplate(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchTemplate, error) {
	return c.templates.GetTemplate(ctx, tenantID, ownerID, id)
}

func (c *combinedRepo) GetTemplateWithContent(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchTemplate, error) {
	return c.templates.GetTemplateWithContent(ctx, tenantID, ownerID, id)
}

func (c *combinedRepo) UpdateTemplate(
	ctx context.Context, tenantID uint64, ownerID, id string, fields map[string]any,
) (*types.WorkbenchTemplate, error) {
	return c.templates.UpdateTemplate(ctx, tenantID, ownerID, id, fields)
}

func (c *combinedRepo) DeleteTemplate(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (int64, error) {
	return c.templates.DeleteTemplate(ctx, tenantID, ownerID, id)
}

func (c *combinedRepo) DeleteByOwner(
	ctx context.Context, tenantID uint64, ownerID string,
) (int64, error) {
	return c.templates.DeleteByOwner(ctx, tenantID, ownerID)
}

func ctxFor(tenantID uint64, userID string) context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, types.UserIDContextKey, userID)
	return ctx
}

// seedCanvas 造一个已保存内容的画布条目。
func (s *templateTestService) seedCanvas(tenantID uint64, ownerID string, saved bool) *types.WorkbenchItem {
	item := &types.WorkbenchItem{
		ID:                  "item-1",
		TenantID:            tenantID,
		OwnerID:             ownerID,
		Kind:                types.WorkbenchKindCanvas,
		Title:               "我的文档",
		SnapshotVersion:     3,
		EditorSchemaVersion: types.SupportedEditorSchemaVersion,
		YDoc:                []byte{1, 2, 3, 4, 5},
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
	if !saved {
		item.SnapshotVersion = 0
		item.YDoc = nil
	}
	s.items.items[item.ID] = item
	return item
}

func TestCreateTemplate_CopiesSavedBytes(t *testing.T) {
	s := newTemplateTestService()
	s.seedCanvas(1, "user-a", true)
	ctx := ctxFor(1, "user-a")

	tpl, err := s.CreateTemplate(ctx, &types.CreateWorkbenchTemplateRequest{
		SourceItemID: "item-1",
		Name:         "我的周复盘",
		Description:  "每周五使用",
	})
	require.NoError(t, err)
	assert.Equal(t, "我的周复盘", tpl.Name)
	assert.Equal(t, 5, tpl.SizeBytes)
	assert.Equal(t, types.SupportedEditorSchemaVersion, tpl.EditorSchemaVersion)

	// 服务端复制的是已落盘字节，不接受客户端上传。
	stored := s.templates.rows[tpl.ID]
	require.NotNil(t, stored)
	assert.Equal(t, []byte{1, 2, 3, 4, 5}, stored.YDoc)
}

func TestCreateTemplate_RejectsUnsavedSource(t *testing.T) {
	s := newTemplateTestService()
	s.seedCanvas(1, "user-a", false)
	_, err := s.CreateTemplate(ctxFor(1, "user-a"), &types.CreateWorkbenchTemplateRequest{
		SourceItemID: "item-1",
		Name:         "空文档",
	})
	require.Error(t, err)
	_, ok := apperrors.IsAppError(err)
	assert.True(t, ok, "必须是 AppError，handler 才能正确映射状态码")
}

func TestCreateTemplate_RejectsOtherOwnerSource(t *testing.T) {
	s := newTemplateTestService()
	s.seedCanvas(1, "user-a", true)
	// 他人无法用别人的条目建模板：2400，不泄露存在性。
	_, err := s.CreateTemplate(ctxFor(1, "user-b"), &types.CreateWorkbenchTemplateRequest{
		SourceItemID: "item-1",
		Name:         "偷来的",
	})
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 404, appErr.HTTPCode)
}

func TestCreateTemplate_Quota(t *testing.T) {
	s := newTemplateTestService()
	s.seedCanvas(1, "user-a", true)
	// 填满到上限
	for i := 0; i < types.WorkbenchTemplateMaxPerOwner; i++ {
		id := fmt.Sprintf("tpl-%03d", i)
		s.templates.rows[id] = &types.WorkbenchTemplate{ID: id, TenantID: 1, OwnerID: "user-a"}
	}
	_, err := s.CreateTemplate(ctxFor(1, "user-a"), &types.CreateWorkbenchTemplateRequest{
		SourceItemID: "item-1",
		Name:         "第 51 个",
	})
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 400, appErr.HTTPCode)
}

func TestListTemplates_IsolatedByOwner(t *testing.T) {
	s := newTemplateTestService()
	s.seedCanvas(1, "user-a", true)
	_, err := s.CreateTemplate(ctxFor(1, "user-a"), &types.CreateWorkbenchTemplateRequest{
		SourceItemID: "item-1",
		Name:         "A 的模板",
	})
	require.NoError(t, err)

	mine, err := s.ListTemplates(ctxFor(1, "user-a"))
	require.NoError(t, err)
	assert.Len(t, mine, 1)

	other, err := s.ListTemplates(ctxFor(1, "user-b"))
	require.NoError(t, err)
	assert.Empty(t, other, "他人模板不可见")

	// 换租户同样为空
	crossTenant, err := s.ListTemplates(ctxFor(2, "user-a"))
	require.NoError(t, err)
	assert.Empty(t, crossTenant)
}

func TestGetTemplateContent_ReturnsBase64(t *testing.T) {
	s := newTemplateTestService()
	s.seedCanvas(1, "user-a", true)
	created, err := s.CreateTemplate(ctxFor(1, "user-a"), &types.CreateWorkbenchTemplateRequest{
		SourceItemID: "item-1",
		Name:         "T",
	})
	require.NoError(t, err)

	content, err := s.GetTemplateContent(ctxFor(1, "user-a"), created.ID)
	require.NoError(t, err)
	assert.Equal(t, "T", content.Name)
	assert.Equal(t, "AQIDBAU=", content.YDoc, "base64 of 1..5")
	assert.Equal(t, 5, content.SizeBytes)
}

func TestGetTemplateContent_OtherOwnerIs404(t *testing.T) {
	s := newTemplateTestService()
	s.seedCanvas(1, "user-a", true)
	created, err := s.CreateTemplate(ctxFor(1, "user-a"), &types.CreateWorkbenchTemplateRequest{
		SourceItemID: "item-1",
		Name:         "T",
	})
	require.NoError(t, err)

	_, err = s.GetTemplateContent(ctxFor(1, "user-b"), created.ID)
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 404, appErr.HTTPCode)
}

func TestUpdateTemplate_ValidatesName(t *testing.T) {
	s := newTemplateTestService()
	s.seedCanvas(1, "user-a", true)
	created, err := s.CreateTemplate(ctxFor(1, "user-a"), &types.CreateWorkbenchTemplateRequest{
		SourceItemID: "item-1",
		Name:         "T",
	})
	require.NoError(t, err)

	empty := "   "
	_, err = s.UpdateTemplate(ctxFor(1, "user-a"), created.ID, &types.UpdateWorkbenchTemplateRequest{
		Name: &empty,
	})
	require.Error(t, err, "空白名称必须被拒绝")

	newName := "新名字"
	updated, err := s.UpdateTemplate(ctxFor(1, "user-a"), created.ID, &types.UpdateWorkbenchTemplateRequest{
		Name: &newName,
	})
	require.NoError(t, err)
	assert.Equal(t, "新名字", updated.Name)
}

func TestDeleteTemplate_DoesNotAffectItem(t *testing.T) {
	s := newTemplateTestService()
	s.seedCanvas(1, "user-a", true)
	created, err := s.CreateTemplate(ctxFor(1, "user-a"), &types.CreateWorkbenchTemplateRequest{
		SourceItemID: "item-1",
		Name:         "T",
	})
	require.NoError(t, err)

	require.NoError(t, s.DeleteTemplate(ctxFor(1, "user-a"), created.ID))
	// 源条目仍在，且内容完好。
	item, err := s.items.GetItem(context.Background(), 1, "user-a", "item-1")
	require.NoError(t, err)
	assert.Equal(t, 3, item.SnapshotVersion)
	assert.Equal(t, []byte{1, 2, 3, 4, 5}, item.YDoc)

	// 再删一次是 404
	err = s.DeleteTemplate(ctxFor(1, "user-a"), created.ID)
	require.Error(t, err)
}

func TestCreateTemplate_InvalidNameAndDescription(t *testing.T) {
	s := newTemplateTestService()
	s.seedCanvas(1, "user-a", true)

	_, err := s.CreateTemplate(ctxFor(1, "user-a"), &types.CreateWorkbenchTemplateRequest{
		SourceItemID: "item-1",
		Name:         "",
	})
	require.Error(t, err)

	_, err = s.CreateTemplate(ctxFor(1, "user-a"), &types.CreateWorkbenchTemplateRequest{
		SourceItemID: "item-1",
		Name:         "ok",
		Description:  string(make([]rune, types.WorkbenchTemplateDescMaxRunes+1)),
	})
	require.Error(t, err)
}
