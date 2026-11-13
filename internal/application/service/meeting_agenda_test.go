package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ---- 主题统一 ----

func TestEffectiveTopic(t *testing.T) {
	// TopicBrief 优先于 Title（收敛后主题必须贯穿所有治理环节）
	assert.Equal(t, "评估 v2 发布风险", effectiveTopic(&types.MeetingRoom{Title: "发布评审", TopicBrief: "评估 v2 发布风险"}))
	// TopicBrief 为空或仅空白时回退 Title
	assert.Equal(t, "发布评审", effectiveTopic(&types.MeetingRoom{Title: "发布评审"}))
	assert.Equal(t, "发布评审", effectiveTopic(&types.MeetingRoom{Title: "发布评审", TopicBrief: "   "}))
	assert.Equal(t, "", effectiveTopic(nil))
}

// ---- 议程解析 ----

func TestParseAgenda(t *testing.T) {
	raw := "## 议题拆解\n<agenda>\n[{\"title\":\"成本评估\",\"goal\":\"给出成本区间\",\"owner_hint\":\"财务\"}," +
		"{\"title\":\"兼容性\",\"goal\":\"列出不兼容项\"}]\n</agenda>\n## 建议流程\n..."
	items, ok := parseAgenda(raw)
	require.True(t, ok)
	require.Len(t, items, 2)
	assert.Equal(t, "成本评估", items[0].Title)
	assert.Equal(t, "给出成本区间", items[0].Goal)
	assert.Equal(t, "财务", items[0].OwnerHint)

	// 无 agenda 标签 → ok=false，调用方退化为无议程（不阻断开会）
	_, ok = parseAgenda("## 主题分析\n没有拆解")
	assert.False(t, ok)

	// JSON 非法 → ok=false
	_, ok = parseAgenda("<agenda>not-json</agenda>")
	assert.False(t, ok)

	// 空标题项被丢弃；超上限截断
	items, ok = parseAgenda(`<agenda>[{"title":""},{"title":"A"},{"title":"B"},{"title":"C"},{"title":"D"},{"title":"E"},{"title":"F"}]</agenda>`)
	require.True(t, ok)
	assert.Len(t, items, meetingAgendaMaxItems)
}

func TestStripAgendaTag(t *testing.T) {
	got := stripAgendaTag("## 主题分析\n<agenda>[]</agenda>\n## 建议流程\n流程正文")
	assert.NotContains(t, got, "<agenda>")
	assert.Contains(t, got, "## 建议流程")
}

func TestToAgendaItemsMapsOwnerHint(t *testing.T) {
	agents := []*types.CustomAgent{{ID: "a1", Name: "财务"}, {ID: "a2", Name: "架构师"}}
	items := toAgendaItems("r1", []agendaRawItem{
		{Title: "成本", Goal: "区间", OwnerHint: "财务"},
		{Title: "架compatibility", Goal: "", OwnerHint: "不存在的名字"},
	}, agents)
	require.Len(t, items, 2)
	assert.Equal(t, 0, items[0].Seq)
	assert.Equal(t, types.StringList{"a1"}, items[0].OwnerAgentIDs)
	assert.Equal(t, types.MeetingAgendaPending, items[0].Status)
	assert.Nil(t, items[1].OwnerAgentIDs, "未知 owner_hint 不产生归属")
}

// ---- 议程进度摘要 ----

func TestAgendaProgressText(t *testing.T) {
	items := []*types.MeetingAgendaItem{
		{Seq: 0, Title: "成本", Status: types.MeetingAgendaDone, Summary: "区间 10-20 万"},
		{Seq: 1, Title: "兼容性", Status: types.MeetingAgendaActive, Goal: "列出不兼容项"},
		{Seq: 2, Title: "回滚", Status: types.MeetingAgendaSkipped},
	}
	got := agendaProgressText(items)
	assert.Contains(t, got, "[已完成] 成本：区间 10-20 万")
	assert.Contains(t, got, "[讨论中] 兼容性：列出不兼容项")
	assert.Contains(t, got, "[已跳过] 回滚")
	assert.Equal(t, "", agendaProgressText(nil))
}

// ---- 按议题组织上下文 ----

