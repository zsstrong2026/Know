package repository

import (
	"context"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newSopTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.SopProject{}))
	return db
}

func TestAllocItemNoConcurrent(t *testing.T) {
	db := newSopTestDB(t)
	repo := NewSopProjectRepo(db)
	p := &types.SopProject{TenantID: 1, Name: "p", Code: "KNOW"}
	require.NoError(t, repo.Create(context.Background(), p))
	var wg sync.WaitGroup
	nums := make(chan int, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := repo.AllocItemNo(context.Background(), 1, p.ID)
			require.NoError(t, err)
			nums <- n
		}()
	}
	wg.Wait()
	close(nums)
	seen := map[int]bool{}
	for n := range nums {
		require.False(t, seen[n], "duplicate item no %d", n)
		require.GreaterOrEqual(t, n, 1)
		require.LessOrEqual(t, n, 20)
		seen[n] = true
	}
	require.Len(t, seen, 20)
}

func TestRepoDefaultFallback(t *testing.T) {
	db := newSopTestDB(t)
	repo := NewSopProjectRepo(db)
	p := &types.SopProject{TenantID: 1, Name: "p", Code: "KNOW"}
	require.NoError(t, repo.Create(context.Background(), p))
	ctx := context.Background()
	r1, err := repo.AddRepo(ctx, 1, p.ID, types.SopProjectRepo{URL: "https://github.com/a/know.git"})
	require.NoError(t, err)
	r2, err := repo.AddRepo(ctx, 1, p.ID, types.SopProjectRepo{URL: "https://github.com/a/web.git"})
	require.NoError(t, err)
	require.True(t, r1.Default)
	require.NoError(t, repo.SetDefaultRepo(ctx, 1, p.ID, r2.ID))
	require.NoError(t, repo.RemoveRepo(ctx, 1, p.ID, r2.ID))
	got, err := repo.GetByID(ctx, 1, p.ID)
	require.NoError(t, err)
	require.True(t, got.Repos[0].Default, "解绑默认后应顺延到剩余第一个")
}

// 租户过滤与 JSON 列整行更新：后者若被 gorm 的零值跳过悄悄吞掉，
// 后续 Task（工作区根目录、简报 seq、仓库绑定）都会静默丢数据。
func TestSopProjectTenantScopeAndUpdate(t *testing.T) {
	db := newSopTestDB(t)
	repo := NewSopProjectRepo(db)
	ctx := context.Background()
	p := &types.SopProject{TenantID: 1, Name: "p", Code: "KNOW"}
	require.NoError(t, repo.Create(ctx, p))
	other := &types.SopProject{TenantID: 2, Name: "q", Code: "OTHER"}
	require.NoError(t, repo.Create(ctx, other))

	_, err := repo.GetByID(ctx, 1, other.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "跨租户不可见")
	_, err = repo.AllocItemNo(ctx, 1, other.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	_, err = repo.AddRepo(ctx, 1, p.ID, types.SopProjectRepo{URL: "git@github.com:a/web.git"})
	require.NoError(t, err)
	p, err = repo.GetByID(ctx, 1, p.ID)
	require.NoError(t, err)
	p.WorkspaceRoot = "data/sop/workspaces/KNOW"
	p.NotifyOnGateOrFail = false
	p.DomainData = map[string]any{"pm_source_id": "x1"}
	require.NoError(t, repo.Update(ctx, p))

	got, err := repo.GetByID(ctx, 1, p.ID)
	require.NoError(t, err)
	require.Len(t, got.Repos, 1)
	require.Equal(t, "data/sop/workspaces/KNOW", got.WorkspaceRoot)
	require.False(t, got.NotifyOnGateOrFail)
	require.Equal(t, "x1", got.DomainData["pm_source_id"])
}

// Update 必须按租户过滤：拿别租户的 ID 去覆盖同 id 的行属于越权，
// gorm 的 Save 会按主键 upsert，因此这里显式断言「拒绝 + 原行不变」。
func TestSopProjectUpdateRejectsOtherTenant(t *testing.T) {
	db := newSopTestDB(t)
	repo := NewSopProjectRepo(db)
	ctx := context.Background()
	p := &types.SopProject{TenantID: 1, Name: "p", Code: "KNOW"}
	require.NoError(t, repo.Create(ctx, p))

	crossTenant := *p
	crossTenant.TenantID = 2
	crossTenant.Name = "hijacked"
	err := repo.Update(ctx, &crossTenant)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "跨租户更新必须返回未找到，而不是改写行")

	got, err := repo.GetByID(ctx, 1, p.ID)
	require.NoError(t, err)
	require.Equal(t, "p", got.Name, "原租户的行不得被改写")
	require.EqualValues(t, 1, got.TenantID)
}

// 软删或根本不存在的 id：Update 必须显式报未找到，
// 不能靠 Save 的 upsert 把行「复活」或抛 UNIQUE 约束错误。
func TestSopProjectUpdateMissingReturnsNotFound(t *testing.T) {
	db := newSopTestDB(t)
	repo := NewSopProjectRepo(db)
	ctx := context.Background()

	deleted := &types.SopProject{TenantID: 1, Name: "gone", Code: "GONE"}
	require.NoError(t, repo.Create(ctx, deleted))
	require.NoError(t, repo.Delete(ctx, 1, deleted.ID))
	require.ErrorIs(t, repo.Update(ctx, deleted), gorm.ErrRecordNotFound, "软删的行不可被更新")

	absent := &types.SopProject{
		ID:       "00000000-0000-0000-0000-000000000000",
		TenantID: 1,
		Name:     "absent",
		Code:     "NONE",
	}
	require.ErrorIs(t, repo.Update(ctx, absent), gorm.ErrRecordNotFound, "不存在的 id 不可被 upsert 出来")

	var count int64
	require.NoError(t, db.Model(&types.SopProject{}).
		Where("id = ?", absent.ID).Count(&count).Error)
	require.Zero(t, count, "未命中的 Update 不得插入新行")
}
