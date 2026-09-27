package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterDocAssistantRoutes 注册文档助手路由（模板/生成任务/文档/导出，个人私有）
func RegisterDocAssistantRoutes(r *gin.RouterGroup, handler *handler.DocAssistantHandler, g *rbacGuards) {
	if handler == nil {
		return
	}
	// 文档助手是个人私有资源：登录用户（Viewer+）即可操作，无需 KB 归属守卫。
	// API key 按 full access 能力位放行。
	group := g.apiKeyGroup(r.Group("/doc-assistant"), apiKeyRetrieve(apiKeyFullAccess()))
	{
		group.GET("/templates", g.Viewer(), handler.ListTemplates)
		group.POST("/templates", g.Viewer(), handler.CreateTemplate)
		group.GET("/templates/:id", g.Viewer(), handler.GetTemplate)
		group.PUT("/templates/:id", g.Viewer(), handler.UpdateTemplate)
		group.DELETE("/templates/:id", g.Viewer(), handler.DeleteTemplate)

		group.POST("/generate", g.Viewer(), handler.CreateGenerationTask)
		group.GET("/tasks", g.Viewer(), handler.ListTasks)
		group.GET("/tasks/:id", g.Viewer(), handler.GetTask)
		group.DELETE("/tasks/:id", g.Viewer(), handler.DeleteTask)
		group.GET("/tasks/:id/documents", g.Viewer(), handler.ListDocuments)

		group.GET("/documents/:id", g.Viewer(), handler.GetDocument)
		group.DELETE("/documents/:id", g.Viewer(), handler.DeleteDocument)
		group.POST("/documents/:id/chapters/:no/regenerate", g.Viewer(), handler.RegenerateChapter)
		group.PUT("/documents/:id/chapters/:no", g.Viewer(), handler.UpdateChapterContent)
		group.GET("/documents/:id/export/markdown", g.Viewer(), handler.ExportMarkdown)
		group.GET("/documents/:id/export/word", g.Viewer(), handler.ExportWord)
	}
}
