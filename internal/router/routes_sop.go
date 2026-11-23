package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
)

// RegisterSOPRoutes SOP 工作台路由（项目组；事项/模板/运行/审批由后续任务追加）。
//
// 权限口径（与计划一致）：
//   - read_sop    → Viewer 起（读项目/事项/运行）；
//   - run_sop     → Contributor 起（发起运行，T22 起使用）；
//   - manage_sop  → Contributor 起（项目与配置的增删改）。
//
// API Key 侧：读路由声明 read_sop，写路由声明 manage_sop；完整访问的 key 照旧直通，
// 未声明的路由由 API Key 门禁默认拒绝（语义不变）。
func RegisterSOPRoutes(v1 *gin.RouterGroup, h *handler.SOPHandler, g *rbacGuards) {
	// 读组与写组挂在同一 URL 前缀上，各自声明自己的 API Key 策略。
	read := g.apiKeyGroup(v1.Group("/sop/projects"), apiKeyReadSOP(apiKeyFullAccess()))
	manage := g.apiKeyGroup(v1.Group("/sop/projects"), apiKeyManageSOP(apiKeyFullAccess()))

	read.GET("", g.Viewer(), h.ListProjects)
	read.GET("/:id", g.Viewer(), h.GetProject)

	manage.POST("", g.Contributor(), h.CreateProject)
	manage.PUT("/:id", g.Contributor(), h.UpdateProject)
	manage.DELETE("/:id", g.Contributor(), h.DeleteProject)
	manage.POST("/:id/repos", g.Contributor(), h.AddRepo)
	manage.DELETE("/:id/repos/:name", g.Contributor(), h.RemoveRepo)
	manage.PUT("/:id/repos/default", g.Contributor(), h.SetDefaultRepo)
}

// apiKeyReadSOP 让带 read_sop 能力的 key 读取 SOP 工作台。
func apiKeyReadSOP(base middleware.APIKeyRoutePolicy) middleware.APIKeyRoutePolicy {
	return base.WithCapability(types.APIKeyCapabilityReadSOP)
}

// apiKeyRunSOP 让带 run_sop 能力的 key 发起运行、答复/审批节点（T22 起使用）。
func apiKeyRunSOP(base middleware.APIKeyRoutePolicy) middleware.APIKeyRoutePolicy {
	return base.WithCapability(types.APIKeyCapabilityRunSOP)
}

// apiKeyManageSOP 让带 manage_sop 能力的 key 管理 SOP 工作台（项目与配置）。
//
// 注意：manage_sop 不顺带授予 read_sop——能力是并列的显式授权，读权限要单独给，
// 与「manage_agents 含读」不同（SOP 的读面覆盖运行历史与产物，面更宽）。
func apiKeyManageSOP(base middleware.APIKeyRoutePolicy) middleware.APIKeyRoutePolicy {
	return base.WithCapability(types.APIKeyCapabilityManageSOP)
}
