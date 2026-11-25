package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/types"
)

// 状态机落库必须带 from 条件（CAS）：先读后写之间别人改了状态时，
// 无条件 UPDATE 会以旧状态为依据覆盖掉对方的迁移。
func TestUpdateStatusUsesCASOnFromStatus(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()
	it := seedSopItem(t, repo, 1, "p1", "TP-1", "t")

	captured := &sopItemSQLCapture{Interface: logger.Discard}
	db.Logger = captured

	require.NoError(t, repo.UpdateStatus(ctx, 1, it.ID, types.SopItemStatusTodo))
	updateSQL := sopItemUpdateStatement(t, captured.snapshot())
	_, where, ok := strings.Cut(updateSQL, " WHERE ")
	require.True(t, ok, "UPDATE 必须带 WHERE：\n%s", updateSQL)
	require.Contains(t, where, "status =", "WHERE 必须带 status = <from> 条件：\n%s", updateSQL)
	require.Contains(t, where, types.SopItemStatusBacklog, "CAS 条件必须是比较原状态：\n%s", updateSQL)
}

// 竞争落败：读到的 from 已过期（别人先改了），CAS 未命中必须返回冲突哨兵，
// 而不是静默「成功」把别人的迁移覆盖掉。
func TestUpdateStatusReportsConflictWhenStatusMoved(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()
	it := seedSopItem(t, repo, 1, "p1", "TP-1", "t")

	// 模拟「另一请求刚把它迁到 cancelled」：本请求手里的 from 仍是 backlog。
	require.NoError(t, db.Model(&types.SopItem{}).Where("id = ?", it.ID).
		Update("status", types.SopItemStatusCancelled).Error)

	err := applySopItemStatus(db, &types.SopItem{
		ID: it.ID, TenantID: 1, Status: types.SopItemStatusBacklog,
	}, types.SopItemStatusTodo)
	require.ErrorIs(t, err, ErrSopItemStatusConflict)

	got, err := repo.GetByID(ctx, 1, it.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopItemStatusCancelled, got.Status, "落败方不得改写状态")
}

// 命中时返回 nil 且状态落库：CAS 不能让正常路径也失败。
func TestUpdateStatusSucceedsWhenFromMatches(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()
	it := seedSopItem(t, repo, 1, "p1", "TP-1", "t")

	require.NoError(t, repo.UpdateStatus(ctx, 1, it.ID, types.SopItemStatusTodo))
	got, err := repo.GetByID(ctx, 1, it.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopItemStatusTodo, got.Status)
}
