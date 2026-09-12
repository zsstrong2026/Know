# WeKnora 智能体前端冒烟测试设计文档（v1.0）

> 姊妹篇：《WeKnora智能体CI回归测试方案_v1.1.md》（DeepEval 深度离线回归，独立保留，本方案不依赖它）

## 1. 目的与背景

智能体创建后，创建者目前只能靠人工提问验证其行为是否正常。本方案在 WeKnora 产品内提供**一键冒烟测试**能力：

- 智能体创建后，可从前端**主动触发**测试（列表行操作 + 创建成功通知快捷入口）
- 测试**异步执行**，前端实时查看进度
- 测试结果**存档到数据库**，可随时查看历史与逐用例详情

定位：面向智能体创建者的**即时在线冒烟验证**（1-2 分钟内出结果）；开发者日常/发布前的深度回归继续走 weknora-eval（DeepEval）CI 流程，两者互补。

## 2. 范围与非目标

### 范围（P0）

| 模块 | 内容 |
|---|---|
| 后端引擎 | Go 原生测试执行器：6 条预置冒烟用例，确定性断言 + 可选 AI 评分 |
| 后端 API | 触发 / 历史 / 详情 / 删除 4 个端点 |
| 数据库 | 2 张存档表（postgres + sqlite 双迁移） |
| 前端 | 测试抽屉组件 + 列表行入口 + 创建成功通知快捷按钮 |

### 非目标（后续可扩展）

- 用户自定义测试用例集（本版仅预置用例）
- judge 评分计入通过/失败判定（本版 judge 结果仅展示，不影响结论）
- 独立"评测中心"页面、批量评测、定时评测
- 依赖 Python/DeepEval 运行时

## 3. 总体架构与数据流

```
前端 AgentList / 创建成功通知
   │ POST /api/v1/agents/:id/evaluations          （透传用户 Authorization）
   ▼
后端 AgentEvalService
   ├─ 1. 校验 agent 存在 + 无 running run（互斥）
   ├─ 2. 创建 agent_eval_runs 记录（status=running）──▶ 立即返回 run_id
   └─ 3. goroutine 异步执行：
        逐用例（串行，6 条）：
          a. 创建会话（复用现有 session 创建 API）
          b. HTTP 自调用 POST /api/v1/agent-chat/{session_id}（SSE，黑盒真实）
             请求体不含 knowledge_base_ids（保持智能体自主工具决策路径，
             该要点来自 CI 调试期间的关键发现：传入 KB 会使内置 agent 绕过工具决策）
          c. 解析 SSE：收集 tool_calls / answer / references
          d. 确定性断言（工具链 / 引用数 / 回答非空 / 不报错）
          e. AI 评分（可选）：ModelService.GetChatModel 取 KnowledgeQA 默认模型
          f. 写 agent_eval_case_results
        全部完成 ──▶ 更新 run：status=completed, passed/failed/duration
   ▼
前端抽屉 2s 轮询 GET .../evaluations/:run_id ──▶ running(进度) / completed(详情)
```

关键决策依据：

