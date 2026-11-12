package types

import (
	"errors"
	"fmt"
	"strings"
)

// 工作台「事项识别」的请求与结果契约。
//
// 与数据库 AI 的分工：数据库 AI 按用户一句话生成补丁；这里只做「从文档正文里
// 抽取待办事项」，服务端不写库、不解析 Y.Doc，候选行由前端确认或直接写入事项表。
// 输出同样是整包 JSON（非流式），因为半截 JSON 无法预览。

const (
	// WorkbenchAIItemsMarkdownMaxRunes 是参与抽取的正文上限；超出直接拒绝，
	// 避免把整篇长文塞进上下文（前端可先截取或让用户分段识别）。
	WorkbenchAIItemsMarkdownMaxRunes = 200_000
	// WorkbenchAIItemsDefaultLimit / MaxLimit 是单次候选条数上限。
	WorkbenchAIItemsDefaultLimit = 20
	WorkbenchAIItemsMaxLimit     = 50
	// 单条候选的字段上限。
	WorkbenchAIItemsTitleMaxRunes   = 200
	WorkbenchAIItemsOwnerMaxRunes   = 60
	WorkbenchAIItemsEvidenceMaxRune = 300
)

// ErrWorkbenchAIItemsEmptySource 表示请求没带正文。
var ErrWorkbenchAIItemsEmptySource = errors.New("workbench items ai: markdown is required")

// ErrWorkbenchAIItemsBadPayload 表示模型输出不是合法的事项候选 JSON。
var ErrWorkbenchAIItemsBadPayload = errors.New("workbench items ai: model output is not valid items")

// WorkbenchAIItemsRequest 一次事项抽取请求。
//
// ItemID 是来源文档条目 id（用于归属校验与来源回填），必填；
// StatusOptions / PriorityOptions 是事项表当前语言的选项值，模型只能从中挑。
type WorkbenchAIItemsRequest struct {
	ItemID  string `json:"item_id" binding:"required"`
	AgentID string `json:"agent_id"`
	// SourceTitle 是来源文档标题，写进候选的「来源文档」列。
	SourceTitle string `json:"source_title"`
	// Markdown 是文档正文（前端用 projection 投影得到）。
	Markdown string `json:"markdown"`
	// StatusOptions / PriorityOptions 允许的取值；空表示不限制。
	StatusOptions   []string `json:"status_options"`
	PriorityOptions []string `json:"priority_options"`
	// ExistingTitles 是事项表已有标题，供模型去重（只做提示，最终去重仍在前端）。
	ExistingTitles []string `json:"existing_titles"`
	// Limit 期望的候选条数；0 用默认值。
	Limit int `json:"limit"`
}

