// taskbook.go 把"这一步要做什么"渲染成一份大白话任务书（Markdown），并在需要时
// 渲染执行器的系统提示（角色规则 + 技能 + 节点补充规则）。
//
// 产品口径（与检查器第 2 步一致）：
//   - 用户只写"这一步做什么"，正文里没有变量插入入口、没有 {{…}} 芯片；
//   - 上下文由本函数在运行时自动附带：事项背景与上游步骤产物每次必带，
//     返工意见只在返工轮出现，人工补充意见只在审批 / 澄清之后出现；
//   - {{…}} 占位符通道只为兼容"旧发布模板"保留在服务端：旧模板的 TaskPrompt
//     里若残留 {{input}} 之类的写法，先做字符串替换再组装，替换不掉的令牌直接丢弃，
//     保证输出里永远不会出现 {{ （检查器没有对应入口，新模板不会产生这些写法）。
//
// 全部是纯函数：不读库、不碰文件、不迭代 map（所有集合先排序再输出），
// 同一输入永远得到同一份任务书。
package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// 摘要长度上限：任务书是给执行器读的，过长会挤占上下文；超长一律截断加省略号。
const (
	// taskBookRuleClipRunes 「遵循规则」里角色规则摘要的字符上限。
	taskBookRuleClipRunes = 1200
	// taskBookOutputClipRunes 上游步骤产物摘要的字符上限。
	taskBookOutputClipRunes = 2000
	// taskBookFullOutputClipRunes task_refs 指定步骤完整输出的字符上限。
	taskBookFullOutputClipRunes = 8000
)

// TaskBookInput 组装一份任务书所需的全部输入。字段由引擎在节点执行前填好，
// 任何字段为空都有白话兜底，不会出现空白小节。
type TaskBookInput struct {
	Node                   types.SopNode
	Item                   *types.SopItem
	RunInput               map[string]any
	UpstreamOutputs        map[string]string // nodeID→output 摘要
	UpstreamNames          map[string]string
	RefOutputs             map[string]string // task_refs 勾选节点完整输出
	Feedback               string
	HumanContext           []types.SopHumanContextEntry
	AnsweredClarifications []types.SopItemClarification
	AttachmentsText        []string
	Role                   *types.CustomAgent
	// ResolvedSkills 是节点实际可用的技能子集（ResolveSkills 的结果）。
	// 任务书正文不带技能内容，由 RenderAgentSystem 渲染系统提示，这里供引擎
	// 转填 executor.TaskBook.Skills 使用。
	ResolvedSkills []string
	InputsDir      string
	OutDir         string
}

// RenderTaskBook 组装任务书正文（Markdown 大白话）。
func RenderTaskBook(in TaskBookInput) string {
	prompt := substituteLegacyPlaceholders(in, in.Node.TaskPrompt)
	extraRules := substituteLegacyPlaceholders(in, in.Node.ExtraRules)

	var b strings.Builder
	writeStepTitle(&b, in.Node)
	writeRoleGoal(&b, in, prompt)
	writeRules(&b, in, extraRules)
	writeWhatToDo(&b, prompt)
	writeItemContext(&b, in)
	writeUpstream(&b, in)
	writeFeedback(&b, in)
	writeHumanContext(&b, in)
	writeArtifacts(&b, in)
	writeSubmit(&b, in)

	return b.String()
}

// ---- 各小节 ----

// writeStepTitle 标题只用步骤名，不带内部 id。
func writeStepTitle(b *strings.Builder, node types.SopNode) {
	name := strings.TrimSpace(node.Name)
	if name == "" {
		name = "未命名步骤"
	}
	fmt.Fprintf(b, "# 步骤：%s\n\n", name)
}

// writeRoleGoal 角色与目标：一句角色定位 + 这一步的目标（正文首行，避免与
// 「这一步做什么」整段重复两次）。
func writeRoleGoal(b *strings.Builder, in TaskBookInput, prompt string) {
	b.WriteString("## 角色与目标\n\n")
	b.WriteString(roleLine(in.Role))
	b.WriteString("\n")
	if goal := goalLine(prompt); goal != "" {
		b.WriteString("\n目标：" + goal + "\n")
	}
	b.WriteString("\n")
}

