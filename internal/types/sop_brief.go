package types

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// DeriveParentExecState 父事项（大项）执行情况：由子项五态汇总，不落库。
//
// 口径与事项五态一致（spec §3.10）：任一子项 intervene→intervene；否则任一
// running→running；全部 closed→closed；否则任一 pr→pr；否则 none。
// 空容器（还没有子项）= none：大项可以为空，不能因为「没有子项」就当作已闭环。
func DeriveParentExecState(states []string) string {
	if len(states) == 0 {
		return SopExecStateNone
	}
	seen := make(map[string]bool, len(states))
	for _, s := range states {
		seen[s] = true
	}
	switch {
	case seen[SopExecStateIntervene]:
		return SopExecStateIntervene
	case seen[SopExecStateRunning]:
		return SopExecStateRunning
	case seen[SopExecStatePR]:
		return SopExecStatePR
	case seen[SopExecStateClosed] && len(seen) == 1:
		return SopExecStateClosed
	}
	return SopExecStateNone
}

// ---- 项目简报渲染 ----
//
// 简报是黑板的投影（spec §3.10），只依赖入参、无 IO、无时间源（时间由入参提供），
// 同一份入参永远渲染出同一段 Markdown——渲染幂等是「seq 未变就不重写知识库」的前提。

// briefDefaultMaxChars 是未指定上限时的正文硬顶。
const briefDefaultMaxChars = 4000

