package types

import (
	"errors"
	"fmt"
	"strings"
)

// 工作台 AI 的校验错误。这里只为「400 还是 413」提供可判定的类型，
// HTTP 状态与错误码由 service/handler 映射，types 包不依赖 errors 包。
var (
	ErrWorkbenchAIInvalidAction = errors.New("workbench ai: action must be one of polish, continue, translate, summarize, outline, custom")
	ErrWorkbenchAIEmptyText     = errors.New("workbench ai: text is required")
	ErrWorkbenchAIEmptyQuestion = errors.New("workbench ai: question is required")
)

// WorkbenchAILengthError 表示某个字段超过上限（→ 2402/413）。
type WorkbenchAILengthError struct {
	Field  string
	Limit  int
	Actual int
}

func (e *WorkbenchAILengthError) Error() string {
	return fmt.Sprintf("workbench ai: %s exceeds %d characters (got %d)", e.Field, e.Limit, e.Actual)
}

// 工作台 AI 的类型与常量（§11.1–§11.3）。
//
// 这里只定义契约，不放业务逻辑：校验函数只做「长度与白名单」这类纯判断，
// 让 handler 与 service 共用同一套口径，避免两边各写一份 limit。

// 工作台内联 AI 允许的六个 action。前端 19 个菜单项全部映射到这里，
// 预设名称不是安全边界，服务端只认这六个值（§11.2 规则 1）。
const (
	WorkbenchAIActionPolish    = "polish"
	WorkbenchAIActionContinue  = "continue"
	WorkbenchAIActionTranslate = "translate"
	WorkbenchAIActionSummarize = "summarize"
	WorkbenchAIActionOutline   = "outline"
	WorkbenchAIActionCustom    = "custom"
)

// 长度上限（§11.2 规则 2）。超限返回 2402/413，不静默截断。
const (
	WorkbenchAITextMaxRunes        = 8000
	WorkbenchAIInstructionMaxRunes = 500
	WorkbenchAIContextMaxRunes     = 4000
)

// workbenchAIActions 是 action 白名单；顺序即错误信息里的提示顺序。
var workbenchAIActions = []string{
	WorkbenchAIActionPolish,
	WorkbenchAIActionContinue,
	WorkbenchAIActionTranslate,
	WorkbenchAIActionSummarize,
	WorkbenchAIActionOutline,
	WorkbenchAIActionCustom,
}

// InlineAIRequest 内联车道请求（§11.2）。
//
// 刻意不收 preset_id：菜单只是前端预设，服务端只认 action 与 target_lang；
// 也不收 model_id / system_prompt / tools —— 那些一律由服务端解析，不信前端。
type InlineAIRequest struct {
	Action      string `json:"action" binding:"required"`
	Text        string `json:"text" binding:"required"`
	Instruction string `json:"instruction"`
	TargetLang  string `json:"target_lang"`
	ContextText string `json:"context_text"`
	ItemID      string `json:"item_id"`
	// AgentID 可选；空=租户默认模型 + 工作台基础提示词。
	AgentID string `json:"agent_id"`
	// Stream 为 false 时走非流式分支，用于测试与不支持 SSE 的客户端。
	Stream bool `json:"stream"`
}

// WorkbenchAgentConfig 是工作台从智能体读取的唯一白名单（§11.2）。
//
// 智能体的 KB 绑定、MCP、Skills、Sandbox、IM、分享、API Key 与 AllowedTools
// 一律不读：工作台三条车道的工具集与检索范围由车道固定。
type WorkbenchAgentConfig struct {
	AgentID      string
	ModelID      string
	SystemPrompt string
	Temperature  float64
	MaxTokens    int
	Thinking     *bool
	Reasoning    string
}

// InlineAIResult 非流式分支的返回体（§11.3 stream=false）。
type InlineAIResult struct {
	Text    string      `json:"text"`
	ModelID string      `json:"model_id"`
	AgentID string      `json:"agent_id,omitempty"`
	Usage   *TokenUsage `json:"usage,omitempty"`
}

