package service

import (
	"context"
	"errors"
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

func TestParseSpeakerSelection(t *testing.T) {
	// 主持人 LLM 返回 JSON 数组，容错解析
	ids := parseSpeakerSelection(`["a1","a2"]`, []string{"a1", "a2", "a3"})
	assert.Equal(t, []string{"a1", "a2"}, ids)

	// 含 markdown 代码块包裹也能解析
	ids = parseSpeakerSelection("```json\n[\"a3\"]\n```", []string{"a1", "a2", "a3"})
	assert.Equal(t, []string{"a3"}, ids)

	// 非法输出 → 返回 nil（调用方回退全员）
	ids = parseSpeakerSelection("我觉得都应该说", []string{"a1"})
	assert.Nil(t, ids)

	// 过滤不在候选集合内的 id（防幻觉）
	ids = parseSpeakerSelection(`["a1","ghost"]`, []string{"a1", "a2"})
	assert.Equal(t, []string{"a1"}, ids)
}

func TestBuildSpeakerSelectionPrompt(t *testing.T) {
	agents := []speakerCandidate{
		{ID: "a1", Name: "法务顾问", Description: "审合同"},
		{ID: "a2", Name: "技术架构师", Description: "系统设计"},
	}
	p := buildSpeakerSelectionPrompt("客服机器人选型", "数据合规怎么处理？", agents)
	assert.Contains(t, p, "客服机器人选型")
	assert.Contains(t, p, "数据合规怎么处理？")
	assert.Contains(t, p, "法务顾问")
	assert.Contains(t, p, `"a1"`) // 候选 id 必须出现在提示词中，便于模型引用
}

func TestSelectSpeakersMentionWins(t *testing.T) {
	agents := []*types.CustomAgent{{ID: "a1"}, {ID: "a2"}}
	// availableModels 为空：若误触发主持人调用将回退全员，从而无法得到 [a1]
	engine := &meetingEngine{modelService: &stubModelService{}}
	trigger := &types.MeetingMessage{MentionedAgentIDs: []string{"a1"}}
	got := engine.selectSpeakers(context.Background(), &types.MeetingRoom{}, trigger, agents)
	assert.Equal(t, []*types.CustomAgent{agents[0]}, got)
}

func TestSelectSpeakersMentionMissFallsBackToAll(t *testing.T) {
	agents := []*types.CustomAgent{{ID: "a1"}, {ID: "a2"}}
	engine := &meetingEngine{modelService: &stubModelService{}}
	// @ 了不存在的 id：降级到主持人（此处不可用）再回退全员，顺序保持
	trigger := &types.MeetingMessage{MentionedAgentIDs: []string{"ghost"}}
	got := engine.selectSpeakers(context.Background(), &types.MeetingRoom{}, trigger, agents)
	assert.Equal(t, agents, got)
}

func TestSelectSpeakersHostModelUnavailableFallsBackToAll(t *testing.T) {
	agents := []*types.CustomAgent{{ID: "a1"}, {ID: "a2"}}
	engine := &meetingEngine{modelService: &stubModelService{availableModels: nil}}
	trigger := &types.MeetingMessage{}
	got := engine.selectSpeakers(context.Background(), &types.MeetingRoom{}, trigger, agents)
	assert.Equal(t, agents, got)
}

func TestSelectSpeakersUsesHostSelection(t *testing.T) {
	agents := []*types.CustomAgent{{ID: "a1"}, {ID: "a2"}}
	engine := &meetingEngine{modelService: &stubModelService{
		availableModels: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA, IsDefault: true}},
		chatModel:       &hostChatStub{content: `["a2"]`},
	}}
	trigger := &types.MeetingMessage{}
	got := engine.selectSpeakers(context.Background(), &types.MeetingRoom{}, trigger, agents)
	assert.Equal(t, []*types.CustomAgent{agents[1]}, got)
}

func TestSelectSpeakersByLLMReturnsNilOnChatError(t *testing.T) {
	engine := &meetingEngine{modelService: &stubModelService{
		availableModels: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA, IsDefault: true}},
		chatModel:       &hostChatStub{err: errors.New("boom")},
	}}
	got := engine.selectSpeakersByLLM(context.Background(), &types.MeetingRoom{}, &types.MeetingMessage{},
		[]speakerCandidate{{ID: "a1"}})
	assert.Nil(t, got)
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

func TestParseSpeakerSelectionEdgeCases(t *testing.T) {
	valid := []string{"a1", "a2"}
	for _, in := range []string{"", "[]", "null", `[["a1"]]`, "[1,2]"} {
		assert.Nil(t, parseSpeakerSelection(in, valid), "input=%q", in)
	}
	// 去重
	assert.Equal(t, []string{"a1"}, parseSpeakerSelection(`["a1","a1"]`, valid))
}
