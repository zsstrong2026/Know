package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestPMTaskRepositoryLifecycle(t *testing.T) {
	db := newPMTestDB(t)
	repo := NewPMTaskRepository(db)
	ctx := context.Background()

	task := &types.PMTask{
		TenantID: 1, ProjectID: "p-1", TaskType: types.TypePMPRDGenerate,
		Status: types.PMTaskStatusPending, Payload: types.JSON(`{"version_id":"v-1"}`),
	}
	require.NoError(t, repo.Create(ctx, task))
	require.NotEmpty(t, task.ID)

	// 跨租户查询不可见
	_, err := repo.GetByID(ctx, 2, task.ID)
	assert.ErrorIs(t, err, ErrPMTaskNotFound)

	require.NoError(t, repo.UpdateStatus(ctx, task.ID, types.PMTaskStatusRunning, 30))
	got, err := repo.GetByID(ctx, 1, task.ID)
	require.NoError(t, err)
	assert.Equal(t, types.PMTaskStatusRunning, got.Status)
	assert.Equal(t, 30, got.Progress)

	require.NoError(t, repo.Succeed(ctx, task.ID, types.JSON(`{"knowledge_id":"k-1"}`)))
	got, err = repo.GetByID(ctx, 1, task.ID)
	require.NoError(t, err)
	assert.Equal(t, types.PMTaskStatusSucceeded, got.Status)
	assert.Equal(t, 100, got.Progress)
	assert.NotNil(t, got.FinishedAt)
	assert.JSONEq(t, `{"knowledge_id":"k-1"}`, got.Result.ToString())

	// 失败分支：记录错误并保留失败时的进度
	require.NoError(t, repo.UpdateStatus(ctx, task.ID, types.PMTaskStatusRunning, 60))
	require.NoError(t, repo.Fail(ctx, task.ID, "模型调用超时"))
	got, err = repo.GetByID(ctx, 1, task.ID)
	require.NoError(t, err)
	assert.Equal(t, types.PMTaskStatusFailed, got.Status)
	assert.Equal(t, 60, got.Progress)
	assert.Equal(t, "模型调用超时", got.ErrorMessage)

	// 不存在的任务返回 notFound
	assert.ErrorIs(t,
		repo.UpdateStatus(ctx, "not-exist", types.PMTaskStatusRunning, 1),
		ErrPMTaskNotFound)
	assert.ErrorIs(t, repo.Fail(ctx, "not-exist", "x"), ErrPMTaskNotFound)
}