// AskAIRequest 工作台问答车道的请求（§11.8）。
//
// 刻意不收 knowledge_base_ids：检索范围由服务端强制为「当前用户的工作 KB」，
// 不接受前端指定，私有性不能靠参数保证。
type AskAIRequest struct {
	Question       string `json:"question" binding:"required"`
	ItemID         string `json:"item_id"`
	DocTaskID      string `json:"doc_task_id"`
	IncludeContext bool   `json:"include_context"`
	AgentID        string `json:"agent_id"`
}

// WorkbenchAIReference 问答命中来源的展示投影。
// 只带定位所需字段，不回传 chunk 正文（内容已写进回答的提示词里）。
type WorkbenchAIReference struct {
	KnowledgeID    string `json:"knowledge_id"`
	KnowledgeTitle string `json:"knowledge_title"`
	ChunkIndex     int    `json:"chunk_index"`
	Score          float64 `json:"score"`
}

// Normalize 归一化问答请求的可选字段。
func (r *AskAIRequest) Normalize() {
	if r == nil {
		return
	}
	r.ItemID = strings.TrimSpace(r.ItemID)
	r.DocTaskID = strings.TrimSpace(r.DocTaskID)
	r.AgentID = strings.TrimSpace(r.AgentID)
}

// Validate 校验问题长度；超长与内联动作共用同一条 2402 口径。
func (r *AskAIRequest) Validate() error {
	if r == nil || strings.TrimSpace(r.Question) == "" {
		return ErrWorkbenchAIEmptyQuestion
	}
	if runeLen(r.Question) > WorkbenchAITextMaxRunes {
		return &WorkbenchAILengthError{
			Field: "question", Limit: WorkbenchAITextMaxRunes, Actual: runeLen(r.Question),
		}
	}
	return nil
}

// IsWorkbenchAIAction 判断 action 是否在白名单内。
func IsWorkbenchAIAction(action string) bool {
	for _, allowed := range workbenchAIActions {
		if action == allowed {
			return true
		}
	}
	return false
}

// Normalize 归一化可选字段：去空白、action 与 target_lang 统一小写无关，
// 但 target_lang 保留原样（前端只传 en / zh-Hans 这类稳定码）。
func (r *InlineAIRequest) Normalize() {
	if r == nil {
		return
	}
	r.Action = strings.ToLower(strings.TrimSpace(r.Action))
	r.ItemID = strings.TrimSpace(r.ItemID)
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.TargetLang = strings.TrimSpace(r.TargetLang)
}

// Validate 校验长度与 action 白名单；返回带字段名的错误，供 handler 映射成 2402/400。
//
// text 不能在服务端 trim 后判空：全空白选区没有生成价值，但错误语义是 400 而非 413。
func (r *InlineAIRequest) Validate() error {
	if r == nil {
		return ErrWorkbenchAIInvalidAction
	}
	if !IsWorkbenchAIAction(r.Action) {
		return ErrWorkbenchAIInvalidAction
	}
	if strings.TrimSpace(r.Text) == "" {
		return ErrWorkbenchAIEmptyText
	}
	if runeLen(r.Text) > WorkbenchAITextMaxRunes {
		return &WorkbenchAILengthError{Field: "text", Limit: WorkbenchAITextMaxRunes, Actual: runeLen(r.Text)}
	}
	if runeLen(r.Instruction) > WorkbenchAIInstructionMaxRunes {
		return &WorkbenchAILengthError{Field: "instruction", Limit: WorkbenchAIInstructionMaxRunes, Actual: runeLen(r.Instruction)}
	}
	if runeLen(r.ContextText) > WorkbenchAIContextMaxRunes {
		return &WorkbenchAILengthError{Field: "context_text", Limit: WorkbenchAIContextMaxRunes, Actual: runeLen(r.ContextText)}
	}
	return nil
}

// runeLen 按字符计数：上限面向用户可见字符，不是字节。
func runeLen(s string) int {
	if s == "" {
		return 0
	}
	return len([]rune(s))
}
