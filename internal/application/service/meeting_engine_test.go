package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestUnionKnowledgeBaseIDs(t *testing.T) {
	// selected 模式：智能体 KB ∪ 会议级 KB，去重
	got := unionKnowledgeBaseIDs("selected", []string{"a", "b"}, []string{"b", "c"})
	assert.ElementsMatch(t, []string{"a", "b", "c"}, got)
	// none 模式：仅会议级 KB
	got = unionKnowledgeBaseIDs("none", []string{"a"}, []string{"c"})
	assert.Equal(t, []string{"c"}, got)
	// all 模式：返回 nil（AgentQA 回退为智能体"全部知识库"，天然是会议级 KB 的超集）
	got = unionKnowledgeBaseIDs("all", nil, []string{"c"})
	assert.Nil(t, got)
}

func TestBuildMeetingQuery(t *testing.T) {
	room := roomFixture()
	msgs := messagesFixture()
	q := buildMeetingQuery(room, "讨论下部署方案", msgs)
	// 应包含会议主题与共享上下文
	assert.Contains(t, q, room.Title)
	assert.Contains(t, q, "讨论下部署方案")
	assert.Contains(t, q, msgs[0].Content)
}

func roomFixture() *types.MeetingRoom {
	return &types.MeetingRoom{ID: "r1", Title: "发布方案评审", KnowledgeBaseIDs: []string{"c"}}
}

func messagesFixture() []*types.MeetingMessage {
	return []*types.MeetingMessage{
		{ID: "m1", SenderKind: types.MeetingSenderUser, Content: "先同步下背景"},
		{ID: "m2", SenderKind: types.MeetingSenderAgent, SenderID: "a1", Content: "我建议用蓝绿发布"},
	}
}
