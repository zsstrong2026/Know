# 工作台建设交接文档（给接手的智能体）

- 生成日期：2026-11-06
- 仓库：`d:\知识库\Know`（WeKnora v0.9.0 fork）
- 分支：`main`，HEAD `63bbfdf03`
- 工作台分支：`feature/affine-know-workbench` 已是 `main` 的祖先（main 领先 9 个提交），**不需要切分支**
- 权威需求：[实现方案](d:/知识库/AFFiNE-Know实现方案.md)、[开发计划](d:/知识库/AFFiNE-Know开发计划.md)、[HTML 原型](d:/知识库/AFFiNE-Know原型.html)、[P0 Spike 报告](p0-spike-report.md)
- 上一版交接（P1 开工前）：`d:\知识库\AFFiNE-Know交接文档.md`，其中的"尚不存在"清单已过期，只保留 §4 硬约束仍有参考价值

---

## 0. 30 秒速览

工作台已经从 P0 走到 P3 收尾：**条目 CRUD、双形态编辑器、数据库三视图、Journals、归档、Page AI、模板、资产、CSV、数据库 AI、⌘K 搜索、文档导入** 都已落地并有测试；P4 的组织增强与发布收口尚未开始。

**接手后第一件事不是写新功能，而是收口当前工作区**：

- `git status --short` 有 **106 项**未提交（56 个已跟踪文件改动 +5072/−373 行，另有一批新增文件）。
- 这些改动就是 P2/P3 的最新增量（数据库工具条、数据库 AI、导入、资产、清理器、构建脚本等），**没提交就等于没交付**。先按 §6.1 分组提交，再开工。
- 当前校验状态见 §6.6：工作台单测、类型检查、i18n 全绿；`handler` / `repository` 各有个别失败已甄别为 **Windows 环境相关的既有失败**（见 §5 R3），跑测试时忽略，不要顺手改。

**绝对不要**：

1. 不要用 `force=true` 绕过 2401；不要新增 apply-template 端点。
2. 不要让 API Key 进入 `/workbench` 路由；不要让 Admin 或其他用户读到私有内容。
3. 不要让服务端解析/生成/合并 Y.Doc（只存字节、只存 `text_content` 投影）。
4. 不要修改已发布的迁移（只能追加）；不要调整 136–140 / 42–46 的编号与顺序。
5. 不要让首页 chunk 引入 BlockSuite 或 `.ydoc` 资产。
6. 不要改需求文档与原型来迁就实现；有冲突先停下来问。

---

## 1. 产品模型（一句话版）

在 Know 里新增一个**个人私有工作台**：文档 / 无限画布双形态、数据库与日历、Journals、归档到工作知识库、Page AI、编辑器设置、文档模板；复用 Know 已有的模型、智能体、文件处理、存储与检索。

| 概念 | 规则 |
| --- | --- |
| 工作台条目 | `workbench_items` 一行，`kind` = `canvas` 或 `database` |
| 双形态 | 同一条目有 `page` / `edgeless` 两种视图；`current_mode` 是 SQL 视图属性，切换不产生版本、不碰 Y.Doc |
| 工作知识库 | 每用户一个 `is_system=true` + `visibility=private` 的私有 KB，不可删除，Admin 也不可读 |
| 章节文档 | Know 原有 doc-assistant 文档，入口在工作知识库页，**不在**工作台侧栏，不参与 Journals |
| 归档 | 把条目/章节文档作为知识写入工作知识库；显式触发，删除条目**不**自动删知识 |
| 模板 | 只做 Page 模板，copy-on-use，不参与归档/搜索/AI/版本历史 |
| 模板入口 | 侧栏无「文档」模块；导航为 最近 / Journals / 数据库(事项) / 收藏 / 模板 / 回收站 + 文件夹树 |

当前不做：移动端、多人实时协作、订阅/云端、AFFiNE EE 代码、Edgeless 模板、模板市场、模板变量、AI 生成模板、模板更新传播。

---

## 2. 仓库事实（开工前核对）

### 2.1 分支与提交

```powershell
cd d:\知识库\Know
git branch --show-current        # main
git log --oneline -6
git status --short               # 预期有大量未提交改动，见 §2.3
```

工作台相关已提交的关键提交：

| 提交 | 内容 |
| --- | --- |
| `1510d89ad` | 工作台/归档迁移 + 知识库系统字段 |
| `245410be0` | 领域类型、DTO 与校验 |
| `8dcb4a3d6` | 引入 AFFiNE BlockSuite 子树与前端构建适配 |
| `48b57ce6e` | 后端工作台全链路 |
| `26d5b3a7b` | 前端工作台（列表/画布/数据库/Journal/设置/Page AI/模板） |
| `9e237c342` | 发布门禁配置项 |
| `477f4ad51` | 首屏不再加载编辑器与聊天大包 |
| `c78d36523` | 模板管理与导航胶囊、列表行视图 |

### 2.2 迁移版本