// WorkbenchAIItemCandidate 是一条识别出来的事项。
//
// 全部字段都是可选（除 Title）：模型没把握时留空，由前端按列配置补默认值。
type WorkbenchAIItemCandidate struct {
	Title      string  `json:"title"`
	Status     string  `json:"status,omitempty"`
	Owner      string  `json:"owner,omitempty"`
	Due        string  `json:"due,omitempty"`
	Priority   string  `json:"priority,omitempty"`
	Relation   string  `json:"relation,omitempty"`
	Evidence   string  `json:"evidence,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// WorkbenchAIItemsPayload 是模型必须返回的 JSON 结构。
type WorkbenchAIItemsPayload struct {
	Items []WorkbenchAIItemCandidate `json:"items"`
}

// WorkbenchAIItemsResult 是抽取结果，附带模型标识供前端展示来源。
type WorkbenchAIItemsResult struct {
	Items   []WorkbenchAIItemCandidate `json:"items"`
	ModelID string                     `json:"model_id"`
	AgentID string                     `json:"agent_id,omitempty"`
	Usage   *TokenUsage                `json:"usage,omitempty"`
}

// Normalize 归一化请求：去空白、截断超限的参考列表。
func (r *WorkbenchAIItemsRequest) Normalize() {
	if r == nil {
		return
	}
	r.ItemID = strings.TrimSpace(r.ItemID)
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.SourceTitle = strings.TrimSpace(r.SourceTitle)
	r.Markdown = strings.TrimSpace(r.Markdown)
	if r.Limit <= 0 {
		r.Limit = WorkbenchAIItemsDefaultLimit
	}
	if r.Limit > WorkbenchAIItemsMaxLimit {
		r.Limit = WorkbenchAIItemsMaxLimit
	}
	// 参考列表只影响提示词质量，超限直接截断而不是报错。
	r.StatusOptions = trimList(r.StatusOptions, 20)
	r.PriorityOptions = trimList(r.PriorityOptions, 20)
	r.ExistingTitles = trimList(r.ExistingTitles, 200)
}

// Validate 校验正文与规模；错误交给 handler 映射成 2402 / 2400。
func (r *WorkbenchAIItemsRequest) Validate() error {
	if r == nil || r.Markdown == "" {
		return ErrWorkbenchAIItemsEmptySource
	}
	if runeLen(r.Markdown) > WorkbenchAIItemsMarkdownMaxRunes {
		return &WorkbenchAILengthError{
			Field: "markdown", Limit: WorkbenchAIItemsMarkdownMaxRunes, Actual: runeLen(r.Markdown),
		}
	}
	return nil
}

// Validate 校验模型输出：标题必填、长度受限、枚举值必须在允许集合内。
//
// 不在集合内的枚举值不报错而是清空：宁可少一个字段，也不要因为模型多写了一个
// 「紧急」把整批候选作废。
func (p *WorkbenchAIItemsPayload) Validate(
	statusOptions, priorityOptions []string,
) error {
	if p == nil {
		return fmt.Errorf("%w: payload is nil", ErrWorkbenchAIItemsBadPayload)
	}
	if len(p.Items) == 0 {
		return fmt.Errorf("%w: payload has no items", ErrWorkbenchAIItemsBadPayload)
	}
	statusSet := toLowerSet(statusOptions)
	prioritySet := toLowerSet(priorityOptions)
	for index := range p.Items {
		item := &p.Items[index]
		item.Title = strings.TrimSpace(item.Title)
		if item.Title == "" {
			return fmt.Errorf("%w: items[%d]: title is required", ErrWorkbenchAIItemsBadPayload, index)
		}
		if runeLen(item.Title) > WorkbenchAIItemsTitleMaxRunes {
			item.Title = truncateWorkbenchRunes(item.Title, WorkbenchAIItemsTitleMaxRunes)
		}
		item.Owner = truncateWorkbenchRunes(strings.TrimSpace(item.Owner), WorkbenchAIItemsOwnerMaxRunes)
		item.Relation = truncateWorkbenchRunes(strings.TrimSpace(item.Relation), WorkbenchAIItemsOwnerMaxRunes)
		item.Evidence = truncateWorkbenchRunes(strings.TrimSpace(item.Evidence), WorkbenchAIItemsEvidenceMaxRune)
		item.Due = normalizeDueDate(item.Due)
		if len(statusSet) > 0 && !statusSet[strings.ToLower(item.Status)] {
			item.Status = ""
		}
		if len(prioritySet) > 0 && !prioritySet[strings.ToLower(item.Priority)] {
			item.Priority = ""
		}
		if item.Confidence < 0 {
			item.Confidence = 0
		}
		if item.Confidence > 1 {
			item.Confidence = 1
		}
	}
	return nil
}

// normalizeDueDate 只接受 YYYY-MM-DD；其它写法一律清空，避免写进日期列时解析失败。
func normalizeDueDate(value string) string {
	text := strings.TrimSpace(value)
	if len(text) != 10 || text[4] != '-' || text[7] != '-' {
		return ""
	}
	for i, r := range text {
		if i == 4 || i == 7 {
			continue
		}
		if r < '0' || r > '9' {
			return ""
		}
	}
	return text
}

func trimList(values []string, limit int) []string {
	if len(values) == 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func toLowerSet(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[strings.ToLower(strings.TrimSpace(value))] = true
	}
	return set
}

func truncateWorkbenchRunes(value string, limit int) string {
	if runeLen(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}
