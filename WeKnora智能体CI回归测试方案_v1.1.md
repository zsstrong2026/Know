# WeKnora 智能体 CI 回归测试方案

> 版本：v1.2（2026-09-11）
> 适用对象：WeKnora 0.8.0（本地部署，`D:\wiki\WeKnora-0.8.0`）
> 选型结论：**DeepEval 为主 + promptfoo 红队补充（可选二期）**
> v1.1 修订：补充 MCP 工具审批/OAuth 阻塞对策（§7.1/§10）、勘误 ConversationalTestCase 类名与 deepeval 版本约束（§5/§11）、澄清工具序列期望/实际来源并新增名称映射（§5/§6）
> v1.2 修订：并入评审 6 条次要建议——契约探针 fail-fast 与限流并发策略（§8）、runs 奇数次与阈值基线校准（§5）、runs 示例改 3（§6）、retrieval_context 映射与空引用处理（§7.1）、judge 自评偏好标注（§7.3）

---

## 1. 背景与目标

WeKnora 0.8.0 内置 ReAct Agent（检索、MCP 工具、技能沙箱、记忆等能力），上游社区版本迭代频繁。
本项目目标：为 Agent 建立一套 **黑盒 CI 回归测试体系**，在上游版本升级后能快速、量化地回答：

1. Agent 的问答质量有没有退化？
2. 工具调用行为（选对工具、参数正确）有没有退化？
3. 多轮对话一致性、权限边界、拒答策略有没有被破坏？

**非目标**：不测 WeKnora 内部 Go 代码（上游自带单测会随版本变化），不做白盒覆盖率。

---

## 2. 设计原则（升级隔离的三条铁律）

| # | 原则 | 说明 |
|---|------|------|
| P1 | **零侵入** | 不修改 WeKnora 源码、不往其目录写任何文件；评测模块放在 `D:\wiki\weknora-eval\`（伴生目录） |
| P2 | **单点耦合** | 全部对 WeKnora 的依赖收敛到 `weknora_client/` 适配层（约 200 行）；升级只需补丁这一层 |
| P3 | **契约先行** | 契约探针测试（test_contract.py）永远第一个跑：升级后 10 秒内判定 API 是否还兼容，失败信息直接指向需要修补的端点 |

耦合面审计结论（基于 0.8.0 源码）：

- REST API `/api/v1`：sessions / messages / SSE 流式 —— 主驱动面
- Scoped API Key（principal 模型）：官方程序化集成入口，评测用独立 key，与人工使用解耦
- Langfuse（OTLP/OTel）：工具调用轨迹的**只读**数据源，不改 WeKnora 代码即可取数
- `weknora` CLI：NDJSON 契约版本风险高于 REST，**不采用**

---

## 3. 总体架构

```
┌─────────────────────────────────────────────────────────────┐
│                    weknora-eval（独立模块）                   │
│                                                             │
│  datasets/            tests/                reports/        │
│  测试数据集      →     pytest 用例层       →    报告/趋势      │
│  (YAML, 版本化)       (指标断言+阈值)            │           │
│                          │                     │           │
│  ┌───────────────────────┼─────────────────────┴───────┐   │
│  │        weknora_client/ 适配层（唯一耦合点，P2）        │   │
│  │  client.py   认证/会话/SSE 聚合/历史消息               │   │
│  │  traces.py   Langfuse 痕迹导出 → 工具调用序列          │   │
│  └──────────┬──────────────────────────┬────────────────┘   │
└─────────────┼──────────────────────────┼────────────────────┘
              │ HTTPS (Scoped API Key)   │ OTLP 导出(只读)
              ▼                          ▼
   ┌──────────────────┐        ┌──────────────────┐
   │  WeKnora 0.8.x   │        │     Langfuse     │
   │  REST /api/v1    │  ───►  │  (自托管/已有)    │
   │  ReAct Agent     │ 痕迹上报 │                  │
   └──────────────────┘        └──────────────────┘
        ▲ 只升级这里，评测模块零改动