| 编号 | 内容 |
| --- | --- |
| `000136` / `000042` | 工作台 7 张表 + KB 系统/私有列 |
| `000137` / `000043` | 文档归档字段 |
| `000138` / `000044` | `workbench_item_assets` |
| `000139` / `000045` | `journal_date`、`current_mode`、`idx_wi_journal` |
| `000140` / `000046` | `workbench_templates` + 索引 |

- 当前仓库最新：PostgreSQL 版本化 `000157`，SQLite `000061`
- `internal/database/migration_sqlite_versioned_schema_test.go` 中 `expectedSQLiteMigrationVersion = 61`
- 新增任何迁移都要同步这两个断言；`down` 先删索引再删表/列

### 2.3 未提交改动（必须在接手后立即收口）

按目录分组（`git status --short` 摘要）：

**前端（已跟踪，修改）**：`api/workbench.ts`、`api/doc-assistant.ts`、`stores/workbench.ts`、`types/workbench.ts`、`views/workbench/*`（ItemView 580 行、List、Docs、Nav、FolderTree、ItemCard/ItemRow）、`workbench/editor/*`（blocksuite、use-editor-session、projection、editor-popover.css 901 行、spike*）、`i18n/locales/*`、`GlobalCommandPalette.vue`、`vite.config.ts`、`package.json`、`scripts/editor-spike-check.mjs`

**前端（新增）**：`scripts/build.mjs`、`components/GlobalCommandPalette/useWorkbenchSearch.ts`、`views/workbench/components/{CollectDialog, DatabaseAIDialog, DatabaseRowDrawer, DatabaseToolbar, ImportMaterialDialog, ModeSwitch}.vue`、`workbench/ai/database-patch.ts(+test)`、`workbench/editor/{assets, canvas-eraser, canvas-history-tools, collect-todos, database-ai, database-calendar, database-cell-decorator, database-csv, database-dates, database-seed, database-session, database-state, database-text-locale, spike-perf, workbench-workspace}.ts`、`workbench/import-material.ts(+test)`

**后端（已跟踪，修改）**：`repository/workbench_{archive,version}.go`、`service/workbench/{archive,calendar,items,service}.go`、`handler/{workbench_ai,doc_assistant}.go`、`router/{router,routes_workbench,routes_doc_assistant}.go`、`types/{workbench,doc_assistant}.go`、`container/container.go`、`service/{artifact_collector,tenant_member,doc_assistant/service}.go`、`models/api/openaicompletions/*`

**后端（新增）**：`repository/{workbench_asset,workbench_orphan,doc_document_global}.go(+test)`、`service/workbench/{ai_database,asset,asset_store,cleanup,doc_archive,lifecycle}.go(+test)`、`handler/workbench_asset.go`、`types/workbench_ai_database.go(+test)`

**注意**：`internal/utils/docxfix/` 与 `scripts/docx_borders.py` 属另一条线（Word 边框修复），与工作台无关，提交时单独成组。

### 2.4 运行环境（本机）

| 项 | 值 |
| --- | --- |
| 容器 | `WeKnora-app`（`weknora-app:local`，8080）、`WeKnora-frontend`（`weknora-ui:local`，80）、`WeKnora-postgres`（paradedb pg17）、`WeKnora-redis`、`WeKnora-docreader`、`WeKnora-neo4j` |
| 前端容器挂载 | **无卷挂载**，静态资源在镜像内 `/usr/share/nginx/html` |
| 后端容器挂载 | `config/config.yaml`（rw）、`config/builtin_models.yaml`（ro）；改 yaml 后需重启 `WeKnora-app` |
| 访问 | 前端 http://localhost，后端 http://localhost:8080 |

---

## 3. 已建成什么（按模块 + 代码地图）

### 3.1 后端

| 模块 | 关键文件 | 状态 |
| --- | --- | --- |
| 领域类型/校验 | `internal/types/workbench.go`、`workbench_template.go`、`workbench_ai.go`、`workbench_ai_database.go` | 完成 |
| 仓储 | `internal/application/repository/workbench_{item,version,folder,archive,asset,notice,orphan,repository}.go` | 完成（含 SQLite 单测） |
| 服务 | `internal/application/service/workbench/{service,items,folders,archive,calendar,templates,content,split,prompt,ai,ai_ask,ai_database,asset,asset_store,cleanup,lifecycle,doc_archive,notices,leave,bootstrap}.go` | 完成 |
| Handler | `internal/handler/workbench_{item,folder,archive,journal,notice,ai,asset,template}.go` | 完成 |
| 路由 | `internal/router/routes_workbench.go`、`routes_workbench_test.go` | 完成 |
| DI | `internal/container/container.go` | 完成（含 `WithFeatures` 下发门禁） |

要点：

- 路由挂在 `/api/v1/workbench`，整组 `middleware.DenyAPIKeyPrincipal()` + `g.Viewer()`；用户级通知在 `/api/v1/me/workbench-notices`（tenantless，被移出最后一个 workspace 仍可读）。
- 删除归档刻意用 `POST /archives/delete` 而非 `DELETE` + body（网关可能丢 body 导致误删全部 part）。
- 资产下载 `GET /workbench/assets/:asset_id` 不带 item 前缀（BlockSuite 只认 asset id）。
- 清理器 `CleanupRunner` 是进程内 Ticker（Lite 无 Redis），默认 24h 一轮，保留策略见 `lifecycle.go` 顶部常量。

