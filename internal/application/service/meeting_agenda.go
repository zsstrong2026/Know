package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// 会议室结构性治理：议程拆解与按议题组织上下文。
//
// 目的：把"围绕主题展开"从一句软性提示词，升级为可判定的结构——
//  1. 主持人开场把主题拆成 2-5 个子议题（议程项），每个子议题有可判定的目标；
//  2. 讨论按子议题推进，每个子议题只带本议题记录 + 其他议题的一句话小结；
//  3. 收敛时给当前子议题写小结并推进下一项，避免"全量历史灌入"导致的链式漂移。

const (
	// meetingAgendaMinItems/MaxItems 议程项数量约束（过少等于没拆，过多讨论不可收拾）
	meetingAgendaMinItems = 2
	meetingAgendaMaxItems = 5
	// meetingAgendaContextMax 单个子议题回溯的消息条数（远小于旧的全房间 50 条）
	meetingAgendaContextMax = 8
)

// buildAgendaPromptSection 组装开场提示词中的「议题拆解」段（纯函数，单测覆盖）。
// 要求模型输出 <agenda> 包裹的 JSON 数组，字段与 MeetingAgendaItem 对齐。
func buildAgendaPromptSection() string {
	var b strings.Builder
	b.WriteString("\n## 议题拆解\n")
	fmt.Fprintf(&b, "把会议主题拆解为 %d-%d 个可独立讨论的子议题（按讨论顺序），每个子议题必须能被明确判定是否已覆盖。\n",
		meetingAgendaMinItems, meetingAgendaMaxItems)
	b.WriteString("紧跟在「## 议题拆解」标题下，用 <agenda> 与 </agenda> 包裹一个 JSON 数组，元素字段固定为：\n")
	b.WriteString(`{"title": "子议题标题（一句话）", "goal": "该子议题要产出的结论（可判定）", "owner_hint": "建议先发言的智能体名称，可为空"}`)
	b.WriteString("\n要求：子议题之间不重叠；必须覆盖会议主题的关键方面；不要照抄主题原文当成第一个子议题。\n")
	return b.String()
}

// agendaTagRe 提取 <agenda> 包裹体（含换行与 JSON 代码围栏容错）
var agendaTagRe = regexp.MustCompile(`(?is)<agenda[^>]*>(.*?)</agenda>`)

// agendaRawItem 解析用的宽松结构（模型可能只给部分字段）
type agendaRawItem struct {
	Title     string `json:"title"`
	Goal      string `json:"goal"`
	OwnerHint string `json:"owner_hint"`
}

// parseAgenda 解析模型输出中的议程 JSON；解析失败或数量越界时返回 ok=false，
// 由调用方退化为"无议程"（不阻断开会）。
func parseAgenda(content string) ([]agendaRawItem, bool) {
	m := agendaTagRe.FindStringSubmatch(content)
	if len(m) != 2 {
		return nil, false
	}
	text := strings.TrimSpace(m[1])
	// 容忍 ```json 围栏
	if i := strings.Index(text, "["); i >= 0 {
		if j := strings.LastIndex(text, "]"); j > i {
			text = text[i : j+1]
		}
	}
	var items []agendaRawItem
	if err := json.Unmarshal([]byte(text), &items); err != nil {
		return nil, false
	}
	out := make([]agendaRawItem, 0, len(items))
	for _, it := range items {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			continue
		}
		out = append(out, agendaRawItem{Title: title, Goal: strings.TrimSpace(it.Goal), OwnerHint: strings.TrimSpace(it.OwnerHint)})
	}
	if len(out) < 1 {
		return nil, false
	}
	if len(out) > meetingAgendaMaxItems {
		out = out[:meetingAgendaMaxItems]
	}
	return out, true
}

// stripAgendaTag 落库前剥离 <agenda> 标记（保留正文其余部分）
func stripAgendaTag(content string) string {
	return strings.TrimSpace(agendaTagRe.ReplaceAllString(content, ""))
}

