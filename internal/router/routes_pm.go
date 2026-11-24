package router

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterPMRoutes PM 工作站路由（只读归档）。
//
// T58 起 PM 工作站并入 SOP 工作台：需求活数据在「事项」，PM 数据只作为冻结快照
// 供「PM 归档」页查看。因此这里只保留 GET 读端点；所有写端点统一回 410 白话提示，
// 而不是直接摘掉路由——旧前端/书签拿到「404 找不到」会当成网络故障，410 + 明确
// 文案才能告诉用户「去哪继续做」。
func RegisterPMRoutes(r *gin.RouterGroup, pmHandler *handler.PMHandler, g *rbacGuards) {
	pm := g.apiKeyGroup(r.Group("/pm"), apiKeyFullAccess())
	{
		// 写端点统一下线（保留路径与方法，回 410 白话）。
		gone := func(c *gin.Context) {
			c.JSON(http.StatusGone, gin.H{
				"success": false,
				"error": gin.H{
					"code":    http.StatusGone,
					"message": "PM 工作站已并入 SOP 工作台：需求与版本请在「SOP 工作台」处理，PM 数据仅保留只读归档",
				},
			})
		}
		pm.POST("/projects", gone)
		pm.PATCH("/projects/:id", gone)
		pm.DELETE("/projects/:id", gone)
		pm.POST("/projects/:id/archive", gone)
		pm.POST("/projects/:id/plan-batches", gone)
		pm.POST("/projects/:id/roadmap/guide", gone)
		pm.POST("/plan-batches/:id/draft", gone)
		pm.POST("/plan-batches/:id/guide-session", gone)
		pm.POST("/plan-batches/:id/generate", gone)
		pm.POST("/plan-batches/:id/activate", gone)
		pm.DELETE("/plan-batches/:id", gone)
		pm.POST("/projects/:id/requirements", gone)
		pm.PATCH("/requirements/:id", gone)
		pm.DELETE("/requirements/:id", gone)
		pm.POST("/requirements/:id/analyze", gone)
		pm.POST("/requirements/:id/analysis", gone)
		pm.POST("/requirements/:id/reactivate", gone)
		pm.POST("/requirements/:id/launch-verification", gone)
		pm.POST("/requirements/:id/attachments", gone)
		pm.DELETE("/requirements/:id/attachments/:attachment_id", gone)
		pm.POST("/projects/:id/versions", gone)
		pm.PATCH("/versions/:id", gone)
		pm.POST("/versions/:id/review", gone)
		pm.POST("/versions/:id/finalize", gone)
		pm.POST("/versions/:id/generate", gone)
		pm.PATCH("/versions/:id/review-opinions", gone)
		pm.DELETE("/versions/:id/requirements/:req_id", gone)
		pm.POST("/versions/:id/requirements/:req_id/shelve", gone)
		pm.PATCH("/versions/:id/requirements/:req_id", gone)
		pm.POST("/versions/:id/requirements/:req_id/replace", gone)
		pm.POST("/features", gone)
		pm.POST("/materials", gone)
		pm.POST("/materials/:id/generate", gone)
		pm.POST("/materials/:id/publish", gone)
		pm.POST("/materials/:id/update", gone)
		pm.DELETE("/materials/:id", gone)

		// ---- 只读端点（PM 归档页消费）----
		pm.GET("/projects", g.Viewer(), pmHandler.ListProjects)
		pm.GET("/projects/:id", g.OwnedPMProjectOrAdmin(), pmHandler.GetProject)

		// 功能规划：批次与路线图（只读）
		pm.GET("/projects/:id/plan-batches", g.OwnedPMProjectOrAdmin(), pmHandler.ListPlanBatches)
		pm.GET("/plan-batches/:id", g.Viewer(), pmHandler.GetPlanBatch)
		pm.GET("/plan-batches/:id/snapshot", g.Viewer(), pmHandler.GetPlanSnapshot)

		// 需求（只读）
		pm.GET("/projects/:id/requirements", g.OwnedPMProjectOrAdmin(), pmHandler.ListRequirements)
		pm.GET("/requirements/:id", g.Viewer(), pmHandler.GetRequirement)
		pm.GET("/requirements/:id/attachments", g.Viewer(), pmHandler.ListRequirementAttachments)
		pm.GET(
			"/requirements/:id/attachments/:attachment_id/download",
			g.Viewer(), pmHandler.DownloadRequirementAttachment,
		)

		// 版本（只读）
		pm.GET("/projects/:id/versions", g.OwnedPMProjectOrAdmin(), pmHandler.ListVersions)
		pm.GET("/versions/:id", g.Viewer(), pmHandler.GetVersion)
		pm.GET("/versions/:id/requirements", g.Viewer(), pmHandler.ListVersionRequirements)

		// 功能清单（只读）
		pm.GET("/projects/:id/features", g.OwnedPMProjectOrAdmin(), pmHandler.ListFeatures)
		pm.GET("/features/:id/iterations", g.Viewer(), pmHandler.ListFeatureIterations)

		// 项目资料（只读）
		pm.GET("/projects/:id/materials", g.OwnedPMProjectOrAdmin(), pmHandler.ListMaterials)
		pm.GET("/materials/:id", g.Viewer(), pmHandler.GetMaterial)
		pm.GET("/materials/:id/download", g.Viewer(), pmHandler.DownloadMaterial)

		// 通用异步任务（PM 归档页的生成进度查询；不再产生新任务）
		pm.GET("/tasks/:task_id", g.Viewer(), pmHandler.GetTask)
	}
}
