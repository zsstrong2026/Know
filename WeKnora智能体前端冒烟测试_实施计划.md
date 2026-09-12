# WeKnora 智能体前端冒烟测试 — 实施计划

> 规格来源：《WeKnora智能体前端冒烟测试设计_v1.0.md》（已批准，含 2 处修正）。本文档只记录执行顺序、文件锚点与关键契约，不重复规格细节。

## 一、目的

按已批准设计文档，实现"前端触发的智能体冒烟测试"后端引擎（Go 原生）与前端展示（TDesign 抽屉）：6 个预置用例经 HTTP 自调用 agent-chat SSE 通道执行，确定性断言 + AI 评分（仅展示），结果落库存档可查。独立于 weknora-eval（DeepEval CI）链路，不改动既有代码行为。

## 二、事实核查结论（已全部验证）

| 事项 | 结论 | 位置 |
|---|---|---|
| agent-chat 注册 | `POST /api/v1/agent-chat/:session_id` → `session.Handler.AgentQA`（Viewer + apiKeyChat） | internal/router/routes_chat.go:112-115 |
| 自调用端口 | dig 注入 `*config.Config`（同 agent_service.go:118 模式），取 `cfg.Server.Port` | internal/config/config.go:156-162 |
| 409/404 裸 JSON | 错误中间件仅在 `len(c.Errors)>0` 时介入 → eval 端点裸 `c.JSON` 直通且可精确返回 `{"code":409,...}` | internal/middleware/error_handler.go:18-33 |
| AppError | `Details` 为 `any`；`Code` 是 1005 式 ErrorCode ≠ HTTP 409 → eval 端点不走 `c.Error` | internal/errors/errors.go |
| judge 模型 | TriggerRun（请求 ctx）内解析：ListModels→Type==KnowledgeQA→IsDefault 优先→回退首个→`GetChatModel(ctx, id)` 返回 `chat.Chat`；goroutine 用 `context.Background()` | internal/types/interfaces/model.go |
| 租户提取 | `types.TenantIDFromContext(ctx) (uint64, bool)` | internal/types/context_helpers.go:25-28 |
| trigger 保留字 | 列名 `eval_trigger` + gorm tag `column:eval_trigger` | 设计 §4.1 |
| 会话契约 | POST /sessions body `{"title"}` → `{"success":true,"data":{"id":...}}`（必须检查 success）；chat body `{"query","disable_title":true,"channel":"api","agent_enabled":true,"agent_id"}`（不带 knowledge_base_ids） | weknora-eval/weknora_client/client.py:40-179 |
| SSE 分发 | 仅 `response_type=="answer"` 累加 content；references 数 `knowledge_references[]` 中 content 非空项；tool_call 读嵌套 `data.tool_name` + 顶层 `tool_calls[]` 回退；error/blocking/complete 即断 | client.py:104-147 |
| 容器锚点 | repo ~L168、service ~L242、handler ~L407、Invoke 先例 L421/L444 | internal/container/container.go |
| 前端 API 模式 | `get/post/put/del` 泛型；错误 reject `{status,message,...data}` → 409 可直接读 `run_id` | frontend/src/utils/request.ts:241-245 |
| AgentList 锚点 | 卡片菜单 L206-222 / 列表菜单 L399-429 / 弹窗挂载 L803-807 / handleEditorSuccess L1547 / canManageAgent L1323 / imports L815-840 | frontend/src/views/agent/AgentList.vue |
| 语言包 | zh-CN.ts / en-US.ts / ko-KR.ts / ru-RU.ts（4 个） | frontend/src/i18n/locales/ |
| 迁移编号 | versioned 最新 000084 → 新增 000085；sqlite 最新 000011 → 新增 000012；sqlite 首行 `-- Mirrors versioned migration ...` 例外 | migrations/ |
| Go 依赖 | gorm sqlite driver v1.6.0、testify v1.11.1、google/uuid v1.6.0 均已在 go.mod；module `github.com/Tencent/WeKnora` | go.mod |

## 三、任务分解（t1–t12）

