// ai_runner.go 是 AI 助手背后真正的问答执行器：把一次 Ask 翻译成一次 AgentQA 调用，
// 复用 Know 现有的智能体运行时（技能/知识库/MCP/模型），不另造一套。
//
// 会话口径与平台执行器一致：会话描述带 SOPSessionMarker，首页对话列表会隐藏它们；
// 追问复用同一会话（SessionID 非空时不新建）。
package sop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

// QADeps 是问答执行器的外部依赖（真实实现都是会话/消息服务）。
type QADeps struct {
	Sessions interface {
		CreateSession(ctx context.Context, session *types.Session) (*types.Session, error)
		GetSessionByID(ctx context.Context, tenantID uint64, id string) (*types.Session, error)
	}
	Messages interface {
		CreateMessage(ctx context.Context, message *types.Message) (*types.Message, error)
		UpdateMessage(ctx context.Context, message *types.Message) error
	}
	QA interface {
		AgentQA(ctx context.Context, req *types.QARequest, bus *event.EventBus) error
	}
	// Role 提供技能/MCP 等基础配置（可 nil：用空配置，仅用提示词）。
	Role *types.CustomAgent
}

// QARunner 基于 AgentQA 的 AI 助手执行器。
type QARunner struct{ deps QADeps }

// NewQARunner 构造执行器。
func NewQARunner(deps QADeps) *QARunner { return &QARunner{deps: deps} }

// Ask 跑一次问答并返回最终正文与会话 id。
//
// 无人值守（RunModeUnattended）：助手不弹提问卡片——它只需要一次成文的回答；
// 需要追问时把问题写进正文，由前端转成澄清项或多轮对话。
func (r *QARunner) Ask(ctx context.Context, req AIRequest) (string, string, error) {
	if r == nil || r.deps.QA == nil || r.deps.Sessions == nil || r.deps.Messages == nil {
		return "", "", errors.New("AI 助手未就绪：问答服务未注入")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return "", "", errors.New("AI 助手的输入为空")
	}
	session, err := r.session(ctx, req)
	if err != nil {
		return "", "", err
	}
	now := time.Now()
	userMsg, err := r.deps.Messages.CreateMessage(ctx, &types.Message{
		SessionID: session.ID, Role: "user", Content: req.Prompt,
		IsCompleted: true, CreatedAt: now, Channel: types.ChannelWeb,
	})
	if err != nil {
		return "", "", fmt.Errorf("记录助手输入失败：%w", err)
	}
	answerMsg, err := r.deps.Messages.CreateMessage(ctx, &types.Message{
		SessionID: session.ID, Role: "assistant", CreatedAt: now, Channel: types.ChannelWeb,
	})
	if err != nil {
		return "", "", fmt.Errorf("记录助手输出失败：%w", err)
	}

	bus := event.NewEventBus()
	var (
		mu  sync.Mutex
		out strings.Builder
	)
	bus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		if data, ok := evt.Data.(event.AgentFinalAnswerData); ok && data.Content != "" {
			mu.Lock()
			out.WriteString(data.Content)
			mu.Unlock()
		}
		return nil
	})
	bus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentCompleteData)
		if !ok {
			return nil
		}
		mu.Lock()
		if out.Len() == 0 && data.FinalAnswer != "" {
			out.WriteString(data.FinalAnswer)
		}
		mu.Unlock()
		return nil
	})

	qaReq := &types.QARequest{
		Session:            session,
		Query:              req.Prompt,
		AssistantMessageID: answerMsg.ID,
		UserMessageID:      userMsg.ID,
		CustomAgent:        aiAgent(req.System, r.deps.Role),
		Mode:               types.RunModeUnattended,
		RunNote: "这是 SOP 工作台的 AI 助手请求：一次成文回答，不要向用户弹出提问卡片；" +
			"需要更多信息时把问题写进正文。",
	}
	if err := r.deps.QA.AgentQA(ctx, qaReq, bus); err != nil {
		return "", session.ID, err
	}
	mu.Lock()
	text := strings.TrimSpace(out.String())
	mu.Unlock()

	answerMsg.Content = text
	answerMsg.IsCompleted = true
	answerMsg.UpdatedAt = time.Now()
	if saveErr := r.deps.Messages.UpdateMessage(context.WithoutCancel(ctx), answerMsg); saveErr != nil && text != "" {
		// 正文已拿到，落库失败只影响历史回看，不当作回答失败。
		_ = saveErr
	}
	return text, session.ID, nil
}

// session 复用或新建 SOP 助手会话。
func (r *QARunner) session(ctx context.Context, req AIRequest) (*types.Session, error) {
	if id := strings.TrimSpace(req.SessionID); id != "" {
		session, err := r.deps.Sessions.GetSessionByID(ctx, req.TenantID, id)
		if err != nil || session == nil {
			return nil, fmt.Errorf("复用助手会话失败：%w", err)
		}
		return session, nil
	}
	created, err := r.deps.Sessions.CreateSession(ctx, &types.Session{
		Title:       "SOP 助手",
		Description: types.SOPSessionMarker + "ai",
		TenantID:    req.TenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("创建助手会话失败：%w", err)
	}
	if created == nil {
		return nil, errors.New("创建助手会话失败：会话服务没有返回会话")
	}
	return created, nil
}

// aiAgent 组装一次问答用的角色配置：系统提示 = 助手约束 + 可选的用户追加约束。
//
// 角色对象深拷贝后再改：同一角色可能被并行请求共用，就地改写会让约束串台。
func aiAgent(system string, base *types.CustomAgent) *types.CustomAgent {
	var clone types.CustomAgent
	if base != nil {
		clone = *base
	}
	parts := []string{"你是 SOP 工作台的 AI 助手，回答简洁、对事不对人，直接给结论与要点。"}
	if s := strings.TrimSpace(system); s != "" {
		parts = append(parts, s)
	}
	clone.Config.SystemPrompt = strings.Join(parts, "\n\n")
	return &clone
}
