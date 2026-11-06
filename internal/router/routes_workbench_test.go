package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/workbench"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// emptyWorkbenchRepo 让 handler 走通但总是返回“条目不存在”，用于验证路由与门禁。
// 内嵌组合接口：本文件只覆盖内容端点，其余方法保持 nil。
type emptyWorkbenchRepo struct {
	interfaces.WorkbenchRepository
}

func (emptyWorkbenchRepo) GetItem(
	context.Context, uint64, string, string,
) (*types.WorkbenchItem, error) {
	return nil, gorm.ErrRecordNotFound
}

func (emptyWorkbenchRepo) UpdateItemContent(
	context.Context, uint64, string, string, int, bool, []byte, string, int,
) (int, time.Time, error) {
	return 0, time.Time{}, gorm.ErrRecordNotFound
}

// ListItems 返回空分页：让「条目端点未被门禁影响」的断言能走完整条链路，
// 而不是在内嵌接口的 nil 方法上 panic。
func (emptyWorkbenchRepo) ListItems(
	context.Context, uint64, string, types.WorkbenchItemFilter,
) ([]*types.WorkbenchItem, bool, error) {
	return nil, false, nil
}

func newWorkbenchFolderHandlerForTest() *handler.WorkbenchFolderHandler {
	return handler.NewWorkbenchFolderHandler(workbench.NewService(emptyWorkbenchRepo{}))
}

func newWorkbenchArchiveHandlerForTest() *handler.WorkbenchArchiveHandler {
	return handler.NewWorkbenchArchiveHandler(workbench.NewService(emptyWorkbenchRepo{}))
}

func newWorkbenchJournalHandlerForTest() *handler.WorkbenchJournalHandler {
	return handler.NewWorkbenchJournalHandler(workbench.NewService(emptyWorkbenchRepo{}))
}

func newWorkbenchNoticeHandlerForTest() *handler.WorkbenchNoticeHandler {
	return handler.NewWorkbenchNoticeHandler(workbench.NewService(emptyWorkbenchRepo{}))
}

// newWorkbenchAIHandlerForTest 只需要一个可注册的 handler；
// 路由级测试在 DenyAPIKeyPrincipal 就返回了，不会走到模型解析。
func newWorkbenchAIHandlerForTest() *handler.WorkbenchAIHandler {
	return handler.NewWorkbenchAIHandler(workbench.NewAIService(emptyWorkbenchRepo{}, nil, nil, nil))
}

func newWorkbenchTemplateHandlerForTest() *handler.WorkbenchTemplateHandler {
	return handler.NewWorkbenchTemplateHandler(workbench.NewService(emptyWorkbenchRepo{}))
}

// newWorkbenchAssetHandlerForTest 只为注册路由；未注入 AssetStore 时端点返回 503。
func newWorkbenchAssetHandlerForTest() *handler.WorkbenchAssetHandler {
	return handler.NewWorkbenchAssetHandler(workbench.NewService(emptyWorkbenchRepo{}))
}

// workbenchTestEngine 复刻 rbac 测试引擎：注入租户/用户/角色，可选 API Key scope。
func workbenchTestEngine(t *testing.T, apiKeyScope *types.TenantAPIKeyScope) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	enabled := true
	guards := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}}
	h := handler.NewWorkbenchItemHandler(workbench.NewService(emptyWorkbenchRepo{}))

	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
		ctx = context.WithValue(ctx, types.UserIDContextKey, "user-a")
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleOwner)
		if apiKeyScope != nil {
			ctx = types.WithTenantAPIKeyScope(ctx, *apiKeyScope)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	RegisterWorkbenchRoutes(r.Group("/api/v1"), h, newWorkbenchFolderHandlerForTest(),
		newWorkbenchArchiveHandlerForTest(), newWorkbenchJournalHandlerForTest(),
		newWorkbenchNoticeHandlerForTest(), newWorkbenchAIHandlerForTest(),
		newWorkbenchTemplateHandlerForTest(), newWorkbenchAssetHandlerForTest(),
		// 测试引擎把三个门禁全部打开，覆盖到所有子路由。
		WorkbenchFeatureFlags{Templates: true, Journals: true, AI: true}, guards)
	RegisterWorkbenchNoticeRoutes(r.Group("/api/v1"), newWorkbenchNoticeHandlerForTest())
	return r
}

