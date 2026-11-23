// sop_runs.go 是运行相关的 HTTP 端点：创建、列表、统计、详情聚合、SSE 直播、
// 产物预览/下载、节点日志，以及全部人工操作（重试/暂停/恢复/取消/补充上下文/
// 审批四操作/答复）。
//
// 关键口径：
//   - 租户必填：运行仓储把 tenantID==0 当引擎内部跨租户直通，HTTP 入口一律拒绝；
//   - SSE 只推事件本身，不负责补历史：前端断线重连后靠 GET /runs/:id 重新对齐；
//   - 产物与日志路径一律经 SafeWorkspacePath 解析，绝不允许逃出运行工作区。
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/service/sop"
	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/types"
)

// sopRunDetail 运行详情聚合 DTO（前端一次拿齐运行/节点/产物）。
type sopRunDetail struct {
	Run       *types.SopRun       `json:"run"`
	NodeRuns  []types.SopNodeRun  `json:"node_runs"`
	Artifacts []types.SopArtifact `json:"artifacts"`
	// ProjectContext 本次运行注入的任务书「项目现状」文本（T64 写入，本任务留空）。
	ProjectContext string `json:"project_context"`
}

// sopCreateRunRequest 创建运行的请求体。
type sopCreateRunRequest struct {
	TemplateID   string         `json:"template_id"`
	ItemID       string         `json:"item_id"`
	Input        map[string]any `json:"input"`
	TriggerKind  string         `json:"trigger_kind"`
	TriggerRefID string         `json:"trigger_ref_id"`
}