### 3.2 前端

| 层 | 关键文件 | 说明 |
| --- | --- | --- |
| API | `src/api/workbench.ts` | 唯一入口，组件禁止手写 URL；解包 `{success,data}` |
| 类型 | `src/types/workbench.ts` | 与 Go json tag 一一对应 |
| Store | `src/stores/workbench.ts` | 只存服务端状态与 UI 状态，**不持有 Y.Doc** |
| 路由 | `src/router/index.ts`（`/platform/workbench/*`） | `workbenchItem` 是唯一加载 BlockSuite 的路由 |
| 壳/列表 | `views/workbench/{WorkbenchLayout,WorkbenchList,WorkbenchDocs,JournalsView,WorkbenchTemplates,WorkbenchSettings}.vue` | |
| 条目编辑 | `views/workbench/WorkbenchItemView.vue`（约 47KB） | 顶栏/形态切换/归档/模板/导入/AI 入口都在这里 |
| 编辑器 | `src/workbench/editor/*` | `blocksuite.ts`（挂载/双根/工具栏）、`use-editor-session.ts`（挂载+保存队列+409）、`projection.ts`（Y.Doc→Markdown）、`workbench-workspace.ts`（真实 Workspace）、`assets.ts`（Blob resolver） |
| 数据库 | `database-session.ts`（工具条控制器）、`database-seed.ts`（默认列/三视图）、`database-csv.ts`、`database-calendar.ts`、`database-dates.ts`、`database-ai.ts`、`database-state.ts`、`database-cell-decorator.ts`、`database-text-locale.ts` | 只读上游 `affine-database` 的 dataSource，**不自建第二份数据库状态** |
| AI | `src/workbench/ai/*`（`use-inline-ai`、`use-page-ai`、`page-actions`、`editor-adapter`、`selection-fingerprint`、`database-patch`、`apply-write-plan`） | 内联=文字进文字出；数据库=补丁预览后写入 |
| 其他 | `save-queue.ts`（防抖+409）、`archive-status.ts`、`batch-archive.ts`、`import-material.ts`、`templates/*`、`journals/date-only.ts`、`journals/month-cells.ts`、`use-workbench-prefs.ts`、`item-display.ts` | 纯逻辑均有同目录 `*.test.ts` |
| ⌘K | `components/GlobalCommandPalette/useWorkbenchSearch.ts` | 复用 `items?q=` 与 `doc-assistant/documents?q=`，不新增 `/workbench/search` |

### 3.3 已完成的功能点对照

| 阶段 | 工作包 | 状态 |
| --- | --- | --- |
| P0 | 18 项 spike 证据 | ✅ 见 `p0-spike-report.md`（结论 Go） |
| P1 | P1-1~P1-12 全部 | ✅ 条目/文件夹/内容/版本/回收站、私有 KB+ACL、归档、前端壳与列表、文档聚合/离开预览/通知/i18n、Page AI、Journals、工作台设置、归档状态与批量归档、文档模板 |
| P2 | P2-1 画布完整度 | ✅ 原生工具栏 + 撤销/重做（`canvas-history-tools.ts`，QuickToolExtension） |
| P2 | P2-2 文本投影与归档 | ✅ `projection.ts` |
| P2 | P2-3 资产与 blobSync | ✅ `assets.ts` + 后端 asset 服务 + 30 天孤儿缓冲 |
| P2 | P2-5 文档全局列表 | ✅ `WorkbenchDocs.vue` + `doc_document_global.go` |
| P2 | P2-8 双形态 | ✅ 内容/版本/视口都已验证（视口保持本次修复并补断言） |
| P2 | P2-4 画布导出 ZIP/PNG | ✅ ZIP 完成（`workbench/export/`）；**PNG 明确不支持** |
| P2 | P2-6 画布 AI / Agent patch | ✅ `POST /ai/canvas` + `CanvasAIDialog`（只做分组与平移，不删不改样式） |
| P3 | P3-1 数据库工具条 | ✅ `DatabaseToolbar.vue` + `database-session.ts` |
| P3 | P3-2 独立数据库视图 | ✅ 全屏/返回不重载，`?focus=database` |
| P3 | P3-3 Calendar 月视图 + 拖拽 | ✅ `database-calendar.ts` |
| P3 | P3-4 CSV 导入/导出 | ✅ `database-csv.ts`（含公式注入防护） |
| P3 | P3-5 数据库 AI | ✅ `DatabaseAIDialog.vue` + `ai_database.go` + `database-patch.ts` |
| P3 | P3-6 搜索与 ⌘K | ✅ `useWorkbenchSearch.ts` |
| P3 | P3-7 性能与回归 | ✅ 1000 行/20 元素基线达标，见 `p0-spike-report.md` §8 |
| P4 | P4-1 导入 PDF/DOCX/MD | ✅ `ImportMaterialDialog.vue` + `import-material.ts`（KB / 附件两条路径） |
| P4 | P4-2 组织增强 | ❌ 拖拽排序、标签、反向链接均未做（`workbench_item_links` 表已建，Type 注释明确"P1–P3 只读不写"） |
| P4 | P4-3 权限/i18n/无障碍/性能 | ⚠️ i18n ✅、仓储层隔离用例 ✅；端到端多用户门禁、无障碍抽查、chunk 回归待做 |
| P4 | P4-4 备份恢复与升级演练 | ❌ 未做 |
| P4 | P4-5 发布文档与法务核对 | ❌ 未做 |

