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

// workbenchLifecycleDDL 内联 000042 的孤儿表与归档/模板/资产等从属表，
// 只覆盖清理路径用到的列。
const workbenchLifecycleDDL = `
CREATE TABLE IF NOT EXISTS workbench_orphans (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    reason VARCHAR(32) NOT NULL,
    suspended_at DATETIME NOT NULL,
    purge_after DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_wo_tenant_user ON workbench_orphans (tenant_id, user_id);
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
CREATE TABLE IF NOT EXISTS workbench_item_assets (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    owner_id VARCHAR(36) NOT NULL,
    item_id VARCHAR(36) NOT NULL,
    storage_path TEXT NOT NULL,
    original_name TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    content_hash TEXT,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    last_seen_at DATETIME,
    created_at DATETIME NOT NULL,
    deleted_at DATETIME
);
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
    status TEXT NOT NULL DEFAULT 'active',
    archived_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS workbench_templates (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    owner_id VARCHAR(36) NOT NULL,
    name VARCHAR(60) NOT NULL,
    description VARCHAR(200) NOT NULL DEFAULT '',
    ydoc BLOB NOT NULL,
    size_bytes INTEGER NOT NULL,
    editor_schema_version INTEGER NOT NULL DEFAULT 1,
    source_item_id VARCHAR(36),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
`

func setupWorkbenchLifecycleDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupWorkbenchFolderTestDB(t)
	require.NoError(t, db.Exec(workbenchLifecycleDDL).Error)
	return db
}

