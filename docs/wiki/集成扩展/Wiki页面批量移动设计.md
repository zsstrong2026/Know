# Wiki 页面批量移动设计

日期：2026-10-10
状态：已确认（方案 A：前端复选框批量模式 + 后端批量移动接口）

## 1. 背景与目标

Wiki 知识库的树形视图中，概念页面常被生成到错误目录。当前仅支持单页拖拽移动，且每次拖放都需单独确认，批量调整时操作繁琐。

**目标**：支持复选框批量选中页面后一次拖动、一次确认、一次请求完成移动。

**明确不做（YAGNI）**：

- 文件夹批量移动（文件夹移动需重算整棵子树路径，代价高，仍走单项拖拽）
- 同级排序（`sort_order` 字段已预留但无 API 使用，本期不引入）
- 全选/反选、跨目录框选
- 仓储层批量 UPDATE（≤200 次单行更新，与现有 `recomputePagesForFolders` 风格一致）
- 撤销功能

## 2. 现状关键事实

- 树为 `WikiBrowser.vue` 手工渲染的 div 列表 + 原生 HTML5 拖拽，无第三方树组件；UI 库为 tdesign-vue-next
- `DraggedItem` 联合类型仅支持单项（page | folder），见 `WikiBrowser.vue` L2252
- drop 后暂存 `pendingMove`，就地确认卡片（L769-789）确认后才调 API
- 单项移动接口：`PUT /api/v1/knowledgebase/:kb_id/wiki/move-page`，body `{ slug, folder_id }`，`folder_id=""` 表示根目录
- 后端 `wikiPageService.MovePage` 为单 slug 实现：取页 → 改 FolderID → `applyFolderToPage` 重算缓存路径 → `normalizeWikiHierarchy` → `repo.UpdateMeta`（簿记写入，不 bump version）
- 批量操作先例：`POST /knowledge/batch-delete` 上限 200 条（`internal/handler/knowledge.go` L1449），整体校验、整体拒绝
- service 测试方式：sqlite 内存库 + 真实 repository + 真实 service（参照 `TestMovePageIntoTypeLabelNamedFolderKeepsHierarchy`，`wiki_page_test.go` L667）

## 3. 前端设计（`frontend/src/views/knowledge/wiki/WikiBrowser.vue`）

### 3.1 批量模式开关

- 树视图工具栏（新建文件夹/新建页面按钮旁）新增"批量管理"切换按钮，仅 `props.canEdit` 且树视图模式下可见
- 开启后：页面行行首出现复选框；文件夹行不变
- 再次点击按钮或按 Esc 退出批量模式，退出时清空选择

### 3.2 选择状态

- `batchMode = ref(false)`；`checkedSlugs = ref<Set<string>>(new Set())`
- 批量模式下点击页面行 = 切换勾选（不打开页面）；勾选行高亮
- 工具条显示"已选 N 项 · 清空"

### 3.3 拖拽行为

- `DraggedItem` 扩展：`{ kind: 'pages'; slugs: string[] }`
- 批量模式下拖动已勾选页面 → 整批入 `draggedItem`；拖动未勾选页面 → 维持现有单项拖动；非批量模式行为完全不变
- drop 时 no-op 过滤：已在目标文件夹的页面从批次剔除；剔除后为空则直接返回（沿用单项语义）
- drop 高亮复用现有 `dropTargetKey`，不改

### 3.4 确认卡片

- 复用就地确认卡片；批量文案："将 N 个页面移动到「目标」？"
- 一次确认 → 一次 `moveWikiPages` 请求 → 成功 toast → 清空选择、退出批量模式 → `reloadDirectoryForType` 刷新树
- 移动失败：toast 报错，保留勾选与批量模式，便于修正后重试

### 3.5 API 封装（`frontend/src/api/wiki/index.ts`）