// CreateRun 从已发布模板创建运行。
func (h *SOPHandler) CreateRun(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.runs2 == nil {
		return
	}
	var req sopCreateRunRequest
	if !sopBindJSON(c, &req) {
		return
	}
	run, err := h.runs2.Create(c.Request.Context(), tenantID, sopUserID(c), sop.CreateRunInput{
		TemplateID: req.TemplateID, ItemID: req.ItemID, Input: req.Input,
		TriggerKind: req.TriggerKind, TriggerRefID: req.TriggerRefID,
	})
	if err != nil {
		sopFail(c, err, "模板不存在")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": run})
}

// ListRuns 列运行（?project_id&item_id&status&keyword&page&page_size）。
func (h *SOPHandler) ListRuns(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.runs2 == nil {
		return
	}
	page := atoiOr(c.Query("page"), 0)
	pageSize := atoiOr(c.Query("page_size"), 0)
	list, err := h.runs2.List(c.Request.Context(), tenantID, types.SopRunFilter{
		ProjectID: c.Query("project_id"),
		ItemID:    c.Query("item_id"),
		Status:    c.Query("status"),
		Q:         strings.TrimSpace(c.Query("keyword")),
	}, page, pageSize)
	if err != nil {
		sopFail(c, err, "")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// RunStats 运行统计摘要。
func (h *SOPHandler) RunStats(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.runs2 == nil {
		return
	}
	stats, err := h.runs2.Stats(c.Request.Context(), tenantID, c.Query("project_id"))
	if err != nil {
		sopFail(c, err, "")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": stats})
}

// GetRun 运行详情（run + 节点运行 + 产物）。
func (h *SOPHandler) GetRun(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.runs2 == nil || h.runs == nil {
		return
	}
	run, err := h.runs2.Get(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopFail(c, err, "运行不存在")
		return
	}
	nodes, err := h.runs.ListNodeRuns(c.Request.Context(), tenantID, run.ID)
	if err != nil {
		sopFail(c, err, "")
		return
	}
	arts, err := h.runs.ListArtifacts(c.Request.Context(), tenantID, run.ID)
	if err != nil {
		sopFail(c, err, "")
		return
	}
	if nodes == nil {
		nodes = []types.SopNodeRun{}
	}
	if arts == nil {
		arts = []types.SopArtifact{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": sopRunDetail{
		Run: run, NodeRuns: nodes, Artifacts: arts, ProjectContext: run.ProjectContext,
	}})
}

// RunEvents SSE 直播：订阅该运行的事件，15s 一次心跳。
func (h *SOPHandler) RunEvents(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.runs == nil || h.hub == nil {
		return
	}
	runID := c.Param("id")
	if _, err := h.runs.GetRun(c.Request.Context(), tenantID, runID); err != nil {
		sopFail(c, err, "运行不存在")
		return
	}
	ch, unsub := h.hub.Subscribe(runID)
	defer unsub()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		sopAbort(c, sopClientf("当前连接不支持事件流"))
		return
	}
	// 首帧：告诉前端订阅已就绪（也顺带把缓冲刷出去）。
	fmt.Fprint(c.Writer, ": connected\n\n")
	flusher.Flush()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			body, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(c.Writer, "data: %s\n\n", body)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(c.Writer, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// ListRunArtifacts 列某运行的产物（含历史尝试）。
func (h *SOPHandler) ListRunArtifacts(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.runs == nil {
		return
	}
	list, err := h.runs.ListArtifacts(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopFail(c, err, "运行不存在")
		return
	}
	for i := range list {
		list[i].Path = filepath.ToSlash(list[i].Path)
	}
	if list == nil {
		list = []types.SopArtifact{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// DownloadArtifact 产物预览/下载：?download=1 走附件下载，否则按媒体类型 inline。
func (h *SOPHandler) DownloadArtifact(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.runs == nil {
		return
	}
	run, err := h.runs.GetRun(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopFail(c, err, "运行不存在")
		return
	}
	art, err := h.runs.GetArtifact(c.Request.Context(), tenantID, c.Param("aid"))
	if err != nil {
		sopFail(c, err, "产物不存在")
		return
	}
	if art.RunID != run.ID {
		// 产物属于别的运行：当作不存在（不泄露别的运行有没有这个 id）。
		sopFail(c, gorm.ErrRecordNotFound, "产物不存在")
		return
	}
	ws := &engine.Workspace{Root: strings.TrimSpace(run.WorkspaceDir)}
	full, err := engine.SafeWorkspacePath(ws, art.Path)
	if err != nil {
		c.Error(sopClientf("产物路径非法"))
		return
	}
	if _, statErr := os.Stat(full); statErr != nil {
		// 文件不在磁盘上：按 404 处理（statErr 是 os 包错误，sopFail 认不出）。
		sopFail(c, gorm.ErrRecordNotFound, "产物文件不存在")
		return
	}
	if c.Query("download") == "1" {
		c.FileAttachment(full, path.Base(art.Path))
		return
	}
	if media := strings.TrimSpace(art.MediaType); media != "" {
		c.Header("Content-Type", media)
	}
	c.File(full)
}

// NodeLogs 节点历史日志：默认取最近一次尝试的 jsonl，退化为 .log。
func (h *SOPHandler) NodeLogs(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.runs == nil {
		return
	}
	run, err := h.runs.GetRun(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopFail(c, err, "运行不存在")
		return
	}
	nodeID := c.Param("nid")
	nr, err := h.runs.GetNodeRun(c.Request.Context(), tenantID, run.ID, nodeID)
	if err != nil {
		sopFail(c, err, "步骤不存在")
		return
	}
	attempt := atoiOr(c.Query("attempt"), nr.Attempt)
	if attempt < 1 {
		attempt = 1
	}
	ws := &engine.Workspace{Root: strings.TrimSpace(run.WorkspaceDir)}
	for _, name := range []string{
		fmt.Sprintf("logs/%s-attempt-%d.jsonl", safeNodeSegment(nodeID), attempt),
		fmt.Sprintf("logs/%s-attempt-%d.log", safeNodeSegment(nodeID), attempt),
	} {
		full, pathErr := engine.SafeWorkspacePath(ws, name)
		if pathErr != nil {
			continue
		}
		if _, statErr := os.Stat(full); statErr == nil {
			c.Header("Content-Type", "text/plain; charset=utf-8")
			c.File(full)
			return
		}
	}
	sopAbort(c, sopClientf("该节点暂无日志"))
}

// safeNodeSegment 把节点 id 收敛为可安全放进文件名的单段（与引擎落盘口径一致）。
func safeNodeSegment(id string) string {
	out := []rune(strings.TrimSpace(id))
	for i, r := range out {
		ok := r < 128 && (r == '.' || r == '_' || r == '-' ||
			(r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'))
		if !ok {
			out[i] = '_'
		}
	}
	s := strings.Trim(string(out), ".")
	if s == "" {
		return "_"
	}
	return s
}

// ---- 运行级操作 ----

// RetryRun 整体重试失败/取消的运行。
func (h *SOPHandler) RetryRun(c *gin.Context) {
	if !h.runOpReady(c) {
		return
	}
	if err := h.engine.Retry(c.Request.Context(), sopTenantID(c), c.Param("id")); err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"ok": true}})
}

// PauseRun 请求暂停。
func (h *SOPHandler) PauseRun(c *gin.Context) {
	if !h.runOpReady(c) {
		return
	}
	if err := h.engine.Pause(c.Request.Context(), sopTenantID(c), c.Param("id")); err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"ok": true}})
}

// ResumeRunPaused 恢复已暂停的运行。
func (h *SOPHandler) ResumeRunPaused(c *gin.Context) {
	if !h.runOpReady(c) {
		return
	}
	if err := h.engine.Resume(c.Request.Context(), sopTenantID(c), c.Param("id")); err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"ok": true}})
}