// toAgendaItems 把解析结果转成实体，owner_hint 按展示名映射到智能体 ID。
func toAgendaItems(roomID string, raw []agendaRawItem, agents []*types.CustomAgent) []*types.MeetingAgendaItem {
	byName := make(map[string]string, len(agents))
	for _, a := range agents {
		if a.Name != "" {
			byName[a.Name] = a.ID
		}
	}
	items := make([]*types.MeetingAgendaItem, 0, len(raw))
	for i, it := range raw {
		item := &types.MeetingAgendaItem{
			MeetingRoomID: roomID,
			Seq:           i,
			Title:         it.Title,
			Goal:          it.Goal,
			Status:        types.MeetingAgendaPending,
		}
		if id := byName[it.OwnerHint]; id != "" {
			item.OwnerAgentIDs = types.StringList{id}
		}
		items = append(items, item)
	}
	return items
}

// persistAgenda 落库议程并激活第一项；replace=false 且已有议程时不覆盖。
// agendaRepo 为 nil（单测精简引擎）时静默跳过。
func (e *meetingEngine) persistAgenda(ctx context.Context, roomID string, items []*types.MeetingAgendaItem, replace bool) {
	if e.agendaRepo == nil || len(items) == 0 {
		return
	}
	if !replace {
		if existing, err := e.agendaRepo.ListByRoom(ctx, roomID); err == nil && len(existing) > 0 {
			return
		}
	}
	items[0].Status = types.MeetingAgendaActive
	if err := e.agendaRepo.ReplaceByRoom(ctx, roomID, items); err != nil {
		logger.Warnf(ctx, "meeting agenda persist failed, room=%s err=%v", roomID, err)
	}
}

// ensureActiveAgenda 取当前活跃议程项；没有活跃项时按 seq 激活下一个待讨论项。
// 返回 (全部议程, 活跃项)；未启用议程或仓储缺失时返回 (nil, nil)。
func (e *meetingEngine) ensureActiveAgenda(ctx context.Context, room *types.MeetingRoom) ([]*types.MeetingAgendaItem, *types.MeetingAgendaItem) {
	if !room.AgendaEnabled || e.agendaRepo == nil {
		return nil, nil
	}
	items, err := e.agendaRepo.ListByRoom(ctx, room.ID)
	if err != nil || len(items) == 0 {
		return nil, nil
	}
	if active, aerr := e.agendaRepo.Active(ctx, room.ID); aerr == nil && active != nil {
		return items, active
	}
	for _, it := range items {
		if it.Status == types.MeetingAgendaPending {
			it.Status = types.MeetingAgendaActive
			if err := e.agendaRepo.Update(ctx, it); err != nil {
				logger.Warnf(ctx, "meeting agenda activate failed, room=%s item=%s err=%v", room.ID, it.ID, err)
				return items, nil
			}
			return items, it
		}
	}
	return items, nil // 全部覆盖完成
}

// ActiveAgendaItem 对外只读查询：返回当前活跃议程项（未启用议程或无活跃项时 nil）。
// 供 service/handler 层做消息归属与前端展示，不触发推进。
func (e *meetingEngine) ActiveAgendaItem(ctx context.Context, room *types.MeetingRoom) (*types.MeetingAgendaItem, error) {
	if !room.AgendaEnabled || e.agendaRepo == nil {
		return nil, nil
	}
	return e.agendaRepo.Active(ctx, room.ID)
}

// ListAgenda 对外只读查询：返回房间全部议程项（按 seq 升序）
func (e *meetingEngine) ListAgenda(ctx context.Context, room *types.MeetingRoom) ([]*types.MeetingAgendaItem, error) {
	if e.agendaRepo == nil {
		return nil, nil
	}
	return e.agendaRepo.ListByRoom(ctx, room.ID)
}

// firstNonEmpty 返回第一个非空白字符串（用于小结优先取共识结论、缺失时回退判定依据）
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// buildAgendaDecomposePrompt 组装"仅议题拆解"的提示词（用户跳过开场、直接发言时懒生成议程）。
func buildAgendaDecomposePrompt(room *types.MeetingRoom, agents []*types.CustomAgent) string {
	var b strings.Builder
	b.WriteString("你是会议室主持人。现在只做议题拆解，不发表观点、不追问。\n")
	b.WriteString("会议主题：" + effectiveTopic(room) + "\n")
	if room.Description != "" {
		b.WriteString("会议描述：" + room.Description + "\n")
	}
	if len(agents) > 0 {
		b.WriteString("参会智能体：\n")
		for _, a := range agents {
			line := "- " + a.Name
			if a.Description != "" {
				line += "：" + truncate(a.Description, 60)
			}
			b.WriteString(line + "\n")
		}
	}
	b.WriteString(buildAgendaPromptSection())
	b.WriteString("\n只输出 <agenda> 包裹的 JSON 数组，不要输出任何其他内容。")
	return b.String()
}

