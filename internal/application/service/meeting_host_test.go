package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
