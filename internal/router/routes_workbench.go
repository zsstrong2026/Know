package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
)

// WorkbenchFeatureFlags 是工作台子功能的发布门禁快照（§9.1）。
// 由 router 从 config 一次读出后传入，注册函数本身不读全局配置，便于测试。
type WorkbenchFeatureFlags struct {
	Templates bool
	Journals  bool
	AI        bool
}

// RegisterWorkbenchRoutes 注册工作台的条目、内容、版本与文件夹端点（JWT-only）。
//
// 工作台数据是“个人私有资源”：owner 由服务端从 ctx 取，路径与 query 里都没有
// owner 参数。这里刻意挂在裸 *gin.RouterGroup 上并显式 DenyAPIKeyPrincipal：
// API Key 一律拒绝，既能挡住任何有效 key，也避免它被 /api/v1 的 APIKeyGate
// 当成“未声明路由”而给出语义含糊的 403 提示。
//
// flags 是发布门禁（§9.1）：关闭的子功能不注册路由，已写入的数据仍可读。
func RegisterWorkbenchRoutes(
	r *gin.RouterGroup,
	itemHandler *handler.WorkbenchItemHandler,
	folderHandler *handler.WorkbenchFolderHandler,
	archiveHandler *handler.WorkbenchArchiveHandler,
	journalHandler *handler.WorkbenchJournalHandler,
	noticeHandler *handler.WorkbenchNoticeHandler,
	aiHandler *handler.WorkbenchAIHandler,
	templateHandler *handler.WorkbenchTemplateHandler,
	assetHandler *handler.WorkbenchAssetHandler,
	flags WorkbenchFeatureFlags,
	g *rbacGuards,
) {
	if itemHandler == nil {
		return
	}
	group := r.Group("/workbench", middleware.DenyAPIKeyPrincipal())
	{
		// 首屏聚合：确保工作知识库存在并返回计数与配额。
		group.GET("/bootstrap", g.Viewer(), itemHandler.Bootstrap)

		group.GET("/items", g.Viewer(), itemHandler.ListItems)
		group.POST("/items", g.Viewer(), itemHandler.CreateItem)
		group.GET("/items/:id", g.Viewer(), itemHandler.GetItem)
		group.PUT("/items/:id", g.Viewer(), itemHandler.UpdateItem)
		group.DELETE("/items/:id", g.Viewer(), itemHandler.DeleteItem)
		group.POST("/items/:id/restore", g.Viewer(), itemHandler.RestoreItem)

		group.GET("/items/:id/content", g.Viewer(), itemHandler.GetContent)
		group.PUT("/items/:id/content", g.Viewer(), itemHandler.SaveContent)

		// 标签（P4-2）：个人资源，与条目同一套隔离；覆盖式设置条目标签
		group.GET("/tags", g.Viewer(), itemHandler.ListTags)
		group.POST("/tags", g.Viewer(), itemHandler.CreateTag)
		group.PUT("/tags/:id", g.Viewer(), itemHandler.UpdateTag)
		group.DELETE("/tags/:id", g.Viewer(), itemHandler.DeleteTag)
		group.GET("/items/:id/tags", g.Viewer(), itemHandler.ListItemTags)
		group.PUT("/items/:id/tags", g.Viewer(), itemHandler.SetItemTags)

		group.GET("/items/:id/versions", g.Viewer(), itemHandler.ListVersions)
		group.POST("/items/:id/versions/:version/restore", g.Viewer(), itemHandler.RestoreVersion)
	}
	if folderHandler != nil {
		group.GET("/folders", g.Viewer(), folderHandler.ListFolders)
		group.POST("/folders", g.Viewer(), folderHandler.CreateFolder)
		group.PUT("/folders/:id", g.Viewer(), folderHandler.UpdateFolder)
		// 拖拽排序 / 跨层移动（P4-2）：只改 parent_id 与层内顺序
		group.PUT("/folders/:id/move", g.Viewer(), folderHandler.MoveFolder)
		group.DELETE("/folders/:id", g.Viewer(), folderHandler.DeleteFolder)
	}
	if archiveHandler != nil {
		group.GET("/archives/state", g.Viewer(), archiveHandler.GetArchiveState)
		group.POST("/archives", g.Viewer(), archiveHandler.Archive)
		group.POST("/archives/refresh", g.Viewer(), archiveHandler.RefreshArchive)
		// 删除不用 DELETE + body：网关可能丢弃 body 导致误删全部 part。
		group.POST("/archives/delete", g.Viewer(), archiveHandler.DeleteArchive)
	}
	if journalHandler != nil && flags.Journals {
		group.GET("/journals/month", g.Viewer(), journalHandler.GetMonth)
		group.GET("/journals/:date", g.Viewer(), journalHandler.GetDay)
	}
	if noticeHandler != nil {
		// 离开预览需要租户（统计当前 workspace 的数据），因此放在 /workbench 下。
		group.GET("/leave-preview", g.Viewer(), noticeHandler.LeavePreview)
	}
	if aiHandler != nil && flags.AI {
		// 内联 AI：只做文字进文字出，不挂工具、不检索（§11.1）。
		group.POST("/ai/inline", g.Viewer(), aiHandler.Inline)
		// 问答车道：只查当前用户的私有工作 KB，检索范围由服务端强制（§11.8）。
		group.POST("/ai/ask", g.Viewer(), aiHandler.Ask)
		// 数据库 AI（P3-5）：自然语言 → 补丁，前端预览确认后再写入（§11.7）。
		group.POST("/ai/database", g.Viewer(), aiHandler.Database)
		// 画布 AI（P2-6）：自然语言 → 分组/平移补丁，同样先预览后写入。
		group.POST("/ai/canvas", g.Viewer(), aiHandler.Canvas)
	}
	if templateHandler != nil && flags.Templates {
		// 文档模板（§9.16）：个人资源，按 tenant_id + owner_id 隔离；
		// 没有 apply-template 端点——套用由客户端取快照后走普通保存链路。
		group.GET("/templates", g.Viewer(), templateHandler.ListTemplates)
		group.POST("/templates", g.Viewer(), templateHandler.CreateTemplate)
		group.PATCH("/templates/:id", g.Viewer(), templateHandler.UpdateTemplate)
		group.DELETE("/templates/:id", g.Viewer(), templateHandler.DeleteTemplate)
		group.GET("/templates/:id/content", g.Viewer(), templateHandler.GetTemplateContent)
	}
	if assetHandler != nil {
		// 资产（§12.1）：上传 multipart，下载只认 asset id。
		// touch 是引用上报，须在 /:asset_id 之前注册，避免被它当成 id 吞掉。
		group.POST("/items/:id/assets/touch", g.Viewer(), assetHandler.TouchItemAssets)
		group.POST("/items/:id/assets", g.Viewer(), assetHandler.UploadItemAsset)
		group.GET("/items/:id/assets", g.Viewer(), assetHandler.ListItemAssets)
		group.DELETE("/items/:id/assets/:asset_id", g.Viewer(), assetHandler.DeleteItemAsset)
		// 下载不带 item 前缀：BlockSuite 只知道 asset id，拼不出 item 路径。
		group.GET("/assets/:asset_id", g.Viewer(), assetHandler.DownloadAsset)
	}
}

// RegisterWorkbenchNoticeRoutes 注册用户级通知（tenantless）。
//
// 必须在 /api/v1 组上单独注册，而不是放进 RegisterWorkbenchRoutes：
// 被移出最后一个 workspace 后请求上下文里没有租户，挂在 /workbench 下会被
// Auth 中间件的 TENANT_REQUIRED 拦掉；这里同时把前缀加进 isTenantOptionalAPI。
func RegisterWorkbenchNoticeRoutes(r *gin.RouterGroup, noticeHandler *handler.WorkbenchNoticeHandler) {
	if noticeHandler == nil {
		return
	}
	me := r.Group("/me", middleware.DenyAPIKeyPrincipal())
	me.GET("/workbench-notices", noticeHandler.ListMyNotices)
	me.POST("/workbench-notices/read-all", noticeHandler.MarkAllNoticesRead)
	me.POST("/workbench-notices/:id/read", noticeHandler.MarkNoticeRead)
}
