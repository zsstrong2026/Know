# SOP 工作台 · M11 验收记录（spec §16.22–26）

> 日期：2026-11-24 · 分支 `trae/metting` · 环境：Windows 10.0.26300（DuckDB 静态库链接失败、容器内后端不含 SOP 路由）
> 验收口径与 [第 1–21 项记录](./2026-11-19-sop-acceptance.md) 一致：能自动化的用测试证明，界面项用 dev server + 本地 SOP 桩做浏览器走查；**不能在本环境真实验证的一律标「未验证 / 部分验证」并写明原因**。

## 总览

| # | 验收项 | 结论 | 证据 |
| --- | --- | --- | --- |
| 22 | 项目知识库与简报同步 | ⚠️ 部分验证（删库级联已有实现与测试） | 简报 seq 去重/降级/失败不前移水位有单测；E2E 覆盖「运行摘要自动落黑板 → 简报入库」；创建模态三选与改绑走查 PASS；删项目连带删自建库有 `TestDeleteProjectDeletesOnlyOwnKB` / `TestDeleteProjectKeepsLinkedKB`；真实建库（Embedding 模型）与真实 KB 数据删除未验证 |
| 23 | 项目黑板与上下文注入 | ✅ 单测 + E2E PASS；⚠️ 运行详情「任务书」视图未逐项走查 | `TestRenderTaskBookProjectContextBlock`、`TestBuildProjectContext*`、`TestMaterializeProjectBrief`、`TestSharedContextFlowsToNodesAndBriefToKB`（任务书含「## 项目现状」+ 决策 + 兄弟进度，`inputs/project-brief.md` 落盘，`sop_runs.project_context` 回写） |
| 24 | 大需求拆分（两级） | ✅ 后端 + 前端桩走查 PASS；⚠️ 模板产出子项真实运行未验证 | 后端 `TestSplit*` / `TestParentAutoAdvances*` / `TestEnsureNotBigParent` / `TestListRollsUpParentExec` / `TestChildItems*`；前端 T67 走查（折叠/汇总/子项 tab/拆分卡）；`TestSplitThreeChildrenEachRunIndependently` |
| 25 | 知识库改绑与降级 | ⚠️ 部分验证 | 改绑走查 PASS（徽标变「改绑」、删除策略变「只解绑不删除」、简报重置为未渲染）；`kb_scope=project` 空列表解析已有单测（T31/T37）；补绑后检索命中需真实 KB，未验证 |
| 26 | 既有能力零回归 | ⚠️ 部分 | 后端 `go test ./internal/{types,application/repository,application/service/sop/...,handler,router}` 全绿；前端 `npm test` 有 9 项**既有失败**（见下文），SOP 相关 21/21、i18n 16/16、type-check 全绿。已修复一次 i18n 语法回归（见「收尾修复」） |

## 逐项说明

### 22. 项目知识库与简报同步 — 部分验证

**已验证**

| 场景 | 操作路径 | 期望 | 实际 |
| --- | --- | --- | --- |
| 创建项目默认建库（三选） | 事项页 →「＋ 新建事项」旁的创建项目 → 知识库三选 | 默认「新建「<项目名>」知识库」；可切「使用已有」/「暂不创建」；选择随 `kb_mode` 提交 | 走查：三选可选、选「已有」未选库时提交被拦、无裸 i18n 键 |
| seq 未变不重写 | 单测 `TestBriefRenderSkipsWhenSeqUnchanged` | 第二次 EnsureInitial 不产生任何 KB 写 | PASS（createCalls+updateCalls 恒为 1） |
| 新增动态后覆盖同一文档 | 同上 | `updateCalls=1` 且 `brief_knowledge_id` 回填、`brief_rendered_seq` 前进 | PASS |
| 未绑 KB 降级 | `TestBriefDegradesWithoutKB` | 渲染可用、同步 no-op、不报错 | PASS |
| 写入失败不前移水位 | `TestBriefEnsureInitialKeepsWatermarkOnFailure` | 返回错误、`brief_rendered_seq` 不变，重试成功 | PASS |
| 运行终态进简报 | `TestSharedContextFlowsToNodesAndBriefToKB` | 首版入库正文含「运行结论」；再记一条只 update 不 create | PASS |
| 改绑知识库 | 配置页 → 知识库与简报 → 选择另一个库 → 确认绑定 | 徽标变「改绑」、删除策略变「只解绑不删除」、简报卡片变「有 N 条新动态未同步」 | 走查 PASS（桩） |
| 删项目连带删库 | `DELETE /projects/:id` → 有进行中运行先 400 | `kb_owner=project` 连带删库、`linked`/`pm` 仅解绑 | 实现 + 单测 PASS（`TestDeleteProjectDeletesOnlyOwnKB` / `TestDeleteProjectKeepsLinkedKB`，断言只对自建库调用 `DeleteKnowledgeBase`）；真实 KB 文档清理未在大库上验证 |

