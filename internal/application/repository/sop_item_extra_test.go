package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/types"
)

// newSopExtraTestDB 建独立 SQLite 内存库并 AutoMigrate 澄清/活动两张表。
// 澄清与活动的写入都要求事项在租户内存在，故同时建 sop_items 并预置
// 测试用到的 it1..it3（租户 1），测试体本身只关心澄清/活动。
func newSopExtraTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:sopextratest_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.AutoMigrate(
		&types.SopItem{}, &types.SopItemClarification{}, &types.SopItemActivity{},
	))
	for i, id := range []string{"it1", "it2", "it3"} {
		require.NoError(t, db.Create(&types.SopItem{
			ID: id, TenantID: 1, ProjectID: "p1", No: fmt.Sprintf("TP-%d", i+1), Title: id,
		}).Error)
	}
	return db
}

func TestClarificationAnswerOneWay(t *testing.T) {
	db := newSopExtraTestDB(t) // AutoMigrate 两模型
	repo := NewSopItemExtraRepo(db)
	ctx := context.Background()
	c := &types.SopItemClarification{TenantID: 1, ItemID: "it1", Question: "目标用户是谁？", Source: "ai"}
	require.NoError(t, repo.AddClarification(ctx, c))
	require.Equal(t, "open", c.Status)
	require.NoError(t, repo.AnswerClarification(ctx, 1, c.ID, "内部运营"))
	got, err := repo.ListClarifications(ctx, 1, "it1")
	require.NoError(t, err)
	require.Equal(t, "answered", got[0].Status)
	require.Equal(t, "内部运营", got[0].Answer)
	require.Error(t, repo.AnswerClarification(ctx, 1, c.ID, "二次覆盖"), "已回答不可再改")
}

func TestSeedAnswered(t *testing.T) {
	db := newSopExtraTestDB(t)
	repo := NewSopItemExtraRepo(db)
	ctx := context.Background()
	require.NoError(t, repo.SeedAnswered(ctx, 1, "it2", []types.SopItemClarification{
		{Question: "q1", Answer: "a1"}, {Question: "q2", Answer: "a2"},
	}))
	got, _ := repo.ListClarifications(ctx, 1, "it2")
	require.Len(t, got, 2)
	for _, c := range got {
		require.Equal(t, "answered", c.Status)
	}
}

func TestActivitiesAppendOnly(t *testing.T) {
	db := newSopExtraTestDB(t)
	repo := NewSopItemExtraRepo(db)
	ctx := context.Background()
	require.NoError(t, repo.AddActivity(ctx, &types.SopItemActivity{TenantID: 1, ItemID: "it3", Kind: "business", Actor: "u", Action: "创建事项"}))
	require.NoError(t, repo.AddActivity(ctx, &types.SopItemActivity{TenantID: 1, ItemID: "it3", Kind: "audit", Actor: "u", Action: "编辑"}))
	b, _ := repo.ListActivities(ctx, 1, "it3", "business")
	a, _ := repo.ListActivities(ctx, 1, "it3", "audit")
	require.Len(t, b, 1)
	require.Len(t, a, 1)
}

// 写入前必须在事务内校验事项归属：不存在 / 跨租户的 item_id 不得落库。
func TestExtraRejectsMissingOrCrossTenantItem(t *testing.T) {
	db := newSopExtraTestDB(t)
	repo := NewSopItemExtraRepo(db)
	ctx := context.Background()

	err := repo.AddClarification(ctx, &types.SopItemClarification{TenantID: 1, ItemID: "it1", Question: "q"})
	require.NoError(t, err)

	err = repo.AddClarification(ctx, &types.SopItemClarification{TenantID: 2, ItemID: "it1", Question: "q"})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "跨租户不得写入澄清")

	err = repo.AddActivity(ctx, &types.SopItemActivity{TenantID: 1, ItemID: "nope", Kind: "business", Action: "x"})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "事项不存在不得写入活动")

	err = repo.SeedAnswered(ctx, 2, "it2", []types.SopItemClarification{{Question: "q", Answer: "a"}})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "跨租户不得 seed 澄清")
}

// 租户过滤：别的租户读不到本租户的澄清与活动。
func TestExtraListScopedByTenant(t *testing.T) {
	db := newSopExtraTestDB(t)
	repo := NewSopItemExtraRepo(db)
	ctx := context.Background()
	c := &types.SopItemClarification{TenantID: 1, ItemID: "it1", Question: "q"}
	require.NoError(t, repo.AddClarification(ctx, c))
	require.NoError(t, repo.AddActivity(ctx, &types.SopItemActivity{TenantID: 1, ItemID: "it1", Kind: "business", Action: "x"}))

	got, err := repo.ListClarifications(ctx, 2, "it1")
	require.NoError(t, err)
	require.Empty(t, got)
	acts, err := repo.ListActivities(ctx, 2, "it1", "")
	require.NoError(t, err)
	require.Empty(t, acts)
}
