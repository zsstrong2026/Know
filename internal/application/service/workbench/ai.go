// 工作台 AI 内联车道（§11.1–§11.3）。
//
// 只做「文字进、文字出」：不挂工具、不检索、不写数据库。条目归属与模型解析都在
// 服务端完成，前端传来的 model_id / system_prompt / tools 一律不读。
package workbench

import (
	"context"
	"errors"
	"strings"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// aiModelProvider 是内联车道需要的模型能力面：列模型 + 取聊天模型。
// 用窄接口而不是完整 ModelService，测试里只实现两个方法即可。
type aiModelProvider interface {
	GetChatModel(ctx context.Context, modelID string) (chat.Chat, error)
	ListModels(ctx context.Context) ([]*types.Model, error)
}

// aiAgentProvider 只按 tenant + id 取智能体；内置智能体由实现自行回退。
type aiAgentProvider interface {
	GetAgentByID(ctx context.Context, id string) (*types.CustomAgent, error)
}

// aiPromptResolver 解析智能体的系统提示（含 system_prompt_id 模板引用）。
type aiPromptResolver interface {
	ResolveCustomAgentPrompts(agent *types.CustomAgent) (system, context string)
}

// aiRetriever 是问答车道需要的检索能力面。
// 只暴露 SearchKnowledge：问答不走 Agent 引擎，也不挂任何工具。
type aiRetriever interface {
	SearchKnowledge(
		ctx context.Context, knowledgeBaseIDs []string, knowledgeIDs []string,
		tagScopes []types.TagScope, query string, opts *types.KnowledgeSearchOptions,
	) (*types.RetrievalResult, error)
}

// aiWorkKBProvider 取（必要时懒创建）当前用户的私有工作知识库。
type aiWorkKBProvider interface {
	EnsureWorkKnowledgeBase(ctx context.Context, tenantID uint64, userID string) (*types.KnowledgeBase, error)
}

// AIService 工作台内联 AI 服务。
//
// 依赖刻意用本包的窄接口：内联车道只用到「列模型 / 取聊天模型 / 取智能体 /
// 解析提示词」四件事，宽接口会让测试为了编译而实现几十个无关方法。
type AIService struct {
	repo    interfaces.WorkbenchRepository
	models  aiModelProvider
	agents  aiAgentProvider
	prompts aiPromptResolver
	// 问答车道依赖；未注入时 /ai/ask 返回 503，内联车道不受影响。
	retriever aiRetriever
	workKB    aiWorkKBProvider
}

// NewAIService 构造内联 AI 服务。
func NewAIService(
	repo interfaces.WorkbenchRepository,
	models aiModelProvider,
	agents aiAgentProvider,
	prompts aiPromptResolver,
) *AIService {
	return &AIService{repo: repo, models: models, agents: agents, prompts: prompts}
}

// WithAskLane 注入问答车道所需的检索与工作知识库依赖。
func (s *AIService) WithAskLane(retriever aiRetriever, workKB aiWorkKBProvider) *AIService {
	s.retriever = retriever
	s.workKB = workKB
	return s
}

// InlineStream 执行一次内联动作，返回 StreamResponse 通道（§11.2、§11.3）。
//
// 通道里的约定：正文增量是 ResponseTypeAnswer 且 Done=false；正常结束发一条
// Done=true 并带 Usage；上游中断发 ResponseTypeError 并带 Done=true。handler 只做
// 帧格式转换，不参与判断。
//
// 校验与解析都在启动 goroutine 之前完成：这样“无模型 / 条目不存在”还能返回普通
// HTTP 错误，而不是先开流再报错（流已开始就改不了状态码）。
func (s *AIService) InlineStream(
	ctx context.Context, req types.InlineAIRequest,
) (<-chan types.StreamResponse, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	req.Normalize()
	if err := req.Validate(); err != nil {
		return nil, mapInlineValidationError(err)
	}
	// item_id 可选，但传了就必须是当前用户的条目（2400 而不是 403）。
	if req.ItemID != "" {
		if _, err := s.repo.GetItemMeta(ctx, tenantID, ownerID, req.ItemID); err != nil {
			return nil, notFound("workbench item not found")
		}
	}

	agentConfig, err := s.resolveWorkbenchAgent(ctx, req.AgentID)
	if err != nil {
		return nil, err
	}
	chatModel, err := s.chatModelFor(ctx, agentConfig)
	if err != nil {
		return nil, err
	}

	promptMessages := buildInlineMessages(req, agentConfig)
	messages := toChatMessages(promptMessages)
	opts := chatOptionsFor(agentConfig)

	// 日志只记动作与字符数，不记正文（§11.9）。
	logger.Infof(ctx, "workbench ai inline: action=%s target_lang=%s item=%s agent=%s prompt=%s",
		req.Action, req.TargetLang, req.ItemID, req.AgentID, promptDebugSummary(promptMessages))

	upstream, err := chatModel.ChatStream(ctx, messages, opts)
	if err != nil {
		return nil, apperrors.NewWorkbenchStreamBroken("model call failed")
	}
	if upstream == nil {
		return nil, apperrors.NewWorkbenchStreamBroken("model stream is empty")
	}

	out := make(chan types.StreamResponse, 16)
	go func() {
		// 通道所有权归调用方：forwardInlineStream 只写不关，避免两层 defer close 冲突。
		defer close(out)
		forwardInlineStream(ctx, upstream, out, chatModel.GetModelID(), req.AgentID)
	}()
	return out, nil
}

// forwardInlineStream 把上游 chunk 转成内联协议的帧。
//
// 只写不关通道：关闭由发起方的 goroutine 统一负责，否则 AskStream 的
// references 预处理与这里的两层 defer 会重复 close 同一个通道。
func forwardInlineStream(
	ctx context.Context,
	upstream <-chan types.StreamResponse,
	out chan<- types.StreamResponse,
	modelID, agentID string,
) {
	var usage *types.TokenUsage
	trimmer := &prefixTrimmer{}

	emit := func(resp types.StreamResponse) bool {
		select {
		case out <- resp:
			return true
		case <-ctx.Done():
			return false
		}
	}
	emitText := func(text string) bool {
		if text == "" {
			return true
		}
		return emit(types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Content:      text,
		})
	}

	for {
		select {
		case <-ctx.Done():
			// 客户端断开：不再写帧，由 handler 侧关闭连接。
			return
		case resp, ok := <-upstream:
			if !ok {
				// 收尾时把被前缀探测扣住的尾巴放出去，否则短回答会“一个字都不显示”。
				if !emitText(trimmer.flush()) {
					return
				}
				emit(types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Done:         true,
					Usage:        usage,
					Data:         doneData(modelID, agentID),
				})
				return
			}
			if resp.Usage != nil {
				usage = resp.Usage
			}
			switch resp.ResponseType {
			case types.ResponseTypeAnswer:
				if !emitText(trimmer.push(resp.Content)) {
					return
				}
			case types.ResponseTypeError:
				// 不把上游错误正文写进日志或响应：可能是带内容的 provider 报错。
				logger.Warnf(ctx, "workbench ai inline: upstream stream error, model=%s", modelID)
				// 已经发出去的正文保留，错误帧让前端“保留部分结果并允许重试”。
				emit(types.StreamResponse{
					ResponseType: types.ResponseTypeError,
					Content:      "model stream interrupted",
					Done:         true,
					Data:         doneData(modelID, agentID),
				})
				return
			default:
				// thinking / tool_call / references 等不属于内联车道，直接丢弃。
			}
		}
	}
}

