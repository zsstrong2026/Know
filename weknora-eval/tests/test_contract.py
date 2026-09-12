import pytest
import requests

from weknora_client import WeKnoraAPIError

pytestmark = pytest.mark.contract


def test_health_is_unauthenticated(base_url):
    resp = requests.get(f"{base_url}/health", timeout=10)
    assert resp.status_code == 200, f"/health 返回 {resp.status_code}"
    assert resp.json().get("status") == "ok"


def test_api_key_enforced(base_url):
    resp = requests.get(f"{base_url}/api/v1/sessions", timeout=10)
    assert resp.status_code >= 400, (
        f"受保护路由未带 X-API-Key 应被拒绝（非 2xx），实际 {resp.status_code}（fail-closed 门禁被破坏）"
    )
    assert resp.status_code != 200, "不得泄露受保护资源内容"



def test_unknown_route_rejected(base_url):
    from conftest import env_str

    api_key = env_str("WEKNORA_API_KEY")
    if not api_key:
        pytest.skip("缺少 WEKNORA_API_KEY")
    resp = requests.get(
        f"{base_url}/api/v1/nonexistent-route",
        headers={"X-API-Key": api_key},
        timeout=10,
    )
    assert resp.status_code in (403, 404), (
        f"未声明路由带 key 也应被门禁拒绝，实际 {resp.status_code}"
    )


def test_session_crud_contract(weknora, require_reachable):
    session_id = weknora.create_session(title="contract-probe")
    assert session_id, "POST /sessions 未返回 data.id"
    weknora.delete_session(session_id)


def test_sse_stream_contract(eval_session, weknora):
    from weknora_client import RESPONSE_TYPES

    result = weknora.chat(eval_session, "你好，请只回复两个字：收到")
    assert result.blocked_by is None, (
        f"流被阻断事件 {result.blocked_by} 暂停：该环境需要人工审批/OAuth，"
        "请改用免审批工具或调整审批配置（v1.2 §7.1）"
    )
    assert result.error is None, f"服务端返回 error 事件：{result.error}"
    unknown = {
        evt.get("response_type")
        for evt in result.raw_events
        if evt.get("response_type") not in RESPONSE_TYPES
    }
    assert not unknown, f"发现未知 response_type（契约漂移）：{unknown}"
    assert result.answer.strip(), "未收到 answer 内容"


def test_agent_chat_requires_agent_id(eval_session, weknora):
    with pytest.raises(WeKnoraAPIError) as exc_info:
        weknora.chat(eval_session, "ping", agent_mode=True)
    assert exc_info.value.status_code == 400, (
        f"agent_enabled=true 且无法解析 agent_id 应 400，实际 {exc_info.value.status_code}"
    )