// goalLine 取任务正文的第一句有效内容作为目标摘要（跳过 Markdown 标题等结构行）。
func goalLine(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "#") || l == "```" {
			continue
		}
		return clipRunes(strings.TrimSpace(strings.TrimLeft(l, "-*>"+" ")), 120)
	}
	return ""
}

// roleLine 把角色转成一句口语化定位；没有角色时给一句中性说明。
func roleLine(role *types.CustomAgent) string {
	if role == nil {
		return "本步骤没有单独指定角色，请直接按「这一步做什么」把事情做完。"
	}
	name := strings.TrimSpace(role.Name)
	desc := strings.TrimSpace(role.Description)
	switch {
	case name != "" && desc != "":
		return fmt.Sprintf("你是「%s」，%s。", name, strings.TrimSuffix(desc, "。"))
	case name != "":
		return fmt.Sprintf("你是「%s」，负责把这一步做完。", name)
	case desc != "":
		return fmt.Sprintf("你是%s，负责把这一步做完。", strings.TrimSuffix(desc, "。"))
	default:
		return "你负责把这一步做完。"
	}
}

// writeRules 遵循规则：角色规则（SystemPrompt，也就是智能体管理里的角色提示词）
// 摘要 + 本步骤补充规则。
func writeRules(b *strings.Builder, in TaskBookInput, extraRules string) {
	b.WriteString("## 遵循规则\n\n")
	wrote := false
	if in.Role != nil {
		if rules := strings.TrimSpace(in.Role.Config.SystemPrompt); rules != "" {
			b.WriteString("角色规则（长期有效，优先遵守）：\n\n")
			b.WriteString(clipRunes(rules, taskBookRuleClipRunes))
			b.WriteString("\n\n")
			wrote = true
		}
	}
	if extra := strings.TrimSpace(extraRules); extra != "" {
		b.WriteString("这一步的补充规则：\n\n")
		b.WriteString(extra)
		b.WriteString("\n\n")
		wrote = true
	}
	// 技能不进任务书正文：技能清单与按需加载说明由 RenderAgentSystem 渲染
	// （正文由 skills.Manager 渐进披露，任务书不复制技能内容）。
	if !wrote {
		b.WriteString("这一步没有额外规则，按上面「角色与目标」把事情做对即可。\n\n")
	}
}

// writeWhatToDo 用户写的大白话正文。附带一句说明：上下文是自动带的，正文里不需要写变量。
func writeWhatToDo(b *strings.Builder, prompt string) {
	b.WriteString("## 这一步做什么\n\n")
	if p := strings.TrimSpace(prompt); p != "" {
		b.WriteString(p)
		b.WriteString("\n\n")
	} else {
		b.WriteString("这一步还没有填写要做什么。请结合下面的「事项背景」与「上游步骤产物」自行判断本步骤该完成什么，并把结果说清楚。\n\n")
	}
	b.WriteString("下面的事项背景、上游步骤产物等上下文是运行时自动附上的，你不需要在正文里写任何变量。\n\n")
}