### t1 迁移 SQL ×4（新文件）
- `migrations/versioned/000085_agent_eval.up.sql`：`agent_eval_runs`（id/tenant_id 复合 PK、agent_id、status、total、passed、failed、error、eval_trigger、duration_ms、created_at/updated_at/finished_at）+ `agent_eval_case_results`（id uuid PK、run_id、tenant_id、case_id、case_title、query、status、assertions/judge/tool_calls JSONB、answer、reference_count、error、duration_ms、created_at/updated_at）+ 索引 `(tenant_id, agent_id, created_at DESC)`、`(run_id)`
- `.down.sql`：DROP 两个表
- `migrations/sqlite/000012_agent_eval.{up,down}.sql`：镜像（jsonb→TEXT）

### t2 types + 接口（新文件）
- `internal/types/agent_eval.go`：`AgentEvalRun` / `AgentEvalCaseResult` 复合主键 tag（仿 custom_agent.go）；`AgentEvalAssertionList` / `AgentEvalJudgeList` / `AgentEvalToolCallList` 三个 JSON 包装（Value/Scan）；状态常量 running/completed/failed、passed/failed、trigger=manual
- `internal/types/interfaces/agent_eval.go`：`AgentEvalRepository`（CreateRun/UpdateRun/GetRun/FindRunningByAgent/ListRunsByAgent/ListRunningRuns/DeleteRun/CreateCaseResult/ListCaseResultsByRun）+ `AgentEvalService`（TriggerRun/ListRuns/GetRunDetail/DeleteRun/RecoverOrphanRuns）+ `AgentEvalCredentials{Authorization, XAPIKey, XTenantID string}`

### t3 repository（新文件 + 测试）
- `internal/application/repository/agent_eval.go`：`ErrAgentEvalRunNotFound` 哨兵；WithContext；删除先删 case_results 再删 run；GetRun 按 `(id, tenant_id)`；ListRunsByAgent 按 created_at DESC
- `internal/application/repository/agent_eval_test.go`：sqlite 内存库 CRUD + 孤儿恢复 + 级联删除

### t4 用例与断言（TDD，新文件）
- `internal/application/service/agent_eval_cases.go`：6 个预置用例（设计 §6.1 原文）；`runAgentEvalAssertions` / `evaluateAssertion` / `normalizeToolName`（小写归一 + 常见别名映射）
- `agent_eval_cases_test.go`：断言纯函数全覆盖

### t5 SSE 解析器（TDD，新文件）
- `internal/application/service/agent_eval_stream.go`：最小 `sseEvent` 结构（顶层 content/response_type/knowledge_references/tool_calls + json.RawMessage data）；分发循环（120s 超时）；`parseJudgeOutput`（花括号容错提取 `{"score","reason"}`）
- `agent_eval_stream_test.go`

### t6 service 执行器（新文件）
- `internal/application/service/agent_eval.go`：`TriggerRun`（互斥查 running→409；创建 run 行；judge 模型预解析；goroutine）；`runCases`（会话创建重试 1 次 → SSE → 断言 → judge（仅通过用例））；fatal 语义（401/403 中止剩余）；agent 无模型逐用例 failed 继续；answer 截断 4000；保留每 agent 50 条；`GetRunDetail` 组装 cases；`RecoverOrphanRuns`（running→failed "服务重启中断"）
- 会话创建/自调用复用 `AgentEvalCredentials`（内存传递，不落库）

### t7 handler + 路由 + DI
- 新增 `internal/handler/agent_eval.go`：裸 JSON 202/409/404/200（含租户与权限取参）
- 改 `internal/router/routes_agent.go`：`RegisterCustomAgentRoutes` 增加 `evalHandler *handler.AgentEvalHandler` 参数；`:id/evaluations` POST（g.OwnedAgentOrAdmin）/ GET（g.Viewer）/ `:run_id` GET（g.Viewer）/ DELETE（g.OwnedAgentOrAdmin）
- 改 `internal/router/router.go`：`RouterParams` 与 `NewRouter` 参数加 `AgentEvalHandler`；调用点传参
- 改 `internal/container/container.go`：Provide repo/service/handler ×3 + `container.Invoke(func(svc interfaces.AgentEvalService) error { return svc.RecoverOrphanRuns(ctx) })`（置于 L444 recoverPendingWikiTasks 附近）

### t8 前端 API（改文件）
- `frontend/src/api/agent/index.ts`：`AgentEvalAssertion/JudgeItem/ToolCall/Case/Run` 类型 + `runAgentEval/listAgentEvals/getAgentEvalRun/deleteAgentEvalRun`

