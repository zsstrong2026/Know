package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// pmDateLayout 版本计划日期统一按天粒度传输
const pmDateLayout = "2006-01-02"

// pmParseDate 解析日期字符串：空串表示清空（nil），非法格式返回错误
func pmParseDate(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(pmDateLayout, raw)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func pmBadDate(c *gin.Context, field string, err error) {
	c.Error(errors.NewBadRequestError("invalid " + field + ": " + err.Error()))
}

type pmCreateVersionRequest struct {
	Name           string   `json:"name" binding:"required"`
	ReleaseNote    string   `json:"release_note"`
	Reviewers      string   `json:"reviewers"`
	OwnerID        string   `json:"owner_id"`
	PlanDate       string   `json:"plan_date"`
	PlanLaunchDate string   `json:"plan_launch_date"`
	RequirementIDs []string `json:"requirement_ids"`
}

// CreateVersion POST /api/v1/pm/projects/:id/versions
func (h *PMHandler) CreateVersion(c *gin.Context) {
	var req pmCreateVersionRequest
	if !pmBindJSON(c, &req) {
		return
	}
	planDate, err := pmParseDate(req.PlanDate)
	if err != nil {
		pmBadDate(c, "plan_date", err)
		return
	}
	planLaunchDate, err := pmParseDate(req.PlanLaunchDate)
	if err != nil {
		pmBadDate(c, "plan_launch_date", err)
		return
	}
	version, err := h.versionService.CreateVersion(
		c.Request.Context(), pmTenantID(c), &types.PMVersion{
			ProjectID:      c.Param("id"),
			Name:           req.Name,
			ReleaseNote:    req.ReleaseNote,
			Reviewers:      req.Reviewers,
			OwnerID:        req.OwnerID,
			PlanDate:       planDate,
			PlanLaunchDate: planLaunchDate,
		}, req.RequirementIDs,
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMProjectNotFound, "project not found")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": version})
}

// ListVersions GET /api/v1/pm/projects/:id/versions
func (h *PMHandler) ListVersions(c *gin.Context) {
	versions, err := h.versionService.ListVersions(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMProjectNotFound, "project not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": versions})
}