// writeItemContext 事项背景：每次必带。没有关联事项时明确说"独立运行"。
func writeItemContext(b *strings.Builder, in TaskBookInput) {
	b.WriteString("## 事项背景\n\n")
	if in.Item == nil {
		b.WriteString("- 独立运行（未关联事项）\n")
	} else {
		title := strings.TrimSpace(in.Item.Title)
		if title == "" {
			title = "未命名事项"
		}
		fmt.Fprintf(b, "- 事项：%s\n", title)
		if no := strings.TrimSpace(in.Item.No); no != "" {
			fmt.Fprintf(b, "- 编号：%s\n", no)
		}
		if desc := strings.TrimSpace(in.Item.Description); desc != "" {
			b.WriteString("- 描述：\n")
			b.WriteString(indentBlock(desc, "  "))
			b.WriteString("\n")
		}
	}
	for _, a := range in.AttachmentsText {
		if t := strings.TrimSpace(a); t != "" {
			fmt.Fprintf(b, "- 附件内容：%s\n", oneLine(t))
		}
	}
	if kv := renderRunInput(in.RunInput); kv != "" {
		b.WriteString("- 本次运行输入：\n")
		b.WriteString(indentBlock(kv, "  "))
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

// writeUpstream 上游步骤产物：每次必带。按步骤名排序，附上 task_refs 勾选节点的完整输出。
func writeUpstream(b *strings.Builder, in TaskBookInput) {
	b.WriteString("## 上游步骤产物\n\n")
	ids := make([]string, 0, len(in.UpstreamOutputs))
	for id := range in.UpstreamOutputs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ni, nj := in.UpstreamNames[ids[i]], in.UpstreamNames[ids[j]]
		if ni != nj {
			return ni < nj
		}
		return ids[i] < ids[j]
	})
	if len(ids) == 0 {
		b.WriteString("- 这是流程的起点，没有上游步骤产物。\n")
	}
	for _, id := range ids {
		fmt.Fprintf(b, "### %s\n\n", upstreamName(in, id))
		out := strings.TrimSpace(in.UpstreamOutputs[id])
		if out == "" {
			b.WriteString("（这一步没有留下文字结论，产物文件见下面的输入目录。）\n")
		} else {
			b.WriteString(clipRunes(out, taskBookOutputClipRunes))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	refIDs := make([]string, 0, len(in.RefOutputs))
	for id := range in.RefOutputs {
		refIDs = append(refIDs, id)
	}
	sort.Slice(refIDs, func(i, j int) bool {
		ni, nj := in.UpstreamNames[refIDs[i]], in.UpstreamNames[refIDs[j]]
		if ni != nj {
			return ni < nj
		}
		return refIDs[i] < refIDs[j]
	})
	for _, id := range refIDs {
		out := strings.TrimSpace(in.RefOutputs[id])
		if out == "" {
			continue
		}
		fmt.Fprintf(b, "### 完整产出：%s\n\n", upstreamName(in, id))
		b.WriteString(clipRunes(out, taskBookFullOutputClipRunes))
		b.WriteString("\n\n")
	}

	if dir := strings.TrimSpace(in.InputsDir); dir != "" {
		fmt.Fprintf(b, "上游的产物文件已经物化到 %s，可以直接打开读取。\n\n", dir)
	} else {
		b.WriteString("上游的产物文件已经物化到工作区的 inputs 目录，可以直接打开读取。\n\n")
	}
}

// upstreamName 取步骤的展示名，缺失时退回节点 id，保证不出现空标题。
func upstreamName(in TaskBookInput, id string) string {
	if n := strings.TrimSpace(in.UpstreamNames[id]); n != "" {
		return n
	}
	if n := strings.TrimSpace(id); n != "" {
		return n
	}
	return "上游步骤"
}

// writeFeedback 返工意见：只有返工轮（Feedback 非空）才出这一节。
func writeFeedback(b *strings.Builder, in TaskBookInput) {
	fb := strings.TrimSpace(in.Feedback)
	if fb == "" {
		return
	}
	b.WriteString("## 返工意见\n\n")
	b.WriteString("上一轮没有通过，请按下面的意见改好后重新提交：\n\n")
	b.WriteString(fb)
	b.WriteString("\n\n")
}

// writeHumanContext 人工补充意见：人工留言、已回答的澄清。两者都空则整节不出。
func writeHumanContext(b *strings.Builder, in TaskBookInput) {
	var lines []string
	for _, h := range in.HumanContext {
		if t := strings.TrimSpace(h.Text); t != "" {
			lines = append(lines, fmt.Sprintf("- %s：%s", humanContextSourceLabel(h.Source), oneLine(t)))
		}
	}
	for _, c := range in.AnsweredClarifications {
		if c.Status != types.SopClarificationStatusAnswered {
			continue
		}
		q := oneLine(strings.TrimSpace(c.Question))
		a := oneLine(strings.TrimSpace(c.Answer))
		if q == "" && a == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- 澄清「%s」→ %s", q, a))
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString("## 人工补充意见\n\n")
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n\n")
}

// humanContextSourceLabel 把来源转成中文标签（未知来源一律按"人工补充"）。
func humanContextSourceLabel(source string) string {
	switch source {
	case "system_reminder":
		return "系统提醒"
	case "approval":
		return "审批意见"
	default:
		return "人工补充"
	}
}

// writeArtifacts 产出要求：逐条交付物（名称/路径/必须交/怎么检查）+ 写到哪。
func writeArtifacts(b *strings.Builder, in TaskBookInput) {
	b.WriteString("## 产出要求\n\n")
	if len(in.Node.ArtifactOutputs) == 0 {
		b.WriteString("这一步没有约定交付物，把结论和改动的文件说清楚即可。\n\n")
	} else {
		for _, a := range in.Node.ArtifactOutputs {
			name := strings.TrimSpace(a.Name)
			if name == "" {
				name = "交付物"
			}
			path := strings.TrimSpace(a.Path)
			if path == "" {
				path = "（未指定路径）"
			}
			need := "可选"
			if a.Required {
				need = "必须交"
			}
			fmt.Fprintf(b, "- %s → %s（%s；%s）\n", name, path, need, artifactCheckLine(a.Check))
		}
		b.WriteString("\n")
	}
	if dir := strings.TrimSpace(in.OutDir); dir != "" {
		fmt.Fprintf(b, "完成后把产物写到 %s 对应路径（相对路径按本次工作目录计算）。\n\n", dir)
	} else {
		b.WriteString("完成后把产物写到本次工作目录的对应路径。\n\n")
	}
}

// artifactCheckLine 把产物校验方式翻译成一句白话。
func artifactCheckLine(check types.SopArtifactCheck) string {
	switch check.Type {
	case types.SopArtifactCheckContains:
		if kw := strings.TrimSpace(check.Keyword); kw != "" {
			return fmt.Sprintf("文件里必须包含关键词「%s」", kw)
		}
		return "文件里必须包含约定关键词"
	case types.SopArtifactCheckCommand:
		if cmd := strings.TrimSpace(check.Command); cmd != "" {
			return "要能通过这条检查命令：" + cmd
		}
		return "要能通过约定的检查命令"
	default:
		return "文件交了就行，但不能是空文件"
	}
}

// writeSubmit 结果提交：按执行器通道给出白话说明，四种结果逐条解释。
func writeSubmit(b *strings.Builder, in TaskBookInput) {
	b.WriteString("## 结果提交\n\n")
	switch in.Node.Executor.Kind {
	case types.SopExecutorCLI:
		fmt.Fprintf(b, "做完后把结果写到 .sop/gate-results/%s/attempt-N.json（N 是本次尝试序号），内容形如：\n\n",
			nodeSegment(in.Node.ID))
		b.WriteString("```json\n{\"result\": \"pass\", \"reason\": \"一句话说明依据\", \"output\": \"交给下游的结论正文\"}\n```\n\n")
		b.WriteString("result 只能是下面四种之一：\n\n")
		writeSubmitValues(b)
		b.WriteString("没有写这个文件的话，系统会再问你一次，所以要如实汇报。\n\n")
	case types.SopExecutorPlatformAgent:
		b.WriteString("做完后调用 submit_node_result 汇报本步骤结果（result + reason，必要时附 output 结论正文）。\n\n")
		b.WriteString("result 只能是下面四种之一：\n\n")
		writeSubmitValues(b)
		b.WriteString("忘了汇报的话，系统会在同一个会话里再问你一次，所以要如实汇报。\n\n")
	default:
		b.WriteString("做完后如实汇报本步骤结果：按执行环境支持的方式提交（调用 submit_node_result，或把结论写到 .sop/gate-results/ 下的结果文件）。\n\n")
		b.WriteString("结果只能是下面四种之一：\n\n")
		writeSubmitValues(b)
		b.WriteString("\n")
	}
}

// writeSubmitValues 四种终态的白话解释（顺序固定）。
func writeSubmitValues(b *strings.Builder) {
	b.WriteString("- pass（完成）：这一步确实做完了，产物齐全。\n")
	b.WriteString("- rework（需返工）：做了一部分但没达到要求，写清楚差在哪。\n")
	b.WriteString("- fail（失败）：环境或前提不成立，这一步做不下去，写清楚原因。\n")
	b.WriteString("- human（需人裁定）：需要人来拍板，写清楚要人决定什么。\n\n")
}

// nodeSegment 把节点 id 变成可安全放进路径的单段文本（与工作区落盘口径一致）。
func nodeSegment(id string) string {
	if s := strings.TrimSpace(id); s != "" {
		return sanitizeSegment(s)
	}
	return "node"
}

// ---- 旧模板占位符兼容 ----

// legacyTokenRe 匹配 {{ token }} 形式的旧占位符；token 允许字母数字与 . _ -。
var legacyTokenRe = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.\-]+)\s*\}\}`)

// legacyLooseRe 兜底清除替换后仍残留的任意 {{…}}（旧模板的手写笔误也会被清掉）。
var legacyLooseRe = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// substituteLegacyPlaceholders 只能服务端做：把旧模板里的占位符替换成实际内容。
// 支持的令牌见 legacyTokenValue；未知令牌替换为空串。
func substituteLegacyPlaceholders(in TaskBookInput, s string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	out := legacyTokenRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := legacyTokenRe.FindStringSubmatch(m)
		if len(sub) < 2 {
			return ""
		}
		return legacyTokenValue(in, sub[1])
	})
	if strings.Contains(out, "{{") {
		out = legacyLooseRe.ReplaceAllString(out, "")
	}
	return out
}

// legacyTokenValue 是旧占位符到内容的映射：
//
//	input          本次运行输入（无输入时退回事项描述）
//	upstream       全部上游步骤产物摘要（按步骤名排序）
//	node.X.output  节点 X 的完整输出（task_refs 优先，其次摘要）
//	feedback       返工意见
//	humanContext   人工补充意见与已回答澄清
//	inputs_dir     上游产物物化目录
//	artifacts_dir  本步骤产出目录
func legacyTokenValue(in TaskBookInput, token string) string {
	switch token {
	case "input":
		if kv := renderRunInput(in.RunInput); kv != "" {
			return kv
		}
		if in.Item != nil {
			return strings.TrimSpace(in.Item.Description)
		}
		return ""
	case "upstream":
		return upstreamDigest(in)
	case "feedback":
		return strings.TrimSpace(in.Feedback)
	case "humanContext", "human_context":
		return humanContextDigest(in)
	case "artifacts_dir", "output_dir", "out_dir":
		return in.OutDir
	case "inputs_dir":
		return in.InputsDir
	}
	if id, ok := strings.CutSuffix(token, ".output"); ok {
		id = strings.TrimPrefix(id, "node.")
		if id == "" {
			return ""
		}
		if v, ok := in.RefOutputs[id]; ok {
			return v
		}
		return in.UpstreamOutputs[id]
	}
	return ""
}

// upstreamDigest 把上游摘要拼成一段纯文本（旧占位符 {{upstream}} 用）。
func upstreamDigest(in TaskBookInput) string {
	ids := make([]string, 0, len(in.UpstreamOutputs))
	for id := range in.UpstreamOutputs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ni, nj := in.UpstreamNames[ids[i]], in.UpstreamNames[ids[j]]
		if ni != nj {
			return ni < nj
		}
		return ids[i] < ids[j]
	})
	var parts []string
	for _, id := range ids {
		out := strings.TrimSpace(in.UpstreamOutputs[id])
		if out == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("【%s】\n%s", upstreamName(in, id), out))
	}
	return strings.Join(parts, "\n\n")
}

// humanContextDigest 把人工补充与已回答澄清拼成一段纯文本（旧占位符用）。
func humanContextDigest(in TaskBookInput) string {
	var parts []string
	for _, h := range in.HumanContext {
		if t := strings.TrimSpace(h.Text); t != "" {
			parts = append(parts, fmt.Sprintf("%s：%s", humanContextSourceLabel(h.Source), t))
		}
	}
	for _, c := range in.AnsweredClarifications {
		if c.Status != types.SopClarificationStatusAnswered {
			continue
		}
		q, a := strings.TrimSpace(c.Question), strings.TrimSpace(c.Answer)
		if q == "" && a == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("问：%s 答：%s", q, a))
	}
	return strings.Join(parts, "\n")
}

// renderRunInput 把本次运行输入渲染成"键：值"清单（键排序，值按 JSON 保守格式化）。
func renderRunInput(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s：%s", k, formatAny(input[k])))
	}
	return strings.Join(lines, "\n")
}

// formatAny 字符串原样输出，其余类型走 JSON；解析失败退回 %v。
func formatAny(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		if raw, err := json.Marshal(v); err == nil {
			return string(raw)
		}
		return fmt.Sprintf("%v", v)
	}
}

// indentBlock 给多行文本统一加前缀，保持 Markdown 列表内的换行可读。
func indentBlock(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// oneLine 把多行文本压成一行（用于列表项），多余空白收敛成单空格。
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ---- 系统提示（角色规则 + 技能 + 节点补充规则） ----

// RenderAgentSystem 渲染执行器的系统提示：
//
//	角色定位 → 角色规则（SystemPrompt）→ 本步骤补充规则 → 可用技能 → 角色与目标段
//
// 技能只列名称与"按需加载"的约定：技能正文由 skills.Manager 渐进披露，
// 这里不复制技能内容，避免提示词随技能库变大而膨胀。
func RenderAgentSystem(role *types.CustomAgent, skills []string, extraRules, roleSection string) string {
	var b strings.Builder

	head := roleLine(role)
	b.WriteString(head)
	b.WriteString("\n")

	if role != nil {
		if rules := strings.TrimSpace(role.Config.SystemPrompt); rules != "" {
			b.WriteString("\n## 你做事要遵守的规则\n\n")
			b.WriteString(rules)
			b.WriteString("\n")
		}
	}

	if extra := strings.TrimSpace(extraRules); extra != "" {
		b.WriteString("\n## 这一步的补充规则\n\n")
		b.WriteString(extra)
		b.WriteString("\n")
	}

	if names := dedupeStrings(skills); len(names) > 0 {
		b.WriteString("\n## 可用技能\n\n")
		b.WriteString("下面的技能已经为本步骤启用，需要时按名称加载详细说明再动手：\n\n")
		for _, name := range names {
			fmt.Fprintf(&b, "- %s\n", name)
		}
	}

	if section := strings.TrimSpace(roleSection); section != "" {
		b.WriteString("\n## 角色与目标\n\n")
		b.WriteString(section)
		b.WriteString("\n")
	}

	return b.String()
}

// ResolveSkills 计算节点实际可用的技能子集：
//   - nodeSkills 为 nil（JSON null / 用户全选）→ 角色的 SelectedSkills 全部；
//   - nodeSkills 非 nil（含空切片）→ 与角色技能的交集，显式 [] 表示一个都不要；
//   - 角色为空 → 原样使用 nodeSkills（没有角色就没有可收敛的白名单）。
//
// 输出顺序跟随角色（或节点）声明顺序并去重，保证渲染稳定。
func ResolveSkills(role *types.CustomAgent, nodeSkills []string) []string {
	if role == nil {
		if nodeSkills == nil {
			return nil
		}
		return dedupeStrings(nodeSkills)
	}
	owned := role.Config.SelectedSkills
	if nodeSkills == nil {
		return dedupeStrings(owned)
	}
	var out []string
	seen := make(map[string]bool, len(nodeSkills))
	for _, s := range owned {
		if !containsString(nodeSkills, s) || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// containsString 判断切片是否含有某个值。
func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// dedupeStrings 去重并保持首次出现的顺序；nil 输入返回 nil。
func dedupeStrings(list []string) []string {
	if list == nil {
		return nil
	}
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
