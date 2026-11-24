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

// newSopNoteTestDB 建独立 SQLite 内存库：AutoMigrate 建黑板表后按迁移脚本
// 原样补部分唯一索引（GORM tag 表达不了 WHERE deleted_at IS NULL）。
func newSopNoteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:sopnotetest_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.AutoMigrate(&types.SopProjectNote{}))
	require.NoError(t, db.Exec(
		`CREATE UNIQUE INDEX ux_sop_notes_project_seq ON sop_project_notes (project_id, seq) WHERE deleted_at IS NULL`,
	).Error)
	return db
}

// seq 是项目内自增序号：连续创建按 1,2,3 递增；另一个项目从 1 重新开始。
func TestNoteSeqMonotonicPerProject(t *testing.T) {
	db := newSopNoteTestDB(t)
	repo := NewSopProjectNoteRepo(db)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		n := &types.SopProjectNote{
			TenantID: 1, ProjectID: "p1", Kind: types.SopNoteKindNote,
			Body: fmt.Sprintf("n%d", i),
		}
		require.NoError(t, repo.Create(ctx, n))
		require.Equal(t, int64(i+1), n.Seq, "Create 必须把分配到的 seq 写回入参")
	}

	max, err := repo.MaxSeq(ctx, 1, "p1")
	require.NoError(t, err)
	require.Equal(t, int64(3), max)

	other := &types.SopProjectNote{TenantID: 1, ProjectID: "p2", Kind: types.SopNoteKindNote, Body: "x"}
	require.NoError(t, repo.Create(ctx, other))
	require.Equal(t, int64(1), other.Seq, "项目内独立自增")
}

// 列表排序：置顶优先，其次 seq 倒序（新条目在前）。
func TestNoteListPinnedFirst(t *testing.T) {
	db := newSopNoteTestDB(t)
	repo := NewSopProjectNoteRepo(db)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &types.SopProjectNote{
		TenantID: 1, ProjectID: "p1", Kind: types.SopNoteKindDecision, Body: "旧决策",
	}))
	require.NoError(t, repo.Create(ctx, &types.SopProjectNote{
		TenantID: 1, ProjectID: "p1", Kind: types.SopNoteKindRisk, Body: "风险", Pinned: true,
	}))
	require.NoError(t, repo.Create(ctx, &types.SopProjectNote{
		TenantID: 1, ProjectID: "p1", Kind: types.SopNoteKindNote, Body: "最新记录",
	}))

	got, err := repo.List(ctx, 1, "p1", "", nil, 10)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, "风险", got[0].Body, "置顶条目排第一")
	require.Equal(t, "最新记录", got[1].Body, "其余按 seq 倒序")
	require.Equal(t, "旧决策", got[2].Body)

	byKind, err := repo.List(ctx, 1, "p1", types.SopNoteKindDecision, nil, 10)
	require.NoError(t, err)
	require.Len(t, byKind, 1)
	require.Equal(t, "旧决策", byKind[0].Body)

	pinned := true
	onlyPinned, err := repo.List(ctx, 1, "p1", "", &pinned, 10)
	require.NoError(t, err)
	require.Len(t, onlyPinned, 1)
	require.True(t, onlyPinned[0].Pinned)

	limited, err := repo.List(ctx, 1, "p1", "", nil, 2)
	require.NoError(t, err)
	require.Len(t, limited, 2)

	crossTenant, err := repo.List(ctx, 2, "p1", "", nil, 10)
	require.NoError(t, err)
	require.Empty(t, crossTenant, "跨租户不可见")
}

// 编辑推进 seq（保证重新渲染能察觉到变化），但不得改写归属列。
func TestNoteUpdateAdvancesSeq(t *testing.T) {
	db := newSopNoteTestDB(t)
	repo := NewSopProjectNoteRepo(db)
	ctx := context.Background()

	n := &types.SopProjectNote{TenantID: 1, ProjectID: "p1", Kind: types.SopNoteKindNote, Body: "原正文"}
	require.NoError(t, repo.Create(ctx, n))
	require.Equal(t, int64(1), n.Seq)

	n.Body = "新正文"
	n.Pinned = true
	n.ProjectID = "p-hijack" // 归属列不得被改写
	n.TenantID = 2
	require.NoError(t, repo.Update(ctx, 1, n))
	require.Equal(t, int64(2), n.Seq, "编辑必须推进 seq")

	got, err := repo.GetByID(ctx, 1, n.ID)
	require.NoError(t, err)
	require.Equal(t, "新正文", got.Body)
	require.True(t, got.Pinned)
	require.Equal(t, "p1", got.ProjectID, "项目归属不得被改写")
	require.Equal(t, int64(2), got.Seq)

	_, err = repo.GetByID(ctx, 2, n.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "跨租户不可见")
}

// 软删：删除后不可见，seq 释放给后续条目（软删行不占位）。
func TestNoteSoftDelete(t *testing.T) {
	db := newSopNoteTestDB(t)
	repo := NewSopProjectNoteRepo(db)
	ctx := context.Background()

	a := &types.SopProjectNote{TenantID: 1, ProjectID: "p1", Body: "a"}
	require.NoError(t, repo.Create(ctx, a))
	require.Equal(t, int64(1), a.Seq)
	require.ErrorIs(t, repo.SoftDelete(ctx, 2, a.ID), gorm.ErrRecordNotFound, "跨租户不可删")

	require.NoError(t, repo.SoftDelete(ctx, 1, a.ID))
	_, err := repo.GetByID(ctx, 1, a.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	max, err := repo.MaxSeq(ctx, 1, "p1")
	require.NoError(t, err)
	require.Zero(t, max, "MaxSeq 只看活行")

	// 软删行不占位：新条目复用 seq=1 且不被部分唯一索引挡住。
	reuse := &types.SopProjectNote{TenantID: 1, ProjectID: "p1", Body: "reuse"}
	require.NoError(t, repo.Create(ctx, reuse))
	require.Equal(t, int64(1), reuse.Seq)
}

// 非法 kind 不落库（唯一写入口校验）。
func TestNoteCreateRejectsInvalidKind(t *testing.T) {
	db := newSopNoteTestDB(t)
	repo := NewSopProjectNoteRepo(db)
	ctx := context.Background()

	err := repo.Create(ctx, &types.SopProjectNote{TenantID: 1, ProjectID: "p1", Kind: "bogus"})
	require.ErrorIs(t, err, types.ErrInvalidSopNote)
	require.ErrorIs(t, repo.Create(ctx, &types.SopProjectNote{
		TenantID: 1, ProjectID: "p1", Source: "bogus",
	}), types.ErrInvalidSopNote)

	list, err := repo.List(ctx, 1, "p1", "", nil, 10)
	require.NoError(t, err)
	require.Empty(t, list)
}