### t9 抽屉（新文件）
- `frontend/src/views/agent/components/AgentEvalDrawer.vue`：t-drawer ≥720 三段式（操作+摘要 / 历史 t-table / t-collapse 详情）；2s 轮询 running 态、关闭清理定时器；409 透出 run_id

### t10 AgentList 集成（改文件）
- 两处 popup 菜单加"冒烟测试"项（`canManageAgent` 门控，L209/L410 附近）
- 抽屉挂载（L808 附近）+ imports；`handleEditorSuccess` 创建成功分支 `DialogPlugin.confirm` 快捷触发；409 → 提示并开抽屉

### t11 i18n ×4（改文件）
- zh-CN/en-US/ko-KR/ru-RU 增 `agent.eval.*` 键

### t12 整体验证
- 后端：`go build ./...`；`go test ./internal/application/... ./internal/handler/...`
- 前端：`npm run type-check`（如有）+ `npm run build`
- 集成清单：触发 202 → 轮询至 completed → 详情断言/评分展示 → 删除 → 重启孤儿恢复 → 二次触发 409 开抽屉

## 四、关键契约（实现期唯一依据）

### API（裸 JSON，不经错误中间件）
```
POST /api/v1/agents/:id/evaluations  OwnedAgentOrAdmin
  202 {"run_id","status":"running","total":6}
  409 {"code":409,"message":"该智能体已有正在运行的测试","run_id":"..."}
  404 {"code":404,"message":"智能体不存在"}
GET  /api/v1/agents/:id/evaluations            Viewer+ → {"data":[...前20倒序]}
GET  /api/v1/agents/:id/evaluations/:run_id    Viewer+ → {"run_id","agent_id","status","total","passed","failed","duration_ms","cases":[...]}
DELETE 同路径 OwnedAgentOrAdmin → {"success":true}
```

### 断言规则
| 断言 | 通过条件 |
|---|---|
| expect_tool:X | tool_calls 归一化（小写）后包含 X |
| forbid_tool:X | 不包含 X |
| min_references:N | knowledge_references 中 content 非空数 ≥ N |
| answer_not_empty | answer 非空白 |
| no_error | 以 complete 结束且无 error 事件 |

### 6 个预置用例（设计 §6.1 原文）
1. smoke-01 闲聊不打扰知识库｜你好，请介绍一下你自己｜forbid_tool:knowledge_search;answer_not_empty
2. smoke-02 知识问题走检索｜请根据知识库资料回答：这个系统支持哪些文档格式？｜expect_tool:knowledge_search;min_references:1;answer_not_empty
3. smoke-03 复杂问题走理解链｜帮我分析一下：上传文档后检索结果的排序依据是什么？和直接提问相比有什么区别？｜expect_tool:query_understand;answer_not_empty
4. smoke-04 超纲问题不编造｜2026年世界杯决赛的比分是多少？｜no_error;answer_not_empty
5. smoke-05 模糊问题正常应答｜嗯……就是说，那个东西怎么样？｜no_error;answer_not_empty
6. smoke-06 中文知识问题｜知识库里关于系统部署的内容，应该怎么配置环境变量？｜expect_tool:knowledge_search;answer_not_empty

### judge（仅展示，不影响判定）
- 仅对通过用例执行；metrics：relevancy（smoke-02/03/06）、honesty（smoke-04）；输出 `{"score":0-1,"reason":"一句话理由"}`；失败 → `{metric,error}` 项

### 错误处理（设计 §8）
会话创建失败重试 1→run failed；agent 无模型→用例 failed 继续；SSE 超时 120s→failed 继续；judge 失败→仅 error 项；重启孤儿→failed"服务重启中断"；token 过期 401/403→剩余 failed + run failed；answer 截断 4000。

## 五、约束

- 新代码零注释（sqlite 迁移首行 mirrors 注释除外）
- 只改/增上列文件；不重构无关代码；不提交 git
- 敏感凭据（Authorization/X-API-Key/X-Tenant-ID）仅内存传递，不落库、不打日志

## 六、自审结论

- 覆盖设计 §11 全部交付物路径；事实核查表全部经本次会话读码验证
- 409/404 裸 JSON 与错误中间件无冲突（已验证直通逻辑）
- judge 预解析规避 goroutine 无租户 ctx 问题；`trigger` 保留字已规避
- 无占位符：所有锚点带行号，契约字段逐项来自 client.py / 设计文档原文