// ensureAgendaGenerated 确保启用议程的房间已有议程：
// 用户跳过开场直接发言时，用一次主持人调用懒生成，避免结构性治理被绕过。
// 已有议程、未启用议程或模型不可用时静默返回（退回旧的全量上下文行为）。
func (e *meetingEngine) ensureAgendaGenerated(ctx context.Context, room *types.MeetingRoom, agents []*types.CustomAgent) {
	if !room.AgendaEnabled || e.agendaRepo == nil {
		return
	}
	if items, err := e.agendaRepo.ListByRoom(ctx, room.ID); err == nil && len(items) > 0 {
		return
	}
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		logger.Warnf(ctx, "meeting agenda host unavailable, room=%s err=%v", room.ID, err)
		return
	}
	resp, err := hostChat.Chat(ctx, []chat.Message{{Role: "user", Content: buildAgendaDecomposePrompt(room, agents)}}, &chat.ChatOptions{Temperature: 0})
	if err != nil || resp == nil {
		logger.Warnf(ctx, "meeting agenda decompose failed, room=%s err=%v", room.ID, err)
		return
	}
	raw, ok := parseAgenda(resp.Content)
	if !ok {
		logger.Warnf(ctx, "meeting agenda decompose unparsable, room=%s", room.ID)
		return
	}
	e.persistAgenda(ctx, room.ID, toAgendaItems(room.ID, raw, agents), true)
	e.publishAgendaUpdate(ctx, room.ID)
}

// agendaOpenContent 生成一句式议题开题消息：只交代议题与目标，不写长段流程说明。
func agendaOpenContent(item *types.MeetingAgendaItem, items []*types.MeetingAgendaItem) string {
	if item == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "【议题 %d/%d】%s", item.Seq+1, len(items), item.Title)
	if item.Goal != "" {
		b.WriteString("（目标：" + item.Goal + "）")
	}
	b.WriteString("\n请围绕本议题继续讨论。")
	return b.String()
}

// openNextAgenda 为当前活跃议题落一条"开题"主持人消息，作为下一轮讨论的触发点。
// 仅当该议题还没有任何发言时创建（避免重复开题）；返回 nil 表示无需继续（无议题或已在讨论中）。
func (e *meetingEngine) openNextAgenda(ctx context.Context, room *types.MeetingRoom) *types.MeetingMessage {
	if !room.AgendaEnabled || e.agendaRepo == nil {
		return nil
	}
	items, active := e.ensureActiveAgenda(ctx, room)
	if active == nil {
		return nil
	}
	if msgs, err := e.messageRepo.ListByAgendaItem(ctx, room.ID, active.ID, 1); err == nil && len(msgs) > 0 {
		return nil
	}
	msg, err := e.appendHostMessageForItem(ctx, room.ID, active.ID, agendaOpenContent(active, items))
	if err != nil {
		logger.Warnf(ctx, "meeting open agenda failed, room=%s item=%s err=%v", room.ID, active.ID, err)
		return nil
	}
	e.publishAgendaUpdate(ctx, room.ID)
	return msg
}

// advanceAgenda 结束当前议程项：写小结、标记 done。
// 引擎在议题收束时会自动激活并开启下一项；人工推进（PATCH/前端按钮）后仍由用户决定下一步。
func (e *meetingEngine) advanceAgenda(ctx context.Context, room *types.MeetingRoom, active *types.MeetingAgendaItem, summary string) bool {
	if active == nil || e.agendaRepo == nil {
		return false
	}
	active.Status = types.MeetingAgendaDone
	if s := strings.TrimSpace(summary); s != "" {
		active.Summary = truncate(s, 500)
	}
	if err := e.agendaRepo.Update(ctx, active); err != nil {
		logger.Warnf(ctx, "meeting agenda complete failed, room=%s item=%s err=%v", room.ID, active.ID, err)
		return false
	}
	return true
}

