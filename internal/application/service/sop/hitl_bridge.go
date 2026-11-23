// hitl_bridge.go 把 Know 现有的 HITL 机制接到 SOP 引擎的两个窄接口上：
//
//   - executor.SuspensionReader：按会话取「已答复的挂起」快照，执行器据此续跑同一会话；
//   - engine.PromptAnswerer + engine.PromptSuspensionLocator：把人工答复写回挂起提示。
//
// 为什么不改 hitl 模块：它已经有完整的一套（Broker.Resolve 带 kind 校验、租约、
// 跨实例唤醒）。这里只做形状转换，答复与快照的唯一事实仍在 hitl 的存储里。
package sop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// compile-time 断言：桥接对象满足引擎与执行器的窄接口。
var (
	_ executor.SuspensionReader      = (*SuspensionBridge)(nil)
	_ engine.PromptAnswerer          = (*PromptBridge)(nil)
	_ engine.PromptSuspensionLocator = (*PromptBridge)(nil)
)

// SuspensionBridge 按会话读挂起快照（实现 executor.SuspensionReader）。
type SuspensionBridge struct{ broker *hitl.Broker }

// NewSuspensionBridge 构造挂起读取桥；broker 为 nil 时一切返回空（不挂起续跑）。
func NewSuspensionBridge(broker *hitl.Broker) *SuspensionBridge {
	return &SuspensionBridge{broker: broker}
}

// GetBySession 返回该会话最近一条「已答复」的挂起（含快照与提示）。
//
// 只认已答复的提示：未答复的挂起没有「续跑输入」，执行器拿它去 Resume 会被
// agent.Resume 拒掉；返回空让执行器按新一轮执行（任务书里已带人工上下文）。
func (b *SuspensionBridge) GetBySession(ctx context.Context, sessionID string) (*executor.AgentSuspension, error) {
	if b == nil || b.broker == nil || strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	prompts, err := b.broker.SessionPrompts(ctx, sessionID, []string{types.AgentPromptAnswered})
	if err != nil {
		return nil, fmt.Errorf("读取会话挂起失败：%w", err)
	}
	// SessionPrompts 按创建时间升序：最新的在最后。
	for i := len(prompts) - 1; i >= 0; i-- {
		p := prompts[i]
		if p == nil || p.ID == "" {
			continue
		}
		state, err := b.broker.Suspensions().Get(ctx, p.AssistantMessageID)
		if err != nil {
			return nil, fmt.Errorf("读取挂起快照失败：%w", err)
		}
		if state == nil || state.PromptID != p.ID || len(state.Snapshot) == 0 {
			continue
		}
		return &executor.AgentSuspension{SessionID: p.SessionID, Snapshot: state.Snapshot, Prompt: p}, nil
	}
	return nil, nil
}

// PromptBridge 答复挂起提示（实现 engine.PromptAnswerer / PromptSuspensionLocator）。
type PromptBridge struct{ broker *hitl.Broker }

// NewPromptBridge 构造答复桥；broker 为 nil 时答复视为不可用（引擎保留人工上下文注入）。
func NewPromptBridge(broker *hitl.Broker) *PromptBridge { return &PromptBridge{broker: broker} }

// PromptIDBySession 找该会话尚未答复的提示（优先最新一条）。
func (b *PromptBridge) PromptIDBySession(ctx context.Context, sessionID string) (string, error) {
	if b == nil || b.broker == nil {
		return "", errors.New("答复通道未就绪")
	}
	prompts, err := b.broker.SessionPrompts(ctx, sessionID, []string{
		types.AgentPromptPending, types.AgentPromptSuspended,
	})
	if err != nil {
		return "", fmt.Errorf("定位挂起提示失败：%w", err)
	}
	for i := len(prompts) - 1; i >= 0; i-- {
		if p := prompts[i]; p != nil && p.ID != "" {
			return p.ID, nil
		}
	}
	return "", errors.New("该步骤没有等待答复的提示（可能已过期或被撤回）")
}

// Answer 把答复写回提示：按用户输入形状出响应（选项/自由文本），kind 校验由 Broker 做。
func (b *PromptBridge) Answer(ctx context.Context, req engine.PromptAnswer) error {
	if b == nil || b.broker == nil {
		return errors.New("答复通道未就绪")
	}
	promptID := strings.TrimSpace(req.PromptID)
	if promptID == "" {
		id, err := b.PromptIDBySession(ctx, req.SessionID)
		if err != nil {
			return err
		}
		promptID = id
	}
	row, err := b.broker.Store().Get(ctx, promptID)
	if err != nil {
		return fmt.Errorf("读取挂起提示失败：%w", err)
	}
	if row == nil {
		return errors.New("挂起提示不存在")
	}
	response, err := answerResponse(row, req.Text)
	if err != nil {
		return err
	}
	// UserID 取提示自己登记的那位：Broker 对「有用户归属」的提示做严格匹配，
	// 传空会被判越权（fail-close），传错人同理。
	if _, err := b.broker.Resolve(ctx, hitl.ResolveRequest{
		TenantID: req.TenantID, UserID: row.UserID, SessionID: req.SessionID,
		PromptID: promptID, Response: response,
	}); err != nil {
		return fmt.Errorf("提交答复失败：%w", err)
	}
	return nil
}

// answerResponse 按提问形状组装响应：有选项先按文本匹配选项，允许自由文本才回 text，
// 表单题直接拒绝（缺字段名无法映射）。
func answerResponse(p *types.AgentPrompt, text string) (json.RawMessage, error) {
	switch p.Kind {
	case types.AgentPromptKindAskUser:
		var q types.AskUserPromptRequest
		if err := json.Unmarshal(p.Request, &q); err != nil {
			return nil, fmt.Errorf("提问内容损坏，无法作答：%w", err)
		}
		if len(q.Fields) > 0 {
			return nil, errors.New("该提问要求填写表单，请到会话里逐项回答")
		}
		if len(q.Options) > 0 {
			if match := matchOption(q.Options, text); match != "" {
				return json.Marshal(types.AskUserPromptResponse{Selected: []string{match}})
			}
			if !q.AllowFreeText {
				return nil, fmt.Errorf("该提问只接受选项回答，可选：%s", strings.Join(q.Options, " / "))
			}
		}
		return json.Marshal(types.AskUserPromptResponse{Text: text})
	case types.AgentPromptKindToolApproval, types.AgentPromptKindMCPOAuth:
		// 自动批准等于绕过授权：这两类必须由人在会话/审批界面里显式确认。
		return nil, errors.New("工具调用与 MCP 授权需在会话界面确认，不能用文字答复代替")
	default:
		return nil, fmt.Errorf("该类型的挂起（%s）请在对应页面处理", p.Kind)
	}
}

// matchOption 在选项里找与答复一致的项（忽略大小写与首尾空白）。
func matchOption(options []string, text string) string {
	want := strings.ToLower(strings.TrimSpace(text))
	for _, opt := range options {
		if strings.ToLower(strings.TrimSpace(opt)) == want {
			return opt
		}
	}
	return ""
}
