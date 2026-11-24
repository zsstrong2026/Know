// sop_brief.go 是项目黑板与简报的 HTTP 端点：黑板条目读写、简报状态与手动重渲染。
//
// 口径（spec §3.10 / 全局约束 19）：
//   - 简报只是黑板的只读投影：GET /brief 返回渲染文本与水位，不改库；
//   - 手动重渲染要求已绑知识库（未绑 → 400 白话），黑板写入由服务层触发自动重渲染；
//   - AI 决策草稿只返回文本、不落库，人确认后走 POST /notes。
package handler

import (
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/sop"
	"github.com/Tencent/WeKnora/internal/types"
)

// GetProjectBrief 简报状态：正文 + 渲染水位 + 黑板最大 seq + 是否绑库。
func (h *SOPHandler) GetProjectBrief(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.brief == nil {
		return
	}
	status, err := h.brief.Status(c.Request.Context(), tenantID, sopProjectID(c))
	if err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": status})
}

// RenderProjectBrief 手动重新渲染并写入知识库（未绑库 → 400）。
func (h *SOPHandler) RenderProjectBrief(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.brief == nil {
		return
	}
	proj, err := h.projects.GetByID(c.Request.Context(), tenantID, sopProjectID(c))
	if err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	if strings.TrimSpace(proj.KBID) == "" {
		sopAbort(c, sopClientf("请先绑定知识库，简报才能同步"))
		return
	}
	if err := h.brief.EnsureInitial(c.Request.Context(), tenantID, proj.ID); err != nil {
		sopFail(c, err, "")
		return
	}
	status, err := h.brief.Status(c.Request.Context(), tenantID, proj.ID)
	if err != nil {
		sopFail(c, err, "")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": status})
}

// ListProjectNotes 列项目黑板（?kind=&pinned=&limit=）。
func (h *SOPHandler) ListProjectNotes(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.notes == nil {
		return
	}
	kind := strings.TrimSpace(c.Query("kind"))
	pinned := (*bool)(nil)
	if raw, ok := c.GetQuery("pinned"); ok {
		v := raw == "true" || raw == "1"
		pinned = &v
	}
	list, err := h.notes.ListFiltered(c.Request.Context(), tenantID, sopProjectID(c), kind, pinned,
		atoiOr(c.Query("limit"), 0))
	if err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// sopNotePayload 记一条/编辑的请求体。
type sopNotePayload struct {
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	ItemID string `json:"item_id"`
	RunID  string `json:"run_id"`
	Pinned *bool  `json:"pinned"`
}

// CreateProjectNote 记一条（来源固定 human）。
func (h *SOPHandler) CreateProjectNote(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.notes == nil {
		return
	}
	var req sopNotePayload
	if !sopBindJSON(c, &req) {
		return
	}
	if strings.TrimSpace(req.Body) == "" && strings.TrimSpace(req.Title) == "" {
		sopAbort(c, sopClientf("请填写标题或正文"))
		return
	}
	n := &types.SopProjectNote{
		ProjectID: sopProjectID(c), Kind: req.Kind, Title: req.Title, Body: req.Body,
		ItemID: req.ItemID, RunID: req.RunID,
	}
	if req.Pinned != nil {
		n.Pinned = *req.Pinned
	}
	if err := h.notes.Create(c.Request.Context(), tenantID, sopUserID(c), n); err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": n})
}

// UpdateProjectNote 编辑一条（kind/title/body/pinned）。
func (h *SOPHandler) UpdateProjectNote(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.notes == nil {
		return
	}
	n, err := h.notes.Get(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopFail(c, err, "记录不存在")
		return
	}
	var req sopNotePayload
	if !sopBindJSON(c, &req) {
		return
	}
	if req.Kind != "" {
		n.Kind = req.Kind
	}
	if req.Title != "" {
		n.Title = req.Title
	}
	if req.Body != "" {
		n.Body = req.Body
	}
	if req.Pinned != nil {
		n.Pinned = *req.Pinned
	}
	if err := h.notes.Edit(c.Request.Context(), tenantID, sopUserID(c), n); err != nil {
		sopFail(c, err, "记录不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": n})
}

// DeleteProjectNote 软删一条。
func (h *SOPHandler) DeleteProjectNote(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.notes == nil {
		return
	}
	if err := h.notes.SoftDelete(c.Request.Context(), tenantID, c.Param("id")); err != nil {
		sopFail(c, err, "记录不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"deleted": true}})
}

// DecisionDraft 生成决策草稿（不落库；无 AI 时给降级提示）。
func (h *SOPHandler) DecisionDraft(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.notes == nil {
		return
	}
	if h.ai == nil {
		sopAbort(c, sopClientf("AI 助手暂未启用，请直接填写决策内容"))
		return
	}
	draft, err := h.notes.DecisionDraft(c.Request.Context(), tenantID, sopProjectID(c), h.ai)
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"draft": draft}})
}

// ProjectStats 项目统计带：五态计数（父项按子项汇总）。
//
// 口径与列表一致（DeriveExecState/DeriveParentExecState），统计带必须是全量口径——
// 前端筛选只影响列表，不影响这里（spec §7「统计带」）。
func (h *SOPHandler) ProjectStats(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.items == nil {
		return
	}
	list, err := h.items.ListWithDerived(c.Request.Context(), tenantID, sopProjectID(c),
		types.SopItemFilter{})
	if err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	counts := map[string]int{
		types.SopExecStateNone: 0, types.SopExecStateRunning: 0, types.SopExecStateIntervene: 0,
		types.SopExecStatePR: 0, types.SopExecStateClosed: 0,
	}
	for _, it := range list {
		if it.ParentID != "" {
			continue // 子项汇总进父项，五项之和等于顶层事项数
		}
		if _, ok := counts[it.ExecState]; ok {
			counts[it.ExecState]++
		}
	}
	data := gin.H{"total": 0}
	sum := 0
	for k, v := range counts {
		data[k] = v
		sum += v
	}
	data["total"] = sum
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// ---- 拆分与子项 ----

// sopSplitPayload 拆分子事项的请求体。
type sopSplitPayload struct {
	Children []types.SopChildItemSpec `json:"children"`
	Source   string                   `json:"source"`
}

// SplitItem 拆分子事项（父项自动升级为大项）。
func (h *SOPHandler) SplitItem(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.items == nil {
		return
	}
	var req sopSplitPayload
	if !sopBindJSON(c, &req) {
		return
	}
	created, err := h.items.Split(c.Request.Context(), tenantID, sopUserID(c), c.Param("id"),
		req.Children, req.Source)
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": gin.H{"created": created}})
}

// ListItemChildren 列大项的子项（带派生字段）。
func (h *SOPHandler) ListItemChildren(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.items == nil {
		return
	}
	list, err := h.items.ListChildren(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopFail(c, err, "事项不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// DetachItemChild 把子项移出大项。
func (h *SOPHandler) DetachItemChild(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.items == nil {
		return
	}
	if err := h.items.DetachChild(c.Request.Context(), tenantID, sopUserID(c),
		c.Param("id"), c.Param("cid")); err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"detached": true}})
}

// ---- 项目知识库 ----

// ReplaceProjectKB 改绑项目知识库：旧的自建库只改名不删，新库触发简报全量重渲染。
func (h *SOPHandler) ReplaceProjectKB(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	var req struct {
		KBID string `json:"kb_id"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	proj, err := h.projects.GetByID(c.Request.Context(), tenantID, sopProjectID(c))
	if err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	target := strings.TrimSpace(req.KBID)
	if target == "" {
		sopAbort(c, sopClientf("请选择要绑定的知识库"))
		return
	}
	if target == proj.KBID {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": proj})
		return
	}
	// 旧的自建库不删（数据不可恢复），改名追加「（已迁移）」提示用户自行清理。
	if proj.KBOwner == types.SopKBOwnerProject && proj.KBID != "" && h.kbSvc != nil {
		if kb, err := h.kbSvc.GetKnowledgeBaseByID(c.Request.Context(), proj.KBID); err == nil && kb != nil &&
			!strings.Contains(kb.Name, "（已迁移）") {
			if _, err := h.kbSvc.UpdateKnowledgeBase(c.Request.Context(), kb.ID,
				kb.Name+"（已迁移）", kb.Description, nil, nil); err != nil {
				log.Printf("[sop-brief] 旧知识库 %s 改名失败：%v", kb.ID, err)
			}
		}
	}
	proj.KBID = target
	proj.KBOwner = types.SopKBOwnerLinked
	// 换库后旧的简报文档属于旧库，必须清空绑定并从头渲染。
	proj.BriefKnowledgeID = ""
	proj.BriefRenderedSeq = 0
	if err := h.projects.Update(c.Request.Context(), proj); err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	if h.brief != nil {
		if err := h.brief.EnsureInitial(c.Request.Context(), tenantID, proj.ID); err != nil {
			// 渲染失败不回滚绑定：绑定是事实，简报可稍后重试。
			log.Printf("[sop-brief] 改绑后简报渲染失败 project=%s：%v", proj.ID, err)
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": proj})
}

// RelaunchItem 整条重触发：用事项绑定的模板新建一次运行（旧运行保留为历史）。
//
// 大项（或有子项的父项）一律 400：双轨执行会让父子两次运行互相覆盖产物。
func (h *SOPHandler) RelaunchItem(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.items == nil || h.runs2 == nil {
		return
	}
	itemID := c.Param("id")
	it, err := h.items.Get(c.Request.Context(), tenantID, itemID)
	if err != nil {
		sopFail(c, err, "事项不存在")
		return
	}
	if err := h.items.EnsureNotBigParent(c.Request.Context(), tenantID, itemID); err != nil {
		sopAbort(c, err)
		return
	}
	templateID := strings.TrimSpace(it.TemplateID)
	if templateID == "" {
		sopAbort(c, sopClientf("该事项还没有关联流程模板"))
		return
	}
	run, err := h.runs2.Create(c.Request.Context(), tenantID, sopUserID(c), sop.CreateRunInput{
		TemplateID: templateID, ItemID: itemID,
	})
	if err != nil {
		sopFail(c, err, "")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": run})
}