```

要点：

- WeKnora 升级（0.8.0 → 0.9.x）只影响 `weknora_client/`，datasets 与 tests 原样复用
- 评测模块自身的依赖（deepeval、pytest）与 WeKnora 的 Go/Node 技术栈零交集
- 报告按时间序列落盘，形成跨版本的"质量基线"，升级前后对比一目了然

---

## 4. 目录结构

```
D:\wiki\weknora-eval\
├── README.md
├── pyproject.toml              # 或 requirements.txt：deepeval, httpx, httpx-sse, pyyaml, python-dotenv
├── .env.example                # WEKNORA_BASE_URL / WEKNORA_API_KEY / JUDGE_MODEL 配置样例
├── pytest.ini                  # marker 分层：contract / qa / tools / multi_turn / permissions
├── weknora_client\             # 适配层（P2 单点耦合）
│   ├── __init__.py
│   ├── client.py               # create_session / stream_chat(SSE聚合) / load_messages / stop
│   ├── traces.py               # Langfuse 痕迹导出 → List[ToolCall(name, args)]
│   └── exceptions.py           # ContractError 等明确异常
├── datasets\                   # 数据集与代码分离，纯数据、可版本化
│   ├── rag_qa.yaml             # 检索问答用例
│   ├── agent_tools.yaml        # 工具调用用例
│   ├── multi_turn.yaml         # 多轮对话用例
│   └── permissions.yaml        # 越权/拒答用例
├── tests\
│   ├── conftest.py             # fixtures：客户端、数据集加载、阈值配置
│   ├── test_contract.py        # 契约探针（marker: contract）
│   ├── test_rag_qa.py          # Faithfulness / AnswerRelevancy（marker: qa）
│   ├── test_agent_tools.py     # TaskCompletion + ToolCorrectness（marker: tools）
│   ├── test_multi_turn.py      # 多轮一致性（ConversationalTestCase）
│   └── test_permissions.py     # 硬断言：状态码/拒答关键词（marker: permissions）
├── ci\
│   ├── run_eval.bat            # Windows 本地一键执行
│   └── github-actions.yml      # CI 工作流样例（可选）
└── reports\                    # 结果 JSON + 汇总，按日期归档，git ignore
```

---

## 5. 评测指标体系

| 层级 | 用例类别 | DeepEval 指标 | 通过策略 |
|------|---------|--------------|---------|
| 契约层 | API 兼容探针 | —（硬断言：端点可达 + SSE `response_type` 枚举兼容，含 `tool_approval_required` / `mcp_oauth_required` 事件处理策略） | 全部通过，否则 fail-fast 并报告损坏端点 |
| 问答层 | RAG 检索问答 | Faithfulness（忠实度）+ AnswerRelevancy（相关性） | 双指标阈值 ≥ 0.7（可按用例覆盖） |
| 工具层 | ReAct 工具调用 | TaskCompletionMetric + ToolCorrectnessMetric（期望序列 = 数据集 YAML 人工定义；实际序列 = SSE `tool_call` 事件 / Langfuse 痕迹捕获，经名称映射表对齐） | 任务完成 ≥ 0.7；工具序列可配置"精确/子集/忽略参数"三种模式 |
| 对话层 | 多轮一致性 | 多轮 ConversationalTestCase（整体评分）；逐轮需自行拆为单轮 LLMTestCase 评 | 整体阈值 ≥ 0.7，逐轮仅记录不阻断 |
| 边界层 | 权限/拒答 | —（硬断言：状态码、拒答关键词、信息不泄露） | 精确断言，0 容忍 |
| 红队层（二期） | 注入/诱导 | promptfoo redteam | 见 §9 |

**非确定性对策**（ReAct 输出天然波动）：

- 指标一律用 0~1 阈值评分，不用精确文本匹配
- 关键用例支持 `runs: N` 配置，取均值或多数通过；N 建议用奇数（如 3），偶数次"多数通过"会平票
- 阈值统一收敛在 `conftest.py` 的配置块，便于按基线调整
- 阈值 0.7 为初始拍定值：一期先全量跑 3 轮基线，观察指标分布后再正式校准阈值（基线结果由 §8 趋势报告承载）

---

## 6. 数据集规范（datasets/*.yaml）

每个用例独立成条，与代码解耦，示例：

```yaml
# datasets/agent_tools.yaml
suite: agent_tools
description: ReAct Agent 工具调用回归
cases:
  - id: tool_search_kb_001
    name: 知识库检索必走检索工具
    kb_id: kb-00000001
    input: "2024年年报里营收是多少？"
    expect:
      answer_contains: ["营收"]        # 软断言（加权）
      answer_must_not_contain: ["抱歉，无法回答"]
      tools_any: ["search_knowledge_base", "retrieval"]   # 子集匹配
      tools_forbidden: ["web_search"]                     # 明确不应外搜
    runs: 3
    threshold: 0.7