// BriefItem 简报里的事项行：父项（大项）带子项完成计数。
type BriefItem struct {
	No         string `json:"no"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	ExecState  string `json:"exec_state"`
	ChildDone  int    `json:"child_done"`
	ChildTotal int    `json:"child_total"`
}

// BriefInput 简报渲染入参。
type BriefInput struct {
	Project SopProject
	Items   []BriefItem
	Notes   []SopProjectNote
	// MaxChars 是正文硬顶（按 rune 计）；<=0 取 briefDefaultMaxChars。
	MaxChars int
}

// briefLimits 是一档截断预算：0 表示不限制。
type briefLimits struct {
	maxItems       int
	maxDecisions   int
	maxRisks       int
	maxSummaries   int
	decisionBodyAt int // >0 时决策正文截断到此长度
}

// briefDegrade 是按优先级从高到低保留内容的档位：
// 先舍运行结论，再舍风险，再截决策正文，最后缩事项列表（spec §3.10 的裁减顺序）。
var briefDegrade = []briefLimits{
	{maxItems: 20, maxDecisions: 8, maxRisks: 5, maxSummaries: 8},
	{maxItems: 20, maxDecisions: 8, maxRisks: 5},
	{maxItems: 20, maxDecisions: 8},
	{maxItems: 20, maxDecisions: 8, decisionBodyAt: 80},
	{maxItems: 5, maxDecisions: 8, decisionBodyAt: 40},
}

// briefExecPriority 是「在进行的事项」的排序权重：越靠前越需要人关注。
var briefExecPriority = map[string]int{
	SopExecStateIntervene: 0,
	SopExecStateRunning:   1,
	SopExecStatePR:        2,
	SopExecStateNone:      3,
}

// RenderProjectBrief 渲染项目简报正文（Markdown）。
//
// 固定段落顺序：项目名 → 项目概况 → 在进行的事项 → 最近决策 → 最近风险 →
// 最近运行结论。超过硬顶时按 briefDegrade 逐档裁减；仍超长则硬截断并追加
// 「> 完整内容见项目动态」。
func RenderProjectBrief(in BriefInput) string {
	maxChars := in.MaxChars
	if maxChars <= 0 {
		maxChars = briefDefaultMaxChars
	}
	for _, lim := range briefDegrade {
		out := renderBrief(in, lim)
		if utf8.RuneCountInString(out) <= maxChars {
			return out
		}
	}
	out := renderBrief(in, briefDegrade[len(briefDegrade)-1])
	return truncateRunes(out, maxChars) + "\n\n> 完整内容见项目动态\n"
}

func renderBrief(in BriefInput, lim briefLimits) string {
	var b strings.Builder
	name := in.Project.Name
	if name == "" {
		name = "项目"
	}
	fmt.Fprintf(&b, "# %s\n\n", name)

	// 概况：编号前缀 / 知识库绑定 / 默认流程。
	b.WriteString("## 项目概况\n\n")
	if in.Project.Code != "" {
		fmt.Fprintf(&b, "- 编号前缀：%s\n", in.Project.Code)
	}
	if in.Project.BriefKnowledgeID != "" {
		b.WriteString("- 知识库：已绑定（简报同步到该库）\n")
	} else {
		b.WriteString("- 知识库：未绑定（简报暂不落库）\n")
	}
	if in.Project.DefaultTemplateID != "" {
		fmt.Fprintf(&b, "- 默认流程：%s\n", in.Project.DefaultTemplateID)
	} else {
		b.WriteString("- 默认流程：未指定\n")
	}

	if items := briefActiveItems(in.Items, lim.maxItems); len(items) > 0 {
		b.WriteString("\n## 在进行的事项\n\n")
		for _, it := range items {
			label := SopExecStateLabels[it.ExecState]
			if label == "" {
				label = SopExecStateLabels[SopExecStateNone]
			}
			fmt.Fprintf(&b, "- %s %s · %s", it.No, it.Title, label)
			if it.ChildTotal > 0 {
				fmt.Fprintf(&b, "（子项 %d/%d）", it.ChildDone, it.ChildTotal)
			}
			b.WriteString("\n")
		}
	}

	if decisions := selectBriefNotes(in.Notes, SopNoteKindDecision, lim.maxDecisions); len(decisions) > 0 {
		b.WriteString("\n## 最近决策\n\n")
		for _, n := range decisions {
			title := n.Title
			if title == "" {
				title = firstLine(n.Body)
			}
			fmt.Fprintf(&b, "### %s\n\n", title)
			if body := briefBody(n.Body, lim.decisionBodyAt); body != "" {
				fmt.Fprintf(&b, "%s\n\n", body)
			}
			fmt.Fprintf(&b, "（%s · %s）\n\n", briefSourceLabel(n.Source), briefTime(n))
		}
	}

	if risks := selectBriefNotes(in.Notes, SopNoteKindRisk, lim.maxRisks); len(risks) > 0 {
		b.WriteString("## 最近风险\n\n")
		for _, n := range risks {
			fmt.Fprintf(&b, "- %s：%s\n", briefNoteTitle(n), firstLine(n.Body))
		}
	}

	if summaries := selectBriefNotes(in.Notes, SopNoteKindRunSummary, lim.maxSummaries); len(summaries) > 0 {
		b.WriteString("\n## 最近运行结论\n\n")
		for _, n := range summaries {
			fmt.Fprintf(&b, "- %s：%s\n", briefNoteTitle(n), firstLine(n.Body))
		}
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}

// briefActiveItems 取「在进行的事项」：排除已闭环，按五态关注度排序，最多 max 条。
func briefActiveItems(items []BriefItem, max int) []BriefItem {
	out := make([]BriefItem, 0, len(items))
	for _, it := range items {
		if it.ExecState == SopExecStateClosed || it.Status == SopItemStatusDone ||
			it.Status == SopItemStatusClosed {
			continue
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return briefExecPriority[out[i].ExecState] < briefExecPriority[out[j].ExecState]
	})
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

// selectBriefNotes 选某一类型的条目：置顶的全带，其余按 seq 降序补足到 max 条。
func selectBriefNotes(notes []SopProjectNote, kind string, max int) []SopProjectNote {
	if max <= 0 {
		return nil
	}
	var typed []SopProjectNote
	for _, n := range notes {
		if n.Kind == kind {
			typed = append(typed, n)
		}
	}
	sort.SliceStable(typed, func(i, j int) bool { return typed[i].Seq > typed[j].Seq })

	var pinned, rest []SopProjectNote
	for _, n := range typed {
		if n.Pinned {
			pinned = append(pinned, n)
		} else {
			rest = append(rest, n)
		}
	}
	if len(rest) > max {
		rest = rest[:max]
	}
	return append(pinned, rest...)
}

func briefNoteTitle(n SopProjectNote) string {
	if n.Title != "" {
		return n.Title
	}
	return firstLine(n.Body)
}

// briefBody 按 rune 截断正文；limit<=0 不截断。
func briefBody(body string, limit int) string {
	body = strings.TrimSpace(body)
	if limit <= 0 || utf8.RuneCountInString(body) <= limit {
		return body
	}
	runes := []rune(body)
	return string(runes[:limit]) + "…"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

func briefSourceLabel(source string) string {
	switch source {
	case SopNoteSourceEngine:
		return "引擎"
	case SopNoteSourceAI:
		return "AI"
	default:
		return "人工"
	}
}

func briefTime(n SopProjectNote) string {
	t := n.CreatedAt
	if t.IsZero() {
		t = n.UpdatedAt
	}
	if t.IsZero() {
		return "时间未知"
	}
	return t.Format("2006-01-02 15:04")
}

// briefTruncate 按 rune 截断，不产生半个字符。
func briefTruncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
