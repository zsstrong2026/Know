package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseSSEData(t *testing.T) {
	evt, ok := parseSSEData(`data:{"response_type":"answer","content":"你好"}`)
	require.True(t, ok)
	assert.Equal(t, "answer", evt.ResponseType)
	assert.Equal(t, "你好", evt.Content)
	assert.Equal(t, `{"response_type":"answer","content":"你好"}`, string(evt.Raw))

	space, ok := parseSSEData(`data: {"response_type":"complete"}`)
	require.True(t, ok)
	assert.Equal(t, "complete", space.ResponseType)

	_, ok = parseSSEData("event: message")
	assert.False(t, ok)

	_, ok = parseSSEData("data:")
	assert.False(t, ok)

	_, ok = parseSSEData("data:[DONE]")
	assert.False(t, ok)

	_, ok = parseSSEData("data:{invalid json")
	assert.False(t, ok)
}

func TestAgentEvalStreamCollector_AnswerAccumulates(t *testing.T) {
	collector := &agentEvalStreamCollector{}
	assert.False(t, collector.Apply(sseEvent{ResponseType: "answer", Content: "你好"}))
	assert.False(t, collector.Apply(sseEvent{ResponseType: "answer", Content: "，世界"}))
	assert.Equal(t, "你好，世界", collector.Answer)
	assert.False(t, collector.EndedNormally)
}

func TestAgentEvalStreamCollector_ReferenceCount(t *testing.T) {
	collector := &agentEvalStreamCollector{}
	collector.Apply(sseEvent{
		ResponseType: "references",
		KnowledgeReferences: []sseReference{
			{Content: "chunk-1"},
			{Content: ""},
			{Content: "chunk-2"},
		},
	})
	assert.Equal(t, 2, collector.ReferenceCount)

	collector.Apply(sseEvent{
		ResponseType:        "references",
		KnowledgeReferences: []sseReference{{Content: "chunk-3"}},
	})
	assert.Equal(t, 3, collector.ReferenceCount)
}

func TestAgentEvalStreamCollector_ToolCallNestedAndTopLevel(t *testing.T) {
	collector := &agentEvalStreamCollector{}
	collector.Apply(sseEvent{
		ResponseType: "tool_call",
		Data:         json.RawMessage(`{"tool_name":"Knowledge_Search","tool_call_id":"tc-1"}`),
	})
	require.Len(t, collector.ToolCalls, 1)
	assert.Equal(t, "Knowledge_Search", collector.ToolCalls[0].ToolName)
	assert.Equal(t, "tc-1", collector.ToolCalls[0].ToolCallID)

	collector.Apply(sseEvent{
		ResponseType: "tool_call",
		Data:         json.RawMessage(`{"tool_name":"web_search","tool_call_id":"tc-2"}`),
		ToolCalls: []sseToolCall{
			{ToolName: "query_understand", ToolCallID: "tc-3"},
			{ToolName: "", ToolCallID: "tc-4"},
		},
	})
	require.Len(t, collector.ToolCalls, 3)
	assert.Equal(t, "web_search", collector.ToolCalls[1].ToolName)
	assert.Equal(t, "query_understand", collector.ToolCalls[2].ToolName)
}

func TestAgentEvalStreamCollector_CompleteStops(t *testing.T) {
	collector := &agentEvalStreamCollector{}
	stopped := collector.Apply(sseEvent{ResponseType: "complete"})
	assert.True(t, stopped)
	assert.True(t, collector.EndedNormally)
	assert.Empty(t, collector.ErrorMessage)
}

func TestAgentEvalStreamCollector_ErrorStops(t *testing.T) {
	collector := &agentEvalStreamCollector{}
	stopped := collector.Apply(sseEvent{ResponseType: "error", Content: "模型调用失败"})
	assert.True(t, stopped)
	assert.Equal(t, "模型调用失败", collector.ErrorMessage)
	assert.False(t, collector.EndedNormally)

	fallback := &agentEvalStreamCollector{}
	stopped = fallback.Apply(sseEvent{
		ResponseType: "error",
		Raw:          []byte(`{"response_type":"error","data":{"message":"boom"}}`),
	})
	assert.True(t, stopped)
	assert.Equal(t, `{"response_type":"error","data":{"message":"boom"}}`, fallback.ErrorMessage)
}

func TestAgentEvalStreamCollector_ErrorRawTruncated(t *testing.T) {
	collector := &agentEvalStreamCollector{}
	collector.Apply(sseEvent{ResponseType: "error", Raw: []byte(strings.Repeat("错", 600))})
	assert.Len(t, []rune(collector.ErrorMessage), 500)
}

