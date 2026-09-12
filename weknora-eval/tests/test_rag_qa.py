import pytest
from deepeval.metrics import AnswerRelevancyMetric, FaithfulnessMetric
from deepeval.test_case import LLMTestCase

from conftest import env_list, load_cases, majority_assert

pytestmark = [pytest.mark.qa]

CASES = load_cases("rag_qa.yaml")


def _metric(cls, threshold, model):
    kwargs = {"threshold": threshold}
    if model:
        kwargs["model"] = model
    return cls(**kwargs)


@pytest.mark.parametrize("case", CASES, ids=[c["id"] for c in CASES])
def test_rag_qa(case, eval_session, weknora, judge_ready):
    result = weknora.chat(
        eval_session,
        case["query"],
        knowledge_base_ids=env_list("WEKNORA_KB_IDS"),
    )
    assert result.error is None, f"流内 error 事件: {result.error}"
    assert result.blocked_by is None, f"流被 {result.blocked_by} 事件阻断（§7.1）"
    assert result.answer, "回答为空"

    min_refs = case.get("min_references", 0)
    assert len(result.retrieval_context) >= min_refs, (
        f"引用数 {len(result.retrieval_context)} < min_references {min_refs}"
    )
    for keyword in case.get("expected_answer_keywords") or []:
        assert keyword in result.answer, f"回答缺少关键词: {keyword}"

    thresholds = case.get("thresholds") or {}
    if result.retrieval_context:
        metric = _metric(
            FaithfulnessMetric,
            thresholds.get("faithfulness", 0.7),
            judge_ready,
        )
        majority_assert(
            metric,
            LLMTestCase(
                input=case["query"],
                actual_output=result.answer,
                retrieval_context=result.retrieval_context,
            ),
        )
    else:
        print("[§7.1] 本次未返回 references，retrieval_context 为空，跳过 Faithfulness")

    metric = _metric(
        AnswerRelevancyMetric,
        thresholds.get("answer_relevancy", 0.7),
        judge_ready,
    )
    majority_assert(
        metric,
        LLMTestCase(input=case["query"], actual_output=result.answer),
    )
