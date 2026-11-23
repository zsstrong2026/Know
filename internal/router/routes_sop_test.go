package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
)

// SOP 路由必须声明 API Key 策略：未声明的路由会被门禁默认拒绝，
// 而这里断言每条路由都登记了对应能力（读→read_sop，写→manage_sop）。
func TestSOPRoutesDeclareCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := handler.NewSOPHandler(handler.SOPHandlerDeps{})
	g := &rbacGuards{cfg: testSOPConfig()}
	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.Use(g.apiKeyAuthorizer.Middleware())
	RegisterSOPRoutes(v1, h, g)

	readRoutes := map[string]bool{
		http.MethodGet + " /api/v1/sop/projects":     true,
		http.MethodGet + " /api/v1/sop/projects/:id": true,
	}
	writeRoutes := map[string]bool{
		http.MethodPost + " /api/v1/sop/projects":                   true,
		http.MethodPut + " /api/v1/sop/projects/:id":                true,
		http.MethodDelete + " /api/v1/sop/projects/:id":             true,
		http.MethodPost + " /api/v1/sop/projects/:id/repos":         true,
		http.MethodDelete + " /api/v1/sop/projects/:id/repos/:name": true,
		http.MethodPut + " /api/v1/sop/projects/:id/repos/default":  true,
	}
	for key := range readRoutes {
		method, path := splitRouteKey(t, key)
		policy, ok := g.apiKeyAuthorizer.Lookup(method, path)
		require.True(t, ok, "路由 %s 未声明 API Key 策略（会被默认拒绝）", key)
		require.Contains(t, policy.Capabilities, types.APIKeyCapabilityReadSOP, key)
	}
	for key := range writeRoutes {
		method, path := splitRouteKey(t, key)
		policy, ok := g.apiKeyAuthorizer.Lookup(method, path)
		require.True(t, ok, "路由 %s 未声明 API Key 策略", key)
		require.Contains(t, policy.Capabilities, types.APIKeyCapabilityManageSOP, key)
	}
}

// SOP 路由对未认证调用返回 401/403（守卫生效），不会直接落到 handler。
func TestSOPRoutesRequireRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := handler.NewSOPHandler(handler.SOPHandlerDeps{})
	g := &rbacGuards{cfg: testSOPConfig()}
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	RegisterSOPRoutes(r.Group("/api/v1"), h, g)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/sop/projects", nil))
	require.NotEqual(t, http.StatusOK, w.Code, "缺角色时不得放行")
}

// splitRouteKey 把 "METHOD /path" 拆成两段。
func splitRouteKey(t *testing.T, key string) (string, string) {
	t.Helper()
	method, path, ok := strings.Cut(key, " ")
	require.True(t, ok, "路由键格式错误：%s", key)
	return method, path
}

// testSOPConfig 打开 RBAC，让角色守卫真的拦截。
func testSOPConfig() *config.Config {
	enabled := true
	return &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}
}

// 事项路由同样必须声明能力：读→read_sop，写→manage_sop。
func TestSOPItemRoutesDeclareCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := handler.NewSOPHandler(handler.SOPHandlerDeps{})
	g := &rbacGuards{cfg: testSOPConfig()}
	v1 := gin.New().Group("/api/v1")
	RegisterSOPItemRoutes(v1, h, g)

	for _, key := range []string{
		"GET /api/v1/sop/projects/:id/items",
		"GET /api/v1/sop/items/:id",
		"GET /api/v1/sop/items/:id/attachments",
		"GET /api/v1/sop/items/:id/clarifications",
		"GET /api/v1/sop/items/:id/activities",
	} {
		method, path := splitRouteKey(t, key)
		policy, ok := g.apiKeyAuthorizer.Lookup(method, path)
		require.True(t, ok, "路由 %s 未声明 API Key 策略", key)
		require.Contains(t, policy.Capabilities, types.APIKeyCapabilityReadSOP, key)
	}
	for _, key := range []string{
		"POST /api/v1/sop/projects/:id/items",
		"PUT /api/v1/sop/items/:id",
		"DELETE /api/v1/sop/items/:id",
		"PATCH /api/v1/sop/items/:id/status",
		"POST /api/v1/sop/items/bulk-status",
		"POST /api/v1/sop/items/:id/attachments",
		"POST /api/v1/sop/items/:id/clarifications",
		"PATCH /api/v1/sop/items/:id/clarifications/:cid/answer",
	} {
		method, path := splitRouteKey(t, key)
		policy, ok := g.apiKeyAuthorizer.Lookup(method, path)
		require.True(t, ok, "路由 %s 未声明 API Key 策略", key)
		require.Contains(t, policy.Capabilities, types.APIKeyCapabilityManageSOP, key)
	}
}

// 模板路由能力声明：读→read_sop，写→manage_sop。
func TestSOPTemplateRoutesDeclareCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := handler.NewSOPHandler(handler.SOPHandlerDeps{})
	g := &rbacGuards{cfg: testSOPConfig()}
	RegisterSOPTemplateRoutes(gin.New().Group("/api/v1"), h, g)

	reads := []string{
		"GET /api/v1/sop/templates",
		"GET /api/v1/sop/templates/:id",
		"GET /api/v1/sop/templates/:id/revisions",
		"GET /api/v1/sop/revisions/:id",
	}
	writes := []string{
		"POST /api/v1/sop/templates",
		"PUT /api/v1/sop/templates/:id",
		"DELETE /api/v1/sop/templates/:id",
		"POST /api/v1/sop/templates/:id/publish",
		"POST /api/v1/sop/templates/:id/copy",
	}
	for _, key := range reads {
		method, path := splitRouteKey(t, key)
		policy, ok := g.apiKeyAuthorizer.Lookup(method, path)
		require.True(t, ok, "路由 %s 未声明策略", key)
		require.Contains(t, policy.Capabilities, types.APIKeyCapabilityReadSOP, key)
	}
	for _, key := range writes {
		method, path := splitRouteKey(t, key)
		policy, ok := g.apiKeyAuthorizer.Lookup(method, path)
		require.True(t, ok, "路由 %s 未声明策略", key)
		require.Contains(t, policy.Capabilities, types.APIKeyCapabilityManageSOP, key)
	}
}