func TestBuildAgendaQueryFocusesOnItem(t *testing.T) {
	room := &types.MeetingRoom{ID: "r1", Title: "发布评审", TopicBrief: "评估 v2 发布风险"}
	items := []*types.MeetingAgendaItem{
		{ID: "i0", Seq: 0, Title: "成本", Status: types.MeetingAgendaDone, Summary: "区间 10-20 万"},
		{ID: "i1", Seq: 1, Title: "兼容性", Goal: "列出不兼容项", Status: types.MeetingAgendaActive},
	}
	itemMsgs := []*types.MeetingMessage{
		{ID: "m1", SenderKind: types.MeetingSenderAgent, SenderID: "a1", Content: "老版本客户端会失败"},
	}
	participants := []*types.MeetingParticipant{{Kind: types.ParticipantKindAgent, AgentID: "a1", DisplayName: "架构师"}}

	q := buildAgendaQuery(room, items[1], items, "兼容性怎么看？", itemMsgs, participants, "张三")

	assert.Contains(t, q, "评估 v2 发布风险", "必须使用收敛后的主题")
	assert.Contains(t, q, "【当前子议题】兼容性")
	assert.Contains(t, q, "列出不兼容项", "必须带出本议题的目标")
	assert.Contains(t, q, "老版本客户端会失败", "带出本议题记录")
	assert.Contains(t, q, "[已完成] 成本", "其他议题以小结形式出现")
	assert.NotContains(t, q, "【讨论记录】", "不应再注入全量讨论记录")
	assert.Contains(t, q, "【待回应的发言】兼容性怎么看？")
}

// ---- 发言准入 ----

func TestParseAdmissionVerdict(t *testing.T) {
	v, ok := parseAdmissionVerdict("```json\n{\"on_topic\": true, \"adds_info\": false, \"reason\": \"复述\"}\n```")
	require.True(t, ok)
	assert.True(t, v.OnTopic)
	assert.False(t, v.AddsInfo)
	assert.Equal(t, "复述", v.Reason)
	assert.False(t, v.admits(), "只有新增信息才放行")

	v, ok = parseAdmissionVerdict("说明文字 {\"on_topic\": true, \"adds_info\": true, \"reason\": \"ok\"} 结束")
	require.True(t, ok)
	assert.True(t, v.admits())

	_, ok = parseAdmissionVerdict("无法解析")
	assert.False(t, ok)
}

func TestBuildAdmissionPromptIncludesItemGoal(t *testing.T) {
	room := &types.MeetingRoom{Title: "发布评审", TopicBrief: "评估 v2 发布风险"}
	item := &types.MeetingAgendaItem{Title: "兼容性", Goal: "列出不兼容项"}
	recent := []*types.MeetingMessage{{SenderKind: types.MeetingSenderAgent, Content: "老版本会失败"}}
	p := buildAdmissionPrompt(effectiveTopic(room), item, recent, "我觉得还行")
	assert.Contains(t, p, "评估 v2 发布风险")
	assert.Contains(t, p, "当前子议题：兼容性")
	assert.Contains(t, p, "列出不兼容项")
	assert.Contains(t, p, "老版本会失败")
	assert.Contains(t, p, "on_topic")
}

// TestAdmitAnswerDisabled 关闭/未启用议程时不做任何判定，原样返回
func TestAdmitAnswerDisabled(t *testing.T) {
	e := &meetingEngine{}
	room := &types.MeetingRoom{AdmissionMode: types.MeetingAdmissionEnforce, AgendaEnabled: false}
	agent := &types.CustomAgent{ID: "a1"}
	got, state, note := e.admitAnswer(context.Background(), room, "i1", agent, "一段足够长的发言内容用于测试")
	assert.Equal(t, "一段足够长的发言内容用于测试", got)
	assert.Equal(t, types.MeetingAdmissionNone, state)
	assert.Empty(t, note)

	room.AgendaEnabled = true
	room.AdmissionMode = types.MeetingAdmissionOff
	_, state, _ = e.admitAnswer(context.Background(), room, "i1", agent, "一段足够长的发言内容用于测试")
	assert.Equal(t, types.MeetingAdmissionNone, state)
}

// TestMergeUnique 议题级知识库与会议级知识库并集去重
func TestMergeUnique(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, mergeUnique([]string{"a", "b"}, []string{"b", "c"}))
	assert.Equal(t, []string{"c"}, mergeUnique(nil, []string{"", "c"}))
}

// ---- 裁判多维判定 ----

