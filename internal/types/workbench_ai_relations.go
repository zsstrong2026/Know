package types

import (
	"errors"
	"strings"
	"time"
)

// 工作台「智能关联」的请求与响应契约。
//
// 与画布 AI / 数据库 AI 同构的分工：服务端只做召回与判读，不写画布——
// 前端拿到结果后预览勾选，才把引用卡片与连线写进 Y.Doc。
//
// 与其它车道的差异：这里需要读候选文档的正文摘要，而列表接口刻意不返回
// text_content（WorkbenchItem.TextContent 是 json:"-"），因此召回必须在服务端
// 完成，不能像画布 AI 那样由前端带上快照。
const (
	// WorkbenchAIRelationsDefaultLimit 默认返回条数。
	WorkbenchAIRelationsDefaultLimit = 6
	// WorkbenchAIRelationsMaxLimit 单次返回上限：画布放不下更多，也避免噪声。
	WorkbenchAIRelationsMaxLimit = 6
	// WorkbenchAIRelationsMaxExclude 前端最多上报多少个「已应用 / 已忽略」条目。
	WorkbenchAIRelationsMaxExclude = 100
	// WorkbenchAIRelationsMaxRunes / TargetMaxRunes 双方关联句的长度上限。
	WorkbenchAIRelationsMaxRunes       = 120
	WorkbenchAIRelationsTargetMaxRunes = 120
	// WorkbenchAIRelationsReasonMaxRunes 关联理由长度上限。
	WorkbenchAIRelationsReasonMaxRunes = 60
	// WorkbenchAIRelationsLabelMaxRunes 连线标签长度上限。
	WorkbenchAIRelationsLabelMaxRunes = 24
	// WorkbenchAIRelationsTitleMaxRunes 候选标题在提示词里的长度上限。
	WorkbenchAIRelationsTitleMaxRunes = 80
	// WorkbenchAIRelationsDigestMaxRunes 单个候选在提示词里的摘要上限。
	WorkbenchAIRelationsDigestMaxRunes = 120
	// WorkbenchAIRelationsSourceTextMaxRunes 当前文档送进提示词的正文上限。
	WorkbenchAIRelationsSourceTextMaxRunes = 1500
	// WorkbenchAIRelationsCandidatePool 候选池规模（按最近更新取）。
	WorkbenchAIRelationsCandidatePool = 300
	// WorkbenchAIRelationsShortlist 粗排后送进模型的候选数。
	WorkbenchAIRelationsShortlist = 50

	// 关联强度：strong 默认勾选，weak 折叠在「更多候选」里。
	WorkbenchRelationStrengthStrong = "strong"
	WorkbenchRelationStrengthWeak   = "weak"

	// 召回来源：用于前端展示「基于引用关系」等提示。
	WorkbenchRelationSourceMention  = "mention"
	WorkbenchRelationSourceTag      = "tag"
	WorkbenchRelationSourceKeyword  = "keyword"
	WorkbenchRelationSourceSemantic = "semantic"

	// 降级原因：没有聊天模型时只返回关键词召回结果。
	WorkbenchRelationDegradedNoModel = "no_model"
)

var (
	// ErrWorkbenchAIRelationsEmptyItem 表示请求缺少 item_id。
	ErrWorkbenchAIRelationsEmptyItem = errors.New("workbench relations ai: item_id is required")
	// ErrWorkbenchAIRelationsBadOutput 表示模型输出不是合法的关联结构。
	ErrWorkbenchAIRelationsBadOutput = errors.New("workbench relations ai: model output is not valid")
)

// WorkbenchAIRelationsRequest 智能关联的召回请求。
type WorkbenchAIRelationsRequest struct {
	ItemID string `json:"item_id" binding:"required"`
	// AgentID 可选：指定智能体只影响措辞与模型选择，不影响召回口径。
	AgentID string `json:"agent_id"`
	// Limit 期望返回条数；0 走默认值，超过上限按上限裁剪。
	Limit int `json:"limit"`
	// ExcludeItemIDs 是前端已应用 / 已忽略的条目，召回阶段直接剔除。
	ExcludeItemIDs []string `json:"exclude_item_ids"`
}

// WorkbenchAIRelation 一条关联结果。
//
// Quote / TargetQuote 是「原句」而不是摘要：卡片内要高亮它们，
// 打开原文时也按它们做块级定位。
type WorkbenchAIRelation struct {
	ItemID      string `json:"item_id"`
	Title       string `json:"title"`
	Reason      string `json:"reason"`
	Strength    string `json:"strength"`
	Quote       string `json:"quote"`
	TargetQuote string `json:"target_quote"`
	// Label 是画布连线上的短标签。
	Label string `json:"label"`
	// Source 是召回来源，供前端提示「基于引用关系」。
	Source string `json:"source"`
}

