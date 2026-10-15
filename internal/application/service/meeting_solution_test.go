package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestBuildSolutionPrompt(t *testing.T) {
	room := &types.MeetingRoom{Title: "发布方案评审", Description: "讨论 v2 发布策略"}
	adopted := []*types.MeetingMessage{
		{SenderID: "a1", Content: "建议蓝绿发布"},
		{SenderID: "a2", Content: "需要回滚预案"},
	}
	nameOf := func(id string) string {
		return map[string]string{"a1": "架构师", "a2": "测试专家"}[id]
	}
	out := buildSolutionPrompt(room, adopted, nameOf)
	assert.Contains(t, out, "发布方案评审") // 主题入提示词
	assert.Contains(t, out, "架构师：建议蓝绿发布")
	assert.Contains(t, out, "测试专家：需要回滚预案")
	assert.Contains(t, out, "## 背景与目标") // 固定结构四小节
	assert.Contains(t, out, "## 方案要点")
	assert.Contains(t, out, "## 风险与待确认事项")
	assert.Contains(t, out, "## 下一步行动")
}
