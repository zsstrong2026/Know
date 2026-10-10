# Wiki 页面批量移动 — 实施计划

- 日期：2026-10-10
- 设计规格：[Wiki页面批量移动设计.md](./Wiki页面批量移动设计.md)（已批准）

## 目标（Goal）

在知识库 Wiki 树形视图中提供「复选框批量模式」：勾选多个页面后一次拖拽、一次确认、一次请求，把整批页面移动到目标目录。后端新增批量移动接口 `PUT /api/v1/knowledgebase/:kb_id/wiki/move-pages`，前端 WikiBrowser 增加批量管理交互。

## 架构（Architecture）

- 后端：handler → service（`interfaces.WikiPageService`）→ repository 既有分层不变。批量接口复用单项 `MovePage` 的写入语义（`applyFolderToPage` + `normalizeWikiHierarchy` + `repo.UpdateMeta`，不 bump version），不新增仓储方法。
- Service 两阶段：**校验阶段整体拒绝**（任一 slug 不存在或目录无效 → 整批失败，对齐 batch-delete 先例）；**写入阶段逐页更新**，非事务，中途失败不回滚（已接受的限制）。
- 前端：WikiBrowser.vue 手工 div 树 + 原生 HTML5 DnD 基础上，新增 `batchMode`/`checkedSlugs` 状态，`DraggedItem` 扩展 `{ kind: 'pages'; slugs: string[] }` 变体，确认卡片整批一次确认。

## 技术栈（Tech Stack）

- 后端：Go + Gin + GORM；sqlite 内存库测试；swag 生成 Swagger 文档
- 前端：Vue 3 `<script setup>` + TypeScript + tdesign-vue-next；vue-tsc 类型检查

## 任务总览

| # | 任务 | 类型 | 验证 |
|---|------|------|------|
| 1 | 后端：批量请求类型 `WikiPageMoveBatchRequest` | 代码 | `go build ./internal/types/` |
| 2 | 后端：Service 接口签名 `MovePages` | 代码 | `go build ./internal/types/...` |
| 3 | 后端：4 个失败测试先行（TDD Red） | 测试 | 编译失败（预期） |
| 4 | 后端：`MovePages` 实现（TDD Green） | 代码 | 4 个测试全过 |
| 5 | 后端：Handler `MovePages` + 路由注册 | 代码 | `go build ./...` |
| 6 | 后端：Swagger 文档再生成 | 工具 | `make docs` + `git status` |
| 7 | 前端：API `moveWikiPages` | 代码 | `npm run type-check`（任务 12 统一跑） |
| 8 | 前端：5 语言 i18n 新键 | 代码 | 同上 |
| 9 | 前端：批量模式状态 + 工具栏开关 + 批量工具条 | 代码 | 同上 |
| 10 | 前端：页面行复选框 | 代码 | 同上 |
| 11 | 前端：`DraggedItem` 扩展 + 批量拖放/确认分支 | 代码 | 同上 |
| 12 | 前端：样式 + 统一 type-check | 代码 | `npm run type-check` 通过 |
| 13 | 全量验证 + 手动回归 | 验证 | 见清单 |

---

## 任务 1：后端 — 批量请求类型

**文件**：`internal/types/wiki_page.go`
**位置**：`WikiPageMoveRequest`（L469-475）之后插入

```go
// WikiPageMoveBatchRequest moves multiple pages into one folder in a single
// request. slugs is required and capped handler-side at 200 entries;
// duplicates are deduplicated server-side. folder_id empty means root.
type WikiPageMoveBatchRequest struct {
	Slugs    []string `json:"slugs" binding:"required"`
	FolderID string   `json:"folder_id"`
}
```

**验证**（仓库根目录 `d:\知识库\Know`）：

```powershell
go build ./internal/types/
```

预期：无输出，退出码 0。

---

## 任务 2：后端 — Service 接口签名

**文件**：`internal/types/interfaces/wiki_page.go`
**位置**：`MovePage` 声明（L205-207）之后插入

```go
	// MovePages relocates multiple pages into folderID ("" = root) in one
	// call. Validation is all-or-nothing (any unknown slug or folder rejects
	// the whole batch before any write); the write phase updates pages one
	// by one without a transaction. Returns the number of moved pages.
	MovePages(ctx context.Context, kbID string, slugs []string, folderID string) (int, error)
```

