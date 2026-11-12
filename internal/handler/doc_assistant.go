package handler

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	stderrors "errors"
	"regexp"
	"strconv"
	"strings"

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
	// archiveFiller 给全局文档列表补工作台归档状态（P2-5）；未注入时按未归档展示。
	filler interfaces.DocArchiveStatusFiller
}

// NewDocAssistantHandler 创建文档助手 handler
func NewDocAssistantHandler(service interfaces.DocAssistantService) *DocAssistantHandler {
	return &DocAssistantHandler{service: service}
}

// WithDocArchiveFiller 注入归档状态填充器，返回自身便于链式装配。
//
// 用 Setter 而不是改构造签名：doc-assistant 的既有测试与路由只需 service，
// 归档徽标是工作台侧能力，缺失时应降级而不是让整个 handler 构造失败。
func (h *DocAssistantHandler) WithDocArchiveFiller(filler interfaces.DocArchiveStatusFiller) *DocAssistantHandler {
	h.filler = filler
	return h
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

// ListAllDocuments godoc
// @Summary      全局章节文档列表
// @Description  owner 的全部章节文档（默认每个任务只取最新版本），带分页与标题搜索
// @Tags         文档助手
// @Produce      json
// @Param        latest_only query bool false "只取每个任务的最新版本，默认 true"
// @Param        q query string false "按任务主题搜索"
// @Param        cursor query string false "分页游标"
// @Param        limit query int false "每页条数，默认 30，最大 100"
// @Success      200  {object}  map[string]interface{} "文档摘要分页"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /doc-assistant/documents [get]
func (h *DocAssistantHandler) ListAllDocuments(c *gin.Context) {
	ctx := c.Request.Context()
	// latest_only 默认 true：P2 的列表语义是「每个任务一份文档」，
	// 显式传 latest_only=false 才展开全部历史版本。
	latestOnly := true
	if raw, ok := c.GetQuery("latest_only"); ok && raw != "" {
		latestOnly = raw == "1" || strings.EqualFold(raw, "true")
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	page, err := h.service.ListAllDocuments(ctx, types.DocDocumentFilter{
		Query:      strings.TrimSpace(c.Query("q")),
		LatestOnly: latestOnly,
		Cursor:     c.Query("cursor"),
		Limit:      limit,
	})
	if err != nil {
		h.handleError(c, err)
		return
	}
	// 归档徽标失败只影响展示，列表仍要返回（与工作台条目列表同策略）。
	if h.filler != nil && len(page.Documents) > 0 {
		if fillErr := h.filler.FillDocArchiveStatus(ctx, page.Documents); fillErr != nil {
			logger.Warnf(ctx, "[DocAssistant] failed to fill doc archive status: %v", fillErr)
			for _, doc := range page.Documents {
				doc.ArchiveStatus = "none"
				doc.ArchivedVersion = 0
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": page})
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
	c.Header("Content-Disposition", exportContentDisposition(h.exportBaseName(ctx, doc)+".md"))
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
	c.Header("Content-Disposition", exportContentDisposition(h.exportBaseName(ctx, doc)+".docx"))
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", data)
}

// exportNameInvalidRe 导出文件名中的非法字符（Windows 保留字符与换行等）
var exportNameInvalidRe = regexp.MustCompile(`[\\/:*?"<>|\r\n\t]+`)

// exportBaseName 导出文件基础名：与文档名称（生成任务主题）保持一致，异常时回退 document-v版本号
func (h *DocAssistantHandler) exportBaseName(ctx context.Context, doc *types.DocDocument) string {
	if doc != nil && doc.TaskID != "" {
		if task, err := h.service.GetTask(ctx, doc.TaskID); err == nil && task != nil {
			name := strings.TrimSpace(exportNameInvalidRe.ReplaceAllString(task.Theme, "_"))
			if name = strings.Trim(name, ". "); name != "" {
				if runes := []rune(name); len(runes) > 100 {
					name = string(runes[:100])
				}
				return name
			}
		}
	}
	return "document-v" + strconv.Itoa(doc.Version)
}

// exportContentDisposition 生成 Content-Disposition（非 ASCII 文件名自动使用 RFC 2184 编码）
func exportContentDisposition(filename string) string {
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); v != "" {
		return v
	}
	return "attachment; filename=export"
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
	TemplateID       string   `json:"template_id"`
	KbIDs            []string `json:"kb_ids" binding:"required,min=1"`
	ModelID          string   `json:"model_id"`
	Theme            string   `json:"theme" binding:"required"`
	ExtraRequirement string   `json:"extra_requirement"`
	// OutputKBID 自动归档目标（§6.9）；为空时由服务端懒创建工作知识库。
	OutputKBID string `json:"output_kb_id"`
	// AutoArchive 不传时按 true 处理：生成的资料默认归到工作知识库，
	// 显式传 false 才关闭（§6.9 默认值口径）。
	AutoArchive *bool `json:"auto_archive"`
}

func (r *createGenerationRequest) toType() *types.DocGenerationTask {
	kbJSON, _ := json.Marshal(r.KbIDs)
	autoArchive := true
	if r.AutoArchive != nil {
		autoArchive = *r.AutoArchive
	}
	return &types.DocGenerationTask{
		TemplateID:       r.TemplateID,
		KbIDs:            types.JSON(kbJSON),
		ModelID:          r.ModelID,
		Theme:            r.Theme,
		ExtraRequirement: r.ExtraRequirement,
		OutputKBID:       r.OutputKBID,
		AutoArchive:      autoArchive,
	}
}

type updateChapterRequest struct {
	Content string `json:"content" binding:"required"`
}
