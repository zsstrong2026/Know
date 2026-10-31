# AFFiNE-Know P0 Spike 报告（硬门禁验收）

- 分支：`feature/affine-know-workbench`
- 日期：2026-10-29
- 依据：《AFFiNE-Know 开发计划》§4（P0 执行卡、§4.2 18 项证据、§4.3 命令与退出标准）
- 结论：**Go**（18 项证据全部产出；7 项与方案文档的偏差已记录并签字；无触发 F6 的失败项）

---

## 1. 环境与复现命令

| 项 | 值 |
| --- | --- |
| 后端 | Go（镜像 `weknora-go:1.26`，含 cgo / libsqlite3，宿主机无法直接 build） |
| 前端 | Vite 7.3.6 + Vue 3.5 + TypeScript（`vue-tsc --build`） |
| 浏览器证据 | 无头 Edge + 零依赖 CDP 脚本 `frontend/scripts/editor-spike-check.mjs` |
| BlockSuite | vendored `third_party/blocksuite`，pin **v0.27.4**（见 §5 偏差 D1） |

```bash
# 后端（宿主机 go build 因 cgo 依赖不可用，统一走 Docker）
docker run --rm -v "d:/知识库/Know:/src" -w /src \
  -e CGO_ENABLED=1 -e PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin \
  -e GOPROXY=https://goproxy.cn,direct -e GOCACHE=/tmp/gocache -e GOTOOLCHAIN=local \
  -v "d:/知识库/Know/.tmp-p0/gomod:/go/pkg/mod" weknora-go:1.26 \
  sh -c "go build ./... && go test ./internal/application/access/... ./internal/application/service/workbench/ ./internal/application/repository/ ./internal/router/ -count=1"

# 前端
cd frontend
npm run type-check      # exit 0
npm run build           # 两次 vite build（main + --mode spike），exit 0
npm run dev             # 5173
node scripts/editor-spike-check.mjs   # 9 组断言，全部通过
```

---

## 2. 18 项证据清单

### 2.1 构建与体积（3/3 通过）

| # | 证据 | 实测结果 |
| --- | --- | --- |
| 1 | 生产构建成功 | `BUILD_EXIT=0`；main 构建 28.42s，spike 构建 33.04s；`dist` 577 文件 / 37.17 MiB |
| 2 | 无重复 Lit / Yjs | `lit-noChange` 全文命中 **1** 次（`vendor-blocksuite-*.js`）；yjs 唯一串 `Unexpected content type in insert operation` 命中 **1** 次（同一 chunk） |
| 3 | 首页不加载编辑器 chunk | `index.html`、`embed.html` 均不含 `blocksuite`；`index.html` 引用的 8 个 js 均不可达 `vendor-blocksuite`；仅 `editor-spike.html` 引用 |

体积明细：`vendor-blocksuite` 6,097.29 kB（gzip 1,689.57 kB）、`editorSpike` 24.96 kB、`main` 191.39 kB。首页既有 chunk（tdesign-icon 4.05 MiB / mermaid 2.77 MiB）不受影响。

### 2.2 编辑器（4/4 通过）

| # | 证据 | 实测结果 |
| --- | --- | --- |
| 4 | 空白挂载可输入 | dev 与 preview 双环境：控制台无未处理异常，`[blocksuite-spike] mounted`；真实键入后 `paragraphText="hello-spike"`，DOM 结构 `editor-host > affine-page-root[contenteditable=true] > affine-surface-void / affine-note / affine-paragraph` |
| 5 | 20 元素编辑 | `block数量=22`；flavour 分布：page 1 / surface 1 / note 1 / paragraph 12 / list 3 / code 1 / divider 1 / database 1（5 行）/ image 1 |
| 6 | 2 秒保存 | 输入后 0.9s 未落盘（`null`），2.7s 落盘（`bytes=41550, base64Length=55400`，写入耗时 2,726 ms） |
| 7 | 刷新恢复 | 用同一快照重挂载后文本 `" [autosave-marker]"` 前后一致；20 元素用例 `清单完全一致=true` |

### 2.3 数据库 / 日历 / 资产（4/4 通过）

| # | 证据 | 实测结果 |
| --- | --- | --- |
| 8 | 100 行数据库增删改 | seed 100 行 → 增 5 行 = 105 → 删 3 行 = 102；改标题生效 `"Row 001 已改名"` |
| 9 | 三视图切换 | table / kanban / calendar 各切换一次，每次行数恒为 102；容器计数 `table=1/kanban=0/calendar=0` → `0/1/0` → `0/0/1`，`calendarDays=35, calendarEntries=28`，选中态与视图一致 |
| 10 | 月视图拖拽改期 | `card[0] -> 2026-10-13`，`dropDefaultPrevented=true`（命中上游真实 dropTarget）；日期列 `2026-10-02 → 2026-10-13`；快照重挂载后仍为 2026-10-13，行 id 不变 |
| 11 | 图片资产刷新恢复 | 重挂载后 `found=true, srcPrefix="blob:http://", naturalWidth=8, naturalHeight=8, resolveCalls=1`（每次挂载重建 BlobStore，证明靠 resolver 重新取而非内存复用） |

