package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterPMRoutes PM 工作站路由。
// 项目级路径统一按项目归属校验（创建者或管理员）；需求/版本/功能点的详情
// 读写按租户角色下限控制。功能点 PATCH 与迭代 POST 暂无服务层能力，
// 待补齐后再注册，避免声明空路由。
func RegisterPMRoutes(r *gin.RouterGroup, pmHandler *handler.PMHandler, g *rbacGuards) {
	pm := g.apiKeyGroup(r.Group("/pm"), apiKeyFullAccess())
	{
		// ---- 项目 ----
		pm.POST("/projects", g.Contributor(), pmHandler.CreateProject)
		pm.GET("/projects", g.Viewer(), pmHandler.ListProjects)
		pm.GET("/projects/:id", g.OwnedPMProjectOrAdmin(), pmHandler.GetProject)
		pm.PATCH("/projects/:id", g.OwnedPMProjectOrAdmin(), pmHandler.UpdateProject)
		pm.DELETE("/projects/:id", g.OwnedPMProjectOrAdmin(), pmHandler.DeleteProject)
		pm.POST("/projects/:id/archive", g.OwnedPMProjectOrAdmin(), pmHandler.ArchiveProject)

		// ---- 功能规划：批次与路线图 ----
		pm.GET("/projects/:id/plan-batches", g.OwnedPMProjectOrAdmin(), pmHandler.ListPlanBatches)
		pm.POST("/projects/:id/plan-batches", g.OwnedPMProjectOrAdmin(), pmHandler.CreatePlanBatch)
		pm.POST("/projects/:id/roadmap/guide", g.OwnedPMProjectOrAdmin(), pmHandler.GetRoadmapGuide)
		pm.GET("/plan-batches/:id", g.Viewer(), pmHandler.GetPlanBatch)
		pm.POST("/plan-batches/:id/draft", g.Contributor(), pmHandler.SavePlanDraft)
		pm.POST("/plan-batches/:id/guide-session", g.Contributor(), pmHandler.StartPlanGuide)
		pm.POST("/plan-batches/:id/generate", g.Contributor(), pmHandler.GenerateRoadmap)
		pm.POST("/plan-batches/:id/activate", g.Contributor(), pmHandler.ActivatePlanBatch)
		pm.DELETE("/plan-batches/:id", g.Contributor(), pmHandler.DeletePlanBatch)
		pm.GET("/plan-batches/:id/snapshot", g.Viewer(), pmHandler.GetPlanSnapshot)

		// ---- 需求 ----
		pm.POST("/projects/:id/requirements", g.OwnedPMProjectOrAdmin(), pmHandler.CreateRequirement)
		pm.GET("/projects/:id/requirements", g.OwnedPMProjectOrAdmin(), pmHandler.ListRequirements)
		pm.GET("/requirements/:id", g.Viewer(), pmHandler.GetRequirement)
		pm.PATCH("/requirements/:id", g.Contributor(), pmHandler.UpdateRequirement)
		pm.DELETE("/requirements/:id", g.Contributor(), pmHandler.DeleteRequirement)
		pm.POST("/requirements/:id/analyze", g.Contributor(), pmHandler.AnalyzeRequirement)
		pm.POST("/requirements/:id/analysis", g.Contributor(), pmHandler.SaveRequirementAnalysis)
		pm.POST("/requirements/:id/reactivate", g.Contributor(), pmHandler.ReactivateRequirement)
		pm.POST("/requirements/:id/launch-verification", g.Contributor(), pmHandler.VerifyRequirementLaunch)
		// 需求附件：上传/删除需可写角色，列表/下载对只读角色开放
		pm.POST("/requirements/:id/attachments", g.Contributor(), pmHandler.UploadRequirementAttachment)
		pm.GET("/requirements/:id/attachments", g.Viewer(), pmHandler.ListRequirementAttachments)
		pm.DELETE("/requirements/:id/attachments/:attachment_id", g.Contributor(), pmHandler.DeleteRequirementAttachment)
		pm.GET(
			"/requirements/:id/attachments/:attachment_id/download",
			g.Viewer(), pmHandler.DownloadRequirementAttachment,
		)

		// ---- 版本 ----
		pm.POST("/projects/:id/versions", g.OwnedPMProjectOrAdmin(), pmHandler.CreateVersion)
		pm.GET("/projects/:id/versions", g.OwnedPMProjectOrAdmin(), pmHandler.ListVersions)
		pm.GET("/versions/:id", g.Viewer(), pmHandler.GetVersion)
		pm.PATCH("/versions/:id", g.Contributor(), pmHandler.UpdateVersion)
		pm.POST("/versions/:id/review", g.Contributor(), pmHandler.ReviewVersion)
		pm.POST("/versions/:id/finalize", g.Contributor(), pmHandler.FinalizeVersion)
		pm.GET("/versions/:id/requirements", g.Viewer(), pmHandler.ListVersionRequirements)
		pm.POST("/versions/:id/generate", g.Contributor(), pmHandler.GenerateArtifacts)
		pm.PATCH("/versions/:id/review-opinions", g.Contributor(), pmHandler.UpdateReviewOpinion)
		pm.DELETE("/versions/:id/requirements/:req_id", g.Contributor(), pmHandler.RemoveVersionRequirement)
		pm.POST("/versions/:id/requirements/:req_id/shelve", g.Contributor(), pmHandler.ShelveVersionRequirement)
		pm.PATCH("/versions/:id/requirements/:req_id", g.Contributor(), pmHandler.ChangeVersionRequirement)
		pm.POST("/versions/:id/requirements/:req_id/replace", g.Contributor(), pmHandler.ReplaceVersionRequirement)

		// ---- 功能清单 ----
		pm.GET("/projects/:id/features", g.OwnedPMProjectOrAdmin(), pmHandler.ListFeatures)
		pm.POST("/features", g.Contributor(), pmHandler.CreateFeature)
		pm.GET("/features/:id/iterations", g.Viewer(), pmHandler.ListFeatureIterations)

		// ---- 项目资料 ----
		pm.GET("/projects/:id/materials", g.OwnedPMProjectOrAdmin(), pmHandler.ListMaterials)
		pm.POST("/materials", g.Contributor(), pmHandler.CreateMaterial)
		pm.GET("/materials/:id", g.Viewer(), pmHandler.GetMaterial)
		pm.POST("/materials/:id/generate", g.Contributor(), pmHandler.GenerateMaterial)
		pm.POST("/materials/:id/publish", g.Contributor(), pmHandler.PublishMaterial)
		pm.POST("/materials/:id/update", g.Contributor(), pmHandler.UpdateMaterial)
		pm.DELETE("/materials/:id", g.Contributor(), pmHandler.DeleteMaterial)
		pm.GET("/materials/:id/download", g.Viewer(), pmHandler.DownloadMaterial)

		// ---- 通用异步任务 ----
		pm.GET("/tasks/:task_id", g.Viewer(), pmHandler.GetTask)
	}
}
