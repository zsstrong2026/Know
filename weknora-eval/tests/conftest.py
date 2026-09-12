import os
import sys
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parent.parent
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from weknora_client import WeKnoraClient  # noqa: E402


def _load_env_file():
    env_path = ROOT / ".env"
    if not env_path.exists():
        return
    for line in env_path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, value = line.partition("=")
        os.environ.setdefault(key.strip(), value.strip().strip("'").strip('"'))


_load_env_file()


def load_tool_name_map():
    path = ROOT / "datasets" / "tool_name_map.yaml"
    if not path.exists():
        return {}
    return yaml.safe_load(path.read_text(encoding="utf-8")) or {}


def load_cases(filename):
    path = ROOT / "datasets" / filename
    if not path.exists():
        raise FileNotFoundError(f"dataset not found: {path}")
    data = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
    return [case for case in (data.get("cases") or []) if not case.get("disabled")]


def env_str(name):
    return os.environ.get(name) or ""


def env_list(name):
    return [item.strip() for item in env_str(name).split(",") if item.strip()]


@pytest.fixture(scope="session")
def base_url():
    url = env_str("WEKNORA_BASE_URL")
    if not url:
        pytest.skip("缺少 WEKNORA_BASE_URL（复制 .env.example 为 .env 配置）")
    return url.rstrip("/")


@pytest.fixture(scope="session")
def weknora(base_url):
    api_key = env_str("WEKNORA_API_KEY")
    if not api_key:
        pytest.skip("缺少 WEKNORA_API_KEY")
    return WeKnoraClient(
        base_url,
        api_key,
        tool_name_map=load_tool_name_map(),
        default_kb_ids=env_list("WEKNORA_KB_IDS"),
    )


@pytest.fixture(scope="session")
def require_reachable(weknora):
    try:
        weknora.health()
    except Exception as exc:
        pytest.fail(f"WeKnora 不可达，请先通过契约探针（pytest -m contract -x）：{exc}")


@pytest.fixture()
def eval_session(weknora, require_reachable):
    session_id = weknora.create_session(title="weknora-eval")
    yield session_id
    try:
        weknora.delete_session(session_id)
    except Exception:
        pass


@pytest.fixture(scope="session")
def judge_ready():
    if not env_str("OPENAI_API_KEY"):
        pytest.skip("未配置 judge 模型（OPENAI_API_KEY），跳过 LLM 评估指标")


def majority_assert(metric, test_case, runs=3):
    scores = []
    errors = []
    for _ in range(runs):
        try:
            metric.measure(test_case)
            scores.append(metric.score)
        except Exception as exc:
            errors.append(f"{type(exc).__name__}: {exc}")
    if not scores:
        pytest.fail(
            f"全部 {runs} 次 judge 调用均失败（如 invalid JSON / 超时）：{errors}"
        )
    scores.sort()
    median = scores[len(scores) // 2]
    reason = getattr(metric, "reason", "")
    assert median >= metric.threshold, (
        f"{type(metric).__name__} 中位数得分 {median} 低于阈值 {metric.threshold}；"
        f"各次成功得分 {scores}（失败 {len(errors)} 次）；judge 理由：{reason}"
    )
