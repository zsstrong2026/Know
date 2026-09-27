package handler

import (
	"encoding/json"
	"net/http"
	stderrors "errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/repository"
	docassistant "github.com/Tencent/WeKnora/internal/application/service/doc_assistant"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// DocAssistantHandler 文档助手 API
type DocAssistantHandler struct {
	service interfaces.DocAssistantService
}

// NewDocAssistantHandler 创建文档助手 handler
func NewDocAssistantHandler(service interfaces.DocAssistantService) *DocAssistantHandler {
	return &DocAssistantHandler{service: service}
}

// ---- 模板 ----

// CreateTemplate godoc
// @Summary      创建文档模板
// @Description  创建个人私有的章节骨架模板
// @Tags         文档助手
// @Accept       json
// @Produce      json
// @Param        body body createDocTemplateRequest true "模板"
// @Success      201  {object}  map[string]interface{} "模板"
// @Failure      400  {object}  errors.AppError "参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /doc-assistant/templates [post]
func (h *DocAssistantHandler) CreateTemplate(c *gin.Context) {
	ctx := c.Request.Context()
	var req createDocTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("模板参数不合法").WithDetails(err.Error()))
		return
	}
	tpl, err := h.service.CreateTemplate(ctx, req.toType())
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": tpl})
}

// UpdateTemplate godoc
func (h *DocAssistantHandler) UpdateTemplate(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	var req createDocTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("模板参数不合法").WithDetails(err.Error()))
		return
	}
	tpl := req.toType()
	tpl.ID = id
	updated, err := h.service.UpdateTemplate(ctx, tpl)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": updated})
}

// DeleteTemplate godoc
func (h *DocAssistantHandler) DeleteTemplate(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	if err := h.service.DeleteTemplate(ctx, id); err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetTemplate godoc
func (h *DocAssistantHandler) GetTemplate(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	tpl, err := h.service.GetTemplate(ctx, id)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": tpl})
}

// ListTemplates godoc
func (h *DocAssistantHandler) ListTemplates(c *gin.Context) {
	ctx := c.Request.Context()
	templates, err := h.service.ListTemplates(ctx)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": templates})
}

// ---- 生成任务 ----

// CreateGenerationTask godoc
func (h *DocAssistantHandler) CreateGenerationTask(c *gin.Context) {
	ctx := c.Request.Context()
	var req createGenerationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("生成参数不合法").WithDetails(err.Error()))
		return
	}
	task, err := h.service.CreateGenerationTask(ctx, req.toType())
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": task})
}

// ListTasks godoc
func (h *DocAssistantHandler) ListTasks(c *gin.Context) {
	ctx := c.Request.Context()
	tasks, err := h.service.ListTasks(ctx)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": tasks})
}

// GetTask godoc
func (h *DocAssistantHandler) GetTask(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	task, err := h.service.GetTask(ctx, id)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": task})
}

// DeleteTask godoc
func (h *DocAssistantHandler) DeleteTask(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	if err := h.service.DeleteTask(ctx, id); err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---- 文档 ----

// GetDocument godoc
func (h *DocAssistantHandler) GetDocument(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	doc, err := h.service.GetDocument(ctx, id)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": doc})
}

// ListDocuments godoc
func (h *DocAssistantHandler) ListDocuments(c *gin.Context) {
	ctx := c.Request.Context()
	taskID := secutils.SanitizeForLog(c.Param("id"))
	docs, err := h.service.ListDocuments(ctx, taskID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": docs})
}

// RegenerateChapter godoc
func (h *DocAssistantHandler) RegenerateChapter(c *gin.Context) {
	ctx := c.Request.Context()
	docID := secutils.SanitizeForLog(c.Param("id"))
	no, err := strconv.Atoi(c.Param("no"))
	if err != nil || no < 1 {
		c.Error(errors.NewBadRequestError("章节序号不合法"))
		return
	}
	doc, err := h.service.RegenerateChapter(ctx, docID, no)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": doc})
}

// UpdateChapterContent godoc
func (h *DocAssistantHandler) UpdateChapterContent(c *gin.Context) {
	ctx := c.Request.Context()
	docID := secutils.SanitizeForLog(c.Param("id"))
	no, err := strconv.Atoi(c.Param("no"))
	if err != nil || no < 1 {
		c.Error(errors.NewBadRequestError("章节序号不合法"))
		return
	}
	var req updateChapterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("章节内容不能为空").WithDetails(err.Error()))
		return
	}
	doc, err := h.service.UpdateChapterContent(ctx, docID, no, req.Content)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": doc})
}

