package workbench

import (
	"context"
	"testing"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// memWorkbenchRepo 是内存仓储替身，只实现 P1-2 服务层用到的读/写路径。
// 刻意不复刻 SQL 语义（分页、排序由仓储测试覆盖），只用于验证服务层的
// 校验顺序、错误码映射与 owner 隔离判断。
type memWorkbenchRepo struct {
	interfaces.WorkbenchRepository

	items   map[string]*types.WorkbenchItem
	trashed map[string]*types.WorkbenchItem
	folders map[string]*types.WorkbenchFolder
	// scope 记录最后一次调用的 (tenantID, ownerID)，用于断言服务层传对了归属。
	lastTenant uint64
	lastOwner  string
}

func newMemWorkbenchRepo() *memWorkbenchRepo {
	return &memWorkbenchRepo{
		items:   map[string]*types.WorkbenchItem{},
		trashed: map[string]*types.WorkbenchItem{},
		folders: map[string]*types.WorkbenchFolder{},
	}
}

func (m *memWorkbenchRepo) CreateItem(
	_ context.Context, tenantID uint64, ownerID string, item *types.WorkbenchItem,
) error {
	m.lastTenant, m.lastOwner = tenantID, ownerID
	item.TenantID, item.OwnerID = tenantID, ownerID
	m.items[item.ID] = item
	return nil
}

func (m *memWorkbenchRepo) GetItem(
	_ context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchItem, error) {
	return m.get(m.items, tenantID, ownerID, id)
}

func (m *memWorkbenchRepo) GetItemMeta(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchItem, error) {
	return m.GetItem(ctx, tenantID, ownerID, id)
}

func (m *memWorkbenchRepo) GetTrashedItem(
	_ context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchItem, error) {
	return m.get(m.trashed, tenantID, ownerID, id)
}

func (m *memWorkbenchRepo) get(
	bucket map[string]*types.WorkbenchItem, tenantID uint64, ownerID, id string,
) (*types.WorkbenchItem, error) {
	item, ok := bucket[id]
	if !ok || item.TenantID != tenantID || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	return item, nil
}

func (m *memWorkbenchRepo) UpdateItem(
	_ context.Context, tenantID uint64, ownerID, id string,
	fields map[string]any, moveToRoot bool,
) (*types.WorkbenchItem, error) {
	item, err := m.get(m.items, tenantID, ownerID, id)
	if err != nil {
		return nil, err
	}
	if title, ok := fields["title"].(string); ok {
		item.Title = title
	}
	if icon, ok := fields["icon"].(string); ok {
		item.Icon = icon
	}
	if favorite, ok := fields["is_favorite"].(bool); ok {
		item.IsFavorite = favorite
	}
	if moveToRoot {
		item.FolderID = nil
	} else if folderID, ok := fields["folder_id"].(string); ok {
		item.FolderID = &folderID
	}
	return item, nil
}

func (m *memWorkbenchRepo) TrashItem(
	_ context.Context, tenantID uint64, ownerID, id string,
) error {
	item, err := m.get(m.items, tenantID, ownerID, id)
	if err != nil {
		return err
	}
	delete(m.items, id)
	m.trashed[id] = item
	return nil
}

func (m *memWorkbenchRepo) RestoreItem(
	_ context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchItem, error) {
	item, err := m.get(m.trashed, tenantID, ownerID, id)
	if err != nil {
		return nil, err
	}
	delete(m.trashed, id)
	m.items[id] = item
	return item, nil
}

func (m *memWorkbenchRepo) PurgeItem(
	_ context.Context, tenantID uint64, ownerID, id string,
) error {
	if _, err := m.get(m.trashed, tenantID, ownerID, id); err != nil {
		return err
	}
	delete(m.trashed, id)
	return nil
}

func (m *memWorkbenchRepo) ListTrashedItems(
	_ context.Context, tenantID uint64, ownerID string, limit int,
) ([]*types.WorkbenchItem, error) {
	out := make([]*types.WorkbenchItem, 0, len(m.trashed))
	for _, item := range m.trashed {
		if item.TenantID != tenantID || item.OwnerID != ownerID {
			continue
		}
		out = append(out, item)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *memWorkbenchRepo) PurgeTrashedItems(
	_ context.Context, tenantID uint64, ownerID string, ids []string,
) (int64, error) {
	var purged int64
	for _, id := range ids {
		if _, err := m.get(m.trashed, tenantID, ownerID, id); err != nil {
			continue
		}
		delete(m.trashed, id)
		purged++
	}
	return purged, nil
}

func (m *memWorkbenchRepo) TouchLastOpened(
	_ context.Context, _ uint64, _ string, _ string,
) error {
	return nil
}

func (m *memWorkbenchRepo) ListItems(
	_ context.Context, tenantID uint64, ownerID string, filter types.WorkbenchItemFilter,
) ([]*types.WorkbenchItem, bool, error) {
	source := m.items
	if filter.Trashed {
		source = m.trashed
	}
	out := make([]*types.WorkbenchItem, 0, len(source))
	for _, item := range source {
		if item.TenantID == tenantID && item.OwnerID == ownerID {
			out = append(out, item)
		}
	}
	return out, false, nil
}

func (m *memWorkbenchRepo) ListByJournalMonth(
	_ context.Context, _ uint64, _ string, _ string, _ string,
) ([]*types.WorkbenchItem, error) {
	return nil, nil
}

func (m *memWorkbenchRepo) CountItems(context.Context, uint64, string) (int64, error) {
	return int64(len(m.items)), nil
}

// ListLiveBySources 是列表批量填充归档状态的只读聚合。
// 内存仓储没有归档行，统一返回空 map（列表徽标显示 none）。
func (m *memWorkbenchRepo) ListLiveBySources(
	context.Context, uint64, string, string, []string,
) (map[string][]*types.WorkbenchArchive, error) {
	return map[string][]*types.WorkbenchArchive{}, nil
}

func (m *memWorkbenchRepo) PurgeTrashedBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func (m *memWorkbenchRepo) UpdateItemContent(
	_ context.Context, tenantID uint64, ownerID, id string,
	baseVersion int, force bool, ydoc []byte, text string, schemaVersion int,
) (int, time.Time, error) {
	item, err := m.get(m.items, tenantID, ownerID, id)
	if err != nil {
		return 0, time.Time{}, err
	}
	if !force && item.SnapshotVersion != baseVersion {
		return item.SnapshotVersion, item.UpdatedAt, types.ErrWorkbenchVersionConflict
	}
	item.SnapshotVersion++
	item.YDoc, item.TextContent, item.EditorSchemaVersion = ydoc, text, schemaVersion
	item.UpdatedAt = time.Now().UTC()
	return item.SnapshotVersion, item.UpdatedAt, nil
}

func (m *memWorkbenchRepo) CreateFolder(
	_ context.Context, tenantID uint64, ownerID string, folder *types.WorkbenchFolder,
) error {
	folder.TenantID, folder.OwnerID = tenantID, ownerID
	m.folders[folder.ID] = folder
	return nil
}

func (m *memWorkbenchRepo) GetFolder(
	_ context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchFolder, error) {
	folder, ok := m.folders[id]
	if !ok || folder.TenantID != tenantID || folder.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	return folder, nil
}

func (m *memWorkbenchRepo) UpdateFolder(
	_ context.Context, tenantID uint64, ownerID, id string, fields map[string]any,
) (*types.WorkbenchFolder, error) {
	folder, err := m.GetFolder(context.Background(), tenantID, ownerID, id)
	if err != nil {
		return nil, err
	}
	if name, ok := fields["name"].(string); ok {
		folder.Name = name
	}
	if parentID, ok := fields["parent_id"].(string); ok {
		folder.ParentID = &parentID
	}
	return folder, nil
}

func (m *memWorkbenchRepo) DeleteFolder(
	_ context.Context, tenantID uint64, ownerID, id string,
) error {
	if _, err := m.GetFolder(context.Background(), tenantID, ownerID, id); err != nil {
		return err
	}
	delete(m.folders, id)
	return nil
}

func (m *memWorkbenchRepo) ListFolders(
	_ context.Context, tenantID uint64, ownerID string,
) ([]*types.WorkbenchFolder, error) {
	out := make([]*types.WorkbenchFolder, 0, len(m.folders))
	for _, folder := range m.folders {
		if folder.TenantID == tenantID && folder.OwnerID == ownerID {
			out = append(out, folder)
		}
	}
	return out, nil
}

func (m *memWorkbenchRepo) ReparentChildren(
	_ context.Context, tenantID uint64, ownerID, folderID string, newParentID *string,
) error {
	for _, folder := range m.folders {
		if folder.TenantID == tenantID && folder.OwnerID == ownerID &&
			folder.ParentID != nil && *folder.ParentID == folderID {
			folder.ParentID = newParentID
		}
	}
	return nil
}

func (m *memWorkbenchRepo) MoveItemsToFolder(
	_ context.Context, tenantID uint64, ownerID, folderID string, newParentID *string,
) error {
	for _, item := range m.items {
		if item.TenantID == tenantID && item.OwnerID == ownerID &&
			item.FolderID != nil && *item.FolderID == folderID {
			item.FolderID = newParentID
		}
	}
	return nil
}

func (m *memWorkbenchRepo) CountFolders(context.Context, uint64, string) (int64, error) {
	return int64(len(m.folders)), nil
}

// --- 用例 ---

func newServiceWithRepo() (*Service, *memWorkbenchRepo) {
	repo := newMemWorkbenchRepo()
	return NewService(repo), repo
}

// 创建条目：默认值正确，owner 由 ctx 注入而不是请求体。
func TestCreateItemAppliesDefaultsAndScope(t *testing.T) {
	svc, repo := newServiceWithRepo()

	summary, err := svc.CreateItem(newTestContext(), &types.CreateWorkbenchItemRequest{
		Kind:  types.WorkbenchKindCanvas,
		Title: "  我的画布  ",
	})
	require.NoError(t, err)
	assert.Equal(t, "我的画布", summary.Title, "标题必须 trim")
	assert.Equal(t, types.WorkbenchKindCanvas, summary.Kind)
	assert.Equal(t, types.WorkbenchVisibilityPrivate, summary.Visibility)
	assert.Equal(t, types.WorkbenchSnapshotVersionInitial, summary.SnapshotVersion)
	assert.Equal(t, uint64(1), repo.lastTenant)
	assert.Equal(t, "user-a", repo.lastOwner)
}

func TestCreateItemValidation(t *testing.T) {
	svc, _ := newServiceWithRepo()
	ctx := newTestContext()

	for _, tc := range []struct {
		name string
		req  *types.CreateWorkbenchItemRequest
	}{
		{"nil body", nil},
		{"bad kind", &types.CreateWorkbenchItemRequest{Kind: "spreadsheet", Title: "x"}},
		{"empty title", &types.CreateWorkbenchItemRequest{Kind: types.WorkbenchKindCanvas, Title: "   "}},
		{"title too long", &types.CreateWorkbenchItemRequest{
			Kind: types.WorkbenchKindCanvas, Title: string(make([]rune, 201)),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateItem(ctx, tc.req)
			appErr, ok := apperrors.IsAppError(err)
			require.True(t, ok, "必须是 AppError，got %v", err)
			assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)
		})
	}
}

// 跨 owner 的 id 一律 2400，不返回 403，也不透露存在性。
func TestCrossOwnerItemIsNotFound(t *testing.T) {
	svc, repo := newServiceWithRepo()
	ctx := newTestContext()

	created, err := svc.CreateItem(ctx, &types.CreateWorkbenchItemRequest{
		Kind: types.WorkbenchKindCanvas, Title: "A 的画布",
	})
	require.NoError(t, err)

	otherCtx := types.WithCaller(newTestContext(), types.Caller{
		TenantID: 1, UserID: "user-b", Role: types.TenantRoleOwner,
	})
	_, err = svc.GetItem(otherCtx, created.ID)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrWorkbenchNotFound, appErr.Code, "越权必须是 2400")
	require.NotNil(t, repo.items[created.ID], "越权读取不得改动数据")
}

