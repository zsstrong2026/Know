package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// 租约过期的运行回收为 failed 时，它的非终态节点必须一并置 cancelled：
// 否则运行详情页会一直显示某一步「仍在执行」，而运行本身早已失败。
func TestExpireLeasesClosesActiveNodes(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning}
	require.NoError(t, repo.CreateRun(ctx, run))
	for _, n := range []struct{ id, status string }{
		{"running", types.SopNodeRunning},
		{"pending", types.SopNodePending},
		{"waiting", types.SopNodeWaitingApproval},
		{"input", types.SopNodePendingInput},
		{"done", types.SopNodeSucceeded},
	} {
		require.NoError(t, repo.CreateNodeRun(ctx, &types.SopNodeRun{
			TenantID: 1, RunID: run.ID, NodeID: n.id, Status: n.status,
		}))
	}
	require.NoError(t, repo.Heartbeat(ctx, run.ID, time.Now().Add(-time.Minute)))

	n, err := repo.ExpireLeases(ctx, time.Now())
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	got, _ := repo.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunFailed, got.Status)

	nodes, err := repo.ListNodeRuns(ctx, 1, run.ID)
	require.NoError(t, err)
	byID := map[string]types.SopNodeRun{}
	for _, nr := range nodes {
		byID[nr.NodeID] = nr
	}
	for _, id := range []string{"running", "pending", "waiting", "input"} {
		require.Equal(t, types.SopNodeCancelled, byID[id].Status, "节点 %s 必须收口", id)
		require.NotNil(t, byID[id].FinishedAt, "收口的节点要补完成时间")
	}
	require.Equal(t, types.SopNodeSucceeded, byID["done"].Status, "已成功的节点保持原结论")
}

// 取消标记的运行：节点收口口径与 failed 路径一致（此前已实现，防回归）。
func TestExpireLeasesCancelRequestedClosesNodes(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning, CancelRequested: true}
	require.NoError(t, repo.CreateRun(ctx, run))
	require.NoError(t, repo.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeRunning,
	}))
	require.NoError(t, repo.Heartbeat(ctx, run.ID, time.Now().Add(-time.Minute)))

	_, err := repo.ExpireLeases(ctx, time.Now())
	require.NoError(t, err)
	got, _ := repo.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunCancelled, got.Status)
	nr, _ := repo.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodeCancelled, nr.Status)
}
