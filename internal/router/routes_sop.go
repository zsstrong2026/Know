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

	// 项目黑板与简报（T62）：简报读、黑板读写、改绑知识库。
	read.GET("/:id/stats", g.Viewer(), h.ProjectStats)
	read.GET("/:id/brief", g.Viewer(), h.GetProjectBrief)
	read.GET("/:id/notes", g.Viewer(), h.ListProjectNotes)
	manage.POST("/:id/brief/render", g.Contributor(), h.RenderProjectBrief)
	manage.POST("/:id/notes", g.Contributor(), h.CreateProjectNote)
	manage.POST("/:id/kb", g.Contributor(), h.ReplaceProjectKB)
	manage.DELETE("/:id/kb", g.Contributor(), h.UnbindProjectKB)
}

// RegisterSOPNoteRoutes 挂黑板条目组（按 id 的编辑/删除，路径不在项目前缀下）。
func RegisterSOPNoteRoutes(v1 *gin.RouterGroup, h *handler.SOPHandler, g *rbacGuards) {
	run := g.apiKeyGroup(v1.Group("/sop"), apiKeyRunSOP(apiKeyFullAccess()))
	// 决策草稿是「让助手跑一次」，与 AI 组同一能力口径。
	run.POST("/projects/:id/notes/decision-draft", g.Contributor(), h.DecisionDraft)
	run.PATCH("/notes/:id", g.Contributor(), h.UpdateProjectNote)
	run.DELETE("/notes/:id", g.Contributor(), h.DeleteProjectNote)
}

// RegisterSOPItemRoutes 挂事项组：项目下的事项列表/新建与按事项 id 的其余端点。
//
// 与项目组一样按读写分两组声明 API Key 能力：列表/详情/附件列表/澄清列表/活动属
// read_sop，其余写操作属 manage_sop（T24 起 run_sop 用于发起运行与答复）。
func RegisterSOPItemRoutes(v1 *gin.RouterGroup, h *handler.SOPHandler, g *rbacGuards) {
	read := g.apiKeyGroup(v1.Group("/sop"), apiKeyReadSOP(apiKeyFullAccess()))
	manage := g.apiKeyGroup(v1.Group("/sop"), apiKeyManageSOP(apiKeyFullAccess()))

	read.GET("/projects/:id/items", g.Viewer(), h.ListItems)
	read.GET("/items/:id", g.Viewer(), h.GetItem)
	read.GET("/items/:id/attachments", g.Viewer(), h.ListItemAttachments)
	read.GET("/items/:id/clarifications", g.Viewer(), h.ListItemClarifications)
	read.GET("/items/:id/activities", g.Viewer(), h.ListItemActivities)

	manage.POST("/projects/:id/items", g.Contributor(), h.CreateItem)
	manage.PUT("/items/:id", g.Contributor(), h.UpdateItem)
	manage.DELETE("/items/:id", g.Contributor(), h.DeleteItem)
	manage.PATCH("/items/:id/status", g.Contributor(), h.UpdateItemStatus)
	manage.POST("/items/bulk-status", g.Contributor(), h.BulkUpdateItemStatus)
	manage.POST("/items/:id/attachments", g.Contributor(), h.UploadItemAttachment)
	manage.POST("/items/:id/clarifications", g.Contributor(), h.CreateItemClarification)
	manage.PATCH("/items/:id/clarifications/:cid/answer", g.Contributor(), h.AnswerItemClarification)

	// 大需求拆分（T62）：拆子项、列子项、移出子项。
	read.GET("/items/:id/children", g.Viewer(), h.ListItemChildren)
	manage.POST("/items/:id/split", g.Contributor(), h.SplitItem)
	manage.POST("/items/:id/children/:cid/detach", g.Contributor(), h.DetachItemChild)
}