**未验证原因**：本机无法构建 server（DuckDB 静态库链接失败），容器内后端镜像不含本轮 SOP 路由，因此所有涉及真实知识库创建/删除/嵌入的链路只能走桩。

### 23. 项目黑板与上下文注入 — 单测 + E2E PASS

| 场景 | 证据 |
| --- | --- |
| 黑板记一条/编辑/置顶/软删与来源标签 | `TestNotesCreateHuman`、`TestNotesPin`（后端）；T66 走查（kind 筛选、置顶后 📌 计数、删除二次确认后行数 4→3） |
| 运行终态自动摘要（确定性、含产物 sha256 前缀、只取审批原话） | `TestAutoRecordRunIsDeterministic`、`TestRecordRunFinalFetchesContext` |
| AI 决策草稿只填不落库 | T66 走查：点「生成决策草稿」后表单被填为「决策：…」三行，但黑板条数不变；实际落库仍需点「记一条」 |
| 任务书「项目现状」块（硬顶 1500、决策→进度→简报位置） | `TestBuildProjectContextSections`、`TestBuildProjectContextCap` |
| 物化 `inputs/project-brief.md` | `TestMaterializeProjectBrief` + E2E 读文件断言 |
| 节点实际收到该块 | E2E：stub Agent 记录 Prompt，断言含 `## 项目现状`、`采用方案 B`、`已完成` |
| 运行行回写 `project_context` | E2E 断言 `run.ProjectContext` 含决策标题（供运行详情「任务书」视图） |
| seq 未变不重复渲染 | `TestBriefRenderSkipsWhenSeqUnchanged` |

**未逐项走查**：运行详情「任务书」视图的展示样式只在代码层接上 `project_context` 字段，本环境未能启动带 SOP 路由的后端，无法用真实运行数据点验空态文案与逐节排版。

### 24. 大需求拆分（两级）— 后端 + 前端桩 PASS

| 场景 | 证据 |
| --- | --- |
| 拆分子项（父项自动升级大项、编号同一序列） | `TestSplitMarksParentBigAndSetsSource`（handler）、`TestSplitGuardsAndNumbering`（service） |
| 三级嵌套拒绝（白话报错「只能拆两层」） | 同上 + handler 400 |
| 大项不可直接触发（createRun 与 relaunch 两个入口） | `TestParentItemCannotLaunchDirectly`、`TestEnsureNotBigParent` |
| 列表父项汇总（child_total/child_done/child_exec，空容器=none） | `TestListRollsUpParentExec` |
| 全部子项完成自动 done（走状态机最短路） | `TestParentAutoAdvancesWhenAllChildrenDone`、`TestSopItemStatusPath` |
| 模板产出子项（上限 20、格式错即失败、不部分创建） | `TestChildItemsCreatesSpecs`、`TestChildItemsOverLimitRejected`、`TestChildItemsInvalidPayloads` |
| 三子项各自独立运行 + 五态计数之和 = 顶层数 | `TestSplitThreeChildrenEachRunIndependently` |
| 前端：列表折叠/汇总列/子项行/全选展开 | T67 走查：5 行 → 展开后 3 子项缩进灰底、进度「1/3 子项已完成」、关联 SOP「—」、行动作「拆分子事项」 |
| 前端：抽屉子事项 tab | T67 走查：tab「子事项 3」表格 3 行带 打开/触发/移出大项；打开 SEC-10 后概览出现「所属大项 SEC-9」 |
| 前端：创建模态归属大项与拆解卡拆分 | 代码实现 + type-check；拆解卡勾选需真实 SSE 拆解结果，本环境只能用桩固定卡片，未逐条点验 |

