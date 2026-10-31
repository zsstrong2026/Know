package repository

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newJournalItem 造一条带日期的画布条目；date 为空串表示不钉日历（写 NULL）。
func newJournalItem(t *testing.T, db *workbenchRepository, date string, mode *string) *types.WorkbenchItem {
	t.Helper()
	now := time.Now().UTC()
	item := &types.WorkbenchItem{
		ID:                  uuid.NewString(),
		TenantID:            1,
		OwnerID:             "user-a",
		Kind:                types.WorkbenchKindCanvas,
		Title:               "日记",
		SnapshotVersion:     types.WorkbenchSnapshotVersionInitial,
		EditorSchemaVersion: types.SupportedEditorSchemaVersion,
		Visibility:          types.WorkbenchVisibilityPrivate,
		CurrentMode:         mode,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if date != "" {
		item.JournalDate = types.NewDateOnly(date)
	}
	require.NoError(t, db.CreateItem(t.Context(), 1, "user-a", item))
	return item
}

// 未钉日期的条目必须写 NULL；写空串会让 journal_date IS NOT NULL 成立。
func TestCreateItem_UnpinnedJournalDateIsNull(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}

	item := newJournalItem(t, repo, "", nil)

	var isNull bool
	require.NoError(t, db.Raw(
		`SELECT journal_date IS NULL FROM workbench_items WHERE id = ?`, item.ID).Scan(&isNull).Error)
	assert.True(t, isNull, "未钉日历时必须写 NULL，而不是空字符串")

	stored, err := repo.GetItemMeta(t.Context(), 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.False(t, stored.JournalDate.Valid)
	assert.Nil(t, stored.CurrentMode, "未显式设置形态时应保持 NULL")
}

// 钉了日期的条目：date 原样存取，current_mode 也一起返回。
func TestCreateAndReadItem_WithJournalDate(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}

	pageMode := types.WorkbenchModePage
	item := newJournalItem(t, repo, "2026-10-30", &pageMode)

	stored, err := repo.GetItemMeta(t.Context(), 1, "user-a", item.ID)
	require.NoError(t, err)
	require.True(t, stored.JournalDate.Valid)
	assert.Equal(t, "2026-10-30", stored.JournalDate.Date)
	require.NotNil(t, stored.CurrentMode)
	assert.Equal(t, types.WorkbenchModePage, *stored.CurrentMode)
}

// ListByJournalMonth 只返回落在区间的画布条目，按日期升序。
func TestListByJournalMonth_FiltersAndOrders(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}

	newJournalItem(t, repo, "2026-09-30", nil) // 上个月，不返回
	newJournalItem(t, repo, "2026-10-31", nil)
	newJournalItem(t, repo, "2026-10-01", nil)

	// 数据库条目即使钉了日期也不属于 Journals（§3.5 不变量 12）。
	dbItem := newJournalItem(t, repo, "2026-10-15", nil)
	require.NoError(t, db.Model(&types.WorkbenchItem{}).
		Where("id = ?", dbItem.ID).
		Update("kind", types.WorkbenchKindDatabase).Error)

	items, err := repo.ListByJournalMonth(
		t.Context(), 1, "user-a", "2026-10-01", "2026-10-31")
	require.NoError(t, err)
	require.Len(t, items, 2, "只返回本月且 kind=canvas 的条目")
	assert.Equal(t, "2026-10-01", items[0].JournalDate.Date, "必须按日期升序")
	assert.Equal(t, "2026-10-31", items[1].JournalDate.Date)
}

// UpdateItem 的 journal_date 三态：改期、取消钉（写 NULL）、不传（不改）。
func TestUpdateItem_JournalDateTriState(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	item := newJournalItem(t, repo, "2026-10-01", nil)
	ctx := t.Context()

	// 改期
	updated, err := repo.UpdateItem(ctx, 1, "user-a", item.ID,
		map[string]any{"journal_date": "2026-10-05"}, false)
	require.NoError(t, err)
	require.True(t, updated.JournalDate.Valid)
	assert.Equal(t, "2026-10-05", updated.JournalDate.Date)

	// 不传 journal_date：只改标题，日期保持不变
	updated, err = repo.UpdateItem(ctx, 1, "user-a", item.ID,
		map[string]any{"title": "改名"}, false)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-05", updated.JournalDate.Date, "未传 journal_date 时不得改动日期")
	assert.Equal(t, "改名", updated.Title)

	// 取消钉：传 nil 必须写成 SQL NULL
	updated, err = repo.UpdateItem(ctx, 1, "user-a", item.ID,
		map[string]any{"journal_date": nil}, false)
	require.NoError(t, err)
	assert.False(t, updated.JournalDate.Valid, "取消钉后必须读出 NULL")

	var isNull bool
	require.NoError(t, db.Raw(
		`SELECT journal_date IS NULL FROM workbench_items WHERE id = ?`, item.ID).Scan(&isNull).Error)
	assert.True(t, isNull)
}

// current_mode 只通过 UpdateItem 写入，且能与 journal_date 一起更新。
func TestUpdateItem_CurrentMode(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	item := newJournalItem(t, repo, "", nil)

	updated, err := repo.UpdateItem(t.Context(), 1, "user-a", item.ID,
		map[string]any{"current_mode": types.WorkbenchModePage}, false)
	require.NoError(t, err)
	require.NotNil(t, updated.CurrentMode)
	assert.Equal(t, types.WorkbenchModePage, *updated.CurrentMode)
}

// 白名单外的字段被丢弃：不能借 UpdateItem 改 snapshot_version。
func TestUpdateItem_RejectsNonWhitelistedFields(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	item := newJournalItem(t, repo, "", nil)

	_, err := repo.UpdateItem(t.Context(), 1, "user-a", item.ID,
		map[string]any{"snapshot_version": 99, "owner_id": "user-b"}, false)
	require.NoError(t, err)

	stored, err := repo.GetItemMeta(t.Context(), 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Equal(t, types.WorkbenchSnapshotVersionInitial, stored.SnapshotVersion)
	assert.Equal(t, "user-a", stored.OwnerID)
}

// ListItems 的 journal 过滤只返回钉了日期的画布条目。
func TestListItems_JournalFilter(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}

	newJournalItem(t, repo, "2026-10-01", nil) // 命中
	newJournalItem(t, repo, "", nil)           // 未钉日期，不命中

	journal := true
	items, _, err := repo.ListItems(t.Context(), 1, "user-a", types.WorkbenchItemFilter{
		Journal: &journal,
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.True(t, items[0].JournalDate.Valid)
}
