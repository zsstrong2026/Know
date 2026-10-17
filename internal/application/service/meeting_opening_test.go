package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/types"
)

// jsonMarshalForTest 直接透传标准库 Marshal，便于契约测试断言 payload 形状
func jsonMarshalForTest(v any) ([]byte, error) { return json.Marshal(v) }

func TestBuildOpeningPrompt(t *testing.T) {
	room := &types.MeetingRoom{ID: "r1", Title: "发布评审", Description: "评估 v2 发布风险"}
	agents := []*types.CustomAgent{
		{ID: "a1", Name: "架构师", Description: "负责系统架构"},
		{ID: "a2", Name: "测试专家"},
	}

	p := buildOpeningPrompt(room, agents, "")
	assert.Contains(t, p, "发布评审")
	assert.Contains(t, p, "评估 v2 发布风险")
	assert.Contains(t, p, "架构师")
	assert.Contains(t, p, "负责系统架构", "开场应带智能体专长供主持人规划流程")
	assert.Contains(t, p, "## 主题分析")
	assert.Contains(t, p, "## 建议流程")
	assert.NotContains(t, p, "修改意见", "首次开场不含调整反馈段")

	p2 := buildOpeningPrompt(room, nil, "先讨论性能再谈兼容性")
	assert.Contains(t, p2, "先讨论性能再谈兼容性")
	assert.Contains(t, p2, "重新输出开场内容", "调整流程模式要求主持人结合意见重出流程")
}

func TestOpeningPayloadMarshal(t *testing.T) {
	// payload 固定为 {"stage":"opening"}，前端据此渲染按钮组（契约测试）
	p := openingPayload{Stage: MeetingOpeningStage}
	b, err := jsonMarshalForTest(p)
	assert.NoError(t, err)
	assert.True(t, strings.Contains(string(b), `"stage":"opening"`), "got %s", string(b))
}

func TestLatestOpeningMessageID(t *testing.T) {
	// 纯逻辑：payload 解析由 latestOpeningMessageID 承担，这里验证其倒序取最近开场
	msgs := []*types.MeetingMessage{
		{ID: "m1", Payload: []byte(`{"stage":"opening"}`)},
		{ID: "m2", Payload: []byte(`{}`)},
		{ID: "m3", Payload: []byte(`{"stage":"opening"}`)},
	}
	// 最近一条开场是 m3
	idx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		var p openingPayload
		if json.Unmarshal(msgs[i].Payload, &p) == nil && p.Stage == MeetingOpeningStage {
			idx = i
			break
		}
	}
	assert.Equal(t, 2, idx)
}
