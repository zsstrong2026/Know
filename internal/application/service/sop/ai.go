// ai.go 是 SOP 的 AI 助手能力：事项拆解、事项分析会话、澄清生成、流程/节点编排。
//
// 三条硬口径：
//   - 草稿-only：AI 产出永远不落库。编排只返回定义草稿，「应用」由前端逐条确认后
//     走 PUT 保存——让模型直接改线上流程是事故来源，不是效率；
//   - 清单校验：节点编排里出现的 agent/cli/model/skill/mcp 必须在当前可用清单内，
//     不在就跳过并附白话原因（模型编造 id 是常态）；
//   - 派生化字段不接收：gate_commands 一律按定义重算，AI 写了也覆盖。
package sop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// 编排模式。
const (
	OrchestrateModeFlow = "flow"
	OrchestrateModeNode = "node"
)

// AI 层的业务错误。
var (
	ErrSopAIModeInvalid   = errors.New("模式只能是 flow 或 node")
	ErrSopAIOutputInvalid = errors.New("AI 返回的内容无法解析")
)

// Runner 是 AI 能力的执行依赖（真实实现 = AgentQA + 会话/消息服务，T26 接线）。
//
// 只声明一个方法：一次问答拿最终文本（流式直播由底层事件总线负责）。
type Runner interface {
	// Ask 跑一次问答并返回最终正文；sessionID 非空时复用该会话。
	Ask(ctx context.Context, req AIRequest) (text string, sessionID string, err error)
}

// AIRequest 一次 AI 助手的问答请求。
type AIRequest struct {
	TenantID  uint64
	SessionID string // 复用会话（追问/多轮编排）
	System    string // 额外的角色约束
	Prompt    string
}

// AIService AI 助手能力集合。
type AIService struct {
	runner Runner
	items  ItemServiceAPI
	extras interface {
		AddClarification(ctx context.Context, c *types.SopItemClarification) error
	}
}

// ItemServiceAPI 是 AI 层需要的事项读能力（真实实现 = *ItemService）。
type ItemServiceAPI interface {
	Get(ctx context.Context, tenantID uint64, id string) (*types.SopItem, error)
	ListAttachments(ctx context.Context, tenantID uint64, itemID string) ([]types.SopItemAttachment, error)
}

// NewAIService 构造 AI 助手服务；extras 可 nil（澄清落库能力缺失时直接失败）。
func NewAIService(runner Runner, items ItemServiceAPI, extras interface {
	AddClarification(ctx context.Context, c *types.SopItemClarification) error
},
) *AIService {
	return &AIService{runner: runner, items: items, extras: extras}
}

// ItemDraft 事项初稿（拆解输入）。
type ItemDraft struct {
	Title       string
	Description string
}

// SubItem 拆解产出的一条建议子事项。
type SubItem struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority,omitempty"`
}

// Breakdown 把一条初稿拆成建议子事项（JSON 数组）。
func (s *AIService) Breakdown(
	ctx context.Context, tenantID uint64, projectID string, draft ItemDraft,
) ([]SubItem, error) {
	title := strings.TrimSpace(draft.Title)
	if title == "" {
		return nil, ErrSopItemTitleRequired
	}
	text, _, err := s.runner.Ask(ctx, AIRequest{
		TenantID: tenantID,
		System: "你是需求拆解助手。只输出 JSON 数组，不要输出解释、不要用 Markdown 代码块。" +
			"每个元素形如 {\"title\":\"...\",\"description\":\"...\",\"priority\":\"P1\"}。" +
			"子事项要独立可交付，3–8 条，标题不超过 40 字。",
		Prompt: fmt.Sprintf("项目：%s\n需求标题：%s\n需求描述：%s", projectID, title, draft.Description),
	})
	if err != nil {
		return nil, err
	}
	var out []SubItem
	if err := json.Unmarshal([]byte(extractJSON(text)), &out); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrSopAIOutputInvalid, err)
	}
	// 过滤空标题（模型偶尔夹一条示例）并截断过长标题。
	filtered := make([]SubItem, 0, len(out))
	for _, it := range out {
		it.Title = strings.TrimSpace(it.Title)
		if it.Title == "" {
			continue
		}
		it.Title = truncateRunes(it.Title, 60)
		filtered = append(filtered, it)
	}
	return filtered, nil
}