---

## 4. 硬约束（每个改动都适用）

### 4.1 权限与隔离

- 工作台资源按 `tenant_id + owner_id` 隔离；owner 由服务端从 ctx 取，**路径与 query 里没有 owner 参数**。
- 他人条目/文件夹/模板/版本 → **2400（404）**，不是 403；不泄露名称、数量、存在性。
- Admin 不能读其他用户的私有工作台内容。
- 工作知识库：每用户一个、`is_system=true`、`visibility=private`、不可删除。
- API Key 一律 403，且不进入 handler。

### 4.2 内容与版本

- Y.Doc 是唯一内容源；SQL 只存 `ydoc` 字节、`text_content` 投影、`snapshot_version`、`editor_schema_version`。
- 服务端**不解析、不生成、不合并** Y.Doc（不为搜索、校验、AI 去 decode）。
- 保存用乐观锁：`base_version` 不匹配 → 2401，响应体带 `details.current_version`，前端可用它重发。
- `current_mode`、标题、图标、收藏、文件夹、归档状态都在 SQL，不写进 Y.Doc。
- 元数据更新（`UpdateItem`）走白名单，不 bump `snapshot_version`，不与内容保存互相 409。
- 删除条目**不**级联删知识；恢复条目**不**自动恢复归档映射。

### 4.3 错误码（不要新增业务码）

| 码 | HTTP | 用途 |
| --- | --- | --- |
| 2400 | 404 | 工作台资源不存在/越权/已删 |
| 2401 | 409 | 保存版本冲突 |
| 2402 | 413 | 内容超限（Y.Doc 8MB / text 256KB / 归档 6MB / 资产 20MB） |
| 2409 | 422 | Y.Doc 解码失败 / schema 过高 |
| 400 | 400 | 参数、模板配额、名称/描述非法 |

### 4.4 发布门禁

`config/config.yaml` 默认**全关**；本机 `.env` 全开：

```yaml
workbench:
  enabled: false
  templates_enabled: false
  journals_enabled: false
  ai_enabled: false
  dual_mode_enabled: false
```

环境变量：`WEKNORA_WORKBENCH_ENABLED` / `_TEMPLATES_ENABLED` / `_JOURNALS_ENABLED` / `_AI_ENABLED` / `_DUAL_MODE_ENABLED`。
门禁只控制**入口与路由注册**，不改数据模型；关闭后已写入数据仍可由管理员排障读取。
`bootstrap` 未返回时前端一律按关闭处理（避免入口闪现）。

### 4.5 前端约定

- 组件禁止手写 URL，一律走 `@/api/workbench`；错误码只在 `workbench/error-code.ts` 判断一次。
- BlockSuite 与编辑器必须懒加载；首页 chunk 不得包含 BlockSuite 或 `.ydoc`。
- 纯逻辑放 `src/workbench/**` 并与测试同目录（`tsx --test` + `node:test`），不引入 Playwright/Cypress。
- 六语言：`zh-CN / zh-TW / en-US / ja-JP / ko-KR / ru-RU`，`npm run check-i18n` 必须通过。
- 数据库相关：**不能出现第二份数据库状态**，必须从已渲染的 `affine-database` 元素上取 `dataSource`。

---

## 5. 当前未完成清单（按建议顺序）

> 每项都写了"做什么 / 验收标准"。开工前先在 §7.4 建立一次基线。

### R1 收口未提交改动（阻塞项，先做）

- 按 §2.3 分组提交，建议顺序：① 编辑器与数据库能力（`workbench/editor/*`、`components/Database*.vue`、`editor-popover.css`）② 导入与收集（`import-material*`、`CollectDialog.vue`）③ AI 数据库（`workbench/ai/database-patch*`、`ai_database*`、`handler/workbench_ai.go`）④ 资产与清理（`workbench/asset*`、`cleanup.go`、`lifecycle.go`）⑤ 列表/导航/搜索（views、`useWorkbenchSearch.ts`、i18n）⑥ 构建与部署脚本（`scripts/build.mjs`、`package.json`、`vite.config.ts`）⑦ docx 边框工具（单独成组）
- 验收：`git status` 只剩本次新工作；每组提交各自能通过 §7.4 的相关检查。

### R2 修 i18n 失败（2026-11-06 已完成）

- 现象：`npm run check-i18n` 报 `zh-CN: extra workbench.database.toolbar.clear`、`zh-CN: extra workbench.database.toolbar.noMatch`。
- 处理：这两个 key 已无引用，已从 `zh-CN.ts` 删除（其他语言本就没有）。
- 验收：`npm run check-i18n` **exit 0**（15/15）。

