package handler

import (
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/insight"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// WorkbenchInsightHandler 工作台「洞察」模块的 HTTP 入口。
// 所有端点都是个人资源：owner 由服务端从 ctx 取，路径与 query 都不接受 owner。
type WorkbenchInsightHandler struct {
	service *insight.Service
}

// NewWorkbenchInsightHandler 构造 handler。
func NewWorkbenchInsightHandler(service *insight.Service) *WorkbenchInsightHandler {
	return &WorkbenchInsightHandler{service: service}
}

// ListThemes GET /api/v1/workbench/insights/themes
func (h *WorkbenchInsightHandler) ListThemes(c *gin.Context) {
	result, err := h.service.ListThemes(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// CreateTheme POST /api/v1/workbench/insights/themes
func (h *WorkbenchInsightHandler) CreateTheme(c *gin.Context) {
	var req types.CreateInsightThemeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	theme, err := h.service.CreateTheme(c.Request.Context(), req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": theme})
}

// UpdateTheme PUT /api/v1/workbench/insights/themes/:id
func (h *WorkbenchInsightHandler) UpdateTheme(c *gin.Context) {
	var req types.CreateInsightThemeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	theme, err := h.service.UpdateTheme(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": theme})
}

// DeleteTheme DELETE /api/v1/workbench/insights/themes/:id
func (h *WorkbenchInsightHandler) DeleteTheme(c *gin.Context) {
	if err := h.service.DeleteTheme(c.Request.Context(), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// Checkup GET /api/v1/workbench/insights/themes/:id/checkup?folder_path=&recent_days=
func (h *WorkbenchInsightHandler) Checkup(c *gin.Context) {
	filter := types.InsightScopeFilter{
		FolderPath: strings.TrimSpace(c.Query("folder_path")),
		RecentDays: parseIntQuery(c, "recent_days"),
	}
	result, err := h.service.Checkup(c.Request.Context(), c.Param("id"), filter)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// ListThemeRuns GET /api/v1/workbench/insights/themes/:id/runs
func (h *WorkbenchInsightHandler) ListThemeRuns(c *gin.Context) {
	runs, err := h.service.ListRuns(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": runs})
}

// ImportTheme POST /api/v1/workbench/insights/theme-import
//
// 只解析方法包并返回草稿，不落库；是否创建由用户在表单里确认。
func (h *WorkbenchInsightHandler) ImportTheme(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.Error(apperrors.NewValidationError("method package file is required").WithDetails(err.Error()))
		return
	}
	if file.Size > 10<<20 {
		c.Error(apperrors.NewValidationError("method package is too large"))
		return
	}
	opened, err := file.Open()
	if err != nil {
		c.Error(apperrors.NewValidationError("cannot open method package").WithDetails(err.Error()))
		return
	}
	defer opened.Close()
	data, err := io.ReadAll(io.LimitReader(opened, 10<<20))
	if err != nil {
		c.Error(apperrors.NewValidationError("cannot read method package").WithDetails(err.Error()))
		return
	}
	draft, err := h.service.ImportThemePackage(file.Filename, data)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": draft})
}

// StartRun POST /api/v1/workbench/insights/themes/:id/runs
func (h *WorkbenchInsightHandler) StartRun(c *gin.Context) {
	var req types.StartInsightRunRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	run, err := h.service.StartRun(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": run})
}

// GetRun GET /api/v1/workbench/insights/runs/:id
func (h *WorkbenchInsightHandler) GetRun(c *gin.Context) {
	run, err := h.service.GetRun(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": run})
}

// DeleteRun DELETE /api/v1/workbench/insights/runs/:id
func (h *WorkbenchInsightHandler) DeleteRun(c *gin.Context) {
	if err := h.service.DeleteRun(c.Request.Context(), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// CancelRun POST /api/v1/workbench/insights/runs/:id/cancel
func (h *WorkbenchInsightHandler) CancelRun(c *gin.Context) {
	if err := h.service.CancelRun(c.Request.Context(), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListEvidence GET /api/v1/workbench/insights/runs/:id/evidence
func (h *WorkbenchInsightHandler) ListEvidence(c *gin.Context) {
	result, err := h.service.ListEvidence(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// ExcludeEvidence POST /api/v1/workbench/insights/runs/:id/evidence/exclude
func (h *WorkbenchInsightHandler) ExcludeEvidence(c *gin.Context) {
	var req types.ExcludeInsightEvidenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	if err := h.service.ExcludeEvidence(c.Request.Context(), c.Param("id"), req); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ConfirmRun POST /api/v1/workbench/insights/runs/:id/confirm
func (h *WorkbenchInsightHandler) ConfirmRun(c *gin.Context) {
	if err := h.service.ConfirmRun(c.Request.Context(), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListReports GET /api/v1/workbench/insights/themes/:id/reports
func (h *WorkbenchInsightHandler) ListReports(c *gin.Context) {
	reports, err := h.service.ListReports(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": reports})
}

// GetReport GET /api/v1/workbench/insights/reports/:id
func (h *WorkbenchInsightHandler) GetReport(c *gin.Context) {
	report, err := h.service.GetReport(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": report})
}

// DeleteReport DELETE /api/v1/workbench/insights/reports/:id
func (h *WorkbenchInsightHandler) DeleteReport(c *gin.Context) {
	if err := h.service.DeleteReport(c.Request.Context(), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetSelf GET /api/v1/workbench/insights/self
func (h *WorkbenchInsightHandler) GetSelf(c *gin.Context) {
	self, err := h.service.GetSelf(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": self})
}

// GenerateSelf POST /api/v1/workbench/insights/self/generate
func (h *WorkbenchInsightHandler) GenerateSelf(c *gin.Context) {
	if err := h.service.GenerateSelf(c.Request.Context()); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"ok": true}})
}

// GetPrivacy GET /api/v1/workbench/insights/privacy
func (h *WorkbenchInsightHandler) GetPrivacy(c *gin.Context) {
	privacy, err := h.service.GetPrivacy(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": privacy})
}

// UpdatePrivacy PUT /api/v1/workbench/insights/privacy
func (h *WorkbenchInsightHandler) UpdatePrivacy(c *gin.Context) {
	var req types.InsightPrivacy
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	privacy, err := h.service.UpdatePrivacy(c.Request.Context(), &req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": privacy})
}

// ListSelfMessages GET /api/v1/workbench/insights/self/messages
func (h *WorkbenchInsightHandler) ListSelfMessages(c *gin.Context) {
	items, err := h.service.ListSelfMessages(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    types.InsightSelfMessageList{Items: items},
	})
}

// AskSelf POST /api/v1/workbench/insights/self/ask
//
// 上下文限定为当前自我说明书与主题报告摘要；不重新检索工作知识库。
func (h *WorkbenchInsightHandler) AskSelf(c *gin.Context) {
	var req types.AskInsightSelfRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	answer, err := h.service.AskSelf(c.Request.Context(), req.Question)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    types.AskInsightReportResponse{Answer: answer},
	})
}

// ListReportMessages GET /api/v1/workbench/insights/reports/:id/messages
func (h *WorkbenchInsightHandler) ListReportMessages(c *gin.Context) {
	items, err := h.service.ListReportMessages(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    types.InsightReportMessageList{Items: items},
	})
}

// AskReport POST /api/v1/workbench/insights/reports/:id/ask
//
// 上下文限定为这份报告与它本次运行的证据；不重新检索工作知识库。
func (h *WorkbenchInsightHandler) AskReport(c *gin.Context) {
	var req types.AskInsightReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	answer, err := h.service.AskReport(c.Request.Context(), c.Param("id"), req.Question)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    types.AskInsightReportResponse{Answer: answer},
	})
}

// fail 统一错误出口：服务层已翻译成 AppError 的交给全局 ErrorHandler，
// 其余兜底为 500 并保留 details 供排查。
func (h *WorkbenchInsightHandler) fail(c *gin.Context, err error) {
	if _, ok := apperrors.IsAppError(err); ok {
		c.Error(err)
		return
	}
	c.Error(apperrors.NewInternalServerError("Failed to process insight request").WithDetails(err.Error()))
}
