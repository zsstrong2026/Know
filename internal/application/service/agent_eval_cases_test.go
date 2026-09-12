package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentEvalSmokeCases(t *testing.T) {
	cases := agentEvalSmokeCases()
	require.Len(t, cases, 6)

	wantIDs := []string{"smoke-01", "smoke-02", "smoke-03", "smoke-04", "smoke-05", "smoke-06"}
	wantTitles := []string{
		"闲聊不打扰知识库",
		"知识问题走检索",
		"复杂问题走理解链",
		"超纲问题不编造",
		"模糊问题正常应答",
		"中文知识问题",
	}
	wantAssertionCounts := []int{2, 3, 2, 2, 2, 2}

	for i, c := range cases {
		assert.Equal(t, wantIDs[i], c.ID)
		assert.Equal(t, wantTitles[i], c.Title)
		assert.NotEmpty(t, c.Query)
		assert.Len(t, c.Assertions, wantAssertionCounts[i])
	}

	assert.Equal(t, []string{"forbid_tool:knowledge_search", "answer_not_empty"}, cases[0].Assertions)
	assert.Equal(t, []string{"expect_tool:knowledge_search", "min_references:1", "answer_not_empty"}, cases[1].Assertions)
	assert.Equal(t, []string{"expect_tool:query_understand", "answer_not_empty"}, cases[2].Assertions)
	assert.Equal(t, []string{"no_error", "answer_not_empty"}, cases[3].Assertions)
	assert.Equal(t, []string{"no_error", "answer_not_empty"}, cases[4].Assertions)
	assert.Equal(t, []string{"expect_tool:knowledge_search", "answer_not_empty"}, cases[5].Assertions)

	assert.Empty(t, cases[0].JudgeMetrics)
	assert.Equal(t, []string{"relevancy"}, cases[1].JudgeMetrics)
	assert.Equal(t, []string{"relevancy"}, cases[2].JudgeMetrics)
	assert.Equal(t, []string{"honesty"}, cases[3].JudgeMetrics)
	assert.Empty(t, cases[4].JudgeMetrics)
	assert.Equal(t, []string{"relevancy"}, cases[5].JudgeMetrics)
}

func TestAgentEvalSmokeCases_ReturnsFreshSlice(t *testing.T) {
	cases := agentEvalSmokeCases()
	cases[0].Assertions[0] = "mutated"

	fresh := agentEvalSmokeCases()
	assert.Equal(t, "forbid_tool:knowledge_search", fresh[0].Assertions[0])
}

func TestNormalizeToolName(t *testing.T) {
	assert.Equal(t, "knowledge_search", normalizeToolName("Knowledge_Search"))
	assert.Equal(t, "knowledge_search", normalizeToolName("  KNOWLEDGE_SEARCH "))
	assert.Equal(t, "query_understand", normalizeToolName("Query_Understand"))
	assert.Equal(t, "", normalizeToolName(""))
}

func TestEvaluateAgentEvalAssertion_ExpectTool(t *testing.T) {
	obs := agentEvalObservation{ToolNames: []string{"Knowledge_Search", "query_understand"}}

	result := evaluateAgentEvalAssertion("expect_tool:knowledge_search", obs)
	assert.Equal(t, "expect_tool:knowledge_search", result.Name)
	assert.Equal(t, "调用", result.Expected)
	assert.Equal(t, "已调用", result.Actual)
	assert.True(t, result.Passed)

	miss := evaluateAgentEvalAssertion("expect_tool:web_search", obs)
	assert.Equal(t, "调用", miss.Expected)
	assert.Equal(t, "未调用", miss.Actual)
	assert.False(t, miss.Passed)

	none := evaluateAgentEvalAssertion("expect_tool:knowledge_search", agentEvalObservation{})
	assert.False(t, none.Passed)
}

func TestEvaluateAgentEvalAssertion_ForbidTool(t *testing.T) {
	obs := agentEvalObservation{ToolNames: []string{"Knowledge_Search"}}

	result := evaluateAgentEvalAssertion("forbid_tool:knowledge_search", obs)
	assert.Equal(t, "forbid_tool:knowledge_search", result.Name)
	assert.Equal(t, "不调用", result.Expected)
	assert.Equal(t, "已调用", result.Actual)
	assert.False(t, result.Passed)

	ok := evaluateAgentEvalAssertion("forbid_tool:web_search", obs)
	assert.Equal(t, "未调用", ok.Actual)
	assert.True(t, ok.Passed)
}

