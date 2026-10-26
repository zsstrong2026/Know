package handler

import (
	stderrors "errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PMHandler PM 工作站端点聚合：项目、规划、需求、版本、功能清单、任务轮询共用一个 handler，
// 避免 RouterParams 为每张表各加一个字段。
type PMHandler struct {
	projectService     interfaces.PMProjectService
	requirementService interfaces.PMRequirementService
	versionService     interfaces.PMVersionService
	featureService     interfaces.PMFeatureService
	roadmapService     interfaces.PMRoadmapService
	taskService        interfaces.PMTaskService
	materialService    interfaces.PMMaterialService
}

func NewPMHandler(
	projectService interfaces.PMProjectService,
	requirementService interfaces.PMRequirementService,
	versionService interfaces.PMVersionService,
	featureService interfaces.PMFeatureService,
	roadmapService interfaces.PMRoadmapService,
	taskService interfaces.PMTaskService,
	materialService interfaces.PMMaterialService,
) *PMHandler {
	return &PMHandler{
		projectService:     projectService,
		requirementService: requirementService,
		versionService:     versionService,
		featureService:     featureService,
		roadmapService:     roadmapService,
		taskService:        taskService,
		materialService:    materialService,
	}
}

// pmClientErrors 业务校验类错误统一映射 400；未命中且非 404 的错误按 500 处理，避免泄露内部细节。
var pmClientErrors = []error{
	service.ErrIllegalRequirementTransition,
	service.ErrRequirementNotLaunched,
	service.ErrRequirementNotReactivable,
	service.ErrRequirementValueInvalid,
	service.ErrRequirementNoteTooLong,
	service.ErrRequirementAnalysisState,
	service.ErrRequirementAnalysisInvalid,
	service.ErrRequirementTitleRequired,
	service.ErrRequirementFieldInvalid,
	service.ErrRequirementSessionUnready,
	service.ErrRequirementAttachmentEmpty,
	service.ErrRequirementAttachmentTooLarge,
	service.ErrRequirementAttachmentType,
	service.ErrVersionNotApproved,
	service.ErrVersionReviewClosed,
	service.ErrVersionConclusionInvalid,
	service.ErrVersionReviewInvalid,
	service.ErrIllegalVersionTransition,
	service.ErrVersionPlanDateInvalid,
	service.ErrVersionNotEditable,
	service.ErrVersionNoInvalid,
	service.ErrVersionNoDuplicate,
	service.ErrVersionOutlineTooLong,
	service.ErrVersionRequirementNotFound,
	service.ErrVersionRequirementInvalid,
	service.ErrVersionOpinionNotFound,
	service.ErrVersionOpinionInvalid,
	service.ErrVersionShelveReasonRequired,
	service.ErrVersionNoRequirements,
	service.ErrVersionKnowledgeUnready,
	service.ErrVersionTaskUnready,
	service.ErrFeaturePathInvalid,
	service.ErrFeatureOperationOrphan,
	service.ErrFeatureVersionRequired,
	service.ErrRoadmapExistingNotEmpty,
	service.ErrRoadmapDraftEmpty,
	service.ErrRoadmapDraftInvalid,
	service.ErrRoadmapDraftDuplicate,
	service.ErrRoadmapPlanTypeUnknown,
	service.ErrRoadmapBatchNotEditable,
	service.ErrRoadmapNotGenerated,
	service.ErrRoadmapSessionUnready,
	service.ErrPMTaskTitleRequired,
	service.ErrPMTaskTypeUnsupported,
	service.ErrMaterialTitleRequired,
	service.ErrMaterialTypeInvalid,
	service.ErrMaterialVersionRequired,
	service.ErrMaterialVersionInvalid,
	service.ErrMaterialNotDraft,
	service.ErrMaterialArchived,
	service.ErrMaterialAlreadyLatest,
	service.ErrMaterialNoLatestVersion,
	service.ErrMaterialTaskUnready,
	service.ErrMaterialContentEmpty,
	service.ErrMaterialKnowledgeUnready,
	service.ErrMaterialReadOnly,
}

// pmFail 统一错误出口：404 → 400（业务校验）→ 500（记录日志）。
func pmFail(c *gin.Context, err error, notFound error, notFoundMsg string) {
	switch {
	case notFound != nil && stderrors.Is(err, notFound):
		c.Error(errors.NewNotFoundError(notFoundMsg))
	case pmIsClientError(err):
		c.Error(errors.NewBadRequestError(err.Error()))
	default:
		logger.ErrorWithFields(c.Request.Context(), err, map[string]interface{}{"path": c.FullPath()})
		c.Error(errors.NewInternalServerError("Failed to process PM request"))
	}
}

func pmIsClientError(err error) bool {
	for _, sentinel := range pmClientErrors {
		if stderrors.Is(err, sentinel) {
			return true
		}
	}
	return false
}

// pmBindJSON 绑定请求体；空 body 视为未传参数，交由调用方取默认值。
func pmBindJSON(c *gin.Context, req any) bool {
	if err := c.ShouldBindJSON(req); err != nil && !stderrors.Is(err, io.EOF) {
		c.Error(errors.NewBadRequestError(err.Error()))
		return false
	}
	return true
}

func pmTenantID(c *gin.Context) uint64 {
	tenantID, _ := types.TenantIDFromContext(c.Request.Context())
	return tenantID
}

func pmUserID(c *gin.Context) string {
	userID, _ := types.UserIDFromContext(c.Request.Context())
	return userID
}

// ---- 项目 ----

type pmCreateProjectRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	// IsNew=false 时关联同名已有项目，true 才动态建知识库与常驻会话
	IsNew bool `json:"is_new"`
}