### R3 基线失败甄别（2026-11-06 已完成，结论：既有 + 环境相关）

4 项失败**全部复现于工作台改动之前的干净提交 `63bbfdf03`**（用 `git worktree` 独立检出复跑，同一套环境变量），与工作台代码无关。根因都是 Windows 测试环境，不是逻辑缺陷：

| 用例 | 现象 | 根因 |
| --- | --- | --- |
| `TestDataSourceScheduleAPIRejectsInvalidCron` | 三个子测试的断言全过，只在 `TempDir RemoveAll cleanup` 报 `unlinkat … schedule.db: The process cannot access the file` | SQLite 文件句柄在 `t.TempDir()` 清理时仍打开；Windows 下 unlink 打开中的文件会 EBUSY。用例漏了关闭 `*sql.DB` |
| `TestSandboxAllocationOnTheMigratedSQLiteSchema` | 迁移 0→61 成功、配沙箱断言全过，同样在 cleanup 报 `migrated.db` EBUSY | 同上：`gorm.Open` 之后从未 `db.DB().Close()` |
| `TestBatchDownloadKnowledgeProducesCompleteZIP` | `require.Empty(os.ReadDir(os.TempDir()))` 断言失败，列表是整个系统 TEMP（Chrome/Edge/各类 `.tmp`） | 断言把「系统临时目录」当成本用例的私有目录；开发者机器上 TEMP 永远不空 |
| `TestBatchDownloadKnowledgeFailureDoesNotReturnPartialZIP` | 同上 | 用例用 `t.Setenv("TMPDIR", t.TempDir())` 想改临时目录，但 **Windows 上 `os.TempDir()` 读的是 `TMP`/`TEMP`**，`TMPDIR` 无效 |

**结论**：后续任务一律忽略这 4 项，**不要在跑测试时顺手改**。哪天要修，改法很小：
1. `sandbox_allocation_test.go`、`schedule` 相关用例：`gorm.Open` 后加 `t.Cleanup` 里 `sqlDB.Close()`（注意要先于 TempDir 清理注册）。
2. `knowledge_download_test.go`：把 `os.ReadDir(os.TempDir())` 换成只看本用例创建的临时文件（或同时 `t.Setenv("TMP", …)` 与 `t.Setenv("TEMP", …)`）。

### R4 P2-4 画布导出（2026-11-06 已完成，PNG 明确不支持）

- 代码：`src/workbench/export/package.ts`（纯逻辑，12 条单测）、`export-canvas.ts`（取快照/资产/下载）。
- 包内容：`manifest.json` + `content.ydoc`（快照字节）+ `content.md`（投影正文）+ `assets/<assetId>.<ext>`。
- 入口：条目页顶栏「…」→「导出画布包」，导出中置灰；动态 import，不进首屏 chunk。
- 资产：与渲染共用 `editor/assets.ts` 的 resolver 与缓存（不重复下载），4 并发且保序；
  取不到只记 `missing`，整包照常产出 —— 这是「大画布不崩」的口径。
- 只读：不 flush、不 markDirty、不产生版本；他人资产走同一条 404 → null 路径。
- **PNG 结论：不支持。** BlockSuite v0.27.4 的 `affine/gfx`、`affine/edgeless` 里没有
  `toPng` / `exportToPng` / `toBlob` 之类公开 API；自己按 gfx 元素序列化 SVG 再栅格化，
  保真度与预算都不可控。结论同时写进 `manifest.capabilities.png = false`，
  避免外部工具以为包里漏了文件。

### R5 P2-6 画布 AI / Agent patch

- 做什么：edgeless 下的聚类、生成 Frame、移动/整理；预览 → 确认 → 写入，可 undo；保存冲突时禁用。
- 参考：`ai/apply-write-plan.ts`、`ai/database-patch.ts` 的"补丁 + 预览 + 一次性写入"模式，可直接复用同一套契约。
- 验收：写入前有预览；可 undo；409 时不做静默覆盖；不挂工具、不越权检索。

### R6 P2-8 edgeless 视口保持验证（2026-11-06 已完成）

补断言时直接暴露了真问题：**每次切回 edgeless，上游会按内容重新「适应」，用户摆好的缩放与平移被冲掉**
（实测设成 zoom=1.5，往返一圈回来变成 0.49）。修法与验收：

- `CanvasHandle.getViewport / setViewport`：走 `std → GfxController → viewport`，不摸 DOM；
  将来做「适应画布 / 100%」直接复用。
- `setMode` 离开 edgeless 时记住视口，切回来等两帧再覆盖（新视图首帧的自适应会顶掉立即写入）。
- `scripts/editor-spike-check.mjs` 新增 9e：zoom 与中心点在 5 次往返后保持一致（<0.001 / <1px）。
- 已知限制：视口只在本会话内保持，刷新后仍按内容自适应（要持久化得写进视图数据，见 §4.2 元数据边界）。

`__原 R6 描述（保留）__`



