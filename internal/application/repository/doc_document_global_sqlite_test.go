package repository

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// docGlobalDDL 内联 000093（文档助手三张表）中全局列表用到的两张表与索引。
// 只建必要的列，保持测试与生产查询的字段一致（title 来自任务 theme）。
const docGlobalDDL = `
CREATE TABLE IF NOT EXISTS doc_generation_tasks (
    id        VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER     NOT NULL,
    owner_id  VARCHAR(64) NOT NULL,
    theme     TEXT        NOT NULL,
    created_at DATETIME
);
CREATE TABLE IF NOT EXISTS doc_documents (
    id         VARCHAR(36) PRIMARY KEY,
    tenant_id  INTEGER     NOT NULL,
    owner_id   VARCHAR(64) NOT NULL,
    task_id    VARCHAR(36) NOT NULL,
    version    INTEGER     NOT NULL DEFAULT 1,
    status     VARCHAR(32) NOT NULL DEFAULT 'generated',
    chapters   TEXT,
    md_path    TEXT,
    created_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_doc_documents_task_id ON doc_documents (task_id, version);
`

func setupDocGlobalDB(t *testing.T) *DocDocumentRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(docGlobalDDL).Error)
	return &DocDocumentRepository{db: db}
}

// seedDocTask 造一个任务；返回任务 id。
func seedDocTask(t *testing.T, db *gorm.DB, tenantID uint64, ownerID, theme string) string {
	t.Helper()
	id := uuid.NewString()
	require.NoError(t, db.Exec(
		`INSERT INTO doc_generation_tasks (id, tenant_id, owner_id, theme, created_at) VALUES (?,?,?,?,?)`,
		id, tenantID, ownerID, theme, time.Now().UTC(),
	).Error)
	return id
}

// seedDoc 造一条文档版本；created_at 显式给定，便于断言排序。
func seedDoc(
	t *testing.T, db *gorm.DB, tenantID uint64, ownerID, taskID string, version int, at time.Time,
) string {
	t.Helper()
	id := uuid.NewString()
	require.NoError(t, db.Exec(
		`INSERT INTO doc_documents (id, tenant_id, owner_id, task_id, version, status, chapters, created_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		id, tenantID, ownerID, taskID, version, types.DocDocumentStatusGenerated, "[]", at,
	).Error)
	return id
}

// latest_only 默认语义：每个任务只返回最大版本，标题取任务 theme。
func TestListDocumentsByOwner_LatestOnly(t *testing.T) {
	repo := setupDocGlobalDB(t)
	now := time.Now().UTC()
	taskID := seedDocTask(t, repo.db, 1, "user-a", "季度复盘")
	seedDoc(t, repo.db, 1, "user-a", taskID, 1, now.Add(-2*time.Hour))
	latestID := seedDoc(t, repo.db, 1, "user-a", taskID, 2, now.Add(-time.Hour))

	rows, hasMore, err := repo.ListDocumentsByOwner(t.Context(), 1, "user-a",
		types.DocDocumentFilter{LatestOnly: true, Limit: 10})
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Len(t, rows, 1)
	assert.Equal(t, latestID, rows[0].ID)
	assert.Equal(t, 2, rows[0].Version)
	assert.Equal(t, "季度复盘", rows[0].Title)
	// archive_status 不参与查询，空值由服务层填充。
	assert.Empty(t, rows[0].ArchiveStatus)
}

// latest_only=false 展开全部版本，按 created_at 降序。
func TestListDocumentsByOwner_AllVersions(t *testing.T) {
	repo := setupDocGlobalDB(t)
	now := time.Now().UTC()
	taskID := seedDocTask(t, repo.db, 1, "user-a", "主题")
	seedDoc(t, repo.db, 1, "user-a", taskID, 1, now.Add(-2*time.Hour))
	seedDoc(t, repo.db, 1, "user-a", taskID, 2, now.Add(-time.Hour))

	rows, _, err := repo.ListDocumentsByOwner(t.Context(), 1, "user-a",
		types.DocDocumentFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, 2, rows[0].Version)
	assert.Equal(t, 1, rows[1].Version)
}

// owner 隔离：别人的文档与任务都不应出现。
func TestListDocumentsByOwner_OwnerIsolation(t *testing.T) {
	repo := setupDocGlobalDB(t)
	now := time.Now().UTC()
	mine := seedDocTask(t, repo.db, 1, "user-a", "我的")
	theirs := seedDocTask(t, repo.db, 1, "user-b", "别人的")
	seedDoc(t, repo.db, 1, "user-a", mine, 1, now)
	seedDoc(t, repo.db, 1, "user-b", theirs, 1, now)

	rows, _, err := repo.ListDocumentsByOwner(t.Context(), 1, "user-a",
		types.DocDocumentFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "我的", rows[0].Title)
}

// 标题搜索命中 LIKE，通配符被转义（"%": 不应匹配任意内容）。
func TestListDocumentsByOwner_SearchEscapesWildcards(t *testing.T) {
	repo := setupDocGlobalDB(t)
	now := time.Now().UTC()
	hit := seedDocTask(t, repo.db, 1, "user-a", "2026 Q1 计划")
	other := seedDocTask(t, repo.db, 1, "user-a", "别的内容")
	seedDoc(t, repo.db, 1, "user-a", hit, 1, now)
	seedDoc(t, repo.db, 1, "user-a", other, 1, now.Add(-time.Minute))

	rows, _, err := repo.ListDocumentsByOwner(t.Context(), 1, "user-a",
		types.DocDocumentFilter{LatestOnly: true, Query: "Q1", Limit: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "2026 Q1 计划", rows[0].Title)

	// 未转义时 "%" 会匹配全部；转义后应零命中。
	empty, _, err := repo.ListDocumentsByOwner(t.Context(), 1, "user-a",
		types.DocDocumentFilter{LatestOnly: true, Query: "%", Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// 分页：limit+1 探测 hasMore，游标可续页且不重复。
func TestListDocumentsByOwner_CursorPaging(t *testing.T) {
	repo := setupDocGlobalDB(t)
	base := time.Now().UTC()
	for i := 0; i < 3; i++ {
		taskID := seedDocTask(t, repo.db, 1, "user-a", "文档"+string(rune('A'+i)))
		seedDoc(t, repo.db, 1, "user-a", taskID, 1, base.Add(time.Duration(i)*time.Minute))
	}

	first, hasMore, err := repo.ListDocumentsByOwner(t.Context(), 1, "user-a",
		types.DocDocumentFilter{LatestOnly: true, Limit: 2})
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Len(t, first, 2)

	cursor := EncodeItemsCursor(first[1].CreatedAt, first[1].ID)
	second, hasMore, err := repo.ListDocumentsByOwner(t.Context(), 1, "user-a",
		types.DocDocumentFilter{LatestOnly: true, Limit: 2, Cursor: cursor})
	require.NoError(t, err)
	assert.False(t, hasMore)
	require.Len(t, second, 1)
	assert.NotEqual(t, first[1].ID, second[0].ID)
}

// 非法游标返回 ErrWorkbenchInvalidCursor，由服务层翻成 400。
func TestListDocumentsByOwner_InvalidCursor(t *testing.T) {
	repo := setupDocGlobalDB(t)
	_, _, err := repo.ListDocumentsByOwner(t.Context(), 1, "user-a",
		types.DocDocumentFilter{LatestOnly: true, Limit: 10, Cursor: "not-base64!"})
	require.Error(t, err)
	assert.ErrorIs(t, err, types.ErrWorkbenchInvalidCursor)
}