// 元数据更新：白名单外的字段被忽略，且不触碰 snapshot_version。
func TestUpdateItemIgnoresUnknownFields(t *testing.T) {
	svc, _ := newServiceWithRepo()
	ctx := newTestContext()

	created, err := svc.CreateItem(ctx, &types.CreateWorkbenchItemRequest{
		Kind: types.WorkbenchKindCanvas, Title: "原标题",
	})
	require.NoError(t, err)

	title := "新标题"
	favorite := true
	updated, err := svc.UpdateItem(ctx, created.ID, &types.UpdateWorkbenchItemRequest{
		Title: &title, IsFavorite: &favorite,
	})
	require.NoError(t, err)
	assert.Equal(t, "新标题", updated.Title)
	assert.True(t, updated.IsFavorite)
	assert.Equal(t, created.SnapshotVersion, updated.SnapshotVersion)
}

// 回收站状态机：重复软删 400、未删除不能 restore 400、不在回收站不能 purge 400。
func TestDeleteAndRestoreStateMachine(t *testing.T) {
	svc, _ := newServiceWithRepo()
	ctx := newTestContext()

	created, err := svc.CreateItem(ctx, &types.CreateWorkbenchItemRequest{
		Kind: types.WorkbenchKindCanvas, Title: "待删",
	})
	require.NoError(t, err)

	// 未删除时 restore / purge 都应是 400（资源在，但状态不允许）。
	_, err = svc.RestoreItem(ctx, created.ID)
	assertAppCode(t, err, apperrors.ErrBadRequest)
	assertAppCode(t, svc.DeleteItem(ctx, created.ID, true), apperrors.ErrBadRequest)

	require.NoError(t, svc.DeleteItem(ctx, created.ID, false))
	// 已在回收站，再软删是 400。
	assertAppCode(t, svc.DeleteItem(ctx, created.ID, false), apperrors.ErrBadRequest)
	// 回收站内可以恢复。
	restored, err := svc.RestoreItem(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, restored.ID)

	// 再删一次并永久删除。
	require.NoError(t, svc.DeleteItem(ctx, created.ID, false))
	require.NoError(t, svc.DeleteItem(ctx, created.ID, true))
	assertAppCode(t, svc.DeleteItem(ctx, created.ID, true), apperrors.ErrWorkbenchNotFound)
}