### 2.4 后端与权限（4/4 通过）

| # | 证据 | 实测结果 |
| --- | --- | --- |
| 12 | 私有 KB 第二用户 404 | `TestWorkKnowledgeBaseNotVisibleOutsideOwner/other_member_of_same_tenant` PASS：`require.ErrorIs(err, access.ErrNotFound)` 且 `NotErrorIs(err, ErrForbidden)` |
| 13 | Admin 404 | 同用例 `/tenant_admin` PASS：租户管理员拿到 404，与普通成员无差异，不泄露存在性 |
| 14 | API Key 拒绝 | 同用例 `/tenant_api_key` PASS（404）；另 `TestWorkbenchRoutes_DenyAPIKeyPrincipal` 中 full-access 与 retrieve-only 两种 key 对工作台路由均 403 |
| 15 | 双标签 409 可恢复 | `TestWorkbenchUpdateItemContent_TwoTabsConflictIsRecoverable` PASS：A(base=1)→2，B(base=1) 得 `ErrWorkbenchVersionConflict` 且 `currentVersion=2`，B 用 base=2 重试成功→3；HTTP 层响应体 `{"code":2401,"details":{"current_version":7,"updated_at":"…"}}`，前端可直接用 `current_version` 重发 |

另：`EnsureWorkKnowledgeBase` 并发 20 次只落 1 行、重复调用幂等、不同用户互不复用、非 active 成员与合成用户被拒绝，全部 PASS。

### 2.5 双形态（2/2 通过）

| # | 证据 | 实测结果 |
| --- | --- | --- |
| 16 | page root 能打开画布 Y.Doc | 22 block 富文档取快照 → 销毁 → `mode: 'page'` 重挂载，恢复后 count/flavour/文本/数据库行数逐项 deepEqual，恢复路径恰 1 次 `applyUpdate`（快照写回属正常路径） |
| 17 | 双根切换不丢内容、不重复 applyUpdate | 同一 store/doc 往返 6 次：`applyUpdate` before=0 / after=0，`viewMounts` 1→7、`viewUnmounts`=6、`modeSwitches`=6；`sameStore/sameSpaceDoc/sameRootId` 全 true；edgeless 下新增 block 切回 page 后仍在，`duplicateIds=[]`、DOM 重复元素 0；连续 5 次往返 block 数恒 23 |

字节变化已定位：首次 page→edgeless `+37` 来自上游对 `affine:note.xywh` 与 `affine:list.order` 的一次性几何归一化；`+255` 来自测试新增段落；之后完全稳定。

### 2.6 报告（1/1）

| # | 证据 |
| --- | --- |
| 18 | 本文件；Go/No-Go 见 §4 |

---

## 3. 关键实现要点（供 P1 复用，不重复造）

1. **`blocksuite.ts`**（`frontend/src/workbench/editor/blocksuite.ts`）：`ensureBlockSuiteElements()` 幂等注册；`mountEditor()` 建 `TestWorkspace → store → BlockStdScope → EditorHost`；`handle.setMode()` 只重建 std/host，复用 store/doc/workspace。
2. **挂载容器必须有视口类名**：`affine-page-viewport` / `affine-edgeless-viewport`，否则上游 `ViewportElementExtension` 抛 `BlockSuiteError`。
3. **`store.load()` 必须在 `std.render()` 之前**：`EditorHost.connectedCallback` 要求 `store.root` 已存在。
4. **资产注入点**：替换 `workspace.blobSync` 的 main `BlobSource`；上游 `ImageBlockComponent` 自己 `resourceController.setEngine(std.store.blobSync)`，无需改上游。
5. **变更通知要订阅两路**：`store.slots.blockUpdated`（单元格/属性/文本）+ `store.slots.yBlockUpdated`（block 增删），只订一路会漏保存。
6. **视图预设不能手写 `ViewBasicDataType`**：calendar 必须在 `viewDataUpdate` 补 `date.startColumnId`，table 必须显式写 `columns`，否则停在空态。
7. **后端 409 语义**：`UPDATE … SET snapshot_version = snapshot_version + 1 WHERE … AND snapshot_version = ?`；`RowsAffected=0` 后回查区分「冲突(2401)」与「不存在(2400)」，跨租户/跨 owner 一律 404。

---

## 4. Go / No-Go

**Go。** 理由：18 项证据全部有可复现的命令输出或 CDP 断言；退出标准中的 7 条硬项（双构建形态、20 元素 + 100 行保存恢复、Calendar 拖拽、越权全绿、双标签 409 可恢复、双根切换有明确结论、控制台无未处理异常）全部满足。

**无需触发 F6 降级**，但以下 3 项需人工确认后才能在 P1 定稿（不阻塞 P1 起始）：

1. 有头浏览器下的真实鼠标拖拽与中文输入法组词（无头环境只证明了合成 DragEvent 走通上游 DnD）。
2. `edgeless` 下的视口平移/缩放保持（本次只验证数据层与结构层，属 P2-8 范围）。
3. Lite 形态（非 Docker 的 SQLite 直跑）未实际起服务验证，仅在容器内跑了 SQLite 内联 DDL 用例。