// 工作台内容端点必须对任何 API Key 一律拒绝，即使 key 是 full-access。
// 拒绝发生在 DenyAPIKeyPrincipal，handler 永远不会被执行。
func TestWorkbenchRoutes_DenyAPIKeyPrincipal(t *testing.T) {
	cases := []struct {
		name  string
		scope types.TenantAPIKeyScope
	}{
		{name: "full access key", scope: types.TenantAPIKeyScope{FullAccess: true}},
		{name: "retrieve-only key", scope: types.TenantAPIKeyScope{
			Capabilities: types.StringArray{string(types.APIKeyCapabilityRetrieve)},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := workbenchTestEngine(t, &tc.scope)
			// 覆盖三类端点：内容读写、条目元数据、文件夹。
			for _, req := range []*http.Request{
				httptest.NewRequest(http.MethodGet, "/api/v1/workbench/items/item-1/content", nil),
				httptest.NewRequest(http.MethodPut, "/api/v1/workbench/items/item-1/content", nil),
				httptest.NewRequest(http.MethodGet, "/api/v1/workbench/items", nil),
				httptest.NewRequest(http.MethodPut, "/api/v1/workbench/items/item-1", nil),
				httptest.NewRequest(http.MethodGet, "/api/v1/workbench/folders", nil),
				httptest.NewRequest(http.MethodGet, "/api/v1/workbench/archives/state?source_type=item&source_id=x", nil),
				httptest.NewRequest(http.MethodPost, "/api/v1/workbench/archives", nil),
				httptest.NewRequest(http.MethodGet, "/api/v1/workbench/journals/month", nil),
				httptest.NewRequest(http.MethodGet, "/api/v1/workbench/journals/2026-10-30", nil),
				httptest.NewRequest(http.MethodGet, "/api/v1/workbench/leave-preview", nil),
				// 内联 AI：即使请求体非法，也必须先被 API Key 门禁挡住。
				httptest.NewRequest(http.MethodPost, "/api/v1/workbench/ai/inline", nil),
				httptest.NewRequest(http.MethodPost, "/api/v1/workbench/ai/ask", nil),
				// 数据库 AI（P3-5）同样只认 JWT。
				httptest.NewRequest(http.MethodPost, "/api/v1/workbench/ai/database", nil),
				// 用户级通知：tenantless 不等于对 API Key 开放。
				httptest.NewRequest(http.MethodGet, "/api/v1/me/workbench-notices", nil),
				httptest.NewRequest(http.MethodPost, "/api/v1/me/workbench-notices/read-all", nil),
				httptest.NewRequest(http.MethodPost, "/api/v1/me/workbench-notices/n-1/read", nil),
			} {
				rec := httptest.NewRecorder()
				engine.ServeHTTP(rec, req)
				require.Equal(t, http.StatusForbidden, rec.Code,
					"%s %s 必须被 API Key 门禁拒绝，body=%s",
					req.Method, req.URL.Path, rec.Body.String())
			}
		})
	}
}

// JWT 身份（无 API Key scope）不会被 DenyAPIKeyPrincipal 拦下，请求会进入 handler；
// 这里仓储恒返回 NotFound，因此期望 404（2400）而不是 403。
func TestWorkbenchRoutes_JWTPrincipalPassesGate(t *testing.T) {
	engine := workbenchTestEngine(t, nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/v1/workbench/items/item-1/content", nil))
	require.NotEqual(t, http.StatusForbidden, rec.Code, "JWT 身份不应被 API Key 门禁拦截")
	require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())
}

// workbenchGatedEngine 只开指定的子功能，用于验证发布门禁（§9.1）。
func workbenchGatedEngine(t *testing.T, flags WorkbenchFeatureFlags) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	enabled := true
	guards := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}}
	h := handler.NewWorkbenchItemHandler(workbench.NewService(emptyWorkbenchRepo{}))

	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
		ctx = context.WithValue(ctx, types.UserIDContextKey, "user-a")
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleOwner)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	RegisterWorkbenchRoutes(r.Group("/api/v1"), h, newWorkbenchFolderHandlerForTest(),
		newWorkbenchArchiveHandlerForTest(), newWorkbenchJournalHandlerForTest(),
		newWorkbenchNoticeHandlerForTest(), newWorkbenchAIHandlerForTest(),
		newWorkbenchTemplateHandlerForTest(), newWorkbenchAssetHandlerForTest(), flags, guards)
	return r
}

// 关闭子功能时不注册路由：请求落到 gin 的 404，而不是 403/500。
// 这保证「开关只控制入口与路由注册」这条规则在服务端真的生效。
func TestWorkbenchRoutes_FeatureFlagsSkipRegistration(t *testing.T) {
	engine := workbenchGatedEngine(t, WorkbenchFeatureFlags{})

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/workbench/templates", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/workbench/templates", nil),
		httptest.NewRequest(http.MethodGet, "/api/v1/workbench/journals/month", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/workbench/ai/inline", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/workbench/ai/database", nil),
	} {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code,
			"%s %s 在开关关闭时必须未注册", req.Method, req.URL.Path)
	}

	// 核心条目端点不受子功能开关影响。
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/workbench/items", nil))
	require.NotEqual(t, http.StatusNotFound, rec.Code, "条目端点不应被模板/AI/Journals 开关影响")
}
