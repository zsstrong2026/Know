// sop_ai.go 是 AI 助手端点：事项拆解、事项分析（建会话）、澄清生成、流程编排。
//
// 全部是「助手」而不是「自动执行」：
//   - 拆解/澄清只产出建议，落库的澄清也只是 open 状态等人工答复；
//   - 编排只产出草稿 DTO，应用与否由前端逐条确认后走模板 PUT；
//   - 分析返回会话 id，前端自行订阅会话 SSE（与普通对话同一套流）。
package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/sop"
	"github.com/Tencent/WeKnora/internal/types"
)

// BreakdownItems 事项拆解：返回建议子事项（不落库）。
func (h *SOPHandler) BreakdownItems(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.ai == nil {
		return
	}
	var req struct {
		ProjectID   string `json:"project_id"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	items, err := h.ai.Breakdown(c.Request.Context(), tenantID, req.ProjectID, sop.ItemDraft{
		Title: req.Title, Description: req.Description,
	})
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}

// AnalyzeSopItem 为事项建分析会话，返回会话 id（前端转订阅会话 SSE）。
func (h *SOPHandler) AnalyzeSopItem(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.ai == nil {
		return
	}
	var req struct {
		ItemID string `json:"item_id"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	if strings.TrimSpace(req.ItemID) == "" {
		sopAbort(c, sopClientf("请选择要分析的事项"))
		return
	}
	sessionID, err := h.ai.AnalyzeItem(c.Request.Context(), tenantID, req.ItemID)
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"session_id": sessionID}})
}

// GenerateItemClarifications 让 AI 生成待澄清问题（落库 open/ai）。
func (h *SOPHandler) GenerateItemClarifications(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.ai == nil {
		return
	}
	var req struct {
		ItemID string `json:"item_id"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	if strings.TrimSpace(req.ItemID) == "" {
		sopAbort(c, sopClientf("请选择事项"))
		return
	}
	list, err := h.ai.GenerateClarifications(c.Request.Context(), tenantID, req.ItemID)
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// OrchestrateSop 流程/节点编排：返回草稿（永不落库）。
//
// flow 模式产出整份定义草稿 + 警告清单；node 模式把模型给出的 ops 逐条校验后
// 应用到传入定义副本，返回 applied/skipped 与合并后的草稿。
func (h *SOPHandler) OrchestrateSop(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.ai == nil {
		return
	}
	var req struct {
		TemplateID  string               `json:"template_id"`
		Mode        string               `json:"mode"`
		Instruction string               `json:"instruction"`
		NodeID      string               `json:"node_id"`
		Definition  *types.SopDefinition `json:"definition"`
		Inventory   sopInventoryPayload  `json:"inventory"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	if err := validateOrchestrateMode(req.Mode); err != nil {
		sopAbort(c, err)
		return
	}
	// 自定义定义优先；否则从模板草稿读（模板不存在时 404）。
	def := types.SopDefinition{}
	if req.Definition != nil {
		def = *req.Definition
	} else if strings.TrimSpace(req.TemplateID) != "" && h.templates != nil {
		tpl, err := h.templates.Get(c.Request.Context(), tenantID, req.TemplateID)
		if err != nil {
			sopFail(c, err, "模板不存在")
			return
		}
		def = tpl.Definition
	}
	if req.Mode == sop.OrchestrateModeFlow {
		// flow 模式：模型输出整份定义；这里只做解析与校验，不自动落库。
		runner, ok := h.aiRunner()
		if !ok {
			sopAbort(c, sopClientf("AI 助手未就绪"))
			return
		}
		system, _ := sop.OrchestrateSystemPrompt(sop.OrchestrateModeFlow)
		text, _, err := runner.Ask(c.Request.Context(), sop.AIRequest{
			TenantID: tenantID, System: system, Prompt: req.Instruction,
		})
		if err != nil {
			sopAbort(c, sopClientf("AI 生成失败：%v", err))
			return
		}
		draft, err := h.ai.ParseFlowDraft(c.Request.Context(), text)
		if err != nil {
			sopAbort(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": draft})
		return
	}

	// node 模式：模型输出 ops；逐条校验后应用到定义副本。
	runner, ok := h.aiRunner()
	if !ok {
		sopAbort(c, sopClientf("AI 助手未就绪"))
		return
	}
	system, _ := sop.OrchestrateSystemPrompt(sop.OrchestrateModeNode)
	text, _, err := runner.Ask(c.Request.Context(), sop.AIRequest{
		TenantID: tenantID, System: system, Prompt: req.Instruction,
	})
	if err != nil {
		sopAbort(c, sopClientf("AI 生成失败：%v", err))
		return
	}
	inv := sop.Inventory{
		Agents: req.Inventory.Agents, CLIs: req.Inventory.CLIs, Models: req.Inventory.Models,
		Skills: req.Inventory.Skills, MCPs: req.Inventory.MCPs,
	}
	res, err := h.ai.ApplyNodeOps(c.Request.Context(), def, text, inv)
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": res})
}

// sopInventoryPayload 编排清单的请求形状（与 sop.Inventory 对齐）。
type sopInventoryPayload struct {
	Agents []sop.InventoryItem `json:"agents"`
	CLIs   []string            `json:"clis"`
	Models []sop.InventoryItem `json:"models"`
	Skills []string            `json:"skills"`
	MCPs   []string            `json:"mcps"`
}

// validateOrchestrateMode 校验模式（非法值 400 白话）。
func validateOrchestrateMode(mode string) error {
	if _, err := sop.OrchestrateSystemPrompt(mode); err != nil {
		return sopClientf("模式只能是 flow 或 node")
	}
	return nil
}
