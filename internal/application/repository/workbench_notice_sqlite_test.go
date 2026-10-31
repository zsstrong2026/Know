package repository

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newNotice 造一条通知；dedupeKey 为空表示不去重。
func newNotice(userID, kind, dedupeKey string) *types.WorkbenchNotice {
	return &types.WorkbenchNotice{
		ID:        uuid.NewString(),
		UserID:    userID,
		TenantID:  1,
		Kind:      kind,
		DedupeKey: dedupeKey,
		Payload:   []byte(`{"kb_count":1}`),
		CreatedAt: time.Now().UTC(),
	}
}

// 同一 dedupe_key 只落一条：依赖 uq_wn_dedupe 部分唯一索引 + OnConflict DoNothing。
func TestInsertNotice_DeduplicatesByKey(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}

	first, err := repo.InsertNotice(t.Context(), newNotice("user-a", types.WorkbenchNoticeRemoved, "removed:1:100"))
	require.NoError(t, err)
	assert.True(t, first, "首次插入应落库")

	second, err := repo.InsertNotice(t.Context(), newNotice("user-a", types.WorkbenchNoticeRemoved, "removed:1:100"))
	require.NoError(t, err)
	assert.False(t, second, "同一 dedupe_key 第二次不应落库")

	notices, err := repo.ListNotices(t.Context(), "user-a", false, 20)
	require.NoError(t, err)
	assert.Len(t, notices, 1)
}

// dedupe_key 为空串时不去重：部分唯一索引的 WHERE 排除了空串。
func TestInsertNotice_EmptyDedupeKeyIsNotDeduplicated(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}

	for i := 0; i < 2; i++ {
		inserted, err := repo.InsertNotice(t.Context(), newNotice("user-a", types.WorkbenchNoticeArchive, ""))
		require.NoError(t, err)
		assert.True(t, inserted, "空 dedupe_key 每次都落库")
	}
	notices, err := repo.ListNotices(t.Context(), "user-a", false, 20)
	require.NoError(t, err)
	assert.Len(t, notices, 2)
}

// 列表按 user 隔离且 created_at 倒序；unreadOnly 只看未读。
func TestListNotices_IsolatesByUserAndOrdersDesc(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}

	old := newNotice("user-a", types.WorkbenchNoticeRemoved, "")
	old.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
	require.NoError(t, mustInsert(t, repo, old))

	recent := newNotice("user-a", types.WorkbenchNoticeRestored, "")
	require.NoError(t, mustInsert(t, repo, recent))

	other := newNotice("user-b", types.WorkbenchNoticeRemoved, "")
	require.NoError(t, mustInsert(t, repo, other))

	notices, err := repo.ListNotices(t.Context(), "user-a", false, 20)
	require.NoError(t, err)
	require.Len(t, notices, 2)
	assert.Equal(t, recent.ID, notices[0].ID, "created_at 倒序")
	assert.Equal(t, old.ID, notices[1].ID)

	// 标记最旧一条已读后，unreadOnly 只剩一条。
	affected, err := repo.MarkRead(t.Context(), "user-a", old.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, affected)

	unread, err := repo.CountUnread(t.Context(), "user-a")
	require.NoError(t, err)
	assert.Equal(t, 1, unread)

	onlyUnread, err := repo.ListNotices(t.Context(), "user-a", true, 20)
	require.NoError(t, err)
	require.Len(t, onlyUnread, 1)
	assert.Equal(t, recent.ID, onlyUnread[0].ID)
}

// 标记他人通知必须 0 行：这是 2400 的唯一依据。
func TestMarkRead_DoesNotTouchOtherUsersNotice(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}

	notice := newNotice("user-a", types.WorkbenchNoticeRemoved, "")
	require.NoError(t, mustInsert(t, repo, notice))

	affected, err := repo.MarkRead(t.Context(), "user-b", notice.ID)
	require.NoError(t, err)
	assert.Zero(t, affected, "越权标记不得命中")

	// A 的未读数不变。
	unread, err := repo.CountUnread(t.Context(), "user-a")
	require.NoError(t, err)
	assert.Equal(t, 1, unread)
}

// MarkAllRead 只影响自己。
func TestMarkAllRead_IsolatesByUser(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}

	require.NoError(t, mustInsert(t, repo, newNotice("user-a", types.WorkbenchNoticeRemoved, "")))
	require.NoError(t, mustInsert(t, repo, newNotice("user-a", types.WorkbenchNoticeCleanup, "")))
	require.NoError(t, mustInsert(t, repo, newNotice("user-b", types.WorkbenchNoticeRemoved, "")))

	affected, err := repo.MarkAllRead(t.Context(), "user-a")
	require.NoError(t, err)
	assert.EqualValues(t, 2, affected)

	unreadA, err := repo.CountUnread(t.Context(), "user-a")
	require.NoError(t, err)
	assert.Zero(t, unreadA)

	unreadB, err := repo.CountUnread(t.Context(), "user-b")
	require.NoError(t, err)
	assert.Equal(t, 1, unreadB, "不应影响他人未读数")
}

// 重复标记已读是 no-op：第二次 0 行。
func TestMarkRead_IsIdempotent(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}

	notice := newNotice("user-a", types.WorkbenchNoticeRemoved, "")
	require.NoError(t, mustInsert(t, repo, notice))

	first, err := repo.MarkRead(t.Context(), "user-a", notice.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, first)

	second, err := repo.MarkRead(t.Context(), "user-a", notice.ID)
	require.NoError(t, err)
	assert.Zero(t, second)
}

// mustInsert 断言插入成功（忽略 dedupe 命中）。
func mustInsert(t *testing.T, repo *workbenchRepository, notice *types.WorkbenchNotice) error {
	t.Helper()
	_, err := repo.InsertNotice(t.Context(), notice)
	return err
}