// 一键清空回收站：只删当前 owner 的回收站条目，活跃条目不受影响。
func TestEmptyTrashPurgesOnlyOwnerTrash(t *testing.T) {
	svc, repo := newServiceWithRepo()
	ctx := newTestContext()

	live, err := svc.CreateItem(ctx, &types.CreateWorkbenchItemRequest{
		Kind: types.WorkbenchKindCanvas, Title: "活跃条目",
	})
	require.NoError(t, err)
	trashed, err := svc.CreateItem(ctx, &types.CreateWorkbenchItemRequest{
		Kind: types.WorkbenchKindCanvas, Title: "待清空",
	})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteItem(ctx, trashed.ID, false))

	// 另一个 owner 的回收站条目不能被连带清掉。
	otherCtx := types.WithCaller(newTestContext(), types.Caller{
		TenantID: 1, UserID: "user-b", Role: types.TenantRoleOwner,
	})
	other, err := svc.CreateItem(otherCtx, &types.CreateWorkbenchItemRequest{
		Kind: types.WorkbenchKindCanvas, Title: "B 的条目",
	})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteItem(otherCtx, other.ID, false))

	purged, err := svc.EmptyTrash(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, purged)
	assert.NotNil(t, repo.items[live.ID], "活跃条目不得被清空")
	assert.Nil(t, repo.trashed[trashed.ID], "回收站条目应被硬删")
	assert.NotNil(t, repo.trashed[other.ID], "其他 owner 的回收站不受影响")

	// 幂等：回收站已空时返回 0。
	purged, err = svc.EmptyTrash(ctx)
	require.NoError(t, err)
	assert.Zero(t, purged)
}

