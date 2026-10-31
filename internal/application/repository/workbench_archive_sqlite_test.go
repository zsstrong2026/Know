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

// workbenchArchiveDDL 内联 000136 的 workbench_archives 表与部分唯一索引。
// SQLite 从 3.8 起支持部分索引，因此这里能真实复刻 uq_wa_live_part 的语义。
const workbenchArchiveDDL = `
CREATE TABLE IF NOT EXISTS workbench_archives (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    owner_id VARCHAR(36) NOT NULL,
    kb_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    source_type VARCHAR(16) NOT NULL,
    source_id VARCHAR(36) NOT NULL,
    source_version INTEGER NOT NULL,
    part_no INTEGER NOT NULL DEFAULT 1,
    part_count INTEGER NOT NULL DEFAULT 1,
    title TEXT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    archived_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_wa_live_part
    ON workbench_archives (tenant_id, owner_id, source_type, source_id, part_no)
    WHERE status <> 'deleted';
`

func setupWorkbenchArchiveTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupWorkbenchTestDB(t)
	require.NoError(t, db.Exec(workbenchArchiveDDL).Error)
	return db
}

func newArchiveRow(partNo, partCount int) *types.WorkbenchArchive {
	now := time.Now().UTC()
	return &types.WorkbenchArchive{
		ID:            uuid.NewString(),
		TenantID:      1,
		OwnerID:       "user-a",
		KBID:          "kb-work",
		KnowledgeID:   uuid.NewString(),
		SourceType:    types.WorkbenchSourceItem,
		SourceID:      "item-1",
		SourceVersion: 3,
		PartNo:        partNo,
		PartCount:     partCount,
		Title:         "标题",
		ArchivedAt:    now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

// 同一 part 并发插入：唯一索引只放行一条，另一条返回 ErrArchiveInProgress。
func TestCreatePending_UniqueIndexBlocksConcurrentPart(t *testing.T) {
	db := setupWorkbenchArchiveTestDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()

	first := newArchiveRow(1, 1)
	created, err := repo.CreatePending(ctx, first)
	require.NoError(t, err)
	require.NotNil(t, created)

	second := newArchiveRow(1, 1)
	existing, err := repo.CreatePending(ctx, second)
	require.ErrorIs(t, err, types.ErrArchiveInProgress,
		"同一 live part 重复插入必须返回 ErrArchiveInProgress")
	require.NotNil(t, existing)
	assert.Equal(t, first.ID, existing.ID, "冲突时应回查既有 live 行")
}

// 已 deleted 的 part 不占唯一索引，可以重新插入（重建归档的路径）。
func TestCreatePending_AllowsReinsertAfterDeleted(t *testing.T) {
	db := setupWorkbenchArchiveTestDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()

	first := newArchiveRow(1, 1)
	_, err := repo.CreatePending(ctx, first)
	require.NoError(t, err)

	affected, err := repo.MarkDeletedBySourcePart(ctx, 1, "user-a", types.WorkbenchSourceItem, "item-1", 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)

	recreated := newArchiveRow(1, 1)
	_, err = repo.CreatePending(ctx, recreated)
	require.NoError(t, err, "已有 deleted 行时同一 part 可以重新插入")
}

// ActivatePart 落库 active 与最新版本；GetLiveByPart 能查到它。
func TestActivatePart_UpdatesVersionAndStatus(t *testing.T) {
	db := setupWorkbenchArchiveTestDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()

	row := newArchiveRow(1, 2)
	created, err := repo.CreatePending(ctx, row)
	require.NoError(t, err)

	require.NoError(t, repo.ActivatePart(ctx, created.ID, created.KnowledgeID, 7, "新标题"))

	live, err := repo.GetLiveByPart(ctx, 1, "user-a", types.WorkbenchSourceItem, "item-1", 1)
	require.NoError(t, err)
	require.NotNil(t, live)
	assert.Equal(t, types.WorkbenchArchiveActive, live.Status)
	assert.Equal(t, 7, live.SourceVersion)
	assert.Equal(t, "新标题", live.Title)
}

// MarkDeletedByKB 只影响 owner 匹配且 kb_id 匹配的 live 映射。
func TestMarkDeletedByKB_ScopedToOwner(t *testing.T) {
	db := setupWorkbenchArchiveTestDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()

	mine := newArchiveRow(1, 1)
	_, err := repo.CreatePending(ctx, mine)
	require.NoError(t, err)

	other := newArchiveRow(1, 1)
	other.OwnerID = "user-b"
	_, err = repo.CreatePending(ctx, other)
	require.NoError(t, err)

	affected, err := repo.MarkDeletedByKB(ctx, 1, "user-a", "kb-work")
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)

	stillLive, err := repo.GetLiveByPart(ctx, 1, "user-b", types.WorkbenchSourceItem, "item-1", 1)
	require.NoError(t, err)
	require.NotNil(t, stillLive, "他人的映射不得被批量标记")
}

// ListLiveBySources 批量返回结果，缺失的 source 不出现在 map 里。
func TestListLiveBySources_BatchAndOrder(t *testing.T) {
	db := setupWorkbenchArchiveTestDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()

	for _, partNo := range []int{2, 1} {
		row := newArchiveRow(partNo, 2)
		_, err := repo.CreatePending(ctx, row)
		require.NoError(t, err)
		require.NoError(t, repo.ActivatePart(ctx, row.ID, row.KnowledgeID, 3, row.Title))
	}

	grouped, err := repo.ListLiveBySources(
		ctx, 1, "user-a", types.WorkbenchSourceItem, []string{"item-1", "item-missing"})
	require.NoError(t, err)
	require.Len(t, grouped["item-1"], 2)
	assert.Equal(t, 1, grouped["item-1"][0].PartNo, "结果必须按 part_no 升序")
	_, ok := grouped["item-missing"]
	assert.False(t, ok, "没有 live 映射的 source 不应出现在 map 里")

	empty, err := repo.ListLiveBySources(ctx, 1, "user-a", types.WorkbenchSourceItem, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// CountArchiveParts 只统计 owner 名下非 deleted 的行。
func TestCountArchiveParts(t *testing.T) {
	db := setupWorkbenchArchiveTestDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()

	for partNo := 1; partNo <= 3; partNo++ {
		row := newArchiveRow(partNo, 3)
		_, err := repo.CreatePending(ctx, row)
		require.NoError(t, err)
	}
	count, err := repo.CountArchiveParts(ctx, 1, "user-a")
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)

	_, err = repo.MarkDeletedBySourcePart(ctx, 1, "user-a", types.WorkbenchSourceItem, "item-1", 2)
	require.NoError(t, err)
	count, err = repo.CountArchiveParts(ctx, 1, "user-a")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}
