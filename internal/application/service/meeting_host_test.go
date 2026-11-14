package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

// hostChatStub 主持人筛选测试用假模型：Chat 返回预设内容或错误
type hostChatStub struct {
	content string
	err     error
}

func (m *hostChatStub) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &types.ChatResponse{Content: m.content}, nil
}

func (m *hostChatStub) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (m *hostChatStub) GetModelName() string { return "host-stub" }
func (m *hostChatStub) GetModelID() string   { return "host-stub" }

func TestSelectSpeakersMentionWins(t *testing.T) {
	agents := []*types.CustomAgent{{ID: "a1"}, {ID: "a2"}}
	// availableModels 为空：显式 @ 不应触发主持人调用
	engine := &meetingEngine{modelService: &stubModelService{}}
	trigger := &types.MeetingMessage{MentionedAgentIDs: []string{"a1"}}
	got := engine.selectSpeakers(context.Background(), &types.MeetingRoom{}, trigger, agents)
	assert.Equal(t, []*types.CustomAgent{agents[0]}, got)
}

func TestSelectSpeakersMentionMissFallsBackToOne(t *testing.T) {
	agents := []*types.CustomAgent{{ID: "a1"}, {ID: "a2"}}
	engine := &meetingEngine{modelService: &stubModelService{}}
	// @ 了不存在的 id：规则回退为按参会顺序选第一位，而不是全员依次念稿
	trigger := &types.MeetingMessage{MentionedAgentIDs: []string{"ghost"}}
	got := engine.selectSpeakers(context.Background(), &types.MeetingRoom{AgendaEnabled: true}, trigger, agents)
	assert.Equal(t, []*types.CustomAgent{agents[0]}, got)
}

func TestSelectSpeakersHostModelUnavailableFallsBackToOne(t *testing.T) {
	agents := []*types.CustomAgent{{ID: "a1"}, {ID: "a2"}}
	engine := &meetingEngine{modelService: &stubModelService{availableModels: nil}}
	trigger := &types.MeetingMessage{}
	got := engine.selectSpeakers(context.Background(), &types.MeetingRoom{AgendaEnabled: true}, trigger, agents)
	assert.Equal(t, []*types.CustomAgent{agents[0]}, got)
}

func TestSelectSpeakersUsesHostSelection(t *testing.T) {
	agents := []*types.CustomAgent{{ID: "a1"}, {ID: "a2"}}
	engine := &meetingEngine{modelService: &stubModelService{
		availableModels: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA, IsDefault: true}},
		chatModel:       &hostChatStub{content: `{"next_speaker_id":"a2","question":"合规风险怎么兜底？"}`},
	}}
	trigger := &types.MeetingMessage{}
	got := engine.selectSpeakers(context.Background(), &types.MeetingRoom{AgendaEnabled: true}, trigger, agents)
	assert.Equal(t, []*types.CustomAgent{agents[1]}, got)
}

// 路由模式：mention/free/host-assign 的首轮选人语义
func TestSelectSpeakersRoutingModes(t *testing.T) {
	agents := []*types.CustomAgent{{ID: "a1"}, {ID: "a2"}}
	engine := &meetingEngine{modelService: &stubModelService{}}

	// mention：@ 命中者发言
	got := engine.selectSpeakers(context.Background(),
		&types.MeetingRoom{RoutingMode: types.MeetingRoutingMention},
		&types.MeetingMessage{MentionedAgentIDs: []string{"a2"}}, agents)
	assert.Equal(t, []*types.CustomAgent{agents[1]}, got)

	// mention：无人被 @ 时由规则选一人（主持人不可用 → 第一位）
	got = engine.selectSpeakers(context.Background(),
		&types.MeetingRoom{RoutingMode: types.MeetingRoutingMention, AgendaEnabled: true},
		&types.MeetingMessage{}, agents)
	assert.Equal(t, []*types.CustomAgent{agents[0]}, got)

	// free：忽略 @，直接全员
	got = engine.selectSpeakers(context.Background(),
		&types.MeetingRoom{RoutingMode: types.MeetingRoutingFree},
		&types.MeetingMessage{MentionedAgentIDs: []string{"a1"}}, agents)
	assert.Equal(t, agents, got)

	// host-assign：显式 @ 仍优先
	got = engine.selectSpeakers(context.Background(),
		&types.MeetingRoom{RoutingMode: types.MeetingRoutingHostAssign},
		&types.MeetingMessage{MentionedAgentIDs: []string{"a1"}}, agents)
	assert.Equal(t, []*types.CustomAgent{agents[0]}, got)
}