```

- `expect` 内的软断言映射为 DeepEval 自定义/LLM-judge 检查
- 工具序列期望支持三种模式：`exact` / `subset(tools_any)` / `forbidden(tools_forbidden)`
- 数据集更新不需要改任何测试代码
- 工具名映射：YAML 中的工具别名（如 `search_knowledge_base`）与 SSE 事件 / Langfuse span 中的内部注册名可能不一致，适配层维护一张「别名 → 内部名」映射表，期望与实际序列对齐前先过该映射

---

## 7. 关键实现要点

### 7.1 SSE 流式聚合（client.py）

WeKnora 聊天走 SSE 流。适配层职责：

1. `create_session` → 拿 session_id
2. 发消息并消费 SSE 事件流，聚合出：最终答案文本、引用来源、（若事件流携带）工具调用进度事件；`references` 事件映射为 Faithfulness 指标所需的 `retrieval_context`，无引用场景按空值处理（该类用例跳过 Faithfulness、只评 AnswerRelevancy）
3. `load_messages` 做对账（SSE 聚合结果 vs 落库历史），确保评测对象是用户真正收到的内容
4. 处理 `tool_approval_required` / `mcp_oauth_required` 事件：MCP 工具审批默认 fail-close 且会挂起等待人工确认，若不处理将阻塞评测。对策三选一（按用例/环境配置）：
   - 评测用例优先使用不触发审批的内置工具
   - 适配层捕获审批事件后调用审批接口自动放行（仅限评测专用租户/Scoped Key）
   - 评测涉及的 MCP 服务在环境配置中设为免审批

### 7.2 工具轨迹两条捕获路径

| 路径 | 来源 | 优先级 |
|------|------|--------|
| A：SSE 事件流 | agent_stream_handler 推送的工具进度事件 | 首选（零额外部署） |
| B：Langfuse 导出 | WeKnora 内置 Langfuse 上报的完整 span 树 | 备选/兜底（事件流不含工具明细时启用） |

### 7.3 Judge 模型省钱

DeepEval 的 LLM-as-judge 支持任意 OpenAI 兼容端点，指向已配置的 DeepSeek/Qwen（OpenAI 兼容 base_url），judge 成本可控。估算：20 用例 × 3 指标 ≈ 每轮全量 < ¥2（DeepSeek 价）。

注意自评偏好：若被测模型与 judge 同源（同为 DeepSeek/Qwen），存在 self-preference 偏差；报告中标注 judge 型号与版本，跨版本对比时保持 judge 不变，解读分数时将该偏差计入。

---

## 8. CI 集成

| 阶段 | 触发 | 范围 | 时长预估 |
|------|------|------|---------|
| 本地开发 | 手动 `run_eval.bat` | contract + 指定 marker | 1~10 分钟 |
| 夜间基线 | 计划任务 / WorkBuddy 自动化 02:00 | 全量 suites | 20~40 分钟 |
| 升级验证 | 人工触发（升级后） | contract 先行 → 全量 | 探针 10 秒，全量同上 |
| 发布前门禁（二期） | GitHub Actions（若仓库上云） | contract + permissions + tools | < 15 分钟 |

执行约定：

- 契约探针 fail-fast：`run_eval.bat` 先用 `-m contract -x` 跑探针，失败立即终止并输出损坏端点；探针通过后再跑全量（`-m "not contract"`）
- 并发与限流：WeKnora 有内置限流，夜间全量默认串行（或并发 ≤ 2），避免触发限流造成误报

报告产物：JSON 结果 + Markdown 汇总（通过率、各指标均值、与上一基线的差值），红色高亮退化超过 0.1 的用例。

---

## 9. 分期实施计划

### 一期（MVP，1.5~2 天）
1. `weknora_client/` 适配层 + 契约探针（0.5 天）
2. RAG 问答 5~8 例 + Faithfulness/AnswerRelevancy 跑通（0.5 天）
3. 工具层用例 5~8 例 + SSE 路径 A 捕获 + ToolCorrectness（0.5 天）
4. 报告产出 + `run_eval.bat`（0.25 天）

### 二期（1 天）
5. 多轮一致性 + 权限边界用例
6. Langfuse 路径 B 痕迹导出（如路径 A 不够）
7. 夜间基线定时化 + 跨版本趋势报告

### 三期（可选）
8. promptfoo redteam 红队子模块（提示注入、越权诱导）
9. 接入 GitHub Actions 发布门禁

---

## 10. 风险与对策

| 风险 | 概率 | 对策 |
|------|------|------|
| 上游升级改了 SSE 事件格式/端点 | 中 | 契约探针 fail-fast 定位到端点；适配层单独打补丁（半天内） |
| SSE 事件流不含完整工具调用明细 | 中 | 启用 Langfuse 路径 B；两者都不行则降级为"纯答案质量"评测 |
| 用例触发 MCP 工具审批/OAuth 授权，SSE 流挂起 | 中 | 按 §7.1 第 4 条处理（免审批工具 / 自动放行 / 环境免审批）；契约探针覆盖该事件类型 |
| ReAct 输出波动导致误报 | 高 | 阈值评分 + 多次取均值 + 用例级 `runs` 配置 |
| 评测 KB 被污染（Agent 写操作） | 低 | 评测用专用只读 KB + 只授 Viewer/Contributor 权限的 Scoped Key |
| judge 模型评分漂移 | 中 | judge 模型版本固定写入 .env；基线重测时保持 judge 不变 |
| 本机 Python 环境污染 | — | 全部装进受管 venv（`~/.workbuddy/binaries/python/envs/default`） |

---

## 11. 环境依赖清单

| 依赖 | 用途 | 备注 |
|------|------|------|
| Python 3.13（受管 venv） | 评测运行时 | 已有 |
| deepeval（锁定实际安装版本） | 指标库 + pytest 集成 | `pip install deepeval` 后以 pip 实际解析版本为准，写入锁定文件 |
| httpx + httpx-sse | REST/SSE 客户端 | 轻量 |
| WeKnora Scoped API Key | 认证 | 管理台生成，仅授会话/聊天能力 |
| Langfuse（已有） | 路径 B 痕迹 | 可选项，不强制 |
| DeepSeek/Qwen API | LLM-as-judge | OpenAI 兼容端点 |
| promptfoo（三期可选） | 红队 | `npm install -g promptfoo` |

---

## 12. 验收标准

1. **升级隔离验证**：模拟一次 WeKnora 重启/升级，契约探针先行给出明确判定，其余模块零改动
2. **全量回归**：20+ 用例一键跑通，产出含通过率与指标均值的报告
3. **退化可感知**：人为降低某用例期望（或换弱模型），报告能标红对应退化项
4. **成本可控**：单轮全量评测 judge 成本 < ¥5