// ErrAgendaDisabled 会议室未启用议程（不支持追加/恢复议题）
var ErrAgendaDisabled = errors.New("meeting: agenda is disabled")

// ErrAgendaFull 议程项已达上限，无法继续追加
var ErrAgendaFull = errors.New("meeting: agenda items reached upper limit")

// ErrAgendaItemNotFound 指定议题不存在于当前会议
var ErrAgendaItemNotFound = errors.New("meeting: agenda item not found")

// AppendAgendaItem 追加新议题（追加即成为讨论焦点，用户显式要求继续开会时调用）。
// 规则：议程必须已启用；标题必填；数量不超过 meetingAgendaMaxItems；
// 当前无活跃议题时，新项直接置为活跃项。
func (e *meetingEngine) AppendAgendaItem(ctx context.Context, room *types.MeetingRoom, title string, goal string) ([]*types.MeetingAgendaItem, error) {
	if !room.AgendaEnabled || e.agendaRepo == nil {
		return nil, ErrAgendaDisabled
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errors.New("meeting: agenda title is required")
	}
	items, err := e.agendaRepo.ListByRoom(ctx, room.ID)
	if err != nil {
		return nil, err
	}
	if len(items) >= meetingAgendaMaxItems {
		return nil, ErrAgendaFull
	}
	item := &types.MeetingAgendaItem{
		MeetingRoomID: room.ID,
		Title:         title,
		Goal:          strings.TrimSpace(goal),
		Status:        types.MeetingAgendaPending,
	}
	// 没有活跃议题（上一议题已 done/skipped）时，新议题即刻成为讨论焦点；
	// 仍有活跃议题时保持 pending，由用户后续"恢复讨论"决定切换。
	if active, aerr := e.agendaRepo.Active(ctx, room.ID); aerr != nil || active == nil {
		item.Status = types.MeetingAgendaActive
	}
	// seq 由仓储按房间内 MAX(seq)+1 分配，规避 UNIQUE(meeting_room_id, seq) 冲突
	if err := e.agendaRepo.Create(ctx, item); err != nil {
		return nil, err
	}
	e.publishAgendaUpdate(ctx, room.ID)
	return e.agendaRepo.ListByRoom(ctx, room.ID)
}

// ReopenAgenda 恢复指定议题继续讨论：目标项置为活跃，其他活跃项回退为待讨论。
// 已完成议题恢复时保留 Summary，便于继续补充后再次收敛。
func (e *meetingEngine) ReopenAgenda(ctx context.Context, room *types.MeetingRoom, itemID string) ([]*types.MeetingAgendaItem, error) {
	if !room.AgendaEnabled || e.agendaRepo == nil {
		return nil, ErrAgendaDisabled
	}
	items, err := e.agendaRepo.ListByRoom(ctx, room.ID)
	if err != nil {
		return nil, err
	}
	var target *types.MeetingAgendaItem
	for _, it := range items {
		if it.ID == itemID {
			target = it
			continue
		}
		// 其他活跃项回退为待讨论，保证任一时刻只有一个活跃议题
		if it.Status == types.MeetingAgendaActive {
			it.Status = types.MeetingAgendaPending
			if err := e.agendaRepo.Update(ctx, it); err != nil {
				return nil, err
			}
		}
	}
	if target == nil {
		return nil, ErrAgendaItemNotFound
	}
	target.Status = types.MeetingAgendaActive
	if err := e.agendaRepo.Update(ctx, target); err != nil {
		return nil, err
	}
	e.publishAgendaUpdate(ctx, room.ID)
	return e.agendaRepo.ListByRoom(ctx, room.ID)
}