// WorkbenchAIRelationsResult 召回与判读的合并结果。
type WorkbenchAIRelationsResult struct {
	Relations []WorkbenchAIRelation `json:"relations"`
	// DegradedReason 非空表示没走模型，前端据此提示语义关联不可用。
	DegradedReason string `json:"degraded_reason,omitempty"`
	// Truncated 表示候选池或模型输出被裁剪（含模型编造的 id 被丢弃）。
	Truncated bool        `json:"truncated"`
	ModelID   string      `json:"model_id,omitempty"`
	AgentID   string      `json:"agent_id,omitempty"`
	Usage     *TokenUsage `json:"usage,omitempty"`
}

// WorkbenchRelationCandidate 召回阶段的候选快照。
//
// 只带判断主题所需的最小字段：标题 + 正文摘要 + 标签 id。
// TextContent 在服务层内使用，不出现在任何 JSON 响应里。
type WorkbenchRelationCandidate struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Digest     string    `json:"digest"`
	TagIDs     []string  `json:"tag_ids"`
	UpdatedAt  time.Time `json:"updated_at"`
	MatchScore int       `json:"-"`
	MatchedBy  []string  `json:"-"`
}

// Normalize 收敛空白、裁剪超限、去重排除项。
func (r *WorkbenchAIRelationsRequest) Normalize() {
	r.ItemID = strings.TrimSpace(r.ItemID)
	r.AgentID = strings.TrimSpace(r.AgentID)
	if r.Limit <= 0 {
		r.Limit = WorkbenchAIRelationsDefaultLimit
	}
	if r.Limit > WorkbenchAIRelationsMaxLimit {
		r.Limit = WorkbenchAIRelationsMaxLimit
	}
	seen := make(map[string]struct{}, len(r.ExcludeItemIDs))
	out := make([]string, 0, len(r.ExcludeItemIDs))
	for _, id := range r.ExcludeItemIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" || trimmed == r.ItemID {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
		if len(out) >= WorkbenchAIRelationsMaxExclude {
			break
		}
	}
	r.ExcludeItemIDs = out
}

// Validate 校验请求自身边界（不含候选与补丁）。
func (r WorkbenchAIRelationsRequest) Validate() error {
	if r.ItemID == "" {
		return ErrWorkbenchAIRelationsEmptyItem
	}
	return nil
}

// NormalizeRelation 统一清洗一条模型输出：截断、补默认值、丢弃无法定位的弱关联。
//
// 返回 false 表示这条结果不可用（缺 item_id 或缺关联句）。
func (r *WorkbenchAIRelation) NormalizeRelation() bool {
	r.ItemID = strings.TrimSpace(r.ItemID)
	if r.ItemID == "" {
		return false
	}
	r.Title = truncateRunes(strings.TrimSpace(r.Title), WorkbenchAIRelationsTitleMaxRunes)
	r.Reason = truncateRunes(collapseSpaces(r.Reason), WorkbenchAIRelationsReasonMaxRunes)
	r.Quote = truncateRunes(collapseSpaces(r.Quote), WorkbenchAIRelationsMaxRunes)
	r.TargetQuote = truncateRunes(collapseSpaces(r.TargetQuote), WorkbenchAIRelationsTargetMaxRunes)
	r.Label = truncateRunes(collapseSpaces(r.Label), WorkbenchAIRelationsLabelMaxRunes)
	switch r.Strength {
	case WorkbenchRelationStrengthStrong, WorkbenchRelationStrengthWeak:
	default:
		// 模型给不出强度时按弱关联处理：宁可默认不勾选。
		r.Strength = WorkbenchRelationStrengthWeak
	}
	switch r.Source {
	case WorkbenchRelationSourceMention, WorkbenchRelationSourceTag,
		WorkbenchRelationSourceKeyword, WorkbenchRelationSourceSemantic:
	default:
		r.Source = WorkbenchRelationSourceSemantic
	}
	// 强弱判定的硬标准：连一句关联原文都指不出来，就只能算弱关联。
	if r.Quote == "" || r.TargetQuote == "" {
		r.Strength = WorkbenchRelationStrengthWeak
	}
	if r.Label == "" {
		r.Label = r.Reason
	}
	if r.Label == "" {
		r.Label = "关联"
	}
	return true
}

// collapseSpaces 把换行与连续空白压成单个空格：关联句要放进单行卡片与连线标签。
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