// apiKeyReadSOP 让带 read_sop 能力的 key 读取 SOP 工作台（项目/事项/运行）。
func apiKeyReadSOP(base middleware.APIKeyRoutePolicy) middleware.APIKeyRoutePolicy {
	return base.WithCapability(types.APIKeyCapabilityReadSOP)
}

// apiKeyRunSOP 让带 run_sop 能力的 key 发起运行、答复/审批节点（T24 起使用）。
func apiKeyRunSOP(base middleware.APIKeyRoutePolicy) middleware.APIKeyRoutePolicy {
	return base.WithCapability(types.APIKeyCapabilityRunSOP)
}

// apiKeyManageSOP 让带 manage_sop 能力的 key 管理 SOP 工作台（项目/事项/模板）。
//
// manage_sop 不顺带授予 read_sop：能力是并列的显式授权，读面覆盖运行历史与产物，
// 比写面更宽，必须单独给。
func apiKeyManageSOP(base middleware.APIKeyRoutePolicy) middleware.APIKeyRoutePolicy {
	return base.WithCapability(types.APIKeyCapabilityManageSOP)
}

// RegisterSOPTemplateRoutes 挂模板组：草稿 CRUD、发布、副本、发布历史与单条快照。
//
// 读路由声明 read_sop；写路由声明 manage_sop（发布/复制同样是写操作）。
func RegisterSOPTemplateRoutes(v1 *gin.RouterGroup, h *handler.SOPHandler, g *rbacGuards) {
	tpls := v1.Group("/sop")
	read := g.apiKeyGroup(tpls, apiKeyReadSOP(apiKeyFullAccess()))
	manage := g.apiKeyGroup(tpls, apiKeyManageSOP(apiKeyFullAccess()))

	// 能力声明与注册同源：经 apiKeyGroup 包装后的 group 直接注册 handler。
	// （apiKeyRouteGroup.handle 同时登记策略与真实路由。）
	read.GET("/templates", g.Viewer(), h.ListTemplates)
	read.GET("/templates/:id", g.Viewer(), h.GetTemplate)
	read.GET("/templates/:id/revisions", g.Viewer(), h.ListTemplateRevisions)
	read.GET("/revisions/:id", g.Viewer(), h.GetTemplateRevision)

	manage.POST("/templates", g.Contributor(), h.CreateTemplate)
	manage.PUT("/templates/:id", g.Contributor(), h.UpdateTemplate)
	manage.DELETE("/templates/:id", g.Contributor(), h.DeleteTemplate)
	manage.POST("/templates/:id/publish", g.Contributor(), h.PublishTemplate)
	manage.POST("/templates/:id/copy", g.Contributor(), h.CopyTemplate)
}

// RegisterSOPRunRoutes 挂运行组：创建/列表/统计/详情/SSE/产物/日志与全部人工操作。
//
// 读路由声明 read_sop，写路由声明 run_sop（发起运行与人工处置属于「跑」而不是
// 「管」：允许一个只负责执行的集成方不碰项目与模板的编辑面）。
func RegisterSOPRunRoutes(v1 *gin.RouterGroup, h *handler.SOPHandler, g *rbacGuards) {
	runs := v1.Group("/sop")
	read := g.apiKeyGroup(runs, apiKeyReadSOP(apiKeyFullAccess()))
	run := g.apiKeyGroup(runs, apiKeyRunSOP(apiKeyFullAccess()))

	read.GET("/runs", g.Viewer(), h.ListRuns)
	read.GET("/runs/stats/summary", g.Viewer(), h.RunStats)
	read.GET("/runs/:id", g.Viewer(), h.GetRun)
	read.GET("/runs/:id/events", g.Viewer(), h.RunEvents)
	read.GET("/runs/:id/artifacts", g.Viewer(), h.ListRunArtifacts)
	read.GET("/runs/:id/artifacts/:aid/file", g.Viewer(), h.DownloadArtifact)
	read.GET("/runs/:id/nodes/:nid/logs", g.Viewer(), h.NodeLogs)

	// 整条重触发属于「跑」：用事项模板新建运行（大项一律 400）。
	run.POST("/items/:id/relaunch", g.Contributor(), h.RelaunchItem)
	run.POST("/runs", g.Contributor(), h.CreateRun)
	run.POST("/runs/:id/retry", g.Contributor(), h.RetryRun)
	run.POST("/runs/:id/pause", g.Contributor(), h.PauseRun)
	run.POST("/runs/:id/resume", g.Contributor(), h.ResumeRunPaused)
	run.POST("/runs/:id/cancel", g.Contributor(), h.CancelRun)
	run.POST("/runs/:id/human-context", g.Contributor(), h.AddRunHumanContext)
	run.POST("/runs/:id/nodes/:nid/approve", g.Contributor(), h.ApproveNode)
	run.POST("/runs/:id/nodes/:nid/reject", g.Contributor(), h.RejectNode)
	run.POST("/runs/:id/nodes/:nid/rework", g.Contributor(), h.ReworkNode)
	run.POST("/runs/:id/nodes/:nid/terminate", g.Contributor(), h.TerminateNode)
	run.POST("/runs/:id/nodes/:nid/answer", g.Contributor(), h.AnswerNode)
	// 产物归档是「消费产物」的写操作：run_sop 足够（不属于配置管理）。
	run.POST("/runs/:id/artifacts/:aid/ingest", g.Contributor(), h.IngestArtifact)
}