// doneData 是 done 帧的附加字段（§11.3）。
func doneData(modelID, agentID string) map[string]interface{} {
	data := map[string]interface{}{"model_id": modelID}
	if agentID != "" {
		data["agent_id"] = agentID
	}
	return data
}

// resolveWorkbenchAgent 解析工作台使用的智能体白名单（§11.2）。
//
// agent_id 为空返回 (nil, nil)：表示使用租户默认模型与工作台基础提示词。
// 非空时只取模型、系统提示与生成参数；KB 绑定、MCP、Skills、Sandbox、
// AllowedTools 全部不读——工具集由车道固定。
func (s *AIService) resolveWorkbenchAgent(
	ctx context.Context, agentID string,
) (*types.WorkbenchAgentConfig, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, nil
	}
	if s.agents == nil {
		return nil, apperrors.NewWorkbenchNotFoundError("workbench agent is not configured")
	}
	agent, err := s.agents.GetAgentByID(ctx, agentID)
	if err != nil || agent == nil {
		// 跨租户、已删除、不存在统一 404，避免通过错误码探测他人智能体（§11.9）。
		return nil, apperrors.NewWorkbenchNotFoundError("workbench agent not found")
	}
	systemPrompt := agent.Config.SystemPrompt
	if s.prompts != nil {
		if resolved, _ := s.prompts.ResolveCustomAgentPrompts(agent); resolved != "" {
			systemPrompt = resolved
		}
	}
	return &types.WorkbenchAgentConfig{
		AgentID:      agent.ID,
		ModelID:      agent.Config.ModelID,
		SystemPrompt: systemPrompt,
		Temperature:  agent.Config.Temperature,
		MaxTokens:    agent.Config.MaxCompletionTokens,
		Thinking:     agent.Config.Thinking,
		Reasoning:    agent.Config.ReasoningEffort,
	}, nil
}