// 文件夹：深度上限 5 层、不能挂到自己的子孙下、删除时内容上移。
func TestFolderDepthAndCycleGuards(t *testing.T) {
	svc, repo := newServiceWithRepo()
	ctx := newTestContext()

	var parentID *string
	created := make([]*types.WorkbenchFolder, 0, 6)
	for i := 0; i < 5; i++ {
		folder, err := svc.CreateFolder(ctx, &types.WorkbenchFolderRequest{
			Name: "层", ParentID: parentID,
		})
		require.NoError(t, err, "第 %d 层应允许创建", i+1)
		created = append(created, folder)
		parentID = &folder.ID
	}
	// 第 6 层必须被拒绝。
	_, err := svc.CreateFolder(ctx, &types.WorkbenchFolderRequest{Name: "第6层", ParentID: parentID})
	assertAppCode(t, err, apperrors.ErrBadRequest)

	// 把根挂到自己的子孙下必须被拒绝。
	root := created[0]
	deep := created[4].ID
	_, err = svc.UpdateFolder(ctx, root.ID, &types.WorkbenchFolderRequest{ParentID: &deep})
	assertAppCode(t, err, apperrors.ErrBadRequest)

	// 删除中间层：子层上移到它的父级。
	middle := created[2]
	require.NoError(t, svc.DeleteFolder(ctx, middle.ID))
	child := repo.folders[created[3].ID]
	require.NotNil(t, child)
	require.NotNil(t, child.ParentID)
	assert.Equal(t, created[1].ID, *child.ParentID, "子文件夹应上移到祖父级")
}