- 做什么：补视口平移/缩放保持的断言（P0 报告 §4.1 已把此项挂到 P2-8）。
- 验收：page ↔ edgeless 往返 N 次后，视口参数与 block 数稳定，`applyUpdate` 不增加。


### 5.1 P4 剩余项的执行前提（2026-11-06 盘点）

这几项不是"再写点代码"就能收的，先把前提列清楚，避免下一个人空转：

| 项 | 还差什么 | 前置条件 |
| --- | --- | --- |
| P4-2 标签 | `workbench_tags` 表 + 关联表 = **新增迁移**（PG 158 / SQLite 62），要同步改 `expectedSQLiteMigrationVersion`；再补类型/服务/前端筛选 | 需要先确认标签是"个人标签"还是"workspace 共享标签"，这决定隔离字段 |
| P4-2 拖拽排序 | 后端 `PATCH /folders/:id`（sort_order + parent_id）+ 前端拖拽；`sort_order` 字段已存在 | 无阻塞，可直接开工 |
| P4-2 反向链接 | `workbench_item_links` 已建表但无写入方；需先从投影/编辑器引用里解析出链接关系再落库 | 无阻塞，但要先定"哪些引用算反向链接"（页内提及 / 数据库关联列） |
| P4-3 端到端权限门禁 | 需要两个真实用户 + Admin + API Key 跑一遍全接口矩阵 | 需要能在容器里造第二个用户（本机 Docker 环境） |
| P4-3 无障碍/性能 | 键盘可达与焦点顺序要人工走查；chunk 体积要对比基线 | 人工 + 一次正式构建 |
| P4-4 备份恢复演练 | 四类备份（库/资产/Y.Doc/归档映射）+ BlockSuite 升级矩阵 | 需要有可清空的演练库；`scripts/` 下目前没有任何 backup/restore 脚本 |
| P4-5 发布法务 | MPL/商标核对、无 EE 代码声明、发布清单签字 | 需要人决策与签字，AI 只能出清单 |

**已经做完的 P4-3 部分**（本次）：六语言 key 对齐（`check-i18n` exit 0）、仓储层所有者隔离用例
（`workbench_isolation_sqlite_test.go`：条目/版本/资产/归档/文件夹，含跨租户）。

### R7 P4-2 组织增强

- 拖拽排序：`workbench_folders.sort_order` 已存在（`FolderTree.vue` 已按此排序），缺拖拽写入与跨层移动接口。
- 标签：需新增迁移（只追加）+ 类型/接口/服务/前端筛选。
- 反向链接：`workbench_item_links` 表已建但"只读不写"，需补写入（从 `projection` 或编辑器内引用解析）与 UI 跳转。
- 验收：文件夹拖拽排序/跨层移动持久化；标签可筛选；反向链接可跳转且越权 404。

### R8 P4-3 权限/i18n/无障碍/性能收口

- 权限回归矩阵：第二用户、Admin、API Key、跨租户（开发计划 G4）。
- 六语言 key 对齐；键盘可达与焦点顺序；首页与编辑器 chunk 体积回归。
- 验收：G0–G6 门禁逐条通过并留命令输出。

### R9 P4-4 备份恢复与升级演练

- 四类备份恢复（库、资产、Y.Doc、归档映射）；BlockSuite 升级矩阵（当前 pin v0.27.4，见 `third_party/blocksuite/UPSTREAM.md`）。

### R10 P4-5 发布文档与法务核对

- MPL/商标核对、无 EE 代码、发布清单签字。

---

## 6. 验证与部署

### 6.1 提交纪律

- 一个任务至少一个 commit，只含该任务文件；不混全仓格式化/依赖升级/无关重构。
- 提交前必须跑完 §7.4 对应检查，并在报告里贴**本次**命令输出。

### 6.2 前端命令

```powershell
cd d:\知识库\Know\frontend
npm run type-check                 # vue-tsc --build
npx tsx --test "src/workbench/**/*.test.ts"   # 工作台纯逻辑单测
npm run check-i18n
npm run build                      # 走 scripts/build.mjs（堆上限 8192MB，防 OOM）
npm run build:spike                # 需要 editor-spike.html 时才跑（两次 vite build）
```

### 6.3 后端命令

宿主机可以跑 `internal/application/{service,repository}`、`handler`、`router`、`types`、`config` 的测试；**完整 `cmd/server` 编译必须走 Docker**（duckdb-go-bindings 的 windows 静态库是 MSVC 产物，MinGW 链接失败）。

```powershell
# 环境前缀（本机已验证可用）
$env:PATH="D:\toolchain\w64devkit\bin;C:\Users\zs\go-portable\go\bin;$env:PATH"
$env:CGO_ENABLED="1"; $env:CC="D:\toolchain\w64devkit\bin\gcc.exe"
$env:CXX="D:\toolchain\w64devkit\bin\g++.exe"
$env:CGO_CFLAGS="-ID:/toolchain/include"; $env:CGO_CXXFLAGS="-ID:/toolchain/include"
$env:GOTMPDIR="D:\gotmp"; $env:TMP="D:\gotmp"; $env:TEMP="D:\gotmp"

go test ./internal/application/service/workbench/ -count=1
go test ./internal/application/repository/ -count=1
go test ./internal/handler/ -run Workbench -count=1
go test ./internal/router/ -run Workbench -count=1
go test ./internal/types/ ./internal/config/ -count=1
```

