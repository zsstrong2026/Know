package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// 打回/重跑重置节点时，必须把计时与用量清掉，否则运行详情会把上一轮的
// started_at 与 token 用量当成这一轮的（耗时越算越长、用量翻倍）。
// 会话不能无条件清：carry_over 的节点要靠它续跑，清掉就断上下文。
func TestResetNodesClearsTimingAndHonorsCarryOver(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()

	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning}
	require.NoError(t, repo.CreateRun(ctx, run))

	started := time.Now().Add(-time.Minute)
	keep := &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "keep", Status: types.SopNodeSucceeded,
		StartedAt: &started, SessionID: "s1", ResumeSessionID: "r1",
		Usage: types.SopUsage{InputTokens: 100, OutputTokens: 50, CostUSD: 0.5},
	}
	drop := &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "drop", Status: types.SopNodeSucceeded,
		StartedAt: &started, SessionID: "s2", ResumeSessionID: "r2",
		Usage: types.SopUsage{InputTokens: 7, OutputTokens: 3, CostUSD: 0.01},
	}
	require.NoError(t, repo.CreateNodeRun(ctx, keep))
	require.NoError(t, repo.CreateNodeRun(ctx, drop))

	require.NoError(t, repo.ResetNodes(ctx, run.ID, []string{"keep", "drop"},
		map[string]bool{"keep": true}))

	gotKeep, err := repo.GetNodeRun(ctx, 1, run.ID, "keep")
	require.NoError(t, err)
	require.Nil(t, gotKeep.StartedAt, "计时必须清掉")
	require.Equal(t, types.SopUsage{}, gotKeep.Usage, "用量必须清零")
	require.Equal(t, "s1", gotKeep.SessionID, "carry_over 节点保留会话续跑")
	require.Equal(t, "r1", gotKeep.ResumeSessionID)

	gotDrop, err := repo.GetNodeRun(ctx, 1, run.ID, "drop")
	require.NoError(t, err)
	require.Nil(t, gotDrop.StartedAt)
	require.Equal(t, types.SopUsage{}, gotDrop.Usage)
	require.Empty(t, gotDrop.SessionID, "非 carry_over 节点不得复用旧会话")
	require.Empty(t, gotDrop.ResumeSessionID)
}