// chatModelFor 取聊天模型：智能体指定的优先，否则用租户默认 KnowledgeQA 模型。
// 一个可用的都没有返回 2408，让前端给出「去配置模型」而不是反复重试。
func (s *AIService) chatModelFor(
	ctx context.Context, agent *types.WorkbenchAgentConfig,
) (chat.Chat, error) {
	if agent != nil && strings.TrimSpace(agent.ModelID) != "" {
		model, err := s.models.GetChatModel(ctx, agent.ModelID)
		if err != nil || model == nil {
			return nil, apperrors.NewWorkbenchNoModel()
		}
		return model, nil
	}
	models, err := s.models.ListModels(ctx)
	if err != nil {
		return nil, apperrors.NewWorkbenchNoModel()
	}
	var fallback *types.Model
	for _, m := range models {
		if m.Type != types.ModelTypeKnowledgeQA {
			continue
		}
		if m.IsDefault {
			if model, err := s.models.GetChatModel(ctx, m.ID); err == nil && model != nil {
				return model, nil
			}
		}
		if fallback == nil {
			fallback = m
		}
	}
	if fallback == nil {
		return nil, apperrors.NewWorkbenchNoModel()
	}
	model, err := s.models.GetChatModel(ctx, fallback.ID)
	if err != nil || model == nil {
		return nil, apperrors.NewWorkbenchNoModel()
	}
	return model, nil
}

// chatOptionsFor 只透传生成参数，不挂工具（Tools 保持 nil）。
func chatOptionsFor(agent *types.WorkbenchAgentConfig) *chat.ChatOptions {
	opts := &chat.ChatOptions{}
	if agent == nil {
		// 未选智能体时用偏保守的温度，避免默认 0 让写作类动作过于僵硬。
		opts.Temperature = 0.3
		return opts
	}
	opts.Temperature = agent.Temperature
	opts.MaxCompletionTokens = agent.MaxTokens
	opts.Thinking = agent.Thinking
	if agent.Reasoning != "" {
		opts.ReasoningEffort = chat.ReasoningEffort(agent.Reasoning)
	}
	return opts
}

// toChatMessages 把纯函数组装出的消息转成模型层类型。
func toChatMessages(messages []chatMessage) []chat.Message {
	out := make([]chat.Message, 0, len(messages))
	for _, m := range messages {
		out = append(out, chat.Message{Role: m.Role, Content: m.Content})
	}
	return out
}

// mapInlineValidationError 把 types 包的长度/白名单错误映射成对外错误码。
func mapInlineValidationError(err error) error {
	var lengthErr *types.WorkbenchAILengthError
	if errors.As(err, &lengthErr) {
		return apperrors.NewWorkbenchTooLarge(int64(lengthErr.Limit), int64(lengthErr.Actual))
	}
	if errors.Is(err, types.ErrWorkbenchAIInvalidAction) {
		return apperrors.NewBadRequestError(
			"action must be one of polish, continue, translate, summarize, outline, custom")
	}
	return apperrors.NewBadRequestError(err.Error())
}
