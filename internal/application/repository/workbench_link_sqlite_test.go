package repository

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 反向链接（P4-2「页内提及」）的仓储层用例。
//
// 提及匹配是「正文 LIKE 标题」，最容易被误报击穿的就是单字标题——
// 「日」「周」这类字几乎必然出现在正文里，所以表结构约定单字不参与匹配。
const workbenchLinkDDL = `
CREATE TABLE IF NOT EXISTS workbench_item_links (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    owner_id VARCHAR(36) NOT NULL,
    source_item_id VARCHAR(36) NOT NULL,
    target_type VARCHAR(16) NOT NULL,
    target_id VARCHAR(36) NOT NULL,
    source_block_id VARCHAR(64),
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_wil_source
    ON workbench_item_links (tenant_id, source_item_id);
CREATE INDEX IF NOT EXISTS idx_wil_target
    ON workbench_item_links (tenant_id, target_type, target_id);
`

// newTestItemTitled 建一个指定标题的条目（提及匹配要靠标题区分）。
func newTestItemTitled(t *testing.T, db *gorm.DB, tenantID uint64, ownerID, title string) *types.WorkbenchItem {
	t.Helper()
	now := time.Now().UTC()
	item := &types.WorkbenchItem{
		ID:                  uuid.NewString(),
		TenantID:            tenantID,
		OwnerID:             ownerID,
		Kind:                types.WorkbenchKindCanvas,
		Title:               title,
		SnapshotVersion:     types.WorkbenchSnapshotVersionInitial,
		EditorSchemaVersion: types.SupportedEditorSchemaVersion,
		Visibility:          types.WorkbenchVisibilityPrivate,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	require.NoError(t, (&workbenchRepository{db: db}).CreateItem(t.Context(), tenantID, ownerID, item))
	return item
}

func setupWorkbenchLinkDB(t *testing.T) (*gorm.DB, *workbenchRepository) {
	t.Helper()
	db := setupWorkbenchTestDB(t)
	require.NoError(t, db.Exec(workbenchLinkDDL).Error)
	return db, &workbenchRepository{db: db}
}

func TestWorkbenchLink_MentionScanExcludesSingleCharAndSelf(t *testing.T) {
	db, repo := setupWorkbenchLinkDB(t)
	const tenant uint64 = 7

	plan := newTestItemTitled(t, db, tenant, "owner-a", "项目计划")
	weekly := newTestItemTitled(t, db, tenant, "owner-a", "周报")
	single := newTestItemTitled(t, db, tenant, "owner-a", "日")
	self := newTestItemTitled(t, db, tenant, "owner-a", "项目")

	// 正文里出现「项目计划」和「日」，没提「周报」；自己标题「项目」也命中但不该算
	text := "本周项目计划已更新，今天开始执行。"

	ids, err := repo.ListItemIDsMentioning(t.Context(), tenant, "owner-a", self.ID, text, 50)
	require.NoError(t, err)
	assert.Contains(t, ids, plan.ID, "完整标题命中")
	assert.NotContains(t, ids, weekly.ID, "没出现的标题不该命中")
	assert.NotContains(t, ids, single.ID, "单字标题不参与匹配（误报太多）")
	assert.NotContains(t, ids, self.ID, "自己提自己不算反向链接")
}

// 同名文档（通常来自同一模板自动命名）被标题命中时无法判断指向哪一篇，
// 不能算作提及；不同标题的提及仍然保留。
func TestWorkbenchLink_MentionScanExcludesSameTitle(t *testing.T) {
	db, repo := setupWorkbenchLinkDB(t)
	const tenant uint64 = 7

	source := newTestItemTitled(t, db, tenant, "owner-a", "会议纪要")
	twin := newTestItemTitled(t, db, tenant, "owner-a", "会议纪要")
	other := newTestItemTitled(t, db, tenant, "owner-a", "行动项清单")

	text := "会议纪要：负责人：戴格明（2026-11-16）。行动项清单见附件。"
	ids, err := repo.ListItemIDsMentioning(t.Context(), tenant, "owner-a", source.ID, text, 50)
	require.NoError(t, err)
	assert.NotContains(t, ids, twin.ID, "同名文档不算提及")
	assert.Contains(t, ids, other.ID, "不同标题的提及仍然保留")
}

func TestWorkbenchLink_ReplaceIsCoverAndScoped(t *testing.T) {
	db, repo := setupWorkbenchLinkDB(t)
	const tenant uint64 = 7

	source := newTestItem(t, db, tenant, "owner-a")
	first := newTestItem(t, db, tenant, "owner-a")
	second := newTestItem(t, db, tenant, "owner-a")

	itemTarget := func(id string) []types.WorkbenchLinkTarget {
		return []types.WorkbenchLinkTarget{{TargetType: types.WorkbenchLinkTargetItem, TargetID: id}}
	}

	require.NoError(t, repo.ReplaceItemLinks(t.Context(), tenant, "owner-a", source.ID, itemTarget(first.ID)))
	require.NoError(t, repo.ReplaceItemLinks(t.Context(), tenant, "owner-a", source.ID, itemTarget(second.ID)))

	links, err := repo.ListBacklinks(t.Context(), tenant, "owner-a", second.ID, 0)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, source.ID, links[0].ID)

	// 第一次写的 target 不残留
	links, err = repo.ListBacklinks(t.Context(), tenant, "owner-a", first.ID, 0)
	require.NoError(t, err)
	assert.Empty(t, links)
}

func TestWorkbenchLink_DeletedSourceDisappears(t *testing.T) {
	db, repo := setupWorkbenchLinkDB(t)
	const tenant uint64 = 7

	source := newTestItem(t, db, tenant, "owner-a")
	target := newTestItem(t, db, tenant, "owner-a")
	require.NoError(t, repo.ReplaceItemLinks(t.Context(), tenant, "owner-a", source.ID,
		[]types.WorkbenchLinkTarget{{TargetType: types.WorkbenchLinkTargetItem, TargetID: target.ID}}))

	// 引用方被删（软删）后，反向链接里不再出现
	require.NoError(t, repo.TrashItem(t.Context(), tenant, "owner-a", source.ID))

	links, err := repo.ListBacklinks(t.Context(), tenant, "owner-a", target.ID, 0)
	require.NoError(t, err)
	assert.Empty(t, links, "幽灵引用不能出现")
}

func TestWorkbenchLink_OwnerIsolation(t *testing.T) {
	db, repo := setupWorkbenchLinkDB(t)
	const tenant uint64 = 7

	source := newTestItem(t, db, tenant, "owner-a")
	target := newTestItem(t, db, tenant, "owner-a")
	require.NoError(t, repo.ReplaceItemLinks(t.Context(), tenant, "owner-a", source.ID,
		[]types.WorkbenchLinkTarget{{TargetType: types.WorkbenchLinkTargetItem, TargetID: target.ID}}))

	// owner-b 查不到 owner-a 的反向链接
	links, err := repo.ListBacklinks(t.Context(), tenant, "owner-b", target.ID, 0)
	require.NoError(t, err)
	assert.Empty(t, links)

	// owner-b 的提及扫描也扫不到 owner-a 的条目
	ids, err := repo.ListItemIDsMentioning(t.Context(), tenant, "owner-b", source.ID, "项目计划", 50)
	require.NoError(t, err)
	assert.Empty(t, ids)

	// 跨租户同样为空
	links, err = repo.ListBacklinks(t.Context(), 8, "owner-a", target.ID, 0)
	require.NoError(t, err)
	assert.Empty(t, links)
}