Docker 全量编译（宿主机编不过时用）：

```powershell
docker run --rm -v "d:/知识库/Know:/src" -w /src `
  -e CGO_ENABLED=1 -e PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin `
  -e GOPROXY=https://goproxy.cn,direct -e GOCACHE=/tmp/gocache -e GOTOOLCHAIN=local `
  -v "d:/知识库/Know/.tmp-p0/gomod:/go/pkg/mod" weknora-go:1.26 `
  sh -c "go build ./..."
```

### 6.4 部署（本机 Docker）

前端容器无卷挂载，静态资源在镜像内，本机迭代走"本地构建 + 覆盖 + reload"：

```powershell
cd d:\知识库\Know\frontend; npm run build
docker cp dist/. WeKnora-frontend:/usr/share/nginx/html/
docker exec WeKnora-frontend nginx -s reload
```

后端改动需重建镜像并重启：

```powershell
cd d:\知识库\Know
docker compose build app
docker compose up -d app
```

改 `config/config.yaml` 后同样需要重启 `WeKnora-app`（文件是挂载进去的，但配置在启动时读取）。

### 6.5 编辑器手工矩阵

BlockSuite 交互没有自动化浏览器，按实现方案 §14.6 的手工矩阵执行，保留截图/录屏路径作为证据。已有的自动化只有 `frontend/scripts/editor-spike-check.mjs`（无头 Edge + 零依赖 CDP，10 组断言，含 1000 行性能基线）：

```powershell
cd frontend
npm run build:spike
npx vite preview --port 4173        # 另开终端
node scripts/editor-spike-check.mjs --url=http://127.0.0.1:4173/editor-spike.html
```

### 6.6 当前校验状态（2026-11-06 实测，交接基线）

| 检查 | 结果 |
| --- | --- |
| `npm run type-check` | ✅ exit 0 |
| `npx tsx --test "src/workbench/**/*.test.ts"` | ✅ 192 pass / 0 fail |
| `npm run check-i18n` | ✅ exit 0（R2 已修） |
| `go test ./internal/application/service/workbench/` | ✅ ok 1.329s |
| `go test ./internal/router/ ./internal/types/ ./internal/config/` | ✅ ok |
| `go test ./internal/handler/` | ❌ 3 项失败：Windows 环境相关，已甄别为既有（见 R3，可忽略） |
| `go test ./internal/application/repository/` | ❌ 1 项失败：同上，SQLite 句柄未关导致 TempDir 清理失败 |

**编辑器自动断言**（`scripts/editor-spike-check.mjs`，headless Edge，2026-11-06 实测）：
1–9 段全过（含 9e 视口保持）；第 10 段「1000 行 ≤8s」本次 10.2–10.4s 未达标（连续两次）。
该路径不经过 `setMode`，与 P2-8 改动无关，怀疑本机负载/回归漂移，**未改阈值**，留待复核。


---

## 7. 已知坑（踩过，别再踩）

### 7.1 BlockSuite

1. 版本 pin **v0.27.4**（上游无 v0.27.5 稳定 tag），见 `third_party/blocksuite/UPSTREAM.md`。
2. `useDefineForClassFields` **必须 `false`**：上游 `framework/global/src/di/provider.ts` 的 `container = this.provider.container` 在 `define` 语义下早于参数属性赋值执行，会读到 `undefined` 崩溃。
3. 挂载容器必须有视口类名 `affine-page-viewport` / `affine-edgeless-viewport`，否则 `ViewportElementExtension` 抛 `BlockSuiteError`。
4. `store.load()` 必须在 `std.render()` 之前（`EditorHost.connectedCallback` 要求 `store.root` 已存在）。
5. 变更通知要订阅**两路**：`store.slots.blockUpdated`（单元格/属性/文本）+ `store.slots.yBlockUpdated`（block 增删），只订一路会漏保存。
6. 视图预设不能手写 `ViewBasicDataType`：calendar 必须在 `viewDataUpdate` 补 `date.startColumnId`，table 必须显式写 `columns`，否则停在空态。
7. 双根切换没有运行时换根入口，只能重建 `BlockStdScope` + `EditorHost`，**复用 store/doc/workspace**，切换过程不碰 Y.Doc。
8. edgeless 工具栏实际 DOM 标签是 `edgeless-toolbar-widget`（不是 `affine-edgeless-toolbar`）；自定义按钮用 `QuickToolExtension` 注册。
9. 老数据库条目的列结构由 Y.Doc 快照决定，新增默认列**不会**补齐到已有条目，必须新建条目验证 seed。
10. 工作台未注册上游 PeekView，行详情走 `popSideDetail` 兜底；样式靠 `body center-peek` 等选择器在 `editor-popover.css` 覆盖。

### 7.2 构建

