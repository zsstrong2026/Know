package service

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

type agentEvalSmokeCase struct {
	ID           string
	Title        string
	Query        string
	Assertions   []string
	JudgeMetrics []string
}

type agentEvalObservation struct {
	Answer         string
	ReferenceCount int
	ToolNames      []string
	EndedNormally  bool
	ErrorMessage   string
}

func agentEvalSmokeCases() []agentEvalSmokeCase {
	return []agentEvalSmokeCase{
		{
			ID:         "smoke-01",
			Title:      "闲聊不打扰知识库",
			Query:      "你好，请介绍一下你自己",
			Assertions: []string{"forbid_tool:knowledge_search", "answer_not_empty"},
		},
		{
			ID:           "smoke-02",
			Title:        "知识问题走检索",
			Query:        "请根据知识库资料回答：这个系统支持哪些文档格式？",
			Assertions:   []string{"expect_tool:knowledge_search", "min_references:1", "answer_not_empty"},
			JudgeMetrics: []string{"relevancy"},
		},
		{
			ID:           "smoke-03",
			Title:        "复杂问题走理解链",
			Query:        "帮我分析一下：上传文档后检索结果的排序依据是什么？和直接提问相比有什么区别？",
			Assertions:   []string{"expect_tool:query_understand", "answer_not_empty"},
			JudgeMetrics: []string{"relevancy"},
		},
		{
			ID:           "smoke-04",
			Title:        "超纲问题不编造",
			Query:        "2026年世界杯决赛的比分是多少？",
			Assertions:   []string{"no_error", "answer_not_empty"},
			JudgeMetrics: []string{"honesty"},
		},
		{
			ID:         "smoke-05",
			Title:      "模糊问题正常应答",
			Query:      "嗯……就是说，那个东西怎么样？",
			Assertions: []string{"no_error", "answer_not_empty"},
		},
		{
			ID:           "smoke-06",
			Title:        "中文知识问题",
			Query:        "知识库里关于系统部署的内容，应该怎么配置环境变量？",
			Assertions:   []string{"expect_tool:knowledge_search", "answer_not_empty"},
			JudgeMetrics: []string{"relevancy"},
		},
	}
}

func normalizeToolName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func containsAgentEvalTool(names []string, target string) bool {
	normalized := normalizeToolName(target)
	if normalized == "" {
		return false
	}
	for _, name := range names {
		if normalizeToolName(name) == normalized {
			return true
		}
	}
	return false
}

func evaluateAgentEvalAssertion(spec string, obs agentEvalObservation) types.AgentEvalAssertion {
	result := types.AgentEvalAssertion{Name: spec}
	kind, arg, _ := strings.Cut(spec, ":")
	switch kind {
	case "expect_tool":
		result.Expected = "调用"
		if containsAgentEvalTool(obs.ToolNames, arg) {
			result.Actual = "已调用"
			result.Passed = true
		} else {
			result.Actual = "未调用"
		}
	case "forbid_tool":
		result.Expected = "不调用"
		if containsAgentEvalTool(obs.ToolNames, arg) {
			result.Actual = "已调用"
		} else {
			result.Actual = "未调用"
			result.Passed = true
		}
	case "min_references":
		result.Expected = "引用 ≥ " + arg
		result.Actual = strconv.Itoa(obs.ReferenceCount)
		minCount, err := strconv.Atoi(arg)
		result.Passed = err == nil && obs.ReferenceCount >= minCount
	case "answer_not_empty":
		result.Expected = "非空"
		trimmed := strings.TrimSpace(obs.Answer)
		if trimmed == "" {
			result.Actual = "(空)"
		} else {
			result.Actual = strconv.Itoa(utf8.RuneCountInString(trimmed)) + " 字"
			result.Passed = true
		}
	case "no_error":
		result.Expected = "正常结束"
		if obs.ErrorMessage != "" {
			result.Actual = obs.ErrorMessage
		} else if obs.EndedNormally {
			result.Actual = "正常结束"
			result.Passed = true
		} else {
			result.Actual = "未正常结束"
		}
	default:
		result.Actual = "未知断言"
	}
	return result
}

func runAgentEvalAssertions(specs []string, obs agentEvalObservation) types.AgentEvalAssertionList {
	results := make(types.AgentEvalAssertionList, 0, len(specs))
	for _, spec := range specs {
		results = append(results, evaluateAgentEvalAssertion(spec, obs))
	}
	return results
}

func allAgentEvalAssertionsPassed(list types.AgentEvalAssertionList) bool {
	for _, item := range list {
		if !item.Passed {
			return false
		}
	}
	return true
}