// 文件夹跨 owner 不可见，也不可被引用。
func TestFolderCrossOwner(t *testing.T) {
	svc, repo := newServiceWithRepo()
	ctx := newTestContext()

	folder, err := svc.CreateFolder(ctx, &types.WorkbenchFolderRequest{Name: "A 的文件夹"})
	require.NoError(t, err)

	otherCtx := types.WithCaller(newTestContext(), types.Caller{
		TenantID: 1, UserID: "user-b", Role: types.TenantRoleOwner,
	})
	_, err = svc.CreateItem(otherCtx, &types.CreateWorkbenchItemRequest{
		Kind: types.WorkbenchKindCanvas, Title: "B 的画布", FolderID: &folder.ID,
	})
	assertAppCode(t, err, apperrors.ErrWorkbenchNotFound)

	assertAppCode(t, svc.DeleteFolder(otherCtx, folder.ID), apperrors.ErrWorkbenchNotFound)
	require.Contains(t, repo.folders, folder.ID, "越权删除不得生效")
}

// 列表返回空切片而不是 nil（前端直接 v-for，不想判 null）。
func TestListItemsReturnsEmptySlice(t *testing.T) {
	svc, _ := newServiceWithRepo()
	page, err := svc.ListItems(newTestContext(), types.WorkbenchItemFilter{})
	require.NoError(t, err)
	require.NotNil(t, page.Items)
	assert.Empty(t, page.Items)
	assert.Empty(t, page.NextCursor)
}

// assertAppCode 断言错误是 AppError 且 code 匹配。
func assertAppCode(t *testing.T, err error, want apperrors.ErrorCode) {
	t.Helper()
	appErr, ok := apperrors.IsAppError(err)
	require.Truef(t, ok, "expected AppError, got %v", err)
	assert.Equalf(t, want, appErr.Code, "unexpected code (message=%s)", appErr.Message)
}
