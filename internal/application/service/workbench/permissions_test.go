package workbench

import (
	"context"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 端到端权限矩阵（P4-3 / 开发计划 G4）。
//
// 口径（实现方案 §4.1、§9.1）：
//   - 归属只看 tenant_id + owner_id，**不看租户角色**：Admin/Owner 也读不到别人的私有内容；
//   - 越权统一返回 2400（404），不返回 403，也不泄露存在性与名称；
//   - 跨租户与同租户换 owner 等价处理；
//   - API Key 主体（合成用户）在工作台一律 401，路由层另有 403 门禁（见 router 包）。
//
// 这里在服务层跑一遍读写矩阵，路由层的 API Key/总开关门禁由
// `internal/router/routes_workbench_test.go` 覆盖，两者合起来才是完整证据链。

// asCaller 在既有 ctx 上换一个身份；tenant 与 owner 都可独立指定。
func asCaller(parent context.Context, tenantID uint64, userID string, role types.TenantRole) context.Context {
	return types.WithCaller(parent, types.Caller{
		TenantID: tenantID,
		UserID:   userID,
		Role:     role,
	})
}

// assertNotFound 断言错误是 2400 而不是 403/401。
func assertNotFound(t *testing.T, err error) {
	t.Helper()
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok, "必须是 AppError，got %v", err)
	assert.Equal(t, apperrors.ErrWorkbenchNotFound, appErr.Code, "越权必须是 2400")
}

// 第二用户 + Admin + 跨租户对条目的读写矩阵。
func TestPermissionMatrix_ItemAccess(t *testing.T) {
	svc, _ := newServiceWithRepo()
	ownerCtx := asCaller(context.Background(), 1, "user-a", types.TenantRoleOwner)

	created, err := svc.CreateItem(ownerCtx, &types.CreateWorkbenchItemRequest{
		Kind: types.WorkbenchKindCanvas, Title: "A 的私有画布",
	})
	require.NoError(t, err)

	// 同租户的 Admin/Owner 也读不到别人的条目：角色不参与工作台归属判断。
	privileged := []struct {
		name string
		role types.TenantRole
	}{
		{"同租户 Admin", types.TenantRoleAdmin},
		{"同租户 Owner", types.TenantRoleOwner},
		{"同租户 Contributor", types.TenantRoleContributor},
		{"同租户 Viewer", types.TenantRoleViewer},
	}
	for _, tc := range privileged {
		t.Run(tc.name+" 读不到", func(t *testing.T) {
			ctx := asCaller(context.Background(), 1, "user-b", tc.role)
			_, err := svc.GetItem(ctx, created.ID)
			assertNotFound(t, err)

			_, err = svc.UpdateItem(ctx, created.ID, &types.UpdateWorkbenchItemRequest{})
			assertNotFound(t, err)

			err = svc.DeleteItem(ctx, created.ID, false)
			assertNotFound(t, err)

			err = svc.DeleteItem(ctx, created.ID, true)
			assertNotFound(t, err)

			_, err = svc.RestoreItem(ctx, created.ID)
			assertNotFound(t, err)

			_, err = svc.GetContent(ctx, created.ID)
			assertNotFound(t, err)
		})
	}

	t.Run("跨租户读不到", func(t *testing.T) {
		ctx := asCaller(context.Background(), 2, "user-a", types.TenantRoleOwner)
		_, err := svc.GetItem(ctx, created.ID)
		assertNotFound(t, err)
		_, err = svc.GetContent(ctx, created.ID)
		assertNotFound(t, err)
	})

	t.Run("列表只返回自己的", func(t *testing.T) {
		other := asCaller(context.Background(), 1, "user-b", types.TenantRoleAdmin)
		page, err := svc.ListItems(other, types.WorkbenchItemFilter{})
		require.NoError(t, err)
		assert.Empty(t, page.Items)

		own, err := svc.ListItems(ownerCtx, types.WorkbenchItemFilter{})
		require.NoError(t, err)
		require.Len(t, own.Items, 1)
		assert.Equal(t, created.ID, own.Items[0].ID)
	})

	t.Run("原主人仍可读写", func(t *testing.T) {
		got, err := svc.GetItem(ownerCtx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, "A 的私有画布", got.Title)
	})
}

// 文件夹同样按 owner 隔离：越权改名/移动/删除都不生效。
func TestPermissionMatrix_FolderAccess(t *testing.T) {
	svc, repo := newServiceWithRepo()
	ownerCtx := asCaller(context.Background(), 1, "user-a", types.TenantRoleOwner)

	folder, err := svc.CreateFolder(ownerCtx, &types.WorkbenchFolderRequest{Name: "A 的文件夹"})
	require.NoError(t, err)

	adminCtx := asCaller(context.Background(), 1, "user-b", types.TenantRoleAdmin)

	_, err = svc.UpdateFolder(adminCtx, folder.ID, &types.WorkbenchFolderRequest{Name: "被改名"})
	assertNotFound(t, err)

	err = svc.DeleteFolder(adminCtx, folder.ID)
	assertNotFound(t, err)

	// 越权全部失败后，原文件夹必须还在，且名字未被改动。
	require.Contains(t, repo.folders, folder.ID)
	assert.Equal(t, "A 的文件夹", repo.folders[folder.ID].Name)
}

// API Key 主体（合成用户）在服务层兜底拒绝：401，不是 2400。
// 路由层的 DenyAPIKeyPrincipal 会先返回 403，这层只防绕过中间件的直调。
func TestPermissionMatrix_SyntheticUserRejected(t *testing.T) {
	svc, _ := newServiceWithRepo()
	ctx := asCaller(context.Background(), 1, "system-1", types.TenantRoleOwner)

	_, err := svc.ListItems(ctx, types.WorkbenchItemFilter{})
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok, "必须是 AppError，got %v", err)
	assert.Equal(t, apperrors.ErrUnauthorized, appErr.Code)
}

// 缺少租户或用户上下文时是 401（而不是 2400）：身份问题优先于资源问题。
func TestPermissionMatrix_MissingIdentity(t *testing.T) {
	svc, _ := newServiceWithRepo()

	_, err := svc.ListItems(context.Background(), types.WorkbenchItemFilter{})
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrUnauthorized, appErr.Code)
}