// AnalyzeItem 为事项建/复用分析会话，返回会话 id（前端转订阅会话 SSE）。
//
// 上下文 = 事项字段 + 附件文本 + 项目 KB 说明（由调用方拼进 Prompt 的部分在
// buildItemContext 里）。会话描述带 SOP 标记，首页对话列表会隐藏它们。
func (s *AIService) AnalyzeItem(ctx context.Context, tenantID uint64, itemID string) (string, error) {
	item, err := s.items.Get(ctx, tenantID, itemID)
	if err != nil {
		return "", err
	}
	atts, _ := s.items.ListAttachments(ctx, tenantID, itemID)
	prompt := buildItemContext(item, atts)
	_, sessionID, err := s.runner.Ask(ctx, AIRequest{
		TenantID:  tenantID,
		SessionID: "",
		System: "你是 SOP 工作台的分析助手。基于下面的事项与附件回答用户追问；" +
			"信息不足时先列 1–3 个最关键的问题，每个问题附上你的推测答案。",
		Prompt: prompt,
	})
	if err != nil {
		return "", err
	}
	return sessionID, nil
}

// buildItemContext 把事项与附件拼成分析上下文（附件只取抽取文本，限量）。
func buildItemContext(item *types.SopItem, atts []types.SopItemAttachment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "事项编号：%s\n标题：%s\n状态：%s\n优先级：%s\n来源：%s\n",
		item.No, item.Title, item.Status, item.Priority, types.SopSourceLabel(item.Source))
	if desc := strings.TrimSpace(item.Description); desc != "" {
		fmt.Fprintf(&b, "描述：%s\n", desc)
	}
	for i, a := range atts {
		if i >= 5 { // 附件过多时只取前 5 个，避免上下文失控
			break
		}
		text := strings.TrimSpace(a.TextContent)
		if text == "" {
			fmt.Fprintf(&b, "附件：%s（%s，未抽取正文）\n", a.Name, a.MediaType)
			continue
		}
		fmt.Fprintf(&b, "附件：%s\n%s\n", a.Name, truncateRunes(text, 4000))
	}
	return strings.TrimRight(b.String(), "\n")
}

// GenerateClarifications 让 AI 生成待澄清问题并落库（status=open, source=ai）。
func (s *AIService) GenerateClarifications(
	ctx context.Context, tenantID uint64, itemID string,
) ([]types.SopItemClarification, error) {
	if s.extras == nil {
		return nil, errors.New("澄清写入通道未就绪")
	}
	item, err := s.items.Get(ctx, tenantID, itemID)
	if err != nil {
		return nil, err
	}
	atts, _ := s.items.ListAttachments(ctx, tenantID, itemID)
	text, _, err := s.runner.Ask(ctx, AIRequest{
		TenantID: tenantID,
		System: "你是需求澄清助手。只输出 JSON 数组，元素是 {\"question\":\"...\",\"guess\":\"你的推测答案\"}，" +
			"最多 3 条，问最关键、问了就能开工的问题；不要重复需求里已写清的内容。",
		Prompt: buildItemContext(item, atts),
	})
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Question string `json:"question"`
		Guess    string `json:"guess"`
	}
	if err := json.Unmarshal([]byte(extractJSON(text)), &raw); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrSopAIOutputInvalid, err)
	}
	out := make([]types.SopItemClarification, 0, len(raw))
	for _, q := range raw {
		question := strings.TrimSpace(q.Question)
		if question == "" {
			continue
		}
		if guess := strings.TrimSpace(q.Guess); guess != "" {
			question = question + "（推测答案：" + guess + "）"
		}
		c := &types.SopItemClarification{
			TenantID: tenantID, ItemID: itemID, Question: question,
			Status: types.SopClarificationStatusOpen, Source: "ai",
		}
		if err := s.extras.AddClarification(ctx, c); err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, nil
}