// RegisterSOPAIRoutes 挂 AI 助手组：拆解/分析/澄清/编排，全部声明 run_sop
// （它们是「跑一次助手」，不是改配置）。
func RegisterSOPAIRoutes(v1 *gin.RouterGroup, h *handler.SOPHandler, g *rbacGuards) {
	ai := g.apiKeyGroup(v1.Group("/sop"), apiKeyRunSOP(apiKeyFullAccess()))
	ai.POST("/ai/breakdown", g.Contributor(), h.BreakdownItems)
	ai.POST("/ai/analyze-item", g.Contributor(), h.AnalyzeSopItem)
	ai.POST("/ai/clarify-generate", g.Contributor(), h.GenerateItemClarifications)
	ai.POST("/ai/orchestrate", g.Contributor(), h.OrchestrateSop)
}

// RegisterSOPAdminRoutes 挂管理组：审批中心聚合、触发配置与历史、CLI 凭据、
// PM 迁移归档摘要。（cli-status 与 run-now 由 T34/T35 追加，此处不注册。）
func RegisterSOPAdminRoutes(v1 *gin.RouterGroup, h *handler.SOPHandler, g *rbacGuards) {
	admin := v1.Group("/sop")
	read := g.apiKeyGroup(admin, apiKeyReadSOP(apiKeyFullAccess()))
	manage := g.apiKeyGroup(admin, apiKeyManageSOP(apiKeyFullAccess()))

	read.GET("/approvals", g.Viewer(), h.ListApprovals)
	read.GET("/projects/:id/trigger-config", g.Viewer(), h.GetTriggerConfig)
	read.GET("/projects/:id/trigger-logs", g.Viewer(), h.ListTriggerLogs)
	read.GET("/pm-archive/summary", g.Viewer(), h.PMArchiveSummary)
	// CLI 安装状态是只读探测（设置页展示），不需要管理权限。
	read.GET("/cli-status", g.Viewer(), h.CLIStatus)

	manage.PUT("/projects/:id/trigger-config", g.Contributor(), h.PutTriggerConfig)
	manage.POST("/projects/:id/trigger-config/run-now", g.Contributor(), h.RunNowTrigger)
	// 凭据涉及密钥：读也要 manage（列表会暴露「有哪些 CLI 配了密钥」）。
	manage.GET("/cli-credentials", g.Contributor(), h.ListCredentials)
	manage.PUT("/cli-credentials/:kind", g.Contributor(), h.PutCredential)
	manage.DELETE("/cli-credentials/:kind", g.Contributor(), h.DeleteCredential)
}