// CreateProject POST /api/v1/pm/projects
func (h *PMHandler) CreateProject(c *gin.Context) {
	var req pmCreateProjectRequest
	if !pmBindJSON(c, &req) {
		return
	}
	project, err := h.projectService.CreateProject(
		c.Request.Context(), pmTenantID(c), pmUserID(c), req.Name, req.IsNew,
	)
	if err != nil {
		pmFail(c, err, nil, "")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": project})
}

// ListProjects GET /api/v1/pm/projects?status=
func (h *PMHandler) ListProjects(c *gin.Context) {
	projects, err := h.projectService.ListProjects(c.Request.Context(), pmTenantID(c), c.Query("status"))
	if err != nil {
		pmFail(c, err, nil, "")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": projects})
}

// GetProject GET /api/v1/pm/projects/:id
func (h *PMHandler) GetProject(c *gin.Context) {
	project, err := h.projectService.GetProject(c.Request.Context(), pmTenantID(c), c.Param("id"))
	if err != nil {
		pmFail(c, err, apprepo.ErrPMProjectNotFound, "project not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": project})
}

type pmUpdateProjectRequest struct {
	// 指针区分「未传」（保留原值）与显式空串（清空描述）
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// UpdateProject PATCH /api/v1/pm/projects/:id
func (h *PMHandler) UpdateProject(c *gin.Context) {
	var req pmUpdateProjectRequest
	if !pmBindJSON(c, &req) {
		return
	}
	ctx, tenantID := c.Request.Context(), pmTenantID(c)
	cur, err := h.projectService.GetProject(ctx, tenantID, c.Param("id"))
	if err != nil {
		pmFail(c, err, apprepo.ErrPMProjectNotFound, "project not found")
		return
	}
	name, description := cur.Name, cur.Description
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		description = *req.Description
	}
	project, err := h.projectService.UpdateProject(ctx, tenantID, cur.ID, name, description)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMProjectNotFound, "project not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": project})
}

type pmArchiveProjectRequest struct {
	// 未传默认归档，显式 false 表示恢复
	Archived *bool `json:"archived"`
}