**未验证**：`produces_child_items` 节点的真实运行（需要 `out/child-items.json` 由真实 agent/CLI 写出）；上限 20 的报错文案在真实运行里只由单测覆盖。

### 25. 知识库改绑与降级 — 部分验证

- 改绑走查 PASS（见 22 的表格行）。
- `kb_scope=project` 在未绑 KB 时解析为空列表：由 T31/T37 的 `kb_scope` 白名单单测覆盖（`TestResolveKBScope*`），本轮未改该路径。
- 补绑后「下次运行能检索到新库内容」需要真实 KB 与真实检索，**未验证**。

### 26. 既有能力零回归 — 部分

**后端**

```
go build ./internal/...                                        PASS
go test ./internal/types/                                      PASS
go test ./internal/application/repository/                     PASS
go test ./internal/application/service/sop/                    PASS
go test ./internal/application/service/sop/engine/             PASS（81s）
go test ./internal/router/                                     PASS（API Key 策略自检含新路由）
go test ./internal/handler/ -run "TestSOP|TestBrief|TestSplit|TestParent"  PASS
```

**前端**

```
npx vue-tsc --build        PASS
npm test -- src/api/sop/helpers.test.ts   21/21 PASS
npm run check-i18n                        16/16 PASS
npm test 全量                             2810/2821 PASS，9 项失败
```

9 项失败在本次改动前即存在（在 `cea70f6ed` 上执行同一批用例同样 9 项失败）：

- `styleGuard.test.mjs` 5 项：`td-token-fallback` 30>15、`radius-literal` 276>209、`font-size-literal` 282>148、`motion-literal` 78>74、`important` 404>398。**本任务的 SOP 样式已全部改用 `--app-*` 令牌，SOP 目录内 0 处字面量命中该规则**；超出的计数来自 `views/workbench`、`views/doc-assistant` 等既有目录（属工作树里另一条未提交的会议室/工作台改动与基线上限未同步）。
- `useModalShell.test.ts` 4 项：`editorResources.parserEngineDefaultRules is not iterable`，属解析器引擎默认路由用例，与 SOP 无关。

**未验证**：`go test ./...` 全量无法在本机执行（`cmd/server` 与 `cmd/download/duckdb` 的 DuckDB 静态库链接失败，属本机既有环境问题，T58 已记录）；`internal/application/service` 整包同样因 DuckDB 链接失败无法运行，已用 `go vet` 与各子包测试替代。


## 收尾修复（2026-11-25，commit `6d043175a`）

| 问题 | 处理 | 验证 |
| --- | --- | --- |
| **已提交的六个 locale 无法解析**（T67 手术式暂存漏 `split.panel` 行尾逗号） | 补齐逗号并闭合 `settings` 段 | HEAD 版本六个文件经 esbuild 解析通过；`check-i18n` 16/16 |
| 删项目未处理知识库 | `DeleteProject` 按 `kb_owner` 连带删除自建库 / 仅解绑其它库 | `TestDeleteProjectDeletesOnlyOwnKB`、`TestDeleteProjectKeepsLinkedKB` |
| 事项删除守卫依赖 `is_big` 标记 | 改为「存在子项即拒绝」 | `TestDeleteRejectsWhenChildrenExistEvenWithoutBigFlag` |
| 抽屉「整条重触发」绕过父项拦截 | 改调 `POST /items/:id/relaunch` | 后端 `TestParentItemCannotLaunchDirectly` |
| M11 迁移只验证了 down 脚本存在 | 新增真实回滚测试（76 → 75 → 76） | `TestSQLiteMigrationsRollbackProjectNotes` |

**教训**：手术式暂存（`git hash-object` + `update-index`）之后必须校验**暂存版本本身**（`git show :path`）能否解析/编译，只跑工作树上的测试会漏掉「暂存内容语法错误」这类问题。