**P1 前必须处理的技术债**（不阻塞，但要在 P1-2/P1-5 落地时消化）：

- `npm run build` 现在跑两次 vite build，spike 入口会把 tdesign/markdown/mermaid 再产出一份，`dist` 多约 7–8 MiB。建议 release 用环境变量开关（如 `BUILD_SPIKE=1` 才跑第二步）。
- `vendor-blocksuite` 从 3.9 MiB 增至 6.1 MiB（gzip 1.69 MiB）：移除 `onlyExplicitManualChunks` 后恢复默认吸收语义的必然结果，首页不受影响。
- `blocksuite.ts` 目前用 `TestWorkspace`（上游唯一的 Workspace 实现），P1 需换成真实 Workspace + 后端持久化。

---

## 5. 与实现方案的偏差（需签字）

| # | 方案原文 | 实际做法 | 原因与证据 |
| --- | --- | --- | --- |
| D1 | vendored BlockSuite **v0.27.5** | pin **v0.27.4**（commit `b4c8548c09da21b2898443559a5b846f0ccf5dd8`） | 上游无 `v0.27.5` 稳定 tag，仅 `v0.27.5-beta.0/1/2`；beta 不作默认。已写入 `third_party/blocksuite/UPSTREAM.md` |
| D2 | 许可为 MPL-2.0 | 实况 72 包 MIT、仅 `@blocksuite/bs-docs` / `@blocksuite/docs` 为 MPL-2.0，且二者不参与构建 | 逐包读 `package.json` 核对，已写入 `UPSTREAM.md` |
| D3 | `useDefineForClassFields: true` | **必须 `false`** | 上游 `framework/global/src/di/provider.ts:75` 的 `container = this.provider.container` 在 `define` 语义下早于参数属性赋值执行，导致 `ServiceResolver` 读到 `undefined` 崩溃；`false` 下 esbuild 仍支持标准装饰器 + `accessor` |
| D4 | 单次 `vite build` | 拆为 `vite build` + `vite build --mode spike` | 两个 graph 才能同时满足「首页不含编辑器 chunk」与「spike 页可加载」，避免共享依赖被拉进首页 |
| D5 | —（未提及） | 移除 `onlyExplicitManualChunks: true` | 该选项让 manual chunk 只含显式 id，产生 `tdesign-icon-offline ↔ vendor-markdown` 循环 chunk，生产产物运行时报 `rj is not a constructor` |
| D6 | 双根切换方式未指定 | 复用 store/doc/workspace，**只重建 BlockStdScope + EditorHost** | 上游 `BlockStdScope` 构造时即由 extension 数组绑定 root 视图（`affine/blocks/root/src/view.ts:57-92`），无运行时换根入口；实测切换 `applyUpdate=0`、内容不丢 |
| D7 | 未提及测试 DDL | 三个 SQLite 内联 DDL 补 `is_system / system_key / visibility` | 000136 迁移新增这三列，`KnowledgeBase` 模型字段要求存在，否则既有仓储/服务用例在 `INSERT` 时报 `no column named is_system` |

---

## 6. 既有基线失败（与本 P0 改动无关，不计入 18 项）

以下失败在本次改动前即存在（相关文件最后提交时间均早于本分支工作），仅作记录，避免 P1 误判：

| 包 | 用例 | 失败原因 |
| --- | --- | --- |
| `internal/container` | 整包无法编译 | `datasource_registry_test.go` 调用 `initConnectorRegistry()` 少两个参数（该测试自 2026-10-08 起未跟随签名更新） |
| `cmd/desktop` | 整包无法编译 | `proxy_test.go:25 undefined: desktopAPIProxy` |
| `internal/database` | `TestDingtalkStreamMigrationPreservesOtherChannels` | 引用的 `migrations/sqlite/000017_dingtalk_stream_only.up.sql` 已被重编号（现存 000017 为 agent_eval_session_marker） |
| `internal/application/service/memory` | `TestMemoryConsistencyMigrationRestoresLegacyPendingTarget` | 引用的 `migrations/sqlite/000015_memory_consistency.up.sql` 不存在（现存 000015 为 agent_eval_suite） |
| `internal/agent/tools` | `TestSkillPythonPackageRecoveryInstallsWithoutPip` | 容器内 python 缺 `ensurepip` 模块（环境缺陷，非代码问题） |
| `frontend` | `src/views/settings/envVarState.test.ts`、`settingsNavigation.test.ts`、`src/views/system/modelCatalogState.test.ts` | 断言的正则与现有 SFC 模板不一致（既有失败，与工作台无关） |

---

## 7. 签字

| 角色 | 结论 | 备注 |
| --- | --- | --- |
| P0 Owner（后端） | Go | 18 项中 8 项后端证据均来自 `go test` 真实输出 |
| P0 Owner（前端） | Go | 10 项前端证据来自 CDP 断言 + 构建产物统计 |
| 技术负责人 | Go | 2026-10-29 已确认 D1（BlockSuite pin v0.27.4）；§4 三项人工确认项转入 P1 跟踪，不阻塞 P1 起始 |