// TestBuildNextSpeakerPrompt 选人提示词必须带上候选、上一位发言者与"具体问题"的输出契约
func TestBuildNextSpeakerPrompt(t *testing.T) {
	room := &types.MeetingRoom{Title: "客服机器人选型"}
	p := buildNextSpeakerPrompt(room, "数据合规怎么处理？", "- 法务顾问: 先看数据出境", "法务顾问",
		[]speakerCandidate{{ID: "a1", Name: "技术架构师", Description: "系统设计"}})
	assert.Contains(t, p, "客服机器人选型")
	assert.Contains(t, p, "数据合规怎么处理？")
	assert.Contains(t, p, "上一位发言者：法务顾问")
	assert.Contains(t, p, `"a1"`)
	assert.Contains(t, p, "next_speaker_id")
}

// TestParseNextSpeakerChoice 选人结果解析：合法 id、围栏、非法 id、选中上一位发言者
func TestParseNextSpeakerChoice(t *testing.T) {
	id, q, ok := parseNextSpeakerChoice(
		"```json\n{\"next_speaker_id\":\"a2\",\"question\":\"成本怎么算？\"}\n```",
		[]string{"a1", "a2"}, "a1")
	assert.True(t, ok)
	assert.Equal(t, "a2", id)
	assert.Equal(t, "成本怎么算？", q)

	// 选中上一位发言者 → 拒绝
	_, _, ok = parseNextSpeakerChoice(`{"next_speaker_id":"a1"}`, []string{"a1", "a2"}, "a1")
	assert.False(t, ok)

	// 非法 id / 非 JSON → 拒绝
	_, _, ok = parseNextSpeakerChoice(`{"next_speaker_id":"ghost"}`, []string{"a1"}, "")
	assert.False(t, ok)
	_, _, ok = parseNextSpeakerChoice("我觉得都行", []string{"a1"}, "")
	assert.False(t, ok)
}

func TestFilterByIDs(t *testing.T) {
	agents := []*types.CustomAgent{{ID: "a1"}, {ID: "a2"}, {ID: "a3"}}
	// 按 agents 原顺序过滤（与 ids 传入顺序无关）
	got := filterByIDs(agents, []string{"a3", "a1"})
	assert.Equal(t, []*types.CustomAgent{agents[0], agents[2]}, got)
	// 重复 id 输入不产生重复输出
	got = filterByIDs(agents, []string{"a1", "a1", "a1"})
	assert.Equal(t, []*types.CustomAgent{agents[0]}, got)
}

// TestShouldHostIntervene 介入节流：everyN<=0 关闭；每完成 everyN 个子轮介入一次
func TestShouldHostIntervene(t *testing.T) {
	cases := []struct {
		everyN, completed int
		want              bool
	}{
		{0, 2, false},  // 关闭
		{-1, 2, false}, // 非法值视为关闭
		{2, 0, false},  // 尚未完成任何子轮
		{2, 1, false},
		{2, 2, true},
		{2, 3, false},
		{2, 4, true},
		{3, 6, true},
		{3, 5, false},
		{1, 1, true}, // 每轮介入
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, shouldHostIntervene(c.everyN, c.completed), "everyN=%d completed=%d", c.everyN, c.completed)
	}
}

// TestGenerateInterventionMessageFallback 主持人模型不可用时返回兜底文案（含轮次与主题），而非 panic
func TestGenerateInterventionMessageFallback(t *testing.T) {
	e := &meetingEngine{modelService: &stubModelService{}}
	room := &types.MeetingRoom{ID: "r", Title: "发布方案评审"}

	got := e.generateInterventionMessage(context.Background(), room, nil, 2)
	assert.NotEmpty(t, got)
	assert.Contains(t, got, "发布方案评审")
	assert.Contains(t, got, "2", "兜底文案应包含已完成轮次")
}