说明：仓库中没有 `var _ interfaces.WikiPageService = ...` 编译期断言，本步可独立编译通过；实现将在任务 4 补齐。

**验证**：

```powershell
go build ./internal/types/...
```

预期：无输出，退出码 0。

---

## 任务 3：后端 — 失败测试先行（TDD Red）

**文件**：`internal/application/service/wiki_page_test.go`
**位置**：文件末尾（`TestMovePageIntoTypeLabelNamedFolder` 结束之后，L708 之后）追加 4 个测试。测试写法对齐该文件既有模式（sqlite 内存库 + 真实 repository + 真实 service）。

```go
func TestMovePagesMovesAllIntoFolder(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))

	ctx := context.Background()
	repo := repository.NewWikiPageRepository(db)
	svc := NewWikiPageService(repo, nil, nil, nil, nil)
	now := time.Now()

	folder, err := svc.CreateFolder(ctx, "kb-batch", 1, types.WikiFolderRootID, "概念")
	require.NoError(t, err)
	for _, seed := range []struct{ id, slug, title string }{
		{"page-a", "concept/a", "甲"},
		{"page-b", "concept/b", "乙"},
	} {
		require.NoError(t, repo.Create(ctx, &types.WikiPage{
			ID: seed.id, TenantID: 1, KnowledgeBaseID: "kb-batch", Slug: seed.slug,
			Title: seed.title, PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
			Version: 1, CreatedAt: now, UpdatedAt: now,
		}))
	}

	// 请求中的重复 slug 会被去重，两个页面都落入目录
	moved, err := svc.MovePages(ctx, "kb-batch", []string{"concept/a", "concept/b", "concept/a"}, folder.ID)
	require.NoError(t, err)
	require.Equal(t, 2, moved)

	for _, slug := range []string{"concept/a", "concept/b"} {
		stored, err := repo.GetBySlug(ctx, "kb-batch", slug)
		require.NoError(t, err)
		require.Equal(t, folder.ID, stored.FolderID)
		require.Equal(t, types.StringArray{"概念"}, stored.CategoryPath)
		require.Equal(t, 1, stored.Depth)
	}
}

func TestMovePagesRejectsWholeBatchOnUnknownSlug(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))

	ctx := context.Background()
	repo := repository.NewWikiPageRepository(db)
	svc := NewWikiPageService(repo, nil, nil, nil, nil)
	now := time.Now()

	folder, err := svc.CreateFolder(ctx, "kb-batch", 1, types.WikiFolderRootID, "概念")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, &types.WikiPage{
		ID: "page-a", TenantID: 1, KnowledgeBaseID: "kb-batch", Slug: "concept/a",
		Title: "甲", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}))

	_, err = svc.MovePages(ctx, "kb-batch", []string{"concept/a", "concept/missing"}, folder.ID)
	require.ErrorIs(t, err, repository.ErrWikiPageNotFound)

	// 校验整体拒绝：已知页面必须保持原位
	stored, err := repo.GetBySlug(ctx, "kb-batch", "concept/a")
	require.NoError(t, err)
	require.Empty(t, stored.FolderID)
}

func TestMovePagesRejectsUnknownFolder(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))

	ctx := context.Background()
	repo := repository.NewWikiPageRepository(db)
	svc := NewWikiPageService(repo, nil, nil, nil, nil)
	now := time.Now()

	require.NoError(t, repo.Create(ctx, &types.WikiPage{
		ID: "page-a", TenantID: 1, KnowledgeBaseID: "kb-batch", Slug: "concept/a",
		Title: "甲", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}))

	_, err = svc.MovePages(ctx, "kb-batch", []string{"concept/a"}, "folder-does-not-exist")
	require.ErrorIs(t, err, repository.ErrWikiFolderNotFound)

	stored, err := repo.GetBySlug(ctx, "kb-batch", "concept/a")
	require.NoError(t, err)
	require.Empty(t, stored.FolderID)
}

func TestMovePagesToRootClearsFolder(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))

	ctx := context.Background()
	repo := repository.NewWikiPageRepository(db)
	svc := NewWikiPageService(repo, nil, nil, nil, nil)
	now := time.Now()

	folder, err := svc.CreateFolder(ctx, "kb-batch", 1, types.WikiFolderRootID, "概念")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, &types.WikiPage{
		ID: "page-a", TenantID: 1, KnowledgeBaseID: "kb-batch", Slug: "concept/a",
		Title: "甲", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}))

	// 先移入目录，再整批移回根目录
	_, err = svc.MovePage(ctx, "kb-batch", "concept/a", folder.ID)
	require.NoError(t, err)
	moved, err := svc.MovePages(ctx, "kb-batch", []string{"concept/a"}, "")
	require.NoError(t, err)
	require.Equal(t, 1, moved)

	stored, err := repo.GetBySlug(ctx, "kb-batch", "concept/a")
	require.NoError(t, err)
	require.Empty(t, stored.FolderID)
	require.Empty(t, stored.CategoryPath)
	require.Equal(t, 0, stored.Depth)
	require.Equal(t, "concept/甲", stored.WikiPath)
}
```