func newOrphan(tenantID uint64, userID string, purgeAfter time.Time) *types.WorkbenchOrphan {
	now := time.Now().UTC()
	return &types.WorkbenchOrphan{
		ID:          uuid.NewString(),
		TenantID:    tenantID,
		UserID:      userID,
		Reason:      types.WorkbenchOrphanReasonRemoved,
		SuspendedAt: now,
		PurgeAfter:  purgeAfter,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// 同一 (tenant, user) 重复写只留一行，并重置保留期。
func TestWorkbenchUpsertOrphanResetsPurgeAfter(t *testing.T) {
	db := setupWorkbenchLifecycleDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()

	first := newOrphan(1, "user-a", time.Now().UTC().AddDate(0, 0, 10))
	require.NoError(t, repo.UpsertOrphan(ctx, first))

	second := newOrphan(1, "user-a", time.Now().UTC().AddDate(0, 0, 30))
	second.Reason = types.WorkbenchOrphanReasonLeft
	require.NoError(t, repo.UpsertOrphan(ctx, second))

	got, err := repo.GetOrphan(ctx, 1, "user-a")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, types.WorkbenchOrphanReasonLeft, got.Reason, "重复移出应覆盖原因")
	assert.True(t, got.PurgeAfter.After(first.PurgeAfter), "重复移出应重置保留期")

	// 另一个 tenant 的同名用户是独立行。
	other := newOrphan(2, "user-a", time.Now().UTC())
	require.NoError(t, repo.UpsertOrphan(ctx, other))
	kept, err := repo.GetOrphan(ctx, 1, "user-a")
	require.NoError(t, err)
	assert.Equal(t, got.ID, kept.ID, "跨 tenant 不得互相覆盖")
}

// 到期与预警窗口的边界：due 含等号，expiring 左开右闭。
func TestWorkbenchListOrphansByWindow(t *testing.T) {
	db := setupWorkbenchLifecycleDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()
	now := time.Now().UTC()

	due := newOrphan(1, "user-due", now.Add(-time.Minute))
	soon := newOrphan(1, "user-soon", now.AddDate(0, 0, 3))
	far := newOrphan(1, "user-far", now.AddDate(0, 0, 20))
	for _, row := range []*types.WorkbenchOrphan{due, soon, far} {
		require.NoError(t, repo.UpsertOrphan(ctx, row))
	}

	dueRows, err := repo.ListOrphansDue(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, dueRows, 1)
	assert.Equal(t, "user-due", dueRows[0].UserID)

	expiring, err := repo.ListOrphansExpiring(ctx, now, now.AddDate(0, 0, 7), 10)
	require.NoError(t, err)
	require.Len(t, expiring, 1)
	assert.Equal(t, "user-soon", expiring[0].UserID)
}

// PurgeOwnerData 必须只删该 owner 的行，且不碰 orphan 标记本身。
func TestWorkbenchPurgeOwnerDataScopedByOwner(t *testing.T) {
	db := setupWorkbenchLifecycleDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()

	keep := newTestItem(t, db, 1, "user-b")
	victim := newTestItem(t, db, 1, "user-a")
	now := time.Now().UTC()
	require.NoError(t, db.Exec(`INSERT INTO workbench_archives
		(id, tenant_id, owner_id, kb_id, knowledge_id, source_type, source_id,
		 source_version, part_no, part_count, title, status, archived_at, created_at, updated_at)
		VALUES (?, 1, 'user-a', 'kb', 'k1', 'item', ?, 1, 1, 1, 't', 'active', ?, ?, ?)`,
		uuid.NewString(), victim.ID, now, now, now).Error)
	require.NoError(t, db.Exec(`INSERT INTO workbench_templates
		(id, tenant_id, owner_id, name, ydoc, size_bytes, created_at, updated_at)
		VALUES (?, 1, 'user-a', 'tpl', x'00', 1, ?, ?)`,
		uuid.NewString(), now, now).Error)
	repoOrphan := newOrphan(1, "user-a", now)
	require.NoError(t, repo.UpsertOrphan(ctx, repoOrphan))

	require.NoError(t, repo.PurgeOwnerData(ctx, 1, "user-a"))

	// 别人的条目仍在。
	_, err := repo.GetItem(ctx, 1, "user-b", keep.ID)
	require.NoError(t, err)

	var archives, templates int64
	require.NoError(t, db.Table("workbench_archives").Count(&archives).Error)
	require.NoError(t, db.Table("workbench_templates").Count(&templates).Error)
	assert.Zero(t, archives)
	assert.Zero(t, templates)

	// orphan 标记保留：由清理任务在成功之后单独删，失败时才能重试。
	stored, err := repo.GetOrphan(ctx, 1, "user-a")
	require.NoError(t, err)
	assert.NotNil(t, stored)
}

// 归档对账所需的跨用户查询与 pending 激活。
func TestWorkbenchListLiveArchivesAndActivatePending(t *testing.T) {
	db := setupWorkbenchLifecycleDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()
	now := time.Now().UTC()

	pendingID := uuid.NewString()
	for _, row := range []struct {
		id     string
		status string
	}{
		{pendingID, types.WorkbenchArchivePending},
		{uuid.NewString(), types.WorkbenchArchiveActive},
		{uuid.NewString(), types.WorkbenchArchiveDeleted},
	} {
		require.NoError(t, db.Exec(`INSERT INTO workbench_archives
			(id, tenant_id, owner_id, kb_id, knowledge_id, source_type, source_id,
			 source_version, part_no, part_count, title, status, archived_at, created_at, updated_at)
			VALUES (?, 1, 'user-a', 'kb', 'k1', 'item', ?, 1, 1, 1, 't', ?, ?, ?, ?)`,
			row.id, uuid.NewString(), row.status, now, now, now).Error)
	}

	rows, err := repo.ListLiveArchives(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, rows, 2, "deleted 不得出现在对账集合里")

	affected, err := repo.ActivatePending(ctx, pendingID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, affected)

	// 已不是 pending 时再次激活不应命中。
	affected, err = repo.ActivatePending(ctx, pendingID)
	require.NoError(t, err)
	assert.Zero(t, affected)
}

// 版本保留：最近 N 个永不过期，超期且超出 N 的才删。
func TestWorkbenchPruneExpiredVersions(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()
	item := newTestItem(t, db, 1, "user-a")

	old := time.Now().UTC().AddDate(0, 0, -60)
	recent := time.Now().UTC()
	// 2 个超期旧版本 + 2 个新版本；keep=2 时版本 1、2 既超期又不在最近 2 个里，该删。
	for version, created := range map[int]time.Time{
		1: old, 2: old, 3: recent, 4: recent,
	} {
		require.NoError(t, db.Exec(`INSERT INTO workbench_item_versions
			(id, item_id, tenant_id, owner_id, version, editor_schema_version, created_at)
			VALUES (?, ?, 1, 'user-a', ?, 1, ?)`,
			uuid.NewString(), item.ID, version, created).Error)
	}

	deleted, err := repo.PruneExpiredVersions(ctx, time.Now().UTC().AddDate(0, 0, -30), 2, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 2, deleted)

	var remaining []int
	require.NoError(t, db.Table("workbench_item_versions").Order("version ASC").
		Pluck("version", &remaining).Error)
	assert.Equal(t, []int{3, 4}, remaining, "最近 keep 个版本必须保留")
}