func TestParseRefereeVerdictExtended(t *testing.T) {
	v, ok := parseRefereeVerdict(`{"converged": false, "on_topic": false, "drift": true, "goal_covered": false, "reason": "跑题"}`)
	require.True(t, ok)
	assert.False(t, v.Converged)
	assert.False(t, v.OnTopic)
	assert.True(t, v.Drift)
	assert.Equal(t, "跑题", v.Reason)
}

func TestBuildRefereePromptWithItem(t *testing.T) {
	room := &types.MeetingRoom{Title: "发布评审", TopicBrief: "评估 v2 发布风险", Description: "d"}
	item := &types.MeetingAgendaItem{Title: "兼容性", Goal: "列出不兼容项"}
	p := buildRefereePrompt(room, item, "- 甲: 观点\n", 2)
	assert.Contains(t, p, "评估 v2 发布风险", "裁判必须使用收敛后的主题")
	assert.Contains(t, p, "当前子议题：兼容性")
	assert.Contains(t, p, "列出不兼容项")
	assert.Contains(t, p, "on_topic")
	assert.Contains(t, p, "goal_covered")

	// 无子议题：只判整场收敛
	p2 := buildRefereePrompt(room, nil, "- 甲: 观点\n", 1)
	assert.NotContains(t, p2, "当前子议题：兼容性", "无子议题时不得注入具体议题")
	assert.NotContains(t, p2, "列出不兼容项")
	assert.Contains(t, p2, "converged")
}

func TestPendingAgenda(t *testing.T) {
	items := []*types.MeetingAgendaItem{
		{ID: "i0", Status: types.MeetingAgendaActive},
		{ID: "i1", Status: types.MeetingAgendaPending},
		{ID: "i2", Status: types.MeetingAgendaDone},
	}
	got := pendingAgenda(items, "i0")
	require.Len(t, got, 1)
	assert.Equal(t, "i1", got[0].ID)
}

func TestAgendaRecap(t *testing.T) {
	item := &types.MeetingAgendaItem{Title: "兼容性"}
	remaining := []*types.MeetingAgendaItem{{Title: "回滚"}}
	got := agendaRecap(item, remaining, refereeVerdict{Consensus: "确认 3 处不兼容"})
	assert.Contains(t, got, "【议题小结】兼容性")
	assert.Contains(t, got, "确认 3 处不兼容")
	assert.Contains(t, got, "待讨论：回滚")
}

// ---- 选人依据扩展 ----

func TestAgentSkillNamesAndKnowledgeScope(t *testing.T) {
	agent := &types.CustomAgent{Config: types.CustomAgentConfig{
		SkillsSelectionMode: "selected",
		SelectedSkills:      []string{"sql-review", " "},
		KBSelectionMode:     "selected",
		KnowledgeBases:      []string{"kb1", "kb2"},
	}}
	assert.Equal(t, []string{"sql-review"}, agentSkillNames(agent))
	assert.Equal(t, "2 个", describeKnowledgeScope(agent.Config))

	// all 模式不列举技能（避免把全量技能塞进提示词）
	agent.Config.SkillsSelectionMode = "all"
	assert.Nil(t, agentSkillNames(agent))
	// none 模式知识库
	agent.Config.KBSelectionMode = "none"
	assert.Equal(t, "未绑定", describeKnowledgeScope(agent.Config))
}

func TestBuildSpeakerSelectionPromptWithSkillAndKB(t *testing.T) {
	p := buildSpeakerSelectionPrompt("主题", "描述", "问题", []speakerCandidate{
		{ID: "a1", Name: "DBA", Description: "数据库", Skills: []string{"sql-review"}, KnowledgeScope: "2 个"},
	})
	assert.Contains(t, p, "技能=sql-review")
	assert.Contains(t, p, "知识库=2 个")
}

// ---- 议程推进（假仓储） ----

type fakeAgendaRepo struct {
	interfaces.MeetingAgendaRepository
	items    []*types.MeetingAgendaItem
	replaced int
}

func (f *fakeAgendaRepo) ReplaceByRoom(_ context.Context, _ string, items []*types.MeetingAgendaItem) error {
	f.items = items
	f.replaced++
	return nil
}

func (f *fakeAgendaRepo) ListByRoom(_ context.Context, _ string) ([]*types.MeetingAgendaItem, error) {
	return f.items, nil
}

func (f *fakeAgendaRepo) Active(_ context.Context, _ string) (*types.MeetingAgendaItem, error) {
	for _, it := range f.items {
		if it.Status == types.MeetingAgendaActive {
			return it, nil
		}
	}
	return nil, nil
}