// agendaProgressText 生成"议程进度"摘要：已完成项一句话小结 + 未完成项标题。
// 供后续议题作为上下文，替代全量讨论记录。
func agendaProgressText(items []*types.MeetingAgendaItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	for _, it := range items {
		switch it.Status {
		case types.MeetingAgendaDone:
			if it.Summary != "" {
				fmt.Fprintf(&b, "- [已完成] %s：%s\n", it.Title, it.Summary)
			} else {
				fmt.Fprintf(&b, "- [已完成] %s\n", it.Title)
			}
		case types.MeetingAgendaSkipped:
			fmt.Fprintf(&b, "- [已跳过] %s\n", it.Title)
		case types.MeetingAgendaActive:
			fmt.Fprintf(&b, "- [讨论中] %s：%s\n", it.Title, it.Goal)
		default:
			fmt.Fprintf(&b, "- [待讨论] %s：%s\n", it.Title, it.Goal)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// buildAgendaQuery 组装"按议题聚焦"的发言 query（纯函数，单测覆盖）。
// 与 buildMeetingQuery 的差别：用当前议题目标取代"当前议题=上一条发言"，
// 只带本议题记录，其他议题以进度摘要代替。
func buildAgendaQuery(
	room *types.MeetingRoom, item *types.MeetingAgendaItem, items []*types.MeetingAgendaItem,
	triggerContent, triggerSpeaker string, itemMsgs []*types.MeetingMessage, participants []*types.MeetingParticipant,
	userName string,
) string {
	var b strings.Builder
	b.WriteString("【会议主题】" + effectiveTopic(room))
	if room.Description != "" {
		b.WriteString("\n【会议描述】" + room.Description)
	}
	if item != nil {
		fmt.Fprintf(&b, "\n【当前子议题】%s", item.Title)
		if item.Goal != "" {
			b.WriteString("\n【本议题要产出的结论】" + item.Goal)
		}
	}
	if progress := agendaProgressText(items); progress != "" {
		b.WriteString("\n【议程进度】\n" + progress)
	}
	if room.ContextSummary != "" {
		b.WriteString("\n【早期讨论摘要】" + room.ContextSummary)
	}
	if names := collectAgentDisplayNames(participants); len(names) > 0 {
		b.WriteString("\n【参会智能体】" + strings.Join(names, "、"))
	}
	// 用户姓名：让智能体能像真人会议一样直接称呼用户，而不是只会 @用户 这个占位词
	if userName != "" {
		b.WriteString("\n【用户】" + userName + "（可 @用户名 向用户提问）")
	}
	agentNames := agentNamesByID(participants)
	if len(itemMsgs) > 0 {
		b.WriteString("\n【本议题讨论记录】")
		for _, m := range itemMsgs {
			// 触发消息最后单独强调，避免同一段内容出现两次
			if m.Content == triggerContent {
				continue
			}
			// 会议是对话不是报告：上下文只保留近期短片段，避免模型跟着写长文
			fmt.Fprintf(&b, "\n%s: %s", senderLabelWithNames(m, agentNames), truncate(m.Content, 300))
		}
	}
	// 标注待回应发言的发言人：否则模型只能看到一段无主语的内容，容易 @ 错对象（甚至 @ 自己）
	if triggerSpeaker != "" {
		b.WriteString("\n【待回应的发言】" + triggerSpeaker + "：" + triggerContent)
	} else {
		b.WriteString("\n【待回应的发言】" + triggerContent)
	}
	b.WriteString("\n请像在会议桌前发言一样回应【待回应的发言】，只围绕【当前子议题】：")
	b.WriteString("\n- 常态 1-3 句话、不超过 120 字；被追问或关键论证可放宽到 200 字，不要每条都一样长；")
	b.WriteString("\n- 先表态再给理由；不要用标题、列表或代码块，也不要复述背景；")
	b.WriteString("\n- 不要重复本议题讨论记录里已有的内容，优先补充新事实、新约束或明确的分歧理由；")
	b.WriteString("\n- 可以明确反对；不同意就说清反对点和能改变你立场的证据，不要为了和气而附和；")
	b.WriteString("\n- 强烈不同意上一位时，可以用「先打断一下」开头直接反驳并 @ 对方；")
	b.WriteString("\n- 要请某位参会人接话，在句末 @对方姓名；只能 @ 其他参会智能体，不要 @ 自己；")
	if userName != "" {
		b.WriteString("\n- 需要用户拍板或补充，在句末 @" + userName + "（或 @用户）提一个问题；")
	} else {
		b.WriteString("\n- 需要用户拍板或补充，在句末 @用户 提一个问题；")
	}
	b.WriteString("\n- 若相较上述讨论记录没有新增观点，请仅回复 PASS，不要输出任何其他内容。")
	return b.String()
}
