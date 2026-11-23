// submit_tool.go 是平台节点的「结果提交」通道：模型通过 submit_node_result
// 汇报本步骤结论（pass/rework/fail/human），执行器据此填门禁结论，而不是让模型
// 在正文里自评再由引擎猜。
//
// 双通道一致：结果同时写进进程内结果槽（同一进程内执行器立刻读到）和工作区的
// .sop/gate-results/<node>/attempt-<N>.json（与 CLI 通道同格式，崩溃后仍可追溯）。
//
// 作用域：只对 SOP 节点会话开放——会话描述必须带 types.SOPSessionMarker，
// 普通对话里即使工具被注册也调不动。
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// SubmitToolName 是结果提交工具名（任务书与本工具共用同一口径）。
const SubmitToolName = submitToolName

// SubmitResult 一次结果提交的内容。
type SubmitResult struct {
	GateResult string // pass|rework|fail|human
	GateReason string // 白话理由（rework/fail/human 必填）
	Output     string // 交给下游的结论正文（可空，空则沿用模型最终正文）
}

// ReasonsRequiringReason 是必须写理由的结论：只说「打回」不说差在哪，下一步没法干活。
func requiresReason(verdict string) bool {
	switch verdict {
	case types.SopGateRework, types.SopGateFail, types.SopGateHuman:
		return true
	}
	return false
}

// ParseSubmitArgs 校验并解析工具入参。
func ParseSubmitArgs(args map[string]any) (SubmitResult, error) {
	if args == nil {
		return SubmitResult{}, errors.New("结果提交缺少参数")
	}
	verdict, ok := args["result"].(string)
	if !ok {
		return SubmitResult{}, errors.New("result 必须是字符串：pass / rework / fail / human")
	}
	verdict = strings.TrimSpace(verdict)
	switch verdict {
	case types.SopGatePass, types.SopGateRework, types.SopGateFail, types.SopGateHuman:
	default:
		return SubmitResult{}, fmt.Errorf("result 只能是 pass（通过）/ rework（打回）/ fail（失败）/ human（转人工），收到 %q", verdict)
	}
	reason, _ := args["reason"].(string)
	reason = strings.TrimSpace(reason)
	if requiresReason(verdict) && reason == "" {
		return SubmitResult{}, fmt.Errorf("result=%s 必须带 reason：用一句话说清楚依据或差在哪", verdict)
	}
	output, _ := args["output"].(string)
	return SubmitResult{GateResult: verdict, GateReason: reason, Output: strings.TrimSpace(output)}, nil
}

// ResultSlots 是执行器与工具之间的进程内结果槽：键是「运行/节点」，一次执行结束
// 由执行器 Take 走，避免同一节点下一轮读到旧结论。
type ResultSlots struct {
	mu   sync.Mutex
	data map[string]SubmitResult
}

// NewResultSlots 建空结果槽。
func NewResultSlots() *ResultSlots {
	return &ResultSlots{data: map[string]SubmitResult{}}
}

// Put 写入一次提交结果。
func (s *ResultSlots) Put(key string, res SubmitResult) {
	if s == nil || strings.TrimSpace(key) == "" {
		return
	}
	s.mu.Lock()
	s.data[key] = res
	s.mu.Unlock()
}

