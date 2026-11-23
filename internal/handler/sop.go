// sop.go 是 SOP 工作台的 HTTP 入口：项目 CRUD 与仓库绑定。
//
// 约定（与 pm_project.go 同构，前端可直接复用现有包络）：
//   - 成功：`{"success": true, "data": ...}`；错误：经 c.Error 交给全局 ErrorHandler；
//   - 404 用 gorm.ErrRecordNotFound 识别；业务校验类错误（编号非法、还有进行中的运行、
//     仓库重复等）一律 400，错误原文就是给用户看的中文；
//   - 租户缺失时直接 400：仓储把 tenantID==0 当引擎内部的跨租户直通，HTTP 入口绝不能转发 0。
//
// 本文件只放项目组；事项/模板/运行/审批等端点由 T22–T26 追加到同一 handler。
package handler

import (
	"context"
	stderrors "errors"
	"io"
	"net/http"
	"strings"

	"fmt"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/service/sop"
	sopengine "github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// SOPHandler SOP 工作台端点聚合，后续任务的端点在这里继续挂。
type SOPHandler struct {
	projects interfaces.SopProjectRepository
	runs     interfaces.SopRunRepository
	// items 是事项服务（T22 接线；未注入时事项端点返回「未就绪」）。
	items *sop.ItemService
	// templates 是模板服务（T23 接线）。
	templates *sop.TemplateService
	// runs2 是运行业务（T24 接线）；engine 承载运行级/节点级人工操作。
	runs2  *sop.RunService
	engine *sopengine.Engine
	hub    *sop.EventHub
	// ai 是 AI 助手能力（T25）；airunner 是它背后的问答执行器。
	ai       *sop.AIService
	airunner sop.Runner
	// 管理端点依赖（T26）：审批中心聚合、触发配置/历史、CLI 凭据、归档摘要。
	approvals *sop.ApprovalService
	triggers  interfaces.SopTriggerRepository
	creds     interfaces.SopCredentialRepository
	// credSvc 是凭据业务服务（T34）：与仓储二选一，优先用它。
	credSvc    *sop.CredentialService
	trigger    *sop.TriggerService
	archive    *sop.ArchiveService
	credDelete func(ctx context.Context, tenantID uint64, id string) error
}

// SOPHandlerDeps 是 handler 的依赖集合（后续任务继续加字段）。
type SOPHandlerDeps struct {
	Projects  interfaces.SopProjectRepository
	Runs      interfaces.SopRunRepository
	Items     *sop.ItemService
	Templates *sop.TemplateService
	RunSvc    *sop.RunService
	Engine    *sopengine.Engine
	Hub       *sop.EventHub
	AIService *sop.AIService
	AIRunner  sop.Runner
	Approvals *sop.ApprovalService
	Triggers  interfaces.SopTriggerRepository
	Creds     interfaces.SopCredentialRepository
	// CredSvc 是凭据业务服务（T34）；注入后凭据端点走它，否则回落到仓储直连。
	CredSvc *sop.CredentialService
	// Trigger 是触发服务（T35）：立即运行端点用它。
	Trigger *sop.TriggerService
	Archive *sop.ArchiveService
	// CredDelete 是凭据删除的实现（仓储没有 Delete，接线时给出）。
	CredDelete func(ctx context.Context, tenantID uint64, id string) error
}

// NewSOPHandler 构造 SOP handler。
func NewSOPHandler(d SOPHandlerDeps) *SOPHandler {
	return &SOPHandler{
		projects: d.Projects, runs: d.Runs, items: d.Items, templates: d.Templates,
		runs2: d.RunSvc, engine: d.Engine, hub: d.Hub,
		ai: d.AIService, airunner: d.AIRunner,
		approvals: d.Approvals, triggers: d.Triggers, creds: d.Creds, credSvc: d.CredSvc,
		trigger: d.Trigger,
		archive: d.Archive, credDelete: d.CredDelete,
	}
}

// RegisterSOPRoutes 把项目组挂到 /api/v1/sop 下（事项组在 RegisterSOPItemRoutes）。
func (h *SOPHandler) RegisterSOPRoutes(sopGroup *gin.RouterGroup) {
	p := sopGroup.Group("/projects")
	{
		p.POST("", h.CreateProject)
		p.GET("", h.ListProjects)
		p.GET("/:id", h.GetProject)
		p.PUT("/:id", h.UpdateProject)
		p.DELETE("/:id", h.DeleteProject)
		p.POST("/:id/repos", h.AddRepo)
		p.DELETE("/:id/repos/:name", h.RemoveRepo)
		p.PUT("/:id/repos/default", h.SetDefaultRepo)
	}
}

// ---- 包络助手（与 pmFail/pmBindJSON 同构）----

// sopFail 统一错误出口：404 → 400（业务/租户校验）→ 500。
func sopFail(c *gin.Context, err error, notFoundMsg string) {
	switch {
	case stderrors.Is(err, gorm.ErrRecordNotFound):
		c.Error(errors.NewNotFoundError(notFoundMsg))
	case sopIsClientError(err):
		c.Error(errors.NewBadRequestError(err.Error()))
	default:
		logger.ErrorWithFields(c.Request.Context(), err, map[string]interface{}{"path": c.FullPath()})
		c.Error(errors.NewInternalServerError("Failed to process SOP request"))
	}
}

// sopClientError 标记「可以直接展示给用户的校验失败」：由 handler 自己构造，
// 用类型识别而不是文案前缀——前缀匹配会随文案改动静默失效，也会把内部错误误判成 400。
type sopClientError struct{ msg string }

func (e sopClientError) Error() string { return e.msg }

// sopClientf 构造一条客户端可见的业务错误。
func sopClientf(format string, args ...any) error {
	return sopClientError{msg: fmt.Sprintf(format, args...)}
}

// sopSentinels 是 sop 服务层导出的业务错误：它们都由我们自己构造，直接按类型判断。
var sopSentinels = []error{
	sop.ErrSopItemTitleRequired, sop.ErrSopItemTooMany,
	sop.ErrSopAttachmentEmpty, sop.ErrSopAttachmentTooLarge, sop.ErrSopAttachmentType,
	sop.ErrSopClarificationOpen, sop.ErrSopIllegalTransition,
	sop.ErrSopTemplateReadonly, sop.ErrSopTemplateInvalid, sop.ErrSopTemplateNameRequired,
	sop.ErrSopTemplateUnpublished, sop.ErrSopRunInputMissing,
	sop.ErrSopAIModeInvalid, sop.ErrSopAIOutputInvalid,
}

// sopIsClientError 判断是否为业务校验错误（也接受 types 层直接给出的校验文案）。
func sopIsClientError(err error) bool {
	var clientErr sopClientError
	if stderrors.As(err, &clientErr) {
		return true
	}
	for _, sentinel := range sopSentinels {
		if stderrors.Is(err, sentinel) {
			return true
		}
	}
	// types 层与仓储层直接返回普通 error，文案固定且面向用户。
	msg := err.Error()
	for _, prefix := range []string{
		"仅支持 HTTPS 或 SSH Git 地址", "编号前缀须为", "该仓库已绑定", "仓库不存在", "请选择仓库",
	} {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}

// sopBindJSON 绑定请求体；空 body 视为未传参数。
func sopBindJSON(c *gin.Context, req any) bool {
	if err := c.ShouldBindJSON(req); err != nil && !stderrors.Is(err, io.EOF) {
		c.Error(errors.NewBadRequestError(err.Error()))
		return false
	}
	return true
}

// sopAbort 结束请求：业务校验错误统一 400，其它错误交给 sopFail 分流。
func sopAbort(c *gin.Context, err error) {
	if sopIsClientError(err) {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	sopFail(c, err, "")
}

// sopTenantID 取租户；缺失时写 400 并返回 0（调用方据此提前返回）。
func sopTenantID(c *gin.Context) uint64 {
	tenantID, _ := types.TenantIDFromContext(c.Request.Context())
	if tenantID == 0 {
		sopAbort(c, sopClientf("缺少租户上下文：无法处理 SOP 请求"))
	}
	return tenantID
}

// sopUserID 取当前用户（可能为空：系统调用/API Key）。
func sopUserID(c *gin.Context) string {
	userID, _ := types.UserIDFromContext(c.Request.Context())
	return userID
}

// sopProjectID 取路径里的项目 id。
func sopProjectID(c *gin.Context) string { return strings.TrimSpace(c.Param("id")) }

// ---- 请求体 ----

type sopProjectPayload struct {
	Name                   string                 `json:"name"`
	Description            string                 `json:"description"`
	Code                   string                 `json:"code"`
	Repos                  []types.SopProjectRepo `json:"repos"`
	KBID                   string                 `json:"kb_id"`
	DefaultTemplateID      string                 `json:"default_template_id"`
	DefaultExecutor        string                 `json:"default_executor"`
	OnRunSuccessItemStatus string                 `json:"on_run_success_item_status"`
	NotifyOnGateOrFail     *bool                  `json:"notify_on_gate_or_fail"`
	WorkspaceRoot          string                 `json:"workspace_root"`
}

// applyTo 把请求体合并进项目（只覆盖字段有意义的项）。
func (p sopProjectPayload) applyTo(proj *types.SopProject) {
	if name := strings.TrimSpace(p.Name); name != "" {
		proj.Name = name
	}
	if p.Description != "" {
		proj.Description = p.Description
	}
	if p.Code != "" {
		proj.Code = strings.ToUpper(strings.TrimSpace(p.Code))
	}
	if p.Repos != nil {
		proj.Repos = p.Repos
	}
	if p.KBID != "" {
		proj.KBID = p.KBID
	}
	if p.DefaultTemplateID != "" {
		proj.DefaultTemplateID = p.DefaultTemplateID
	}
	if p.DefaultExecutor != "" {
		proj.DefaultExecutor = p.DefaultExecutor
	}
	if p.OnRunSuccessItemStatus != "" {
		proj.OnRunSuccessItemStatus = p.OnRunSuccessItemStatus
	}
	if p.NotifyOnGateOrFail != nil {
		proj.NotifyOnGateOrFail = *p.NotifyOnGateOrFail
	}
	if p.WorkspaceRoot != "" {
		proj.WorkspaceRoot = p.WorkspaceRoot
	}
}

// ---- 项目 ----

// CreateProject 新建项目：校验编号 → 规范化仓库 → 落库。
func (h *SOPHandler) CreateProject(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	var req sopProjectPayload
	if !sopBindJSON(c, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		sopAbort(c, sopClientf("项目名称不能为空"))
		return
	}
	if sopUserID(c) == "" {
		sopAbort(c, sopClientf("缺少用户身份：无法确定项目负责人"))
		return
	}
	if err := types.ValidateSopProjectCode(req.Code); err != nil {
		sopAbort(c, err)
		return
	}
	for _, repo := range req.Repos {
		if err := types.ValidateSopRepoURL(repo.URL); err != nil {
			sopAbort(c, err)
			return
		}
	}
	// 编号与名称在租户内唯一（库里是 partial unique index，SQLite 的 AutoMigrate
	// 建不出来）：先明确查一遍，给用户一句能看懂的话，而不是撞约束后的驱动错误。
	if dup, err := h.sopProjectConflict(c, tenantID, "", req.Name, proj0Code(req)); err != nil {
		sopFail(c, err, "")
		return
	} else if dup != "" {
		sopAbort(c, sopClientError{msg: dup})
		return
	}
	proj := &types.SopProject{TenantID: tenantID, OwnerUserID: sopUserID(c)}
	req.applyTo(proj)
	if err := h.projects.Create(c.Request.Context(), proj); err != nil {
		sopFail(c, err, "")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": proj})
}

// ListProjects 列本租户全部项目。
func (h *SOPHandler) ListProjects(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	projects, err := h.projects.ListByTenant(c.Request.Context(), tenantID)
	if err != nil {
		sopFail(c, err, "")
		return
	}
	if projects == nil {
		projects = []types.SopProject{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": projects})
}

// GetProject 项目详情。
func (h *SOPHandler) GetProject(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	proj, err := h.projects.GetByID(c.Request.Context(), tenantID, sopProjectID(c))
	if err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": proj})
}

// UpdateProject 修改项目；编号创建后不可改（改编号会让既有事项编号前缀失效）。
func (h *SOPHandler) UpdateProject(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	proj, err := h.projects.GetByID(c.Request.Context(), tenantID, sopProjectID(c))
	if err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	var req sopProjectPayload
	if !sopBindJSON(c, &req) {
		return
	}
	if req.Code != "" && !strings.EqualFold(req.Code, proj.Code) {
		sopAbort(c, sopClientf("项目编号创建后不可修改"))
		return
	}
	for _, repo := range req.Repos {
		if err := types.ValidateSopRepoURL(repo.URL); err != nil {
			sopAbort(c, err)
			return
		}
	}
	if dup, err := h.sopProjectConflict(c, tenantID, proj.ID, req.Name, ""); err != nil {
		sopFail(c, err, "")
		return
	} else if dup != "" {
		sopAbort(c, sopClientError{msg: dup})
		return
	}
	req.applyTo(proj)
	if err := h.projects.Update(c.Request.Context(), proj); err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": proj})
}

// DeleteProject 软删项目：有进行中的运行先拦（避免把正在跑的运行悬空）。
func (h *SOPHandler) DeleteProject(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	id := sopProjectID(c)
	active, err := h.projects.CountActiveRuns(c.Request.Context(), tenantID, id)
	if err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	if active > 0 {
		sopAbort(c, sopClientf("该项目还有进行中的运行，请先取消或等待结束"))
		return
	}
	if err := h.projects.Delete(c.Request.Context(), tenantID, id); err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"deleted": true}})
}

// ---- 仓库绑定 ----

// AddRepo 绑定仓库：即时生效（无草稿态），返回入库后的绑定（含 id/默认标记）。
func (h *SOPHandler) AddRepo(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	var req types.SopProjectRepo
	if !sopBindJSON(c, &req) {
		return
	}
	added, err := h.projects.AddRepo(c.Request.Context(), tenantID, sopProjectID(c), req)
	if err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": added})
}

// RemoveRepo 解绑仓库：路径参数是仓库名或 id（前端列表里两者都拿得到）。
func (h *SOPHandler) RemoveRepo(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	proj, err := h.projects.GetByID(c.Request.Context(), tenantID, sopProjectID(c))
	if err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	key := strings.TrimSpace(c.Param("name"))
	repoID, err := findSopRepoID(proj.Repos, key)
	if err != nil {
		c.Error(errors.NewNotFoundError(err.Error()))
		return
	}
	if err := h.projects.RemoveRepo(c.Request.Context(), tenantID, proj.ID, repoID); err != nil {
		sopFail(c, err, "仓库不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"removed": true}})
}

// SetDefaultRepo 指定默认仓库（按名或 id 寻址）。
func (h *SOPHandler) SetDefaultRepo(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	var req struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	proj, err := h.projects.GetByID(c.Request.Context(), tenantID, sopProjectID(c))
	if err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	key := strings.TrimSpace(req.ID)
	if key == "" {
		key = strings.TrimSpace(req.Name)
	}
	repoID, err := findSopRepoID(proj.Repos, key)
	if err != nil {
		c.Error(errors.NewNotFoundError(err.Error()))
		return
	}
	if err := h.projects.SetDefaultRepo(c.Request.Context(), tenantID, proj.ID, repoID); err != nil {
		sopFail(c, err, "仓库不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"ok": true}})
}

// proj0Code 取请求里的编号（统一大写，与落库口径一致）。
func proj0Code(p sopProjectPayload) string {
	return strings.ToUpper(strings.TrimSpace(p.Code))
}

// sopProjectConflict 检查租户内的编号/名称冲突：返回给用户看的白话文案。
// skipID 是更新时的自身 id（不算冲突）；code/name 为空表示不检查该项。
func (h *SOPHandler) sopProjectConflict(
	c *gin.Context, tenantID uint64, skipID, name, code string,
) (string, error) {
	all, err := h.projects.ListByTenant(c.Request.Context(), tenantID)
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	for i := range all {
		other := &all[i]
		if other.ID == skipID {
			continue
		}
		if code != "" && strings.EqualFold(other.Code, code) {
			return "项目编号 " + code + " 已被占用，请换一个", nil
		}
		if name != "" && strings.EqualFold(strings.TrimSpace(other.Name), name) {
			return "项目名称「" + name + "」已存在，请换一个", nil
		}
	}
	return "", nil
}

// findSopRepoID 按 id 或名称找仓库（两者都试，名称忽略大小写）。
func findSopRepoID(repos []types.SopProjectRepo, key string) (string, error) {
	if key == "" {
		return "", stderrors.New("请选择仓库")
	}
	for _, r := range repos {
		if r.ID == key {
			return r.ID, nil
		}
	}
	for _, r := range repos {
		if strings.EqualFold(strings.TrimSpace(r.Name), key) {
			return r.ID, nil
		}
	}
	return "", stderrors.New("仓库不存在：" + key)
}

// RegisterItemRoutes 挂事项组：项目下的事项列表/新建，以及按事项 id 的详情/状态/
// 批量/附件/澄清/活动。由 router 在声明 API Key 策略后调用。
func (h *SOPHandler) RegisterItemRoutes(sopGroup *gin.RouterGroup) {
	items := sopGroup.Group("/items")
	{
		items.GET("/:id", h.GetItem)
		items.PUT("/:id", h.UpdateItem)
		items.DELETE("/:id", h.DeleteItem)
		items.PATCH("/:id/status", h.UpdateItemStatus)
		items.POST("/bulk-status", h.BulkUpdateItemStatus)
		items.POST("/:id/attachments", h.UploadItemAttachment)
		items.GET("/:id/attachments", h.ListItemAttachments)
		items.GET("/:id/clarifications", h.ListItemClarifications)
		items.POST("/:id/clarifications", h.CreateItemClarification)
		items.PATCH("/:id/clarifications/:cid/answer", h.AnswerItemClarification)
		items.GET("/:id/activities", h.ListItemActivities)
	}
	projItems := sopGroup.Group("/projects/:id/items")
	{
		projItems.POST("", h.CreateItem)
		projItems.GET("", h.ListItems)
	}
}

// RegisterTemplateRoutes 挂模板组（router 在声明 API Key 策略后调用；
// handler 包内测试也用它装配，避免各写一遍路径清单）。
func (h *SOPHandler) RegisterTemplateRoutes(sopGroup *gin.RouterGroup) {
	t := sopGroup.Group("/templates")
	{
		t.POST("", h.CreateTemplate)
		t.GET("", h.ListTemplates)
		t.GET("/:id", h.GetTemplate)
		t.PUT("/:id", h.UpdateTemplate)
		t.DELETE("/:id", h.DeleteTemplate)
		t.POST("/:id/publish", h.PublishTemplate)
		t.POST("/:id/copy", h.CopyTemplate)
		t.GET("/:id/revisions", h.ListTemplateRevisions)
	}
	sopGroup.GET("/revisions/:id", h.GetTemplateRevision)
}

// aiRunner 取 AI 问答执行器（未装配时报未就绪）。
func (h *SOPHandler) aiRunner() (sop.Runner, bool) {
	return h.airunner, h.airunner != nil
}

// RegisterAIRoutes 挂 AI 助手组（四个端点）。
func (h *SOPHandler) RegisterAIRoutes(sopGroup *gin.RouterGroup) {
	ai := sopGroup.Group("/ai")
	{
		ai.POST("/breakdown", h.BreakdownItems)
		ai.POST("/analyze-item", h.AnalyzeSopItem)
		ai.POST("/clarify-generate", h.GenerateItemClarifications)
		ai.POST("/orchestrate", h.OrchestrateSop)
	}
}