// DeleteDocument godoc
func (h *DocAssistantHandler) DeleteDocument(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	if err := h.service.DeleteDocument(ctx, id); err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ExportMarkdown godoc
func (h *DocAssistantHandler) ExportMarkdown(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	doc, err := h.service.GetDocument(ctx, id)
	if err != nil {
		h.handleError(c, err)
		return
	}
	md, err := h.service.RenderMarkdown(ctx, doc)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.Header("Content-Disposition", "attachment; filename=document-v"+strconv.Itoa(doc.Version)+".md")
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(md))
}

// ExportWord godoc
func (h *DocAssistantHandler) ExportWord(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	doc, err := h.service.GetDocument(ctx, id)
	if err != nil {
		h.handleError(c, err)
		return
	}
	data, err := h.service.RenderWord(ctx, doc)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.Header("Content-Disposition", "attachment; filename=document-v"+strconv.Itoa(doc.Version)+".docx")
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", data)
}

// handleError 错误映射
func (h *DocAssistantHandler) handleError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, repository.ErrDocTemplateNotFound),
		stderrors.Is(err, repository.ErrDocTaskNotFound),
		stderrors.Is(err, repository.ErrDocDocumentNotFound):
		c.Error(errors.NewNotFoundError("资源不存在"))
	case stderrors.Is(err, docassistant.ErrInvalidTemplate),
		stderrors.Is(err, docassistant.ErrInvalidTask):
		c.Error(errors.NewBadRequestError(err.Error()))
	default:
		logger.Errorf(c.Request.Context(), "[DocAssistant] request failed: %v", err)
		c.Error(errors.NewInternalServerError(err.Error()))
	}
}

// ---- 请求 DTO ----

type createDocTemplateRequest struct {
	Name         string                        `json:"name" binding:"required"`
	Description  string                        `json:"description"`
	GlobalConfig types.DocTemplateGlobalConfig `json:"global_config"`
	Chapters     []types.DocTemplateChapter    `json:"chapters" binding:"required,min=1"`
}

func (r *createDocTemplateRequest) toType() *types.DocTemplate {
	chapters, _ := json.Marshal(r.Chapters)
	globalConfig, _ := json.Marshal(r.GlobalConfig)
	return &types.DocTemplate{
		Name:         r.Name,
		Description:  r.Description,
		GlobalConfig: types.JSON(globalConfig),
		Chapters:     types.JSON(chapters),
	}
}

type createGenerationRequest struct {
	TemplateID       string   `json:"template_id" binding:"required"`
	KbIDs            []string `json:"kb_ids" binding:"required,min=1"`
	ModelID          string   `json:"model_id"`
	Theme            string   `json:"theme" binding:"required"`
	ExtraRequirement string   `json:"extra_requirement"`
}

func (r *createGenerationRequest) toType() *types.DocGenerationTask {
	kbJSON, _ := json.Marshal(r.KbIDs)
	return &types.DocGenerationTask{
		TemplateID:       r.TemplateID,
		KbIDs:            types.JSON(kbJSON),
		ModelID:          r.ModelID,
		Theme:            r.Theme,
		ExtraRequirement: r.ExtraRequirement,
	}
}

type updateChapterRequest struct {
	Content string `json:"content" binding:"required"`
}