// Take 取走结果（取后即删）。
func (s *ResultSlots) Take(key string) (SubmitResult, bool) {
	if s == nil {
		return SubmitResult{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, ok := s.data[key]
	if ok {
		delete(s.data, key)
	}
	return res, ok
}

// SubmitContext 是工具执行时可用的 SOP 上下文（由执行器注入到 ctx）。
type SubmitContext struct {
	SessionID          string
	SessionDescription string // 调用方已知的会话描述（命中可省一次查库）
	Slots              *ResultSlots
	SlotKey            string // 结果槽键；执行器按「运行 + 节点」取值
	WorkspaceRoot      string
	NodeID             string
	Attempt            int
}

type submitCtxKey struct{}

// WithSubmitContext 把 SOP 提交上下文注入 ctx。
func WithSubmitContext(ctx context.Context, sc *SubmitContext) context.Context {
	if sc == nil {
		return ctx
	}
	return context.WithValue(ctx, submitCtxKey{}, sc)
}

// SubmitBindings 是「会话 → SOP 提交上下文」的进程内登记表。
//
// 工具由智能体运行时构造（拿到的是会话 id），而工作区/节点/尝试序号只有执行器
// 知道；执行器在调用 AgentQA 前按会话登记，调用结束注销。工具按会话 id 查回即可，
// 不依赖任何仓库包。
type SubmitBindings struct {
	mu   sync.Mutex
	data map[string]*SubmitContext
}

// NewSubmitBindings 建空登记表。
func NewSubmitBindings() *SubmitBindings {
	return &SubmitBindings{data: map[string]*SubmitContext{}}
}

// Bind 登记一次会话的提交上下文。
func (b *SubmitBindings) Bind(sessionID string, sc *SubmitContext) {
	if b == nil || strings.TrimSpace(sessionID) == "" || sc == nil {
		return
	}
	b.mu.Lock()
	b.data[sessionID] = sc
	b.mu.Unlock()
}

// Unbind 注销会话登记（仅在仍是同一份时删除）。
func (b *SubmitBindings) Unbind(sessionID string, sc *SubmitContext) {
	if b == nil {
		return
	}
	b.mu.Lock()
	if cur, ok := b.data[sessionID]; ok && cur == sc {
		delete(b.data, sessionID)
	}
	b.mu.Unlock()
}

// Has 判断会话是否登记为 SOP 会话（工具注册处据此决定是否挂出该工具）。
func (b *SubmitBindings) Has(sessionID string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.data[sessionID]
	return ok
}

// Lookup 按会话取提交上下文。
func (b *SubmitBindings) Lookup(sessionID string) (*SubmitContext, bool) {
	if b == nil {
		return nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	sc, ok := b.data[sessionID]
	return sc, ok
}

// defaultSubmitBindings 是进程内默认登记表：执行器与智能体运行时（工具注册处）
// 分属两个装配层级，T21 之前用同一份进程内登记表对齐，避免为此多引一条依赖。
var defaultSubmitBindings = NewSubmitBindings()

// DefaultSubmitBindings 返回进程内默认登记表（执行器未显式注入时使用）。
func DefaultSubmitBindings() *SubmitBindings { return defaultSubmitBindings }

// SubmitContextFrom 取 SOP 提交上下文。
func SubmitContextFrom(ctx context.Context) (*SubmitContext, bool) {
	v := ctx.Value(submitCtxKey{})
	if v == nil {
		return nil, false
	}
	sc, ok := v.(*SubmitContext)
	return sc, ok && sc != nil
}

// SessionGetter 按 ID 取会话（真实实现 = 会话服务）；工具据此校验 SOP 标记。
type SessionGetter interface {
	GetSessionByID(ctx context.Context, tenantID uint64, id string) (*types.Session, error)
}

// SubmitReceipt 是工具回给模型的确认文本。
type SubmitReceipt struct {
	Message string
}

// SubmitTool 实现 types.Tool：submit_node_result。
type SubmitTool struct {
	sessions SessionGetter
	bindings *SubmitBindings
}

// NewSubmitTool 构造工具；sessions 可 nil（此时只认 ctx 里带的会话描述或登记表）。
func NewSubmitTool(sessions SessionGetter) *SubmitTool { return &SubmitTool{sessions: sessions} }

// WithBindings 复用执行器的会话登记表（工具按会话查回工作区与节点）。
func (t *SubmitTool) WithBindings(b *SubmitBindings) *SubmitTool {
	t.bindings = b
	return t
}

// Name 实现 types.Tool。
func (t *SubmitTool) Name() string { return submitToolName }

// Description 实现 types.Tool（面向模型的白话说明）。
func (t *SubmitTool) Description() string {
	return "汇报本步骤的执行结果。必须在步骤完成时调用一次：result 取 pass/rework/fail/human，" +
		"rework/fail/human 必须用 reason 写清依据或差在哪，output 可附交给下一步的结论正文。"
}

// Parameters 实现 types.Tool。
func (t *SubmitTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "result": {"type": "string", "enum": ["pass", "rework", "fail", "human"],
      "description": "pass 完成 / rework 需返工 / fail 失败 / human 转人工"},
    "reason": {"type": "string", "description": "一句话依据；rework、fail、human 必填"},
    "output": {"type": "string", "description": "交给下游步骤的结论正文，可空"}
  },
  "required": ["result"]
}`)
}

// Execute 实现 types.Tool：把 json 入参转成 map 后走 Invoke。
func (t *SubmitTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("结果提交参数解析失败：%v", err)}, nil
	}
	receipt, err := t.Invoke(ctx, m)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	return &types.ToolResult{Success: true, Output: receipt.Message}, nil
}

// Invoke 校验作用域、解析参数、双通道落盘，返回确认文本。
//
// ctx 没带提交上下文时（工具由智能体运行时直接构造、ctx 不是执行器发起的），
// 依次回退到会话主键 / 会话描述 / 登记表，保证「只验作用域」这条路径始终成立。
func (t *SubmitTool) Invoke(ctx context.Context, args map[string]any) (SubmitReceipt, error) {
	sc := t.contextFor(ctx)
	if err := t.assertSOPSession(ctx, sc); err != nil {
		return SubmitReceipt{}, err
	}
	res, err := ParseSubmitArgs(args)
	if err != nil {
		return SubmitReceipt{}, err
	}
	if sc != nil {
		if sc.Slots != nil {
			sc.Slots.Put(sc.SlotKey, res)
		}
		if err := writeGateResultFile(sc.WorkspaceRoot, sc.NodeID, sc.Attempt, res); err != nil {
			return SubmitReceipt{}, err
		}
	}
	return SubmitReceipt{Message: fmt.Sprintf("已记录本步骤结果：%s。%s", res.GateResult, nextStepHint(res.GateResult))}, nil
}

// contextFor 解析本次调用的提交上下文：ctx 直传优先，其次按会话 id 查登记表。
func (t *SubmitTool) contextFor(ctx context.Context) *SubmitContext {
	if sc, ok := SubmitContextFrom(ctx); ok {
		return sc
	}
	b := t.bindingFromContext(ctx)
	if b == nil {
		return nil
	}
	id, ok := types.SessionIDFromContext(ctx)
	if !ok {
		return nil
	}
	sc, _ := b.Lookup(id)
	return sc
}

// bindingFromContext 取 ctx 里的登记表（工具构造时可覆盖），无则用工具自身那份。
func (t *SubmitTool) bindingFromContext(ctx context.Context) *SubmitBindings {
	if v := ctx.Value(submitBindingsKey{}); v != nil {
		if b, ok := v.(*SubmitBindings); ok {
			return b
		}
	}
	return t.bindings
}

type submitBindingsKey struct{}

// WithSubmitBindings 把登记表注入 ctx（执行器与工具在会话边界共享一份）。
func WithSubmitBindings(ctx context.Context, b *SubmitBindings) context.Context {
	if b == nil {
		return ctx
	}
	return context.WithValue(ctx, submitBindingsKey{}, b)
}

// SubmitBindingsFrom 取 ctx 里的登记表（工具注册处据此判断本会话是不是 SOP 会话）。
func SubmitBindingsFrom(ctx context.Context) (*SubmitBindings, bool) {
	v := ctx.Value(submitBindingsKey{})
	if v == nil {
		return nil, false
	}
	b, ok := v.(*SubmitBindings)
	return b, ok && b != nil
}

// assertSOPSession 校验会话属于 SOP 节点（描述带 [SOP] 前缀）。
//
// 来源依次为：ctx 提交上下文 → 会话登记表 → 会话服务。登记表命中即可认定 SOP
// 会话（登记只发生在执行器发起的节点会话上）。
func (t *SubmitTool) assertSOPSession(ctx context.Context, sc *SubmitContext) error {
	if sc == nil {
		if b := t.bindingFromContext(ctx); b != nil {
			if id, ok := types.SessionIDFromContext(ctx); ok && b.Has(id) {
				return nil
			}
		}
	}
	desc := ""
	sessionID := ""
	if sc != nil {
		desc = sc.SessionDescription
		sessionID = sc.SessionID
	}
	if sessionID == "" {
		sessionID, _ = types.SessionIDFromContext(ctx)
	}
	if !strings.Contains(desc, types.SOPSessionMarker) && t.sessions != nil && sessionID != "" {
		tenantID, _ := types.TenantIDFromContext(ctx)
		session, err := t.sessions.GetSessionByID(ctx, tenantID, sessionID)
		if err != nil {
			return fmt.Errorf("读取会话失败，无法确认这是 SOP 会话：%w", err)
		}
		if session != nil {
			desc = session.Description
		}
	}
	if !strings.Contains(desc, types.SOPSessionMarker) {
		return errors.New("submit_node_result 只在 SOP 步骤会话内可用：当前会话不是 SOP 会话")
	}
	return nil
}

// nextStepHint 给模型一句下一步提示（避免它提交完继续瞎跑）。
func nextStepHint(verdict string) string {
	switch verdict {
	case types.SopGateRework:
		return "系统会按返工意见安排重做，本步骤到此结束。"
	case types.SopGateFail:
		return "本步骤以失败收尾，不会再继续。"
	case types.SopGateHuman:
		return "已转人工裁定，等待处理即可。"
	default:
		return "可以结束本轮了。"
	}
}

// writeGateResultFile 把结果写成 .sop/gate-results/<node>/attempt-<N>.json（原子替换）。
func writeGateResultFile(root, nodeID string, attempt int, res SubmitResult) error {
	if strings.TrimSpace(root) == "" {
		return nil // 未注入工作区（如单节点重跑前的预演）时不落文件
	}
	if attempt < 1 {
		attempt = 1
	}
	dir := filepath.Join(root, ".sop", "gate-results", safeSegment(nodeID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建结果目录失败：%w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"result": res.GateResult, "reason": res.GateReason, "output": res.Output,
		"at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("结果序列化失败：%w", err)
	}
	final := filepath.Join(dir, fmt.Sprintf("attempt-%d.json", attempt))
	tmp := fmt.Sprintf("%s.%d.tmp", final, time.Now().UnixNano())
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return fmt.Errorf("写入结果文件失败：%w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换结果文件失败：%w", err)
	}
	return nil
}

// safeSegment 把节点 id 收敛成安全路径段（与引擎 sanitizeSegment 同口径的独立实现：
// 执行器不 import 引擎，避免反向依赖）。
func safeSegment(seg string) string {
	out := []rune(strings.TrimSpace(seg))
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
	if len(s) > 128 {
		s = s[:128]
	}
	return s
}
