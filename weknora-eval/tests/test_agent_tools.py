import pytest
from deepeval.metrics import ToolCorrectnessMetric
from deepeval.test_case import LLMTestCase, ToolCall

from conftest import env_str, load_cases

pytestmark = [pytest.mark.tools]

CASES = load_cases("agent_tools.yaml")


@pytest.mark.parametrize("case", CASES, ids=[c["id"] for c in CASES])
def test_agent_tools(case, eval_session, weknora):
    agent_id = case.get("agent_id") or env_str("WEKNORA_AGENT_ID")
    if not agent_id:
        pytest.skip("agent_id 未配置（用例内 agent_id 或 WEKNORA_AGENT_ID）")

    result = weknora.chat(
        eval_session, case["query"], agent_mode=True, agent_id=agent_id
    )
    if result.blocked_by:
        pytest.xfail(
            f"流被 {result.blocked_by} 事件阻断，等待人工审批/OAuth（§7.1），CI 无法自动继续"
        )
    assert result.error is None, f"流内 error 事件: {result.error}"

    actual = list(weknora.tool_sequence(result))
    expected = list(case.get("expected_tools") or [])
    if not expected:
        assert not actual, f"期望不调用工具，实际调用: {actual}"
    elif case.get("exact_order"):
        assert actual == expected, f"工具调用顺序不符，期望 {expected}，实际 {actual}"
    else:
        missing = set(expected) - set(actual)
        assert not missing, f"缺少期望工具 {missing}，实际调用 {actual}"

    threshold = (case.get("thresholds") or {}).get("tool_correctness", 0.7)
    try:
        metric = ToolCorrectnessMetric(threshold=threshold)
    except Exception as exc:
        pytest.skip(
            f"ToolCorrectness 构造需模型配置（deepeval 4.x），确定性断言已通过，跳过指标: {exc}"
        )
    metric.measure(
        LLMTestCase(
            input=case["query"],
            actual_output=result.answer or "(无回答)",
            tools_called=[ToolCall(name=n) for n in actual],
            expected_tools=[ToolCall(name=n) for n in expected],
        )
    )
    assert metric.score >= metric.threshold, (
        f"ToolCorrectness {metric.score} < {metric.threshold}，理由: {metric.reason}"
    )
