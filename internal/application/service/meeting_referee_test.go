package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestBuildRefereePrompt(t *testing.T) {
	room := &types.MeetingRoom{ID: "r", Title: "发布方案评审", Description: "评审发布流程"}
	p := buildRefereePrompt(room, nil, "- 甲: 我建议蓝绿发布\n", 2)
	assert.Contains(t, p, room.Title)
	assert.Contains(t, p, room.Description)
	assert.Contains(t, p, "2")
	assert.Contains(t, p, "蓝绿发布")
	// JSON 契约字段必须出现在提示词中，保证输出可解析
	assert.Contains(t, p, "converged")
	assert.Contains(t, p, "reason")
	assert.Contains(t, p, "consensus")
}

func TestParseRefereeVerdict(t *testing.T) {
	// 裸 JSON
	v, ok := parseRefereeVerdict(`{"converged":true,"reason":"已无分歧","consensus":"采用蓝绿发布"}`)
	require.True(t, ok)
	assert.True(t, v.Converged)
	assert.Equal(t, "已无分歧", v.Reason)
	assert.Equal(t, "采用蓝绿发布", v.Consensus)

	// ```json 围栏包裹
	v, ok = parseRefereeVerdict("```json\n{\"converged\":false,\"reason\":\"仍有分歧\"}\n```")
	require.True(t, ok)
	assert.False(t, v.Converged)
	assert.Equal(t, "仍有分歧", v.Reason)

	// 前后夹带解释性文字：取首个 '{' 到末个 '}'
	v, ok = parseRefereeVerdict("我的判定如下：{\"converged\":true,\"consensus\":\"结论\"} 以上")
	require.True(t, ok)
	assert.True(t, v.Converged)

	// 非法输出 → ok=false（调用方按未收敛兜底）
	for _, in := range []string{"", "讨论还没结束", "[1,2]", "```json\n{bad json}\n```"} {
		_, ok := parseRefereeVerdict(in)
		assert.False(t, ok, "input=%q", in)
	}
}

// TestJudgeConsensusFailsSafe 裁判模型不可用/失败时一律判为未收敛，避免误终止讨论
func TestJudgeConsensusFailsSafe(t *testing.T) {
	room := &types.MeetingRoom{ID: "r", Title: "发布方案评审"}

	// 无可用模型
	e := &meetingEngine{modelService: &stubModelService{}, messageRepo: &fakeMeetingMessageRepo{}, broadcaster: NewMeetingBroadcaster()}
	assert.False(t, e.judgeConsensus(context.Background(), room, nil, nil, 1).Converged)

	// 模型可用但输出不可解析
	e2 := &meetingEngine{
		modelService: &stubModelService{
			availableModels: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA, IsDefault: true}},
			chatModel:       &hostChatStub{content: "我觉得差不多了"},
		},
		messageRepo: &fakeMeetingMessageRepo{}, broadcaster: NewMeetingBroadcaster(),
	}
	assert.False(t, e2.judgeConsensus(context.Background(), room, nil, nil, 1).Converged)
}

// TestConsensusMessageContent 共识正文优先用裁判结论，缺失时回退判定依据/主题
func TestConsensusMessageContent(t *testing.T) {
	room := &types.MeetingRoom{Title: "发布方案评审"}
	assert.Equal(t, "【共识】采用蓝绿发布", consensusMessageContent(room, refereeVerdict{Converged: true, Consensus: " 采用蓝绿发布 "}))
	assert.Contains(t, consensusMessageContent(room, refereeVerdict{Converged: true, Reason: "已无实质分歧"}), "已无实质分歧")
	assert.Contains(t, consensusMessageContent(room, refereeVerdict{Converged: true}), room.Title)
}