// FlowDraft 整份流程草稿（flow 模式产出，永不自动发布）。
type FlowDraft struct {
	Definition types.SopDefinition `json:"definition"`
	Warnings   []string            `json:"warnings"`
	Status     string              `json:"status"` // 恒为 draft
}

// ParseFlowDraft 解析整份定义 JSON 草稿：归一化 → 重算派生命令 → 校验（允许缺执行器）。
func (s *AIService) ParseFlowDraft(_ context.Context, raw string) (*FlowDraft, error) {
	var def types.SopDefinition
	if err := json.Unmarshal([]byte(extractJSON(raw)), &def); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrSopAIOutputInvalid, err)
	}
	types.NormalizeSopDefinition(&def)
	types.EnsureSopDefinitionSlices(&def)
	engine.RefreshDerived(&def)
	// 草稿允许缺执行器（用户还没选角色）；其余结构问题逐条作为 warnings 返回，
	// 不阻塞——前端要在画布上标出问题，而不是让整份草稿「解析失败」。
	warnings := engine.ValidateDefinition(&def, true)
	return &FlowDraft{Definition: def, Warnings: warnings, Status: "draft"}, nil
}

// Inventory 影响编排可选的执行器/模型/技能/MCP 清单（来自当前租户配置）。
type Inventory struct {
	Agents     []InventoryItem
	CLIs       []string
	Models     []InventoryItem
	Skills     []string
	MCPs       []string
	RejectToBy []string // 允许的打回目标节点 id（按节点逐个校验时由调用方给）
}

// InventoryItem 清单项（id + 展示名）。
type InventoryItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// NodeOp 一条节点编排指令。
type NodeOp struct {
	Op     string                     `json:"op"` // set|add_node|remove_node|add_edge|remove_edge
	NodeID string                     `json:"node_id"`
	Fields map[string]json.RawMessage `json:"fields"`
	FromID string                     `json:"from_id"`
	ToID   string                     `json:"to_id"`
	EdgeID string                     `json:"edge_id"`
}

// OpSkip 被跳过的一条指令与白话原因。
type OpSkip struct {
	Op     string `json:"op"`
	Reason string `json:"reason"`
}

// ApplyOpsResult 编排应用结果（草稿，永不落库）。
type ApplyOpsResult struct {
	Definition types.SopDefinition `json:"definition"`
	Applied    []string            `json:"applied"`
	Skipped    []OpSkip            `json:"skipped"`
}

// ApplyNodeOps 把模型给出的节点编排指令逐条校验后应用到定义副本。
//
// 清单校验（不在清单里的引用一律跳过）：executor.agent_id / cli_kind / model /
// skills / mcp；结构校验：node_id 必须存在（add_node 除外）、打回目标必须是上游。
func (s *AIService) ApplyNodeOps(
	_ context.Context, def types.SopDefinition, opsJSON string, inv Inventory,
) (*ApplyOpsResult, error) {
	var payload struct {
		Ops []NodeOp `json:"ops"`
	}
	if err := json.Unmarshal([]byte(extractJSON(opsJSON)), &payload); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrSopAIOutputInvalid, err)
	}
	work := def // 值拷贝：ops 只改副本，调用方的定义不受影响
	res := &ApplyOpsResult{Applied: []string{}, Skipped: []OpSkip{}}
	for _, op := range payload.Ops {
		if reason := applyNodeOp(&work, op, inv); reason != "" {
			res.Skipped = append(res.Skipped, OpSkip{Op: op.Op, Reason: reason})
			continue
		}
		res.Applied = append(res.Applied, op.Op+"@ "+op.NodeID)
	}
	types.NormalizeSopDefinition(&work)
	engine.RefreshDerived(&work)
	res.Definition = work
	return res, nil
}