- **goroutine 而非 asynq**：对齐 `internal/router/sync_task.go` 的同步回退模式，不引入 Redis 强依赖；单 agent 串行 6 用例，资源占用小
- **HTTP 自调用而非进程内直调**：与 CI 方案同构的黑盒验证，走真实鉴权/SSE/会话链路，不耦合 handler 内部逻辑；鉴权复用**触发者的 Authorization 头透传**（测试时长 1-2 分钟，远短于 token 有效期）
- **judge 复用系统模型**：经 `ModelService.GetChatModel`（[internal/types/interfaces/model.go:42](internal/types/interfaces/model.go#L42)）取租户下 `type=KnowledgeQA` 且 `IsDefault` 的模型，零新增配置；judge 失败仅标记该项 error，不影响确定性结论

## 4. 数据模型

### 4.1 表结构

**agent_eval_runs**（一次运行 = 一条存档）

| 列 | 类型 | 说明 |
|---|---|---|
| id | uuid PK | 运行 ID |
| tenant_id | uuid PK(复合) | 租户隔离（对齐 CustomAgent 的 `ID+TenantID` 复合主键模式） |
| agent_id | text | 被测智能体 ID |
| status | text | running / completed / failed |
| total | int | 用例总数（6） |
| passed | int | 通过数（含确定性断言通过；judge 仅记录不判定） |
| failed | int | 失败数 |
| error | text | 运行级错误信息（如 agent 不可达），可空 |
| trigger | text | 触发来源，本版固定 manual |
| duration_ms | int | 总耗时 |
| created_at / updated_at / finished_at | timestamp | |

**agent_eval_case_results**（每用例一条）

| 列 | 类型 | 说明 |
|---|---|---|
| id | uuid PK | |
| run_id | uuid | 关联 run（级联删除） |
| tenant_id | uuid | 租户隔离 |
| case_id | text | smoke-01 … smoke-06 |
| case_title | text | 用例标题（前端直接展示） |
| query | text | 测试问句 |
| status | text | passed / failed |
| assertions | jsonb | `[{name, expected, actual, passed}]` 断言明细 |
| judge | jsonb | `[{metric, score, reason, error}]`，可空 |
| tool_calls | jsonb | `[{tool_name, tool_call_id}]` 实际调用链（按顺序） |
| answer | text | 实际回答（截断至 4000 字符） |
| reference_count | int | 引用数量 |
| error | text | 用例级错误（如 SSE 超时），可空 |
| duration_ms | int | 用例耗时 |
| created_at / updated_at | timestamp | |

索引：`(tenant_id, agent_id, created_at DESC)` 于 runs；`(run_id)` 于 case_results。

### 4.2 迁移文件（两套目录必须同时新增）

| 文件 | 内容要点 |
|---|---|
| `migrations/versioned/000085_agent_eval.up.sql` | `CREATE TABLE IF NOT EXISTS` 两表 + `COMMENT ON COLUMN` + 局部索引（`WHERE deleted_at IS NULL` 不适用则省略，本表不软删）+ 成对 `.down.sql`（DROP TABLE） |
| `migrations/sqlite/000012_agent_eval.up.sql` | 首行注释 `-- Mirrors versioned migration 000085_agent_eval`，镜像同样表结构（jsonb→json，无 COMMENT 语法）+ 成对 `.down.sql` |

> 迁移为 `file://` 文件源（非 embed），postgres 走 `migrations/versioned/`、sqlite 走 `migrations/sqlite/`，由 [internal/database/migration.go:106-109](internal/database/migration.go#L106-L109) 按 DSN 前缀选择。

## 5. 后端 API 规格

路由挂载于 [internal/router/routes_agent.go](internal/router/routes_agent.go)，权限复用现有守卫：

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| POST | `/api/v1/agents/:id/evaluations` | `OwnedAgentOrAdmin`（与 copy 等变更操作一致） | 触发测试 |
| GET | `/api/v1/agents/:id/evaluations` | Viewer+（该 agent 可见即可见） | 运行历史（倒序，默认前 20 条） |
| GET | `/api/v1/agents/:id/evaluations/:run_id` | Viewer+ | 单次运行详情（含全部用例结果） |
| DELETE | `/api/v1/agents/:id/evaluations/:run_id` | `OwnedAgentOrAdmin` | 删除存档（级联删 case results） |

### 5.1 POST 触发

请求：无 body（用例集固定为预置冒烟集）。

响应 `202`：

```json
{
  "run_id": "6f1c2a3e-...",
  "status": "running",
  "total": 6
}
```

冲突 `409`（同 agent 已有 running run）：

```json
{
  "code": 409,
  "message": "该智能体已有正在运行的测试",
  "run_id": "9a0b..."
}
```

### 5.2 GET 详情（前端轮询的主要接口）

响应 `200`（running 中，用例逐个补齐）：

```json
{
  "run_id": "6f1c2a3e-...",
  "agent_id": "agt-xxx",
  "status": "running",
  "total": 6,
  "passed": 0,
  "failed": 0,
  "duration_ms": 15300,
  "cases": [
    {
      "case_id": "smoke-01",
      "case_title": "闲聊不打扰知识库",
      "status": "passed",
      "assertions": [
        { "name": "forbid_tool:knowledge_search", "expected": "不调用", "actual": "未调用", "passed": true },
        { "name": "answer_not_empty", "expected": "非空", "actual": "28 字", "passed": true }
      ],
      "judge": [
        { "metric": "relevancy", "score": 0.9, "reason": "回答与问候语义匹配" }
      ],
      "tool_calls": [],
      "answer": "你好！我是智能助手…",
      "reference_count": 0,
      "duration_ms": 4200
    }
  ]
}
```

### 5.3 响应码约定

| 码 | 场景 |
|---|---|
| 404 | agent 或 run 不存在 / 跨租户访问 |
| 409 | 重复触发（已有 running run） |
| 202 | 触发成功（异步） |

## 6. 测试引擎设计

### 6.1 预置冒烟用例集

用例只断言**结构性行为**，不绑定任何知识库内容，通用适配任意智能体：

| case_id | 标题 | query | 断言 |
|---|---|---|---|
| smoke-01 | 闲聊不打扰知识库 | 你好，请介绍一下你自己 | `forbid_tool:knowledge_search`；`answer_not_empty` |
| smoke-02 | 知识问题走检索 | 请根据知识库资料回答：这个系统支持哪些文档格式？ | `expect_tool:knowledge_search`；`min_references:1`；`answer_not_empty` |
| smoke-03 | 复杂问题走理解链 | 帮我分析一下：上传文档后检索结果的排序依据是什么？和直接提问相比有什么区别？ | `expect_tool:query_understand`；`answer_not_empty` |
| smoke-04 | 超纲问题不编造 | 2026年世界杯决赛的比分是多少？ | `no_error`；`answer_not_empty` |
| smoke-05 | 模糊问题正常应答 | 嗯……就是说，那个东西怎么样？ | `no_error`；`answer_not_empty` |
| smoke-06 | 中文知识问题 | 知识库里关于系统部署的内容，应该怎么配置环境变量？ | `expect_tool:knowledge_search`；`answer_not_empty` |

> 用例集定义在 Go 常量/切片中（`internal/application/service/agent_eval_cases.go`），便于后续版本扩展为用户自定义用例。问句措辞实施时可按通用性微调，断言结构不变。
>
> 说明：仅 smoke-02 设置 `min_references:1`（其问句明确索取"资料"）；smoke-06 虽期望检索动作，但不设引用数下限——预置用例必须不依赖任何知识库的实际内容覆盖度（目标知识库可能没有部署相关文档，引用合法为 0）。

### 6.2 执行流程

每用例执行步骤（串行）：

1. **创建会话**：调用现有 session 创建 API（端点与调用序列对齐 `weknora-eval/weknora_client/client.py` 已验证流程），取得 session_id
2. **发起对话**：`POST /api/v1/agent-chat/{session_id}`，SSE 流式（`Content-Type: text/event-stream`）
   - 头：`Authorization: <触发者凭证透传>`、`X-Tenant-ID: <触发者租户>`
   - 体：`{"query": <用例问句>, "agent_enabled": true, "agent_id": <被测agent>, "channel": "api", "disable_title": true}`
   - **不传 `knowledge_base_ids`**（关键：传 KB 会切换内置 agent 至快速回答路径、跳过工具决策，已在 CI 调试中实测确认，见 [internal/models/chat/openai_stream.go:556-563](internal/models/chat/openai_stream.go#L556-L563) 的事件结构）
3. **SSE 解析**：逐行解析 `data:` JSON，按 `response_type` 分派（事件全集与 `weknora-eval/weknora_client/client.py` 的 14 类 RESPONSE_TYPES 对齐）：
   - `tool_call`：读嵌套结构 `data.tool_name` / `data.tool_call_id` / `data.arguments`（`tool_calls` 数组变体仅存在于 ollama 路径，同样兼容）
   - `references`：累计引用数
   - `answer`：拼接回答文本
   - `complete`：流正常结束
   - `error`：记录错误并终止本用例
   - 超时：单用例 SSE 读取上限 120s，超时按 `no_error` 断言失败处理
4. **确定性断言**（全部通过 → passed）：

| 断言名 | 规则 |
|---|---|
| `expect_tool:<name>` | tool_calls 序列中包含该工具名（`tool_name` 经小写归一化，语义对齐 CI 的 `tool_name_map.yaml` 恒等映射） |
| `forbid_tool:<name>` | tool_calls 序列中不包含该工具名 |
| `min_references:<n>` | reference_count ≥ n |
| `answer_not_empty` | answer 非空白字符 |
| `no_error` | SSE 流正常 complete 且无 error 事件 |

5. **AI 评分（可选，确定性断言之后）**：

| 用例 | judge 指标 | 评分问题 |
|---|---|---|
| smoke-02/03/06 | relevancy | 回答是否切题、且与检索内容一致 |
| smoke-04 | honesty | 面对无答案问题，是否诚实说明信息不足（而非编造事实） |

- 模型获取：`ModelService.GetChatModel(ctx, defaultKnowledgeQAModelID)`；若租户无 KnowledgeQA 模型或调用失败 → judge 数组写入 `{metric, error}`，**不产生任何失败判定**
- 单次调用（无投票），输出要求 JSON：`{"score": 0-1, "reason": "一句话理由"}`，解析失败视为 judge error

### 6.3 结果判定与存档

- 用例 passed = 全部确定性断言通过（judge 不参与判定，仅展示；字段设计已预留后续升级为门槛的空间）
- run completed = 全部用例执行完毕；run failed = 运行级错误（如会话创建持续失败）导致中途中止，已完成的用例结果保留
- 存档保留：每 agent 仅保留最近 **50** 条 run，写入新 run 后异步清理超出部分（级联删 case results）

## 7. 前端设计

技术栈沿用现有：Vue 3 + TDesign + [utils/request.ts](frontend/src/utils/request.ts) 封装（`postChat` 已支持 SSE，本功能仅用普通 GET/POST 轮询）。

### 7.1 新增组件 `frontend/src/views/agent/components/AgentEvalDrawer.vue`

`t-drawer`（width ≥ 720），三段式布局：

1. **顶部操作区**：「运行测试」主按钮 + 运行摘要（最近一次：状态 tag / 通过 x/6 / 耗时）
2. **运行历史列表**（t-table）：时间、状态、通过率、操作（查看 / 删除）；点击行加载详情
3. **详情区**：
   - 运行中：t-progress（已完成用例数/6）+ 用例逐行实时状态（spinner → passed/failed tag）
   - 已完成：用例可展开行（t-table 展开态）→ 断言明细（t-table：断言名/期望/实际/结果）、judge 评分卡（指标、score、reason；error 时显示错误）、实际回答（折叠文本）、工具调用链（t-tag 序列）

状态与轮询：

- 打开抽屉即拉取历史列表；触发测试后进入详情视图，**2s 间隔轮询** GET 详情直至 `completed/failed`；组件卸载/抽屉关闭时清理 timer（对齐 [RuntimeQueues.vue:580](frontend/src/views/system/RuntimeQueues.vue#L580) 的既有轮询模式）
- API 封装新增 `frontend/src/api/agent/index.ts`：`runAgentEval / listAgentEvals / getAgentEvalRun / deleteAgentEvalRun`

### 7.2 AgentList.vue 接入（轻改）

- 行操作下拉（现有操作区）新增「运行测试」项 → 打开抽屉并自动触发 POST
- 409 冲突 → t-message 提示"已有正在运行的测试"并直接打开抽屉查看进度

### 7.3 创建成功通知快捷入口（轻改）

[AgentEditorModal.vue](frontend/src/views/agent/AgentEditorModal.vue) 创建成功的 `emit('success')` 后，[AgentList.vue:1547-1553](frontend/src/views/agent/AgentList.vue#L1547-L1553) 的 `handleEditorSuccess` 处弹出 `DialogPlugin.confirm`（项目内无 t-notification 使用先例，MessagePlugin 不支持按钮，故用 DialogPlugin）："智能体创建成功，是否立即运行冒烟测试？" → 确认后关闭编辑器并打开新 agent 的测试抽屉、自动触发。

## 8. 错误处理与边界

| 场景 | 处理 |
|---|---|
| 重复触发（同 agent running） | 409 + 前端提示并打开抽屉查看进行中的运行 |
| 会话创建失败 | 重试 1 次；仍失败 → run failed，error 记录原因 |
| agent 无可用模型（对话侧报错） | 该用例 failed（no_error 不通过），继续后续用例 |
| SSE 超时（120s） | 用例 failed，继续后续用例 |
| judge 失败/无模型 | judge 项记 error，确定性结论不变 |
| 服务重启时 run 仍为 running | 启动时将孤儿 running run 置为 failed（error="服务重启中断"），幂等安全 |
| 触发者 token 在执行中过期 | 自调用 401 → 剩余用例 failed，run failed（概率极低，接受） |
| answer 过长 | 存档截断至 4000 字符（详情可读性 + 存储保护） |

## 9. 非功能考量

- **租户隔离**：所有查询强制 `tenant_id` 条件（复合主键模式，对齐 CustomAgent）；跨租户访问 run_id 返回 404
- **并发**：每 agent 互斥（running 唯一）；不同 agent 可并行，各自 goroutine 串行执行
- **安全**：不落盘任何凭证；自调用凭证仅存在于 goroutine 内存；answer 可能包含用户知识库内容，存档随 run 删除而删除
- **性能**：单 run 6 用例 × (SSE 对话 10-30s + 可选 judge 2-5s) ≈ 1-3 分钟；对主服务的影响等同 6 次普通对话请求

## 10. 测试与验证策略

| 层 | 方式 |
|---|---|
| Go 单测 | 断言函数纯逻辑（expect/forbid/min_references/answer_not_empty/no_error）；SSE 事件解析（tool_call 嵌套结构、complete 终止、error 中断）；judge 输出 JSON 解析（正常/非法/超时） |
| API 单测 | 触发 202 / 重复 409 / 跨租户 404 / 详情结构 |
| 集成验证（真实服务） | ① 前端触发 → 轮询 running→completed ② 详情逐用例查看（含 judge）③ 删除存档 ④ **重启服务后历史仍在**（DB 持久化验证）⑤ 孤儿 running 恢复验证 |
| 前端验证 | 抽屉三段布局渲染、轮询清理（无内存泄漏）、409 提示、创建成功通知「立即测试」链路 |

## 11. 交付物清单（预估改动）

| 类型 | 文件 | 说明 |
|---|---|---|
| 迁移 | migrations/versioned/000085_agent_eval.{up,down}.sql | postgres 建表 |
| 迁移 | migrations/sqlite/000012_agent_eval.{up,down}.sql | sqlite 镜像 |
| 类型 | internal/types/agent_eval.go | Run / CaseResult / 断言与 judge 结构体 |
| 存储 | internal/application/repository/agent_eval.go | 两表 CRUD + 孤儿 run 清理（对齐现有 repository 组织） |
| 服务 | internal/application/service/agent_eval.go | 触发/查询/删除/异步执行器 |
| 用例 | internal/application/service/agent_eval_cases.go | 6 条预置用例定义 + 断言规则 |
| 路由 | internal/router/routes_agent.go | 4 个端点注册 |
| 前端 API | frontend/src/api/agent/index.ts | 4 个封装函数 |
| 前端组件 | frontend/src/views/agent/components/AgentEvalDrawer.vue | 测试抽屉 |
| 前端接入 | frontend/src/views/agent/AgentList.vue | 行操作 + 通知快捷入口 |
| 文案 | frontend/src/locales/ 相关语言文件 | 抽屉与断言名 i18n |
