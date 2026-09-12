class WeKnoraEvalError(Exception):
    """weknora-eval 模块基础异常"""


class WeKnoraAPIError(WeKnoraEvalError):
    """REST 调用失败（非 2xx 或响应封套异常）"""

    def __init__(self, status_code, message=""):
        self.status_code = status_code
        self.message = message
        super().__init__(f"[{status_code}] {message}")


class ContractViolation(WeKnoraEvalError):
    """服务端行为与钉死的契约不符（探针专用）"""


class StreamBlocked(WeKnoraEvalError):
    """SSE 流被审批/OAuth 事件暂停（CI 阻断场景，v1.2 §7.1）"""

    def __init__(self, response_type, payload=None):
        self.response_type = response_type
        self.payload = payload or {}
        super().__init__(f"stream blocked by {response_type}, needs human action")