func TestEvaluateAgentEvalAssertion_MinReferences(t *testing.T) {
	obs := agentEvalObservation{ReferenceCount: 2}

	result := evaluateAgentEvalAssertion("min_references:1", obs)
	assert.Equal(t, "min_references:1", result.Name)
	assert.Equal(t, "引用 ≥ 1", result.Expected)
	assert.Equal(t, "2", result.Actual)
	assert.True(t, result.Passed)

	exact := evaluateAgentEvalAssertion("min_references:2", obs)
	assert.True(t, exact.Passed)

	fail := evaluateAgentEvalAssertion("min_references:3", obs)
	assert.False(t, fail.Passed)
	assert.Equal(t, "2", fail.Actual)

	invalid := evaluateAgentEvalAssertion("min_references:abc", obs)
	assert.False(t, invalid.Passed)
}

func TestEvaluateAgentEvalAssertion_AnswerNotEmpty(t *testing.T) {
	obs := agentEvalObservation{Answer: "你好！我是智能助手，很高兴为您服务。"}

	result := evaluateAgentEvalAssertion("answer_not_empty", obs)
	assert.Equal(t, "answer_not_empty", result.Name)
	assert.Equal(t, "非空", result.Expected)
	assert.Equal(t, "18 字", result.Actual)
	assert.True(t, result.Passed)

	whitespace := evaluateAgentEvalAssertion("answer_not_empty", agentEvalObservation{Answer: "  \n\t "})
	assert.False(t, whitespace.Passed)
	assert.Equal(t, "(空)", whitespace.Actual)

	empty := evaluateAgentEvalAssertion("answer_not_empty", agentEvalObservation{})
	assert.False(t, empty.Passed)
	assert.Equal(t, "(空)", empty.Actual)
}

func TestEvaluateAgentEvalAssertion_NoError(t *testing.T) {
	obs := agentEvalObservation{EndedNormally: true}

	result := evaluateAgentEvalAssertion("no_error", obs)
	assert.Equal(t, "no_error", result.Name)
	assert.Equal(t, "正常结束", result.Expected)
	assert.Equal(t, "正常结束", result.Actual)
	assert.True(t, result.Passed)

	withError := evaluateAgentEvalAssertion("no_error", agentEvalObservation{EndedNormally: true, ErrorMessage: "模型调用失败"})
	assert.False(t, withError.Passed)
	assert.Equal(t, "模型调用失败", withError.Actual)

	incomplete := evaluateAgentEvalAssertion("no_error", agentEvalObservation{})
	assert.False(t, incomplete.Passed)
	assert.Equal(t, "未正常结束", incomplete.Actual)
}

func TestEvaluateAgentEvalAssertion_UnknownSpec(t *testing.T) {
	result := evaluateAgentEvalAssertion("unknown_assertion:foo", agentEvalObservation{})
	assert.Equal(t, "unknown_assertion:foo", result.Name)
	assert.False(t, result.Passed)
}

func TestRunAgentEvalAssertions(t *testing.T) {
	specs := []string{"expect_tool:knowledge_search", "min_references:1", "answer_not_empty"}
	obs := agentEvalObservation{
		Answer:         "支持的格式包括 PDF。",
		ReferenceCount: 1,
		ToolNames:      []string{"knowledge_search"},
	}

	results := runAgentEvalAssertions(specs, obs)
	require.Len(t, results, 3)
	for _, r := range results {
		assert.True(t, r.Passed, r.Name)
	}

	failing := runAgentEvalAssertions(specs, agentEvalObservation{Answer: "不知道"})
	require.Len(t, failing, 3)
	assert.False(t, failing[0].Passed)
	assert.False(t, failing[1].Passed)
	assert.True(t, failing[2].Passed)
}

func TestRunAgentEvalAssertions_AllPassedHelper(t *testing.T) {
	assert.True(t, allAgentEvalAssertionsPassed(runAgentEvalAssertions(
		[]string{"no_error", "answer_not_empty"},
		agentEvalObservation{Answer: "ok", EndedNormally: true},
	)))
	assert.False(t, allAgentEvalAssertionsPassed(runAgentEvalAssertions(
		[]string{"no_error"},
		agentEvalObservation{},
	)))
}