**验证**：

```powershell
go test ./internal/application/service/ -run TestMovePages -v
```

预期：**编译失败**，报 `svc.MovePages undefined`（TDD Red，证明测试真实引用了未实现的方法）。

---

## 任务 4：后端 — `MovePages` 实现（TDD Green）

**文件**：`internal/application/service/wiki_page.go`
**位置**：`MovePage` 实现（L1604-1623）之后、`RenameOrMoveFolder` 注释（L1625）之前插入

```go
// MovePages relocates multiple pages into folderID ("" = root) in one call.
// Validation is all-or-nothing: any unknown slug or unresolvable folder
// rejects the whole batch before anything is written. The write phase mirrors
// MovePage page-by-page via UpdateMeta (bookkeeping-only, no version bump); it
// is intentionally not transactional, so a mid-batch failure leaves earlier
// pages already moved (accepted limitation, see design doc).
func (s *wikiPageService) MovePages(
	ctx context.Context, kbID string, slugs []string, folderID string,
) (int, error) {
	folderID = strings.TrimSpace(folderID)

	// 去重并剔除空 slug，保持首次出现顺序
	seen := make(map[string]struct{}, len(slugs))
	unique := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		unique = append(unique, slug)
	}
	if len(unique) == 0 {
		return 0, repository.ErrWikiPageNotFound
	}

	// 阶段一：先校验全部，再写任何一条
	if folderID != "" {
		if _, err := s.repo.GetFolderByID(ctx, kbID, folderID); err != nil {
			return 0, err
		}
	}
	pages := make([]*types.WikiPage, 0, len(unique))
	for _, slug := range unique {
		page, err := s.repo.GetBySlug(ctx, kbID, slug)
		if err != nil {
			return 0, err
		}
		pages = append(pages, page)
	}

	// 阶段二：逐页写入，语义与单项 MovePage 完全一致
	for _, page := range pages {
		page.FolderID = folderID
		if err := s.applyFolderToPage(ctx, page); err != nil {
			return 0, err
		}
		page.UpdatedAt = time.Now()
		normalizeWikiHierarchy(page)
		if err := s.repo.UpdateMeta(ctx, page); err != nil {
			return 0, fmt.Errorf("move wiki page: %w", err)
		}
	}
	return len(pages), nil
}
```

说明：
- `s.repo.GetFolderByID` 在目录不存在时返回 `repository.ErrWikiFolderNotFound`（仓储层 sentinel），直接透传即可被 handler 映射为 404。
- `s.repo.GetBySlug` 同理透传 `repository.ErrWikiPageNotFound`。
- 该文件已 import `repository`、`strings`、`time`、`fmt`，无需新增 import。

**验证**：

```powershell
go test ./internal/application/service/ -run TestMovePages -v
```

预期：4 个测试全部 `PASS`（TDD Green）。

```powershell
go test ./internal/application/service/ -run 'TestMovePage|TestNormalizeWikiHierarchy' -v
```

预期：既有相关测试不受影响，全部 `PASS`。

---

## 任务 5：后端 — Handler `MovePages` + 路由注册

**文件 1**：`internal/handler/wiki_page.go`
**位置**：`MovePage` handler（L292-314）之后、`writeWikiFolderError`（L316）之前插入