// ArchiveProject POST /api/v1/pm/projects/:id/archive
func (h *PMHandler) ArchiveProject(c *gin.Context) {
	var req pmArchiveProjectRequest
	if !pmBindJSON(c, &req) {
		return
	}
	archived := req.Archived == nil || *req.Archived
	if err := h.projectService.ArchiveProject(
		c.Request.Context(), pmTenantID(c), c.Param("id"), archived,
	); err != nil {
		pmFail(c, err, apprepo.ErrPMProjectNotFound, "project not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"archived": archived}})
}

// DeleteProject DELETE /api/v1/pm/projects/:id
func (h *PMHandler) DeleteProject(c *gin.Context) {
	if err := h.projectService.DeleteProject(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	); err != nil {
		pmFail(c, err, apprepo.ErrPMProjectNotFound, "project not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---- 功能规划：批次与路线图 ----

// ListPlanBatches GET /api/v1/pm/projects/:id/plan-batches
func (h *PMHandler) ListPlanBatches(c *gin.Context) {
	batches, err := h.roadmapService.ListBatches(c.Request.Context(), pmTenantID(c), c.Param("id"))
	if err != nil {
		pmFail(c, err, apprepo.ErrPMProjectNotFound, "project not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": batches})
}

type pmCreatePlanBatchRequest struct {
	BatchName string `json:"batch_name" binding:"required"`
	PlanType  string `json:"plan_type" binding:"required"`
	// VersionID 仅作前端契约保留：当前批次创建服务不支持关联版本，落库忽略该字段
	VersionID string `json:"version_id"`
}

// CreatePlanBatch POST /api/v1/pm/projects/:id/plan-batches
func (h *PMHandler) CreatePlanBatch(c *gin.Context) {
	var req pmCreatePlanBatchRequest
	if !pmBindJSON(c, &req) {
		return
	}
	batch, err := h.roadmapService.CreateBatch(
		c.Request.Context(), pmTenantID(c), c.Param("id"), req.BatchName, req.PlanType,
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMProjectNotFound, "project not found")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": batch})
}

// GetPlanBatch GET /api/v1/pm/plan-batches/:id
func (h *PMHandler) GetPlanBatch(c *gin.Context) {
	batch, err := h.roadmapService.GetBatch(c.Request.Context(), pmTenantID(c), c.Param("id"))
	if err != nil {
		pmFail(c, err, apprepo.ErrPMPlanBatchNotFound, "plan batch not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": batch})
}

type pmSavePlanDraftRequest struct {
	Draft       types.JSON `json:"draft" binding:"required"`
	CurrentStep int        `json:"current_step"`
}

// SavePlanDraft POST /api/v1/pm/plan-batches/:id/draft
func (h *PMHandler) SavePlanDraft(c *gin.Context) {
	var req pmSavePlanDraftRequest
	if !pmBindJSON(c, &req) {
		return
	}
	if err := h.roadmapService.SaveDraft(
		c.Request.Context(), pmTenantID(c), c.Param("id"), req.Draft, req.CurrentStep,
	); err != nil {
		pmFail(c, err, apprepo.ErrPMPlanBatchNotFound, "plan batch not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type pmGenerateRoadmapRequest struct {
	SyncFeatures bool `json:"sync_features"`
}

// GenerateRoadmap POST /api/v1/pm/plan-batches/:id/generate
// 服务层当前同步执行，返回已完成的 PMTask，前端仍按 task_id 轮询以兼容异步化。
func (h *PMHandler) GenerateRoadmap(c *gin.Context) {
	var req pmGenerateRoadmapRequest
	if !pmBindJSON(c, &req) {
		return
	}
	task, err := h.roadmapService.GenerateRoadmap(
		c.Request.Context(), pmTenantID(c), c.Param("id"), req.SyncFeatures,
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMPlanBatchNotFound, "plan batch not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": task})
}

// ActivatePlanBatch POST /api/v1/pm/plan-batches/:id/activate
func (h *PMHandler) ActivatePlanBatch(c *gin.Context) {
	if err := h.roadmapService.ActivateBatch(
		c.Request.Context(), pmTenantID(c), c.Param("id"), pmUserID(c),
	); err != nil {
		pmFail(c, err, apprepo.ErrPMPlanBatchNotFound, "plan batch not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeletePlanBatch DELETE /api/v1/pm/plan-batches/:id
// 仅草稿批次可删，已生效/已归档批次由服务层拦截。
func (h *PMHandler) DeletePlanBatch(c *gin.Context) {
	if err := h.roadmapService.DeleteBatch(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	); err != nil {
		pmFail(c, err, apprepo.ErrPMPlanBatchNotFound, "plan batch not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetPlanSnapshot GET /api/v1/pm/plan-batches/:id/snapshot
// 当前无知识库正文读取能力，返回快照指针，由前端走知识库文档接口打开。
func (h *PMHandler) GetPlanSnapshot(c *gin.Context) {
	batch, err := h.roadmapService.GetBatch(c.Request.Context(), pmTenantID(c), c.Param("id"))
	if err != nil {
		pmFail(c, err, apprepo.ErrPMPlanBatchNotFound, "plan batch not found")
		return
	}
	if batch.SnapshotKnowledgeID == "" {
		c.Error(errors.NewNotFoundError("snapshot not ready"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"batch_id":              batch.ID,
		"snapshot_knowledge_id": batch.SnapshotKnowledgeID,
		"roadmap_knowledge_id":  batch.RoadmapKnowledgeID,
		"snapshot_at":           batch.SnapshotAt,
		"generated_at":          batch.GeneratedAt,
	}})
}

// StartPlanGuide POST /api/v1/pm/plan-batches/:id/guide-session
// 为草稿批次创建隐藏的 AI 引导会话，返回会话 ID 与首轮指令。
func (h *PMHandler) StartPlanGuide(c *gin.Context) {
	sessionID, prompt, err := h.roadmapService.StartGuideSession(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMPlanBatchNotFound, "plan batch not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"session_id": sessionID,
		"prompt":     prompt,
	}})
}

// GetRoadmapGuide POST /api/v1/pm/projects/:id/roadmap/guide
// 返回项目常驻会话 + 当前批次（优先 active，其次最新 draft）的步骤与草稿，供引导页续接。
func (h *PMHandler) GetRoadmapGuide(c *gin.Context) {
	ctx, tenantID := c.Request.Context(), pmTenantID(c)
	project, err := h.projectService.GetProject(ctx, tenantID, c.Param("id"))
	if err != nil {
		pmFail(c, err, apprepo.ErrPMProjectNotFound, "project not found")
		return
	}
	batches, err := h.roadmapService.ListBatches(ctx, tenantID, project.ID)
	if err != nil {
		pmFail(c, err, nil, "")
		return
	}
	// 列表按 created_at DESC，优先取 active，否则取最新的草稿批次
	var current *types.PMPlanBatch
	for _, b := range batches {
		if b.Status == types.PlanBatchStatusActive {
			current = b
			break
		}
	}
	if current == nil && len(batches) > 0 {
		current = batches[0]
	}

	data := gin.H{"project_id": project.ID, "session_id": project.SessionID}
	if current != nil {
		data["batch_id"] = current.ID
		data["batch_name"] = current.BatchName
		data["plan_type"] = current.PlanType
		data["current_step"] = current.CurrentStep
		data["draft"] = current.Draft
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