func (f *fakeAgendaRepo) Update(_ context.Context, item *types.MeetingAgendaItem) error {
	for i, it := range f.items {
		if it.ID == item.ID {
			f.items[i] = item
			return nil
		}
	}
	return errors.New("agenda item not found")
}

// TestEnsureActiveAgendaActivatesFirst ensureActiveAgenda 在无 active 时激活第一个 pending 项
func TestEnsureActiveAgendaActivatesFirst(t *testing.T) {
	repo := &fakeAgendaRepo{items: []*types.MeetingAgendaItem{
		{ID: "i0", Seq: 0, Status: types.MeetingAgendaPending},
		{ID: "i1", Seq: 1, Status: types.MeetingAgendaPending},
	}}
	e := &meetingEngine{agendaRepo: repo, messageRepo: &fakeMeetingMessageRepo{}, broadcaster: NewMeetingBroadcaster()}
	room := &types.MeetingRoom{ID: "r1", AgendaEnabled: true}

	items, active := e.ensureActiveAgenda(context.Background(), room)
	require.NotNil(t, active)
	assert.Equal(t, "i0", active.ID)
	assert.Len(t, items, 2)
	assert.Equal(t, types.MeetingAgendaActive, repo.items[0].Status)
}

// TestEnsureActiveAgendaDisabled 未启用议程时不返回任何议程（退回旧行为）
func TestEnsureActiveAgendaDisabled(t *testing.T) {
	repo := &fakeAgendaRepo{items: []*types.MeetingAgendaItem{{ID: "i0", Status: types.MeetingAgendaPending}}}
	e := &meetingEngine{agendaRepo: repo}
	items, active := e.ensureActiveAgenda(context.Background(), &types.MeetingRoom{ID: "r1"})
	assert.Nil(t, items)
	assert.Nil(t, active)
}

// TestAdvanceAgendaWritesSummary 推进议程时写入小结并标记完成；不再自动激活下一项（由用户决策后续动作）
func TestAdvanceAgendaWritesSummary(t *testing.T) {
	repo := &fakeAgendaRepo{items: []*types.MeetingAgendaItem{
		{ID: "i0", Seq: 0, Status: types.MeetingAgendaActive},
		{ID: "i1", Seq: 1, Status: types.MeetingAgendaPending},
	}}
	e := &meetingEngine{agendaRepo: repo, messageRepo: &fakeMeetingMessageRepo{}, broadcaster: NewMeetingBroadcaster()}
	room := &types.MeetingRoom{ID: "r1", AgendaEnabled: true}

	hasNext := e.advanceAgenda(context.Background(), room, repo.items[0], "区间 10-20 万")
	assert.True(t, hasNext, "本议题已结束，返回值恒为 true")
	assert.Equal(t, types.MeetingAgendaDone, repo.items[0].Status)
	assert.Equal(t, "区间 10-20 万", repo.items[0].Summary)
	assert.Equal(t, types.MeetingAgendaPending, repo.items[1].Status,
		"不再自动激活下一项：后续由用户在前端「下一步」中决定")
}

// fakeAgendaMessageRepo 为开题测试提供"按议题查消息"的最小实现
type fakeAgendaMessageRepo struct {
	*fakeMeetingMessageRepo
	itemMsgs []*types.MeetingMessage
}

func (f *fakeAgendaMessageRepo) ListByAgendaItem(_ context.Context, _ string, _ string, _ int) ([]*types.MeetingMessage, error) {
	return f.itemMsgs, nil
}

// TestOpenNextAgendaCreatesTrigger 活跃议题尚无发言时，开题消息作为下一轮触发点落库
func TestOpenNextAgendaCreatesTrigger(t *testing.T) {
	agendaRepo := &fakeAgendaRepo{items: []*types.MeetingAgendaItem{
		{ID: "i0", Seq: 0, Status: types.MeetingAgendaDone},
		{ID: "i1", Seq: 1, Title: "兼容性", Goal: "列出不兼容项", Status: types.MeetingAgendaActive},
	}}
	msgRepo := &fakeAgendaMessageRepo{fakeMeetingMessageRepo: &fakeMeetingMessageRepo{}}
	e := &meetingEngine{
		agendaRepo: agendaRepo, messageRepo: msgRepo,
		broadcaster: NewMeetingBroadcaster(),
	}
	room := &types.MeetingRoom{ID: "r1", AgendaEnabled: true}

	msg := e.openNextAgenda(context.Background(), room)
	require.NotNil(t, msg)
	assert.Contains(t, msg.Content, "【议题 2/2】兼容性")
	assert.Contains(t, msg.Content, "列出不兼容项")
}