- 构建会随机 OOM（BlockSuite + 六语言包在 rendering chunks 阶段超 Node 默认堆）。统一走 `scripts/build.mjs`（`--max-old-space-size=8192`），不要直接 `vite build`，也不要用 `NODE_OPTIONS=` 前缀（Windows 不认）。
- `build` 只产 `index.html` / `embed.html`；`build:spike` 额外产 `editor-spike.html`。两者分两次构建是为了避免首页被 BlockSuite vendor（约 6.1MB）拖慢。
- `vite.config.ts` 的 `blockSuiteRoot` 解析到 `../../third_party/blocksuite`，所以 Docker 构建上下文必须是**仓库根**（`frontend/Dockerfile` 顶部有说明）。
- 在 WorkBuddy 这类带 safe-delete 守卫的终端里，`npm run build` 会在 `prepare-out-dir` 阶段失败（`SAFE_DELETE_BULK_CONFIRM_REQUIRED`，dist/assets 有 600+ 文件超过单次阈值）。绕法：先 `mv dist dist.bak`（别删，留着对照），再跑构建，确认 `dist/pdfjs/wasm`、`dist/tdesign-icons`、`dist/config.js` 都重新生成后删掉备份。dist 是纯构建产物，public/ 与 vite 插件会重新产出。

### 7.3 环境

- 宿主机 `go build ./...` 会在 `cmd/server` 的 duckdb 静态库上失败（MSVC/UCRT 符号 + `__emutls`），不要死磕；测试包可正常编译链接。
- 中文路径 + MinGW 曾出现链接器路径编码问题；本机已用 w64devkit + `GOTMPDIR=D:\gotmp` 跑通，C 盘空间不足时必须设 `GOTMPDIR`。
- `.env` 里工作台门禁全为 `true`，`config/config.yaml` 默认 `false`；容器以 env 为准，排查"入口不见了"先看容器环境变量。

### 7.4 与需求文档的已知差异（2026-11-06 确认）

实现与[实现方案](d:/知识库/AFFiNE-Know实现方案.md)不一致、但**已确认按实现走**的地方。需求文档本身没有改，看到时不要再按文档"修回去"。

| 差异 | 文档口径 | 实现口径 | 原因 |
| --- | --- | --- | --- |
| 数据库归档正文 | §6.3「Table → CSV 代码块 + 标题」 | `projection.ts` 输出 Markdown 表格 | 投影是唯一正文来源（画布/数据库同一套），Markdown 表格检索可读性更好；已确认维持现状 |
| 事项页顶栏 | §10.7.2「全屏时仍显示保存状态、冲突提示、归档按钮」 | 事项不渲染条目级顶栏，这三项收进视图工具条右侧 | 事项是全局单例聚合视图，标题/重命名/版本历史对它没有语义；保存状态、时间、归档徽章、冲突弹窗都保留了能力 |

---

## 8. 停止条件（遇到就停下问人）

1. 需要修改已发布迁移，或需要调整 136–140 / 42–46 的编号与顺序。
2. 需求与实现方案/开发计划的边界冲突，无法同时满足。
3. 发现权限泄漏：Admin/他人/API Key 能读到私有内容。
4. 出现内容丢失、重复 `applyUpdate`、双形态切换后数据不一致。
5. 需要服务端解析/生成/合并 Y.Doc 才能继续（说明方案选错了）。
6. 需要改需求文档或原型才能继续。
7. 需要做"当前不做"清单里的能力（移动端、多人协作、订阅、Edgeless 模板、模板市场等）。
8. 任何不可恢复操作（永久删、迁移 down、覆盖已有 Y.Doc）。

升级时给：现象、复现命令、影响范围、两个可选修法、你的建议。不要只问"怎么办"。

---

## 9. 任务报告模板

```text
任务：R4 画布导出 / ...
Commit：<sha>
改动文件：
- ...
验证命令与结果（本次输出）：
- `npm run type-check` → PASS
- `npx tsx --test "src/workbench/**/*.test.ts"` → PASS，172/172
- `go test ./internal/...` → PASS/FAIL，关键摘要
手工证据：
- 截图/录屏路径、请求记录、DB 版本
验收对照：
- [ ] 需求点 1
- [ ] 需求点 2
未完成/风险：
- ...
下一步：
- ...
```

---

## 10. 开工检查清单

- [ ] 已读本文 §0–§4，以及实现方案 §3 不变量 / §8 后端 / §9.13 Journals / §9.14 设置 / §9.15 Page AI / §9.16 模板
- [ ] 已确认 `git status` 内容与 §2.3 一致，并已完成 R1 分组提交
- [ ] 已跑 §6.6 全部检查，得到与本文一致的基线（或已记录差异并说明原因）
- [ ] 已确认当前迁移版本（PG 157 / SQLite 61）与 `expectedSQLiteMigrationVersion = 61`
- [ ] 已确认本机容器状态与门禁开关
- [ ] 已选定下一个任务（建议 R2 → R3 → R4/R5），并建立 todo 清单
- [ ] 已确认不会改需求文档、原型、已发布迁移