func TestAgentEvalStreamCollector_BlockingStops(t *testing.T) {
	collector := &agentEvalStreamCollector{}
	stopped := collector.Apply(sseEvent{ResponseType: "tool_approval_required"})
	assert.True(t, stopped)
	assert.Equal(t, "tool_approval_required", collector.BlockedBy)

	stopped = collector.Apply(sseEvent{ResponseType: "mcp_oauth_required"})
	assert.True(t, stopped)
	assert.Equal(t, "mcp_oauth_required", collector.BlockedBy)
}

func TestAgentEvalStreamCollector_Observation(t *testing.T) {
	collector := &agentEvalStreamCollector{
		Answer:         "答案内容",
		ReferenceCount: 2,
		ToolCalls:      []types.AgentEvalToolCall{{ToolName: "Knowledge_Search", ToolCallID: "tc-1"}},
		EndedNormally:  true,
	}

	obs := collector.Observation()
	assert.Equal(t, "答案内容", obs.Answer)
	assert.Equal(t, 2, obs.ReferenceCount)
	assert.Equal(t, []string{"Knowledge_Search"}, obs.ToolNames)
	assert.True(t, obs.EndedNormally)
	assert.Empty(t, obs.ErrorMessage)
}

func TestConsumeAgentEvalSSE(t *testing.T) {
	stream := strings.Join([]string{
		`data:{"response_type":"agent_query"}`,
		``,
		`event: ping`,
		`data:[DONE]`,
		`data:{"response_type":"answer","content":"支持 PDF"}`,
		`data:{broken`,
		`data: {"response_type":"tool_call","data":{"tool_name":"Knowledge_Search","tool_call_id":"tc-1"}}`,
		`data:{"response_type":"references","knowledge_references":[{"content":"c1"}]}`,
		`data:{"response_type":"complete"}`,
		`data:{"response_type":"answer","content":"不应计入"}`,
	}, "\n")

	collector := &agentEvalStreamCollector{}
	err := consumeAgentEvalSSE(strings.NewReader(stream), collector)
	require.NoError(t, err)
	assert.Equal(t, "支持 PDF", collector.Answer)
	assert.Equal(t, 1, collector.ReferenceCount)
	require.Len(t, collector.ToolCalls, 1)
	assert.Equal(t, "Knowledge_Search", collector.ToolCalls[0].ToolName)
	assert.True(t, collector.EndedNormally)
}

func TestConsumeAgentEvalSSE_EOFWithoutComplete(t *testing.T) {
	collector := &agentEvalStreamCollector{}
	err := consumeAgentEvalSSE(
		strings.NewReader("data:{\"response_type\":\"answer\",\"content\":\"部分回答\"}\n"),
		collector,
	)
	require.NoError(t, err)
	assert.Equal(t, "部分回答", collector.Answer)
	assert.False(t, collector.EndedNormally)
}

func TestConsumeAgentEvalSSE_ErrorEvent(t *testing.T) {
	stream := "data:{\"response_type\":\"answer\",\"content\":\"开始\"}\n" +
		"data:{\"response_type\":\"error\",\"content\":\"服务异常\"}\n"
	collector := &agentEvalStreamCollector{}
	err := consumeAgentEvalSSE(strings.NewReader(stream), collector)
	require.NoError(t, err)
	assert.Equal(t, "开始", collector.Answer)
	assert.Equal(t, "服务异常", collector.ErrorMessage)
}

func TestParseJudgeOutput(t *testing.T) {
	score, reason, err := parseJudgeOutput(`{"score": 0.9, "reason": "回答切题"}`)
	require.NoError(t, err)
	assert.Equal(t, 0.9, score)
	assert.Equal(t, "回答切题", reason)

	score, reason, err = parseJudgeOutput("```json\n{\"score\": 0.5, \"reason\": \"ok\"}\n```")
	require.NoError(t, err)
	assert.Equal(t, 0.5, score)
	assert.Equal(t, "ok", reason)

	score, reason, err = parseJudgeOutput("评分结果：{\"score\": 0, \"reason\": \"不相关\"}（满分 1）")
	require.NoError(t, err)
	assert.Equal(t, float64(0), score)
	assert.Equal(t, "不相关", reason)

	_, _, err = parseJudgeOutput("抱歉，我无法评估。")
	assert.Error(t, err)

	_, _, err = parseJudgeOutput(`{"score": "high", "reason": "x"}`)
	assert.Error(t, err)
}
