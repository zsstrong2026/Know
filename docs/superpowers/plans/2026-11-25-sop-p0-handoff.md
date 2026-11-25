# SOP 工作台 · 遗留缺陷修复交接（2026-11-25）

> 本文件是「新开一个对话窗口继续干」的完整交接。读完这一页就能开工，不需要读前一个会话的记录。
> 上位文档：`docs/superpowers/plans/2026-11-19-sop-workbench.md`（68 个任务已全部交付，含 M11）、
> `.superpowers/sdd/2026-11-19-sop-workbench/progress.md`（156 条 `minor (deferred)`）、
> `docs/superpowers/verification/2026-11-20-sop-brief-split.md`（§16.22–26 验收）。

## 0. 环境与基线

| 项 | 值 |
| --- | --- |
| 仓库 | `D:\知识库\Know`（Windows / Git Bash） |
| 分支 | `trae/metting` |
| 基线 commit | `93ef12871`（docs: 验收记录补充收尾修复） |
| Go | `go build ./internal/...` 可用；**`go build ./...` 与 `go test ./internal/application/service` 不可用**（DuckDB 静态库链接失败，本机既有问题） |
| 前端 | `frontend/`，Vite 7 + Vue 3.5；测试是 `tsx --test`，**不要引入 Vitest** |
| 后端容器 | `WeKnora-app` 在 18080，但**镜像不含本轮 SOP 路由**，不能用来验收 SOP 接口 |

### 开工前必读的三条纪律

1. **工作树里有一批未提交的「会议室」改动，不要提交、不要回滚、不要 `git add -A`**。列清单：
   `frontend/src/api/meeting/index.ts`、`frontend/src/i18n/locales/*.ts`（六个）、`frontend/src/stores/meeting.ts`、
   `frontend/src/views/meeting/*`、`frontend/src/utils/meetingPhase.ts`、`internal/**/meeting*.go`、
   `internal/handler/doc_assistant.go`、`internal/router/routes_meeting.go`、`internal/types/{meeting.go,interfaces/meeting.go}`、
   `migrations/versioned/000174_meeting_phase_events.*`。
   **每个 `git add` 都写具体文件路径。**
2. **locale 文件是双人共用的**：它同时带着会议键。如果必须改 SOP 的 i18n 键，用「手术式暂存」
   （`git hash-object -w` + `git update-index --cacheinfo`），并且**必须校验暂存版本本身**
   （`git show :frontend/src/i18n/locales/zh-CN.ts` → 落地成临时文件 → `npx esbuild` 解析通过）。
   上一轮就是漏了这一步，把六个 locale 提交成语法错误（已在 `6d043175a` 修好）。
3. **失败基线**：前端 `npm test` 全量有 9 项改动前就存在的失败（styleGuard 5 项计数超基线、`useModalShell` 4 项
   `parserEngineDefaultRules`）；后端 `internal/handler` 有 5 项既有失败（Datasource TempDir、批量下载 ZIP）。
   **不要去修，也不要因为它们在而认为自己的改动有问题。**

---

## 1. P0：确认是真缺陷，建议先做（3 项）

### P0-1 项目「解绑并删除该库」是空操作

**现象**：项目配置 → 知识库与简报 →「解绑并删除该库」，二次确认后提示成功，但刷新页面 `kb_id` 还在，
删除策略仍是「项目自建库：随项目删除」。

**根因链**（已核实）：