```ts
// moveWikiPages relocates multiple pages into folderId ("" = root) in one request.
export function moveWikiPages(kbId: string, slugs: string[], folderId: string) {
  return put(`/api/v1/knowledgebase/${kbId}/wiki/move-pages`, { slugs, folder_id: folderId })
}
```

## 4. 后端设计

### 4.1 接口契约

`PUT /api/v1/knowledgebase/:kb_id/wiki/move-pages`

```jsonc
// 请求 body
{ "slugs": ["concept/a", "entity/b"], "folder_id": "" }  // ""=根目录
// 响应 200
{ "moved": 2 }
// 错误
// 400 参数错误 / slugs 为空 / 去重后超过 200 条
// 404 任一页面或目标文件夹不存在（整体拒绝，一个都不动）
// 409 冲突
```

权限与单项 move-page 完全一致：`OwnedWikiKBOrAdmin()` + `KBAccessWrite("kb_id")`。

### 4.2 改动点

| 层 | 文件 | 改动 |
|---|---|---|
| 类型 | `internal/types/wiki_page.go` | 新增 `WikiPageMoveBatchRequest { Slugs []string; FolderID string }`（紧邻 `WikiPageMoveRequest`） |
| 接口 | `internal/types/interfaces/wiki_page.go` | Service 接口新增 `MovePages(ctx, kbID string, slugs []string, folderID string) (int, error)` |
| Handler | `internal/handler/wiki_page.go` | 新增 `MovePages`：trim/去重/去空 → 空则 400 → 上限 200（对齐批量删除先例）→ 调 service → 复用 `writeWikiFolderError` 映射错误；补 swaggo 注释 |
| 路由 | `internal/router/routes_knowledge.go` | L306 后注册 `wiki.PUT("/move-pages", g.OwnedWikiKBOrAdmin(), g.KBAccessWrite("kb_id"), wikiHandler.MovePages)` |
| Service | `internal/application/service/wiki_page.go` | 新增 `MovePages`（两阶段，见 4.3） |
| 文档 | `docs/swagger.yaml/json` | `make docs` 重新生成 |

### 4.3 Service 两阶段逻辑

1. **校验阶段**：`folderID` 非空时 `GetFolderByID` 解析一次并预计算 categoryPath；逐个 `GetBySlug` 取出全部页面，任一缺失 → 返回 `ErrWikiPageNotFound`（404），一个都不动
2. **写入阶段**：逐页设置 `FolderID` + 预计算的 `CategoryPath` → `normalizeWikiHierarchy` → `repo.UpdateMeta`

与单项 `MovePage` 语义一致：簿记写入、不 bump version。写入阶段逐条执行（非事务，与 `recomputePagesForFolders` 现有风格一致）；写入中途出错返回 500，已完成部分不回滚——≤200 条单行 UPDATE，实际风险低，接受该限制。

### 4.4 i18n（zh-CN / en-US / ja-JP / ko-KR / ru-RU 五语言）

`knowledgeEditor.wikiBrowser` 下新增：

- `batchManage`：批量管理
- `selectedCount`：已选 {count} 项
- `clearSelection`：清空
- `moveConfirmBatch`：将 {count} 个页面移动到「{target}」？
- `movePagesSuccess`：已移动 {count} 个页面
- `movePagesFailed`：批量移动失败

## 5. 测试策略

### 5.1 后端（`internal/application/service/wiki_page_test.go`）

沿用 sqlite 内存库 + 真实 repo 方式，新增用例：

1. 批量移入文件夹：全部页面 FolderID / CategoryPath / WikiPath / Depth 正确
2. 批量移到根目录（`folder_id=""`）：CategoryPath 清空
3. 含不存在 slug：返回 404 错误，且其余页面未被改动（all-or-nothing 校验）
4. 目标文件夹不存在：返回 404

### 5.2 验证命令

- `go test ./internal/application/service/ -run TestMovePages`
- `go build ./...`
- 前端 `npm run type-check`
- 手动回归：批量勾选拖动（核心路径）、单项拖动、非批量模式、文件夹单项拖动