```go
// maxWikiMoveBatchSize 限制单次批量移动的页面数，对齐知识库批量删除的 200 上限先例。
const maxWikiMoveBatchSize = 200

// MovePages godoc
// @Summary      Move multiple wiki pages into a folder
// @Description  Relocate a batch of pages (identified by slugs in the body) into one folder (folder_id empty = root) in a single request; validation is all-or-nothing and cached category paths are recomputed
// @Tags         Wiki
// @Accept       json
// @Produce      json
// @Param        kb_id  path  string  true  "Knowledge base ID"
// @Param        move   body  types.WikiPageMoveBatchRequest true "Batch move target"
// @Success      200  {object}  map[string]int
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/wiki/move-pages [put]
func (h *WikiPageHandler) MovePages(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var req types.WikiPageMoveBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	if len(req.Slugs) > maxWikiMoveBatchSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Too many slugs in one request (limit 200)"})
		return
	}
	moved, err := h.wikiService.MovePages(c.Request.Context(), kbID, req.Slugs, strings.TrimSpace(req.FolderID))
	if err != nil {
		writeWikiFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"moved": moved})
}
```

说明：
- `binding:"required"` 作用于切片时，`slugs` 缺失或为空数组会绑定失败 → 400。
- 该文件已 import `http`、`strings`、`types`、`gin`，无需新增 import。

**文件 2**：`internal/router/routes_knowledge.go`
**位置**：L306 `move-page` 路由之后插入一行

```go
		wiki.PUT("/move-pages", g.OwnedWikiKBOrAdmin(), g.KBAccessWrite("kb_id"), wikiHandler.MovePages)
```

**验证**：

```powershell
go build ./...
```

预期：无输出，退出码 0。

---

## 任务 6：后端 — Swagger 文档再生成

**命令**（仓库根目录）：

```powershell
make docs
```

若环境无 `make`，直接执行等价命令：

```powershell
swag init -g ./cmd/server/main.go -o ./docs --parseDependency --parseInternal
```

**验证**：

```powershell
git status --short docs/
```

预期：`docs/docs.go`、`docs/swagger.json`、`docs/swagger.yaml` 有变更，且包含 `move-pages` 路径。

---

## 任务 7：前端 — API `moveWikiPages`

**文件**：`frontend/src/api/wiki/index.ts`
**位置**：`moveWikiPage`（L173 附近）之后插入

```ts
// moveWikiPages relocates multiple pages into folderId ("" = root) in one request.
export function moveWikiPages(kbId: string, slugs: string[], folderId: string) {
  return put(`/api/v1/knowledgebase/${kbId}/wiki/move-pages`, { slugs, folder_id: folderId })
}
```

**验证**：随任务 12 统一执行 `npm run type-check`。

---

## 任务 8：前端 — i18n 新键（5 语言）

在以下 5 个文件中，定位 `knowledgeEditor.wikiBrowser` 块内 `moveConfirmTitle:` 所在行，在其后插入对应 6 个键（若某语言缺少该锚点，则改在 `moveConfirm:` 行后插入）：

**`frontend/src/i18n/locales/zh-CN.ts`**：

```ts
      batchManage: '批量管理',
      selectedCount: '已选 {count} 项',
      clearSelection: '清空选择',
      moveConfirmBatch: '将 {count} 个页面移动到「{target}」？',
      movePagesSuccess: '已移动 {count} 个页面',
      movePagesFailed: '批量移动失败',
```

**`frontend/src/i18n/locales/en-US.ts`**：

```ts
      batchManage: 'Batch manage',
      selectedCount: '{count} selected',
      clearSelection: 'Clear selection',
      moveConfirmBatch: 'Move {count} pages to "{target}"?',
      movePagesSuccess: 'Moved {count} pages',
      movePagesFailed: 'Batch move failed',
```

**`frontend/src/i18n/locales/ja-JP.ts`**：

```ts
      batchManage: '一括管理',
      selectedCount: '{count} 件選択中',
      clearSelection: '選択をクリア',
      moveConfirmBatch: '{count} 件のページを「{target}」に移動しますか？',
      movePagesSuccess: '{count} 件のページを移動しました',
      movePagesFailed: '一括移動に失敗しました',
```

**`frontend/src/i18n/locales/ko-KR.ts`**：

```ts
      batchManage: '일괄 관리',
      selectedCount: '{count}개 선택됨',
      clearSelection: '선택 지우기',
      moveConfirmBatch: '페이지 {count}개를 「{target}」(으)로 이동하시겠습니까?',
      movePagesSuccess: '페이지 {count}개를 이동했습니다',
      movePagesFailed: '일괄 이동에 실패했습니다',
```