- 前端 [SopProjectSettings.vue:730](file:///D:/知识库/Know/frontend/src/views/sop/SopProjectSettings.vue#L730)
  发 `updateProject(id, { kb_id: '' })`；
- 后端 [sop.go:252](file:///D:/知识库/Know/internal/handler/sop.go#L252) 的 `applyTo` 写的是
  `if p.KBID != ""` —— 空串被当成「未传」，永远不写。
- 同一分支里真正的删库逻辑只存在于 `DeleteProject`（[sop.go:450](file:///D:/知识库/Know/internal/handler/sop.go#L450)），
  解绑路径从没调用过 `DeleteKnowledgeBase`。

**修复**（推荐 B）：

- 方案 A（最小改动）：把 `sopProjectPayload.KBID` 改成 `*string`，`nil` = 不动、`""` = 清空并同步清
  `KBOwner`/`BriefKnowledgeID`/`BriefRenderedSeq`；`CreateProject` 里继续按 `kb_mode` 走。
- 方案 B（语义更干净）：加 `DELETE /api/v1/sop/projects/:id/kb`，handler 内按 `kb_owner` 决定
  「删除自建库 + 清空字段」或「仅解绑」，前端按钮改调它。路由注册处：
  [routes_sop.go](file:///D:/知识库/Know/internal/router/routes_sop.go) 的 `manage` 组，
  并在 `internal/handler/sop.go` 的 `RegisterBriefRoutes` 里同步一份（handler 包内测试用它装配）。

**测试（先写，必须看到红）**

```go
// internal/handler/sop_brief_test.go（可复用 newSOPBriefTestRouterWithKB 与 stubSOPKB）
func TestUnbindProjectKBDeletesOwnKBAndClearsBinding(t *testing.T) {
    // 1) 建项目（kb_mode 默认 new → 桩返回 kb-self，kb_owner=project）
    // 2) DELETE /projects/:id/kb
    // 3) stubSOPKB.deleted 必须包含 kb-self
    // 4) GET /projects/:id → kb_id == ""，kb_owner == ""，brief_knowledge_id == ""
}
func TestUnbindRejectsLinkedKBDeletion(t *testing.T) {
    // 改绑到 kb-other（kb_owner=linked）后解绑：只清字段，stubSOPKB.deleted 为空
}
```

**验收**：`go test ./internal/handler/ -run "TestUnbind" -v` 通过；前端 `npm run type-check` + 手工点一次按钮。

---

### P0-2 项目编号/名称并发冲突落 500

**现象**：两个请求同时用同一个 `code`（或同名）创建项目，先查后写之间没有唯一约束兜底，
后到者拿到数据库唯一索引错误 → `sopFail` 走 default 分支 → **500 + 英文内部错误**，
而串行情况下是 400 + 中文「项目编号 X 已被占用」。

**根因**（已核实）：[sop.go:567](file:///D:/知识库/Know/internal/handler/sop.go#L567) `sopProjectConflict`
只做应用层预检；`CreateProject` 的 `projects.Create` 失败后直接 `sopFail`。

**修复**：`CreateProject`/`UpdateProject` 的 `Create/Update` 错误分支里，先判唯一冲突再兜底。
仓储包的 `isUniqueViolation` 是私有函数（[pm_common.go:30](file:///D:/知识库/Know/internal/application/repository/pm_common.go#L30)），
两个选择：

- 在 `repository` 包导出 `func IsUniqueViolation(err error) bool`（一行别名），handler 直接调；
- 或 handler 里用 `stderrors.As(&pgconn.PgError{})` + sqlite 文本匹配 —— 不推荐，会重复实现。

**测试**

```go
// 串行可测的部分：直接构造「预检通过但落库冲突」的场景不易模拟，
// 因此改为断言映射函数本身：新增 repository.IsUniqueViolation 的单测 + handler 的错误分支单测。
func TestProjectConflictMapsTo400(t *testing.T) {
    // 用 stub 仓储让 Create 返回 errors.New(`UNIQUE constraint failed: sop_projects.code`)
    // 断言状态码 400 且 body 含「已被占用」
}
```

如果嫌 stub 麻烦，至少要补 `IsUniqueViolation` 对 PG 错误串 / SQLite 错误串两个用例。

---

### P0-3 Windows 上 `go test ./internal/database/` 必红

**现象**：`TestMigrationSourcesHaveUniqueVersions` 在 Windows 报
`parse "file://D:\知识库\Know\migrations\sqlite": invalid port ":\\知识库..." after host`。

**根因**（已核实）：[migration_source_test.go:17](file:///D:/知识库/Know/internal/database/migration_source_test.go#L17)
用 `"file://" + filepath.Join(root, "migrations", dialect)` 拼绝对 Windows 路径。

**修复**：复用既有 `chdirAndRestore(t, root)` 后用相对路径 `"file://migrations/"+dialect`，
与生产 `RunMigrationsWithOptions` 同口径（[migration.go:106](file:///D:/知识库/Know/internal/database/migration.go#L106) 就是这么写的）；
或用 `url.PathEscape` 转换。**推荐前一种**，与既有 `TestSQLiteMigrationsRollbackProjectNotes` 一致。

**验收**：`go test ./internal/database/ -run "TestMigrationSourcesHaveUniqueVersions" -v` 在本机 PASS。

---

## 2. P1：真实但影响面有限（建议同批做完）

| 编号 | 问题 | 位置 | 修复要点 | 测试 |
| --- | --- | --- | --- | --- |
| P1-1 | 事项状态机并发可绕过：`UpdateStatus` 事务内读状态后，UPDATE 只带 `tenant+id`，两个并发迁移都会成功 | [sop_item.go:215](file:///D:/知识库/Know/internal/application/repository/sop_item.go#L215) | UPDATE 加 `AND status = ?`（用事务内读到的 from）；`RowsAffected==0` 返回新哨兵 `ErrSopItemStatusConflict`，服务层翻成「状态已被他人变更，请刷新」 | 两个 goroutine 同时 `UpdateStatus(backlog→todo)` 与 `(backlog→cancelled)`，断言只有一个成功、库里状态是成功者的 |
| P1-2 | 节点成功后 `Feedback` 不清，重试/重跑会带上上一轮的「返工意见」 | [node.go:786](file:///D:/知识库/Know/internal/application/service/sop/engine/node.go#L786) `case types.SopGatePass` | pass 分支同时 `nr.Feedback = ""`、`nr.GateReason = ""`（`GateResult` 保留 pass） | 返工一轮后 pass，断言 `nr.Feedback == ""` 且任务书里没有「返工意见」小节 |
| P1-3 | 重试后运行耗时把停机时间也算进去：`Retry` 不重置 `started_at`，`finalizeRun` 用 `now - started_at` | [ops.go:306](file:///D:/知识库/Know/internal/application/service/sop/engine/ops.go#L306) | `Retry` 里 `run.StartedAt = nil` + `ExecutionDurationMs = 0`（认领时 `COALESCE(started_at, now)` 会重新打点），并把 run 与 node 的两处更新放进一个事务 | 重试后成功，断言 `ExecutionDurationMs` 小于「首跑时长 + 停机 1s」 |
| P1-4 | 重试/打回后节点仍带旧痕迹：`ResetNodes` 不清 `session_id/resume_session_id/usage/started_at` | [sop_run.go:440](file:///D:/知识库/Know/internal/application/repository/sop_run.go#L440) | **先决策**：带 `carry_over` 的节点要保留 session（返工续会话），所以「清 session」只能在 `carry_over=false` 时做。建议 Rev 为：清 `started_at/usage`，`session*` 由调用方按 `CarryOver` 显式决定（加参数或分两个方法）。**不要直接清 session**，会破坏续跑 | 现有 `TestRetry*` 加断言：`started_at == nil`、`usage == {}`，`carry_over=true` 时 `session_id` 保留 |
| P1-5 | MCP 端点行按「运行×节点」累积，从不清（`mcp_endpoints` 只增） | [mcp.go:58](file:///D:/知识库/Know/internal/application/service/sop/mcp.go#L58)，命名 `sop:<runID>:<nodeID>` | `MCPEndpointStore` 窄接口加 `DeleteByName`/`ListByTenant`；容器里把 `NotesService.RecordRunFinal` 之后（或 `OnRunFinal` 回调内）按前缀清理该运行的端点。注意 `OnRunFinal` 里已有一次 `run` 收口，加清理不能影响黑板摘要 | 跑完一条运行后断言 `ListByTenant` 里没有 `sop:<runID>:*` |
| P1-6 | 批量触发逐条串行 HTTP，几十条会明显变慢 | [SopItemBoard.vue:632](file:///D:/知识库/Know/frontend/src/views/sop/SopItemBoard.vue#L632)；抽屉 `launchChildren` 同理 | 后端加 `POST /api/v1/sop/items/bulk-launch`（body `ids[]`；父项展开为子项、运行中跳过、返回 `started/skipped`），前端换用；沿用 `bulk-status` 的 RBAC 与 API Key 能力声明 | handler 测试：一个父项 + 两子项，传父项 id → 只对子项建运行；运行中事项进 `skipped` |

---

## 3. P2：数据一致性与边角（按需，不做也不算欠债）

| 编号 | 问题 | 位置 | 一句话建议 |
| --- | --- | --- | --- |
| P2-1 | 重复发布内容相同的 revision | [sop_template.go:150](file:///D:/知识库/Know/internal/application/repository/sop_template.go#L150) | 发布前比对 `definition` 规范化 JSON 的 sha256，相同直接复用现有 revision |
| P2-2 | 触发配置 `Upsert` 先查后写竞态 | [sop_trigger.go:49](file:///D:/知识库/Know/internal/application/repository/sop_trigger.go#L49) | 撞 `ux_sop_trigger_project` 时降级为「按 (tenant,project) 更新」重试一次（照 `SopProjectNoteRepo` 的写法） |
| P2-3 | `SeedAnswered` 空 `qas` 时不校验事项归属，且保留调用方 `ID/CreatedAt` | `internal/application/repository/sop_item_extra.go` | 先校验事项属于该租户，再判空；写入时清掉 `ID/CreatedAt` 让模型补 |
| P2-4 | 未知 `check.type` / `node.type` 不拒绝，跑到一半才炸 | `internal/types/sop_template.go` | 加 `ValidateSopDefinition`，在 `Publish` 与 `CreateRun` 前调用 |
| P2-5 | 打回目标为空时退化成本节点重跑；`ResetNodes` 会扫到无关兄弟分支 | [node.go:846](file:///D:/知识库/Know/internal/application/service/sop/engine/node.go#L846) | 空 `RejectTo` 直接 400「请选择打回目标」；重置只走 `reject_to` 链 |
| P2-6 | `isAbsLike` 不拒 Win32 ADS（`name:stream`） | `internal/application/service/sop/engine/workspace.go` | 非盘符位置出现 `:` 即拒绝 |
| P2-7 | 附件表缺 `CreatedBy`；`AddAttachment` 信任调用方租户 | [sop_item.go:329](file:///D:/知识库/Know/internal/application/repository/sop_item.go#L329) | 加列 + 仓储内校验 `ItemID` 归属 |
| P2-8 | `ExpireLeases` 只收口运行，节点行仍 `running`（运行详情会显示「仍在执行」） | [sop_run.go:293](file:///D:/知识库/Know/internal/application/repository/sop_run.go#L293) | 收到租约过期的运行后，同一批把非终态节点置 `cancelled`（与 `cancel_requested` 分支同口径） |
| P2-9 | `AgentProvider` 单例共享一个 `cancel` 槽，`clearCancel` 无条件清空（与注释不符） | [agent.go:364](file:///D:/知识库/Know/internal/application/service/sop/executor/agent.go#L364) | **注意**：引擎目前从不调 `provider.Cancel`（取消靠 run ctx 传播，`Execute` 里的 `runCtx` 派生自它，所以取消是有效的）。这条只是「死代码 + 注释误导」，改成 `map[nodeID]cancel` 或删掉 `Cancel` 均可，**优先级最低** |
| P2-10 | 日志丢 ctx/trace（6 处 `log.Printf`） | `approval_aggregate.go`、`note.go` | 换 `logger.Warnf(ctx, ...)` |
| P2-11 | SSE 不补历史；`running/started` 变更不发事件 | [node.go:633](file:///D:/知识库/Know/internal/application/service/sop/engine/node.go#L633) | 节点进入 running 后补一次 `publishNodeStatus` |
| P2-12 | `cli-status` 每请求 10 次 `LookPath` | `internal/application/service/sop/admin.go` | 进程内 60s 缓存 |

---

## 4. 前端遗留（功能补全，不是缺陷）

- 运行列表 / 触发历史 / 审批中心分页（后端已支持 `page/page_size`，前端一次拉全量）。
- 触发配置时区做成下拉（当前手填）。
- AI 编排的 `inventory` 传真实清单（当前 `{}`，后端按空清单跳过校验）。
- 检查器角色卡四格统计与「编辑角色 prompt」弹窗。
- `computeEdgeAnchors` 已有实现但无消费方（只读画布用的是 Vue Flow 自带锚点）。
- 抽屉产物表「已入库」列只展示状态，没有入库按钮（运行详情页有）。

---

## 5. 每个任务的执行流程（照做即可）

```bash
# 0) 每个任务开始前
cd /d/知识库/Know && git status --porcelain | grep -v meeting   # 确认没有意外改动
git log --oneline -1

# 1) 先写失败测试（TDD：不看实现写测试，跑一次确认红，且失败原因正确）
go test ./internal/<pkg>/ -run "TestXxx" -v

# 2) 实现（最小改动，不加防御性代码、不顺手重构）

# 3) 单包 + 相邻包回归
go build ./internal/...
go test ./internal/<pkg>/ ./internal/handler/ -run "相关用例"

# 4) 提交（只 add 具体文件）
git add <具体文件...> && git commit -m "fix(sop): ..."

# 5) 记台账
#    追加到 .superpowers/sdd/2026-11-19-sop-workbench/progress.md：
#    Task?/Fix-N: complete (commit xxx) — 现象/根因/改法/测试；有 Ruling 也写进去
```

### 每个任务完成前必须跑的全量回归

```bash
cd /d/知识库/Know
go build ./internal/...
go test ./internal/types/ ./internal/application/repository/ ./internal/application/service/sop/ ./internal/application/service/sop/engine/ ./internal/router/ ./internal/database/ -run "TestSQLiteMigrations|TestSop|TestNote|TestItem|TestSplit|TestParent|TestBrief|TestRender|TestUnbind|TestProjectConflict|TestMigrationSources"
go test ./internal/handler/ -run "TestSOP|TestSplit|TestParent|TestBrief|TestDeleteProject|TestUnbind|TestProjectConflict"

cd frontend
npx vue-tsc --build
npm test -- src/api/sop/helpers.test.ts     # 期望 21/21
npm run check-i18n                          # 期望 16/16
```

改动到前端界面时另需浏览器走查（本项目惯例：起 `npm run dev` + 本地 SOP 桩，桩脚本不要提交；
历史桩写法见台账 T66/T67 的验证记录）。

---

## 6. 明确不要做的事

1. 不要 `git add -A` / `git add .`；不要提交任何 `meeting*` 文件。
2. 不要为了「让测试变绿」去改 styleGuard 基线、跳过 `useModalShell`、改 handler 的 5 项既有失败。
3. 不要引入 Vitest、不要新增前端组件测试基建（本仓约定 `tsx --test`）。
4. 不要放宽 `SopItemRepo.Update` 的列白名单来写结构列 —— 结构列只走 `SetBig`/`SetParent`。
5. 不要动 `CreateRun` 的「大项不可直接触发」守卫与 `EnsureNotBigParent`。
6. 不要在 `ResetNodes` 里无条件清 `session_id`（会破坏 `carry_over` 续跑）。
7. 不要声称「已验证」任何需要真实后端/真实知识库/PG 的项；本机做不到的写「未验证 + 原因」。

---

## 7. 建议的开工顺序与预估

| 顺序 | 任务 | 预估 | 备注 |
| --- | --- | --- | --- |
| 1 | P0-3 Windows 迁移测试 | 20m | 独立、无风险，先把红灯清掉 |
| 2 | P0-2 冲突映射 400 | 1h | 需要先导出 `IsUniqueViolation` |
| 3 | P0-1 解绑/删库 | 3h | 涉及前端 + 路由 + 测试，单独一个 commit |
| 4 | P1-2 清 Feedback | 30m | 与 5 同一文件，可合并提交 |
| 5 | P1-3 重试耗时 + 事务 | 1h | |
| 6 | P1-1 状态 CAS | 1.5h | 注意批量入口的错误语义（现在非法迁移进 skipped） |
| 7 | P1-4 ResetNodes（先做决策） | 1.5h | `carry_over` 语义要想清楚再动 |
| 8 | P1-5 MCP 端点清理 | 3h | 有 `OnRunFinal` 钩子可挂 |
| 9 | P1-6 bulk-launch | 4h | 前后端一起 |

做完 1–3 就已经消灭了三个用户可见问题，可以先交付一次。