// GetVersion GET /api/v1/pm/versions/:id
func (h *PMHandler) GetVersion(c *gin.Context) {
	version, err := h.versionService.GetVersion(c.Request.Context(), pmTenantID(c), c.Param("id"))
	if err != nil {
		pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": version})
}

type pmUpdateVersionRequest struct {
	// 指针字段区分「未传」与显式赋值；日期传空串表示清空
	Name           *string `json:"name"`
	ReleaseNote    *string `json:"release_note"`
	Reviewers      *string `json:"reviewers"`
	OwnerID        *string `json:"owner_id"`
	PlanDate       *string `json:"plan_date"`
	PlanLaunchDate *string `json:"plan_launch_date"`
	Status         *string `json:"status"`
}

// UpdateVersion PATCH /api/v1/pm/versions/:id
func (h *PMHandler) UpdateVersion(c *gin.Context) {
	var req pmUpdateVersionRequest
	if !pmBindJSON(c, &req) {
		return
	}
	ctx, tenantID := c.Request.Context(), pmTenantID(c)
	version, err := h.versionService.GetVersion(ctx, tenantID, c.Param("id"))
	if err != nil {
		pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
		return
	}

	// 服务层 Update 为全量语义，这里按入参逐字段合并当前值
	if req.Name != nil {
		version.Name = *req.Name
	}
	if req.ReleaseNote != nil {
		version.ReleaseNote = *req.ReleaseNote
	}
	if req.Reviewers != nil {
		version.Reviewers = *req.Reviewers
	}
	if req.OwnerID != nil {
		version.OwnerID = *req.OwnerID
	}
	if req.PlanDate != nil {
		if version.PlanDate, err = pmParseDate(*req.PlanDate); err != nil {
			pmBadDate(c, "plan_date", err)
			return
		}
	}
	if req.PlanLaunchDate != nil {
		if version.PlanLaunchDate, err = pmParseDate(*req.PlanLaunchDate); err != nil {
			pmBadDate(c, "plan_launch_date", err)
			return
		}
	}
	if err := h.versionService.UpdateVersion(ctx, tenantID, version); err != nil {
		pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
		return
	}

	// 状态迁移单独走状态机入口，并级联关联需求
	if req.Status != nil && *req.Status != "" && *req.Status != version.Status {
		if err := h.versionService.UpdateStatus(ctx, tenantID, version.ID, *req.Status); err != nil {
			pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
			return
		}
	}

	// 状态变更可能重写版本号/定稿日期，统一回读最新结果
	latest, err := h.versionService.GetVersion(ctx, tenantID, version.ID)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": latest})
}

type pmReviewVersionRequest struct {
	Conclusion string     `json:"conclusion" binding:"required"`
	Opinions   types.JSON `json:"opinions"`
	// Reviewers 本轮参会人；为空沿用版本上已保存的参会人
	Reviewers string `json:"reviewers"`
}

// ReviewVersion POST /api/v1/pm/versions/:id/review
func (h *PMHandler) ReviewVersion(c *gin.Context) {
	var req pmReviewVersionRequest
	if !pmBindJSON(c, &req) {
		return
	}
	ctx, tenantID := c.Request.Context(), pmTenantID(c)

	// 评审记录会快照当前参会人，先落库再追加轮次
	if req.Reviewers != "" {
		version, err := h.versionService.GetVersion(ctx, tenantID, c.Param("id"))
		if err != nil {
			pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
			return
		}
		version.Reviewers = req.Reviewers
		if err := h.versionService.UpdateVersion(ctx, tenantID, version); err != nil {
			pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
			return
		}
	}

	version, err := h.versionService.Review(
		ctx, tenantID, c.Param("id"), req.Conclusion, req.Opinions,
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": version})
}

type pmFinalizeVersionRequest struct {
	// VersionNo 人工指定版本号，空串则按 minor 自动递增
	VersionNo string `json:"version_no"`
	// UpdateOutline 定稿时填写的更新内容大纲
	UpdateOutline string `json:"update_outline"`
}

// FinalizeVersion POST /api/v1/pm/versions/:id/finalize
func (h *PMHandler) FinalizeVersion(c *gin.Context) {
	var req pmFinalizeVersionRequest
	if !pmBindJSON(c, &req) {
		return
	}
	version, err := h.versionService.FinalizeWithInput(
		c.Request.Context(), pmTenantID(c), c.Param("id"), req.VersionNo, req.UpdateOutline,
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": version})
}

// ListVersionRequirements GET /api/v1/pm/versions/:id/requirements
func (h *PMHandler) ListVersionRequirements(c *gin.Context) {
	reqs, err := h.versionService.ListVersionRequirements(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": reqs})
}

type pmGenerateArtifactsRequest struct {
	Regenerate bool `json:"regenerate"`
	// Chapters 本次生成的 PRD 章节 key；为空表示全部章节
	Chapters []string `json:"chapters"`
}

// GenerateArtifacts POST /api/v1/pm/versions/:id/generate
// 服务层创建异步入队任务后立即返回 pending 任务，前端按 task_id 轮询章节进度。
func (h *PMHandler) GenerateArtifacts(c *gin.Context) {
	var req pmGenerateArtifactsRequest
	if !pmBindJSON(c, &req) {
		return
	}
	task, err := h.versionService.GenerateArtifacts(
		c.Request.Context(), pmTenantID(c), c.Param("id"), req.Regenerate, req.Chapters,
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": task})
}

type pmUpdateReviewOpinionRequest struct {
	Round  int    `json:"round" binding:"required"`
	Index  int    `json:"index"`
	Status string `json:"status" binding:"required"`
	Note   string `json:"note"`
}

// UpdateReviewOpinion PATCH /api/v1/pm/versions/:id/review-opinions
func (h *PMHandler) UpdateReviewOpinion(c *gin.Context) {
	var req pmUpdateReviewOpinionRequest
	if !pmBindJSON(c, &req) {
		return
	}
	version, err := h.versionService.UpdateReviewOpinion(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
		req.Round, req.Index, req.Status, req.Note,
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": version})
}

// RemoveVersionRequirement DELETE /api/v1/pm/versions/:id/requirements/:req_id
func (h *PMHandler) RemoveVersionRequirement(c *gin.Context) {
	err := h.versionService.RemoveRequirement(
		c.Request.Context(), pmTenantID(c), c.Param("id"), c.Param("req_id"),
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type pmShelveVersionRequirementRequest struct {
	Reason string `json:"reason" binding:"required"`
}

// ShelveVersionRequirement POST /api/v1/pm/versions/:id/requirements/:req_id/shelve
func (h *PMHandler) ShelveVersionRequirement(c *gin.Context) {
	var req pmShelveVersionRequirementRequest
	if !pmBindJSON(c, &req) {
		return
	}
	err := h.versionService.ShelveRequirement(
		c.Request.Context(), pmTenantID(c), c.Param("id"), c.Param("req_id"), req.Reason,
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type pmChangeVersionRequirementRequest struct {
	Priority    string `json:"priority"`
	Description string `json:"description"`
}

// ChangeVersionRequirement PATCH /api/v1/pm/versions/:id/requirements/:req_id
func (h *PMHandler) ChangeVersionRequirement(c *gin.Context) {
	var req pmChangeVersionRequirementRequest
	if !pmBindJSON(c, &req) {
		return
	}
	err := h.versionService.ChangeRequirement(
		c.Request.Context(), pmTenantID(c), c.Param("id"), c.Param("req_id"),
		req.Priority, req.Description,
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type pmReplaceVersionRequirementRequest struct {
	NewRequirementID string `json:"new_requirement_id" binding:"required"`
}

// ReplaceVersionRequirement POST /api/v1/pm/versions/:id/requirements/:req_id/replace
func (h *PMHandler) ReplaceVersionRequirement(c *gin.Context) {
	var req pmReplaceVersionRequirementRequest
	if !pmBindJSON(c, &req) {
		return
	}
	err := h.versionService.ReplaceRequirement(
		c.Request.Context(), pmTenantID(c), c.Param("id"), c.Param("req_id"),
		req.NewRequirementID,
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMVersionNotFound, "version not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