// CancelRun 取消运行（向下传播到子运行由引擎处理）。
func (h *SOPHandler) CancelRun(c *gin.Context) {
	if !h.runOpReady(c) {
		return
	}
	if err := h.engine.Cancel(c.Request.Context(), sopTenantID(c), c.Param("id")); err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"ok": true}})
}

// AddRunHumanContext 追加一条人工上下文（下轮任务书可见）。
func (h *SOPHandler) AddRunHumanContext(c *gin.Context) {
	if !h.runOpReady(c) {
		return
	}
	var req struct {
		Text   string `json:"text"`
		NodeID string `json:"node_id"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	if err := h.engine.AddHumanContext(c.Request.Context(), sopTenantID(c),
		c.Param("id"), req.Text, "manual", req.NodeID); err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"ok": true}})
}

// runOpReady 校验引擎与租户就绪（运行级操作都走这条路）。
func (h *SOPHandler) runOpReady(c *gin.Context) bool {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return false
	}
	if h.engine == nil {
		sopAbort(c, sopClientf("运行引擎未就绪"))
		return false
	}
	return true
}

// ---- 节点级人工操作 ----

// ApproveNode 审批通过。
func (h *SOPHandler) ApproveNode(c *gin.Context) {
	if !h.runOpReady(c) {
		return
	}
	var req struct {
		Comment string `json:"comment"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	if err := h.engine.Approve(c.Request.Context(), sopTenantID(c),
		c.Param("id"), c.Param("nid"), req.Comment, sopUserID(c)); err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"ok": true}})
}

// RejectNode 打回（目标可空：按节点模板的 reject_to）。
func (h *SOPHandler) RejectNode(c *gin.Context) {
	if !h.runOpReady(c) {
		return
	}
	var req struct {
		Comment  string `json:"comment"`
		RejectTo string `json:"reject_to"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	if err := h.engine.Reject(c.Request.Context(), sopTenantID(c),
		c.Param("id"), c.Param("nid"), req.Comment, sopUserID(c), req.RejectTo); err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"ok": true}})
}

// ReworkNode 补充信息重跑当前节点。
func (h *SOPHandler) ReworkNode(c *gin.Context) {
	if !h.runOpReady(c) {
		return
	}
	var req struct {
		Comment string `json:"comment"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	if err := h.engine.ReworkCurrent(c.Request.Context(), sopTenantID(c),
		c.Param("id"), c.Param("nid"), req.Comment, sopUserID(c)); err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"ok": true}})
}

// TerminateNode 终止运行（节点失败 + 运行收口）。
func (h *SOPHandler) TerminateNode(c *gin.Context) {
	if !h.runOpReady(c) {
		return
	}
	var req struct {
		Comment string `json:"comment"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	if err := h.engine.Terminate(c.Request.Context(), sopTenantID(c),
		c.Param("id"), c.Param("nid"), req.Comment, sopUserID(c)); err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"ok": true}})
}

// AnswerNode 答复挂起步骤（pending_input）。
func (h *SOPHandler) AnswerNode(c *gin.Context) {
	if !h.runOpReady(c) {
		return
	}
	var req struct {
		Answer string `json:"answer"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	if err := h.engine.AnswerPrompt(c.Request.Context(), sopTenantID(c),
		c.Param("id"), c.Param("nid"), req.Answer); err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"ok": true}})
}
