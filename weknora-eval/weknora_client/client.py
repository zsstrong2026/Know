import json

import requests

from .exceptions import WeKnoraAPIError

RESPONSE_TYPES = {
    "answer",
    "references",
    "thinking",
    "tool_call",
    "tool_result",
    "error",
    "reflection",
    "session_title",
    "agent_query",
    "complete",
    "tool_approval_required",
    "tool_approval_resolved",
    "mcp_oauth_required",
    "mcp_oauth_resolved",
    "memory_recalled",
}

BLOCKING_RESPONSE_TYPES = {"tool_approval_required", "mcp_oauth_required"}


class ChatResult:
    def __init__(self):
        self.answer = ""
        self.thinking = ""
        self.retrieval_context = []
        self.tool_calls = []
        self.tool_results = []
        self.finish_reason = None
        self.usage = None
        self.raw_events = []
        self.blocked_by = None
        self.error = None


class WeKnoraClient:
    def __init__(self, base_url, api_key, timeout=300, tool_name_map=None, default_kb_ids=None):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout
        self.tool_name_map = dict(tool_name_map or {})
        self.default_kb_ids = list(default_kb_ids or [])
        self._http = requests.Session()
        if api_key:
            self._http.headers["X-API-Key"] = api_key

    def health(self):
        resp = requests.get(f"{self.base_url}/health", timeout=10)
        resp.raise_for_status()
        return resp.json()

    def _api(self, method, path, **kwargs):
        resp = self._http.request(
            method, f"{self.base_url}/api/v1{path}", timeout=self.timeout, **kwargs
        )
        if not resp.ok:
            raise WeKnoraAPIError(resp.status_code, resp.text[:500])
        return resp

    def create_session(self, title="weknora-eval"):
        body = self._api("POST", "/sessions", json={"title": title}).json()
        if not body.get("success"):
            raise WeKnoraAPIError(200, json.dumps(body, ensure_ascii=False)[:500])
        return body["data"]["id"]

    def delete_session(self, session_id):
        self._api("DELETE", f"/sessions/{session_id}")

    def load_messages(self, session_id):
        return self._api("GET", f"/messages/{session_id}/load").json()

    def chat(
        self,
        session_id,
        query,
        agent_mode=False,
        agent_id=None,
        knowledge_base_ids=None,
        disable_title=True,
    ):
        body = {"query": query, "disable_title": disable_title, "channel": "api"}
        if knowledge_base_ids:
            body["knowledge_base_ids"] = list(knowledge_base_ids)
        elif self.default_kb_ids and not agent_mode:
            body["knowledge_base_ids"] = list(self.default_kb_ids)
        if agent_mode:
            body["agent_enabled"] = True
            if agent_id:
                body["agent_id"] = agent_id
        result = ChatResult()
        path = "/agent-chat" if agent_mode else "/knowledge-chat"
        with self._http.post(
            f"{self.base_url}/api/v1{path}/{session_id}",
            json=body,
            stream=True,
            timeout=(10, self.timeout),
        ) as resp:
            if not resp.ok:
                raise WeKnoraAPIError(resp.status_code, resp.text[:500])
            for data in self._iter_sse(resp):
                result.raw_events.append(data)
                rt = data.get("response_type")
                content = data.get("content") or ""
                if rt == "answer":
                    result.answer += content
                elif rt == "thinking":
                    result.thinking += content
                elif rt == "references":
                    for ref in data.get("knowledge_references") or []:
                        if isinstance(ref, dict) and ref.get("content"):
                            result.retrieval_context.append(ref["content"])
                elif rt == "tool_call":
                    payload = data.get("data") or {}
                    if payload.get("tool_name"):
                        result.tool_calls.append(
                            {
                                "tool_name": payload.get("tool_name"),
                                "tool_call_id": payload.get("tool_call_id"),
                                "arguments": payload.get("arguments"),
                            }
                        )
                    for call in data.get("tool_calls") or []:
                        result.tool_calls.append(
                            {
                                "tool_name": (call or {}).get("tool_name"),
                                "tool_call_id": (call or {}).get("tool_call_id"),
                                "arguments": (call or {}).get("arguments"),
                            }
                        )
                elif rt == "tool_result":
                    result.tool_results.append(data)
                elif rt == "error":
                    result.error = content or json.dumps(data, ensure_ascii=False)[:500]
                    break
                elif rt in BLOCKING_RESPONSE_TYPES:
                    result.blocked_by = rt
                    break
                if data.get("usage"):
                    result.usage = data["usage"]
                if data.get("finish_reason"):
                    result.finish_reason = data["finish_reason"]
                if rt == "complete":
                    break
        return result

    @staticmethod
    def _iter_sse(resp):
        for raw in resp.iter_lines():
            if not raw:
                continue
            line = raw.decode("utf-8", errors="replace") if isinstance(raw, bytes) else raw
            if not line.startswith("data:"):
                continue
            chunk = line[len("data:"):].strip()
            if not chunk or chunk == "[DONE]":
                continue
            try:
                yield json.loads(chunk)
            except json.JSONDecodeError:
                continue

    def normalize_tool_name(self, actual_name):
        for expected, actual in self.tool_name_map.items():
            if actual == actual_name:
                return expected
        return actual_name

    def tool_sequence(self, result):
        return tuple(
            self.normalize_tool_name(call["tool_name"] or "")
            for call in result.tool_calls
        )