**`frontend/src/i18n/locales/ru-RU.ts`**：

```ts
      batchManage: 'Пакетное управление',
      selectedCount: 'Выбрано: {count}',
      clearSelection: 'Очистить выбор',
      moveConfirmBatch: 'Переместить страницы ({count}) в «{target}»?',
      movePagesSuccess: 'Перемещено страниц: {count}',
      movePagesFailed: 'Не удалось выполнить пакетное перемещение',
```

**验证**：随任务 12 统一执行 `npm run type-check`。

---

## 任务 9：前端 — 批量模式状态 + 工具栏开关 + 批量工具条

**文件**：`frontend/src/views/knowledge/wiki/WikiBrowser.vue`

**改动 A（script，状态与操作）**：在拖拽区注释 `// --- Drag-and-drop: move a page or folder into a folder ------------------`（L2247）之前插入

```ts
// --- Batch selection mode -------------------------------------------------
// batchMode toggles checkboxes on page rows; checkedSlugs holds the staged
// selection (by slug, so it survives tree reloads). Exiting the mode clears
// the selection; a successful batch move clears it too, while a failed move
// keeps it so the user can retry.
const batchMode = ref(false)
const checkedSlugs = ref<Set<string>>(new Set())

function toggleBatchMode() {
  batchMode.value = !batchMode.value
  if (!batchMode.value) checkedSlugs.value = new Set()
}

function togglePageChecked(slug: string) {
  const next = new Set(checkedSlugs.value)
  if (next.has(slug)) next.delete(slug)
  else next.add(slug)
  checkedSlugs.value = next
}

function clearPageSelection() {
  checkedSlugs.value = new Set()
}

// 批量模式下点击页面行 = 切换勾选；否则保持原有的打开页面行为
function onPageRowClick(page: WikiPage) {
  if (batchMode.value) togglePageChecked(page.slug)
  else selectPage(page)
}
```

**改动 B（template，工具栏开关）**：在「新建页面」tooltip（L258-263）之后插入

```html
                  <t-tooltip v-if="props.canEdit" :content="$t('knowledgeEditor.wikiBrowser.batchManage')" placement="top">
                    <button type="button" class="wiki-tab-bar-action"
                      :class="{ 'wiki-tab-bar-action--active': batchMode }"
                      :disabled="sidebarViewMode !== 'tree' || sidebarViewSwitching || sidebarTabSwitching"
                      :aria-label="$t('knowledgeEditor.wikiBrowser.batchManage')"
                      @click.stop="toggleBatchMode">
                      <t-icon name="check-double" />
                    </button>
                  </t-tooltip>
```

**改动 C（template，批量工具条）**：在标签栏容器闭合（L265 `</div>`）与 `<!-- Active-tab list -->`（L267）之间插入

```html
              <div v-if="batchMode && sidebarViewMode === 'tree'" class="wiki-batch-bar">
                <span class="wiki-batch-bar-count">
                  {{ $t('knowledgeEditor.wikiBrowser.selectedCount', { count: checkedSlugs.size }) }}
                </span>
                <t-button variant="text" theme="default" size="small" :disabled="checkedSlugs.size === 0"
                  @click.stop="clearPageSelection">
                  {{ $t('knowledgeEditor.wikiBrowser.clearSelection') }}
                </t-button>
              </div>
```

**验证**：随任务 12 统一执行 `npm run type-check`。

---

## 任务 10：前端 — 页面行复选框

**文件**：`frontend/src/views/knowledge/wiki/WikiBrowser.vue`
**位置**：页面行模板（L330-338）整体替换为

```html
                    <div v-else
                      :class="['wiki-page-item', 'wiki-page-item--tree', { active: selectedPage?.id === item.page.id, 'wiki-page-item--checked': batchMode && checkedSlugs.has(item.page.slug) }]"
                      :style="{ '--wiki-tree-depth': item.depth }" :title="item.page.title"
                      :draggable="!batchMode || checkedSlugs.has(item.page.slug)"
                      @click="onPageRowClick(item.page)" @dragstart="onPageDragStart($event, item.page)"
                      @dragend="onPageDragEnd">
                      <input v-if="batchMode" type="checkbox" class="wiki-page-checkbox"
                        :checked="checkedSlugs.has(item.page.slug)" @click.stop
                        @change="togglePageChecked(item.page.slug)" />
                      <t-icon :name="getPageIcon(item.page)"
                        :class="['wiki-page-file-icon', `wiki-page-file-icon--${item.page.page_type}`]" />
                      <span class="wiki-page-item-title">{{ item.page.title }}</span>
                    </div>
```