// applyNodeOp 应用单条指令；返回非空字符串表示跳过原因（白话）。
func applyNodeOp(def *types.SopDefinition, op NodeOp, inv Inventory) string {
	switch op.Op {
	case "set":
		idx := indexNode(def, op.NodeID)
		if idx < 0 {
			return "节点不存在：" + op.NodeID
		}
		return applyNodeFields(&def.Nodes[idx], op.Fields, def, inv)
	case "add_node":
		// 新节点只接受 id/name/type/task_prompt；执行器等配置必须走 set 再校验。
		node := types.SopNode{ID: op.NodeID, Type: types.SopNodeTypeAgent}
		if reason := applyNodeFields(&node, op.Fields, def, inv); reason != "" {
			return reason
		}
		if strings.TrimSpace(node.ID) == "" {
			return "新节点缺少 id"
		}
		if indexNode(def, node.ID) >= 0 {
			return "节点 id 已存在：" + node.ID
		}
		def.Nodes = append(def.Nodes, node)
		return ""
	case "remove_node":
		idx := indexNode(def, op.NodeID)
		if idx < 0 {
			return "节点不存在：" + op.NodeID
		}
		def.Nodes = append(def.Nodes[:idx], def.Nodes[idx+1:]...)
		// 连带删掉相关连线，避免留下悬空边
		edges := def.Edges[:0]
		for _, e := range def.Edges {
			if e.Source != op.NodeID && e.Target != op.NodeID {
				edges = append(edges, e)
			}
		}
		def.Edges = edges
		return ""
	case "add_edge":
		if indexNode(def, op.FromID) < 0 || indexNode(def, op.ToID) < 0 {
			return "连线两端必须都是已有节点"
		}
		for _, e := range def.Edges {
			if e.Source == op.FromID && e.Target == op.ToID {
				return "这条连线已经存在"
			}
		}
		id := op.EdgeID
		if id == "" {
			id = op.FromID + "-" + op.ToID
		}
		def.Edges = append(def.Edges, types.SopEdge{ID: id, Source: op.FromID, Target: op.ToID})
		return ""
	case "remove_edge":
		edges := def.Edges[:0]
		found := false
		for _, e := range def.Edges {
			if e.ID == op.EdgeID || (op.FromID != "" && e.Source == op.FromID && e.Target == op.ToID) {
				found = true
				continue
			}
			edges = append(edges, e)
		}
		def.Edges = edges
		if !found {
			return "要删除的连线不存在"
		}
		return ""
	default:
		return "不认识的指令：" + op.Op
	}
}

// indexNode 返回节点下标（不存在 -1）。
func indexNode(def *types.SopDefinition, id string) int {
	for i := range def.Nodes {
		if def.Nodes[i].ID == id {
			return i
		}
	}
	return -1
}

// applyNodeFields 把 fields 合并进节点并做清单校验（只认白名单字段）。
func applyNodeFields(
	node *types.SopNode, fields map[string]json.RawMessage, def *types.SopDefinition, inv Inventory,
) string {
	for key, raw := range fields {
		switch key {
		case "name":
			_ = json.Unmarshal(raw, &node.Name)
		case "type":
			_ = json.Unmarshal(raw, &node.Type)
		case "task_prompt":
			_ = json.Unmarshal(raw, &node.TaskPrompt)
		case "extra_rules":
			_ = json.Unmarshal(raw, &node.ExtraRules)
		case "on_failure":
			_ = json.Unmarshal(raw, &node.OnFailure)
		case "reject_to":
			_ = json.Unmarshal(raw, &node.RejectTo)
		case "carry_over":
			_ = json.Unmarshal(raw, &node.CarryOver)
		case "skills":
			_ = json.Unmarshal(raw, &node.Skills)
		case "mcp":
			_ = json.Unmarshal(raw, &node.MCP)
		case "max_attempts":
			_ = json.Unmarshal(raw, &node.MaxAttempts)
		case "max_reworks":
			_ = json.Unmarshal(raw, &node.MaxReworks)
		case "timeout_minutes":
			_ = json.Unmarshal(raw, &node.TimeoutMinutes)
		case "executor":
			if reason := applyExecutor(&node.Executor, raw, inv); reason != "" {
				return reason
			}
		case "artifact_outputs":
			_ = json.Unmarshal(raw, &node.ArtifactOutputs)
		default:
			// gate_commands 等派生字段与未知字段一律忽略（由 RefreshDerived 重算）。
		}
	}
	// 打回目标必须是上游（或自身）：模型常随手填一个邻居。
	if node.RejectTo != "" && node.OnFailure == types.SopOnFailureReject {
		if node.RejectTo != node.ID && !engine.Ancestors(def, node.ID)[node.RejectTo] {
			return "打回目标必须是上游步骤：" + node.RejectTo
		}
	}
	return ""
}

