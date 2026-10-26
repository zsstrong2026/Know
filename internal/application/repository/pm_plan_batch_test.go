package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestPMPlanBatchRepositoryActivate(t *testing.T) {
	db := newPMTestDB(t)
	repo := NewPMPlanBatchRepository(db)
	ctx := context.Background()

	b1 := &types.PMPlanBatch{
		TenantID: 1, ProjectID: "p-1", BatchName: "首版规划",
		PlanType: types.PlanBatchTypeInitial, Status: types.PlanBatchStatusDraft,
	}
	b2 := &types.PMPlanBatch{
		TenantID: 1, ProjectID: "p-1", BatchName: "迭代规划",
		PlanType: types.PlanBatchTypeIteration, Status: types.PlanBatchStatusDraft,
	}
	require.NoError(t, repo.Create(ctx, b1))
	require.NoError(t, repo.Create(ctx, b2))

	// 无生效批次时应返回 notFound 而非空指针
	_, err := repo.GetActiveByProject(ctx, 1, "p-1")
	assert.ErrorIs(t, err, ErrPMPlanBatchNotFound)

	require.NoError(t, repo.ActivateBatch(ctx, 1, "p-1", b1.ID))
	active, err := repo.GetActiveByProject(ctx, 1, "p-1")
	require.NoError(t, err)
	assert.Equal(t, b1.ID, active.ID)
	assert.NotNil(t, active.ActivatedAt)

	// 激活新批次自动归档旧 active，保证同项目仅一个 active
	require.NoError(t, repo.ActivateBatch(ctx, 1, "p-1", b2.ID))
	active, err = repo.GetActiveByProject(ctx, 1, "p-1")
	require.NoError(t, err)
	assert.Equal(t, b2.ID, active.ID)

	old, err := repo.GetByID(ctx, 1, b1.ID)
	require.NoError(t, err)
	assert.Equal(t, types.PlanBatchStatusArchived, old.Status, "旧活动批次应被归档")

	// 跨租户激活：事务回滚，原 active 状态不被破坏
	err = repo.ActivateBatch(ctx, 2, "p-1", b2.ID)
	assert.ErrorIs(t, err, ErrPMPlanBatchNotFound)
	active, err = repo.GetActiveByProject(ctx, 1, "p-1")
	require.NoError(t, err)
	assert.Equal(t, b2.ID, active.ID)

	// 目标批次不存在
	assert.ErrorIs(t,
		repo.ActivateBatch(ctx, 1, "p-1", "not-exist"),
		ErrPMPlanBatchNotFound)
}

func TestPMPlanBatchRepositoryDelete(t *testing.T) {
	db := newPMTestDB(t)
	repo := NewPMPlanBatchRepository(db)
	ctx := context.Background()

	b := &types.PMPlanBatch{
		TenantID: 1, ProjectID: "p-1", BatchName: "待删草稿",
		PlanType: types.PlanBatchTypeInitial, Status: types.PlanBatchStatusDraft,
	}
	require.NoError(t, repo.Create(ctx, b))

	require.NoError(t, repo.Delete(ctx, 1, b.ID))
	_, err := repo.GetByID(ctx, 1, b.ID)
	assert.ErrorIs(t, err, ErrPMPlanBatchNotFound, "删除后应查不到")

	// 跨租户与重复删除均返回 notFound
	assert.ErrorIs(t, repo.Delete(ctx, 2, b.ID), ErrPMPlanBatchNotFound)
	assert.ErrorIs(t, repo.Delete(ctx, 1, b.ID), ErrPMPlanBatchNotFound)
}