// TestOpenNextAgendaSkipsWhenAlreadyDiscussing 议题已有发言时不重复开题
func TestOpenNextAgendaSkipsWhenAlreadyDiscussing(t *testing.T) {
	agendaRepo := &fakeAgendaRepo{items: []*types.MeetingAgendaItem{
		{ID: "i0", Seq: 0, Title: "成本", Status: types.MeetingAgendaActive},
	}}
	msgRepo := &fakeAgendaMessageRepo{
		fakeMeetingMessageRepo: &fakeMeetingMessageRepo{},
		itemMsgs:               []*types.MeetingMessage{{ID: "m1", Content: "已有发言"}},
	}
	e := &meetingEngine{agendaRepo: agendaRepo, messageRepo: msgRepo, broadcaster: NewMeetingBroadcaster()}
	assert.Nil(t, e.openNextAgenda(context.Background(), &types.MeetingRoom{ID: "r1", AgendaEnabled: true}))
}

// TestBuildAgendaDecomposePrompt 懒生成议程的提示词包含主题、参会智能体与 JSON 契约
func TestBuildAgendaDecomposePrompt(t *testing.T) {
	room := &types.MeetingRoom{Title: "发布评审", TopicBrief: "评估 v2 发布风险", Description: "评估风险"}
	agents := []*types.CustomAgent{{Name: "架构师", Description: "系统设计"}}
	p := buildAgendaDecomposePrompt(room, agents)
	assert.Contains(t, p, "评估 v2 发布风险")
	assert.Contains(t, p, "架构师")
	assert.Contains(t, p, "<agenda>")
	assert.Contains(t, p, `"goal"`)
	assert.NotContains(t, p, "## 建议流程", "仅拆解议题，不复用完整开场提示词")
}

// TestEnsureAgendaGenerated 跳开场时懒生成议程：模型输出 agenda JSON → 落库并激活第一项
func TestEnsureAgendaGenerated(t *testing.T) {
	repo := &fakeAgendaRepo{}
	e := &meetingEngine{
		agendaRepo:  repo,
		messageRepo: &fakeMeetingMessageRepo{},
		broadcaster: NewMeetingBroadcaster(),
		modelService: &stubModelService{
			availableModels: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA, IsDefault: true}},
			chatModel:       &hostChatStub{content: `<agenda>[{"title":"成本","goal":"区间"},{"title":"兼容性","goal":"不兼容项"}]</agenda>`},
		},
	}
	room := &types.MeetingRoom{ID: "r1", AgendaEnabled: true, Title: "发布评审"}
	e.ensureAgendaGenerated(context.Background(), room, []*types.CustomAgent{{Name: "架构师"}})

	require.Len(t, repo.items, 2)
	assert.Equal(t, types.MeetingAgendaActive, repo.items[0].Status)
	assert.Equal(t, "成本", repo.items[0].Title)
}

// TestEnsureAgendaGeneratedSkipsWhenExists 已有议程时不重复生成（避免每次讨论轮多一次模型调用）
func TestEnsureAgendaGeneratedSkipsWhenExists(t *testing.T) {
	repo := &fakeAgendaRepo{items: []*types.MeetingAgendaItem{{ID: "i0"}}}
	e := &meetingEngine{agendaRepo: repo, messageRepo: &fakeMeetingMessageRepo{}, broadcaster: NewMeetingBroadcaster()}
	e.ensureAgendaGenerated(context.Background(), &types.MeetingRoom{ID: "r1", AgendaEnabled: true}, nil)
	assert.Equal(t, 0, repo.replaced)
}

// TestPersistAgendaSkipsWhenExists 非替换模式且已有议程时不动
func TestPersistAgendaSkipsWhenExists(t *testing.T) {
	repo := &fakeAgendaRepo{items: []*types.MeetingAgendaItem{{ID: "old"}}}
	e := &meetingEngine{agendaRepo: repo}
	e.persistAgenda(context.Background(), "r1", []*types.MeetingAgendaItem{{ID: "new"}}, false)
	assert.Equal(t, 0, repo.replaced)

	e.persistAgenda(context.Background(), "r1", []*types.MeetingAgendaItem{{ID: "new"}}, true)
	assert.Equal(t, 1, repo.replaced)
}