// applyExecutor 校验并应用执行器配置（清单白名单）。
func applyExecutor(exec *types.SopNodeExecutor, raw json.RawMessage, inv Inventory) string {
	var next types.SopNodeExecutor
	if err := json.Unmarshal(raw, &next); err != nil {
		return "执行器配置无法解析"
	}
	if next.Kind == types.SopExecutorPlatformAgent || next.Kind == "" {
		if next.AgentID != "" && !hasInventoryItem(inv.Agents, next.AgentID) {
			return "执行角色不存在：" + next.AgentID
		}
	}
	if next.Kind == types.SopExecutorCLI {
		if next.CLIKind != "" && !hasString(inv.CLIs, next.CLIKind) {
			return "不支持的本地 CLI：" + next.CLIKind
		}
	}
	if next.Model != "" && !hasInventoryItem(inv.Models, next.Model) {
		return "模型不存在：" + next.Model
	}
	*exec = next
	// 技能与 MCP 是节点级字段，单独校验。
	return ""
}

// hasInventoryItem 判断 id 是否在清单里（清单为空视为「未提供」，不做限制）。
func hasInventoryItem(items []InventoryItem, id string) bool {
	if len(items) == 0 {
		return true
	}
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

// hasString 判断字符串是否在清单里（清单为空不做限制）。
func hasString(items []string, v string) bool {
	if len(items) == 0 {
		return true
	}
	for _, s := range items {
		if s == v {
			return true
		}
	}
	return false
}

// ValidateInventoryRefs 校验节点上的技能/MCP 是否都在清单内（返回白话原因）。
func ValidateInventoryRefs(node *types.SopNode, inv Inventory) string {
	for _, skill := range node.Skills {
		if !hasString(inv.Skills, skill) {
			return "技能不存在：" + skill
		}
	}
	for _, mcp := range node.MCP {
		if !hasString(inv.MCPs, mcp) {
			return "MCP 服务不存在：" + mcp
		}
	}
	return ""
}

// OrchestrateSystemPrompt 是编排两模式的系统提示（导出供 handler 拼给 AgentQA）。
func OrchestrateSystemPrompt(mode string) (string, error) {
	switch mode {
	case OrchestrateModeFlow:
		return "你是 SOP 流程编排助手。只输出一份定义 JSON：{\"nodes\":[...],\"edges\":[...]}。" +
			"规则：gate_commands 是派生字段不要输出；carry_over 默认 true；" +
			"节点 on_failure 只能取 loop|reject|fail|human，取 reject 时必须同时给 reject_to（上游节点 id）；" +
			"每步都要写清 task_prompt（做什么、产出什么）。", nil
	case OrchestrateModeNode:
		return "你是 SOP 节点编排助手。只输出 {\"ops\":[...]}，op 只能是 set|add_node|remove_node|add_edge|remove_edge。" +
			"executor.agent_id / cli_kind / model / skills / mcp 必须来自给定清单，不要编造。", nil
	default:
		return "", ErrSopAIModeInvalid
	}
}

// extractJSON 从模型输出里取 JSON 片段：兼容 ```json 代码块与前后废话。
func extractJSON(text string) string {
	s := strings.TrimSpace(text)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
		s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "json"))
	}
	// 再按首个 { 或 [ 起、最后一个 } 或 ] 止裁剪。
	startObj, startArr := strings.Index(s, "{"), strings.Index(s, "[")
	start := -1
	switch {
	case startObj >= 0 && (startArr < 0 || startObj < startArr):
		start = startObj
	case startArr >= 0:
		start = startArr
	}
	if start < 0 {
		return s
	}
	end := strings.LastIndexAny(s, "}]")
	if end < start {
		return s
	}
	return s[start : end+1]
}

// truncateRunes 按 rune 截断（避免切坏多字节字符）。
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max]) + "…"
}

// 编译期占位：执行器包的事件常量在 handler SSE 端使用。
var _ = executor.StreamEventTextDelta