交互语义：
- 非批量模式：行为与现状完全一致（点击打开、可拖拽）。
- 批量模式：点击行/复选框切换勾选；未勾选的行不可拖拽，拖动已勾选行 = 拖动整批。

**验证**：随任务 12 统一执行 `npm run type-check`。

---

## 任务 11：前端 — `DraggedItem` 扩展 + 批量拖放/确认分支

**文件**：`frontend/src/views/knowledge/wiki/WikiBrowser.vue`

**改动 A（script，API import）**：在 `@/api/wiki` import 块（L814-838）中，`moveWikiPage,`（L820）之后插入一行

```ts
  moveWikiPages,
```

**改动 B（script，`DraggedItem` 类型扩展）**：L2252-2254 替换为

```ts
type DraggedItem =
  | { kind: 'page'; page: WikiPage }
  | { kind: 'pages'; slugs: string[] }
  | { kind: 'folder'; folderId: string; path: string[] }
```

**改动 C（script，`onPageDragStart` 批量分支）**：L2272-2275 替换为

```ts
function onPageDragStart(e: DragEvent, page: WikiPage) {
  // 批量模式下拖动已勾选行 = 拖动整批选择
  if (batchMode.value && checkedSlugs.value.has(page.slug)) {
    draggedItem.value = { kind: 'pages', slugs: [...checkedSlugs.value] }
  } else {
    draggedItem.value = { kind: 'page', page }
  }
  primeDragData(e)
}
```

**改动 D（script，`onDropOnDirectory` no-op 过滤）**：L2315-2341 替换为

```ts
// findLoadedPageBySlug 在已加载的树行中按 slug 查页面。批量选择本来就来自
// 可见行，因此该查找覆盖整批；查不到时按非 no-op 处理（保守保留在批次中）。
function findLoadedPageBySlug(slug: string): WikiPage | null {
  for (const row of activeTreeRows.value) {
    if (row.kind === 'page' && row.page.slug === slug) return row.page
  }
  return null
}

function onDropOnDirectory(e: DragEvent, folderId: string, path: string[]) {
  let item = draggedItem.value
  draggedItem.value = null
  dropTargetKey.value = ''
  if (!item) return
  const targetPath = path.join('/')

  if (item.kind === 'pages') {
    // 过滤掉本就在目标目录下的页面（no-op）；整批都是 no-op 时直接跳过确认
    const remaining = item.slugs.filter((slug) => {
      const page = findLoadedPageBySlug(slug)
      const current = (page ? pageCategoryPath(page) : null) || []
      return current.join('/') !== targetPath
    })
    if (remaining.length === 0) return
    item = { kind: 'pages', slugs: remaining }
  } else if (item.kind === 'page') {
    // No-op when the page already lives in this folder.
    if ((pageCategoryPath(item.page) || []).join('/') === targetPath) return
  } else {
    // Folder reparent. Reject no-op (same parent) and the illegal move of a
    // folder into itself or one of its descendants.
    const sourcePath = item.path.join('/')
    if (sourcePath === targetPath) return
    if (folderId === item.folderId) return
    if (targetPath === sourcePath || targetPath.startsWith(`${sourcePath}/`)) {
      MessagePlugin.warning(t('knowledgeEditor.wikiBrowser.moveFolderIntoSelf'))
      return
    }
  }

  const targetLabel = path.length > 0
    ? path[path.length - 1]
    : t('knowledgeEditor.wikiBrowser.rootFolderLabel')
  pendingMove.value = { item, folderId, targetLabel, x: e.clientX, y: e.clientY }
}
```

**改动 E（script，`confirmPendingMove` 批量分支）**：在 L2353 `if (item.kind === 'page') {` 之前插入

```ts
  if (item.kind === 'pages') {
    try {
      await moveWikiPages(props.knowledgeBaseId, item.slugs, folderId)
      MessagePlugin.success(t('knowledgeEditor.wikiBrowser.movePagesSuccess', { count: item.slugs.length }))
      // 成功：清空选择并退出批量模式
      checkedSlugs.value = new Set()
      batchMode.value = false
      await reloadDirectoryForType(activeTab.value)
    } catch (e) {
      console.error('Failed to batch move wiki pages:', e)
      // 失败：保留勾选与批量模式，便于修正后重试
      MessagePlugin.error(t('knowledgeEditor.wikiBrowser.movePagesFailed'))
    }
    return
  }
```

**改动 F（template，确认卡片批量文案）**：确认卡片 body（L776-778）替换为

```html
          <div class="anchored-form-popup-body">
            <template v-if="pendingMove.item.kind === 'pages'">
              {{ $t('knowledgeEditor.wikiBrowser.moveConfirmBatch', { count: pendingMove.item.slugs.length, target: pendingMove.targetLabel }) }}
            </template>
            <template v-else>
              {{ $t('knowledgeEditor.wikiBrowser.moveConfirm', { target: pendingMove.targetLabel }) }}
            </template>
          </div>
```

**验证**：随任务 12 统一执行 `npm run type-check`。

---

## 任务 12：前端 — 样式 + 统一 type-check

**文件**：`frontend/src/views/knowledge/wiki/WikiBrowser.vue`
**位置**：`<style>` 中 `.wiki-tab-bar-action { ... }` 规则（L5036-5039）之后追加

```css
.wiki-tab-bar-action--active {
  color: var(--td-brand-color);
  background: var(--td-brand-color-light);
}

.wiki-batch-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 2px 8px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
  border-bottom: 1px solid var(--td-component-border);
}

.wiki-page-checkbox {
  margin-right: 4px;
  flex-shrink: 0;
}

.wiki-page-item--checked {
  background: var(--td-brand-color-light);
}
```

**验证**（工作目录 `d:\知识库\Know\frontend`）：

```powershell
npm run type-check
```

预期：vue-tsc 无错误输出，退出码 0。

---

## 任务 13：全量验证 + 手动回归

**自动验证**（仓库根目录）：

```powershell
go test ./internal/application/service/ -run TestMovePages -v
go build ./...
```

预期：4 个批量测试 `PASS`，构建成功。

**手动回归清单**（启动前后端后，在知识库 Wiki 树形视图中逐项核对）：

1. 点击工具栏「批量管理」→ 页面行出现复选框，标签栏下方出现批量工具条（已选计数 + 清空选择）；再次点击退出，复选框消失。
2. 勾选 2-3 个页面，拖动其中任一已勾选行到某目录 → 确认卡片显示「将 N 个页面移动到「目标」？」→ 确认 → toast「已移动 N 个页面」，树刷新后页面到位，批量模式自动退出。
3. 勾选中含有「本就在目标目录」的页面并拖到该目录 → 该页被过滤，仅移动其余页面（toast 计数为过滤后的数量）。
4. 勾选页面全部已在目标目录 → 拖放后不弹确认卡片。
5. 拖动已勾选行到列表空白处 → 确认后移动到根目录。
6. 弹出确认卡片后点「取消」→ 不移动，勾选保留。
7. 后端返回错误（如批次中含已删除 slug）→ 错误 toast，勾选与批量模式保留，可修正后重试。
8. 回归：非批量模式单项拖动页面、单项拖动文件夹的行为与现状一致。
9. 权限：以非 KB owner 的 Contributor 身份调用 `PUT /api/v1/knowledgebase/:kb_id/wiki/move-pages` → 403。
10. 图标核对：工具栏「批量管理」按钮图标（`check-double`）正常显示；若 tdesign 无此图标则替换为 `edit`。

---

## 自查记录

- [x] 规格覆盖：设计文档改动表 8 项（types / interfaces / handler / router / service / swagger / api-client / WikiBrowser+i18n）均有对应任务；失败保留勾选语义落在任务 11 改动 E。
- [x] 无占位符：所有代码块为可直接应用的完整代码。
- [x] 类型一致性：前后端契约一致（`{ slugs, folder_id }` → `{ moved }`）；`DraggedItem` 新变体在 drop/confirm/模板三处使用一致。
- [x] 接口兼容性：`interfaces.WikiPageService` 新增方法不影响既有测试 fake（`fakeWikiPageService` 等均内嵌该接口，自动继承新方法，无需改动）。
