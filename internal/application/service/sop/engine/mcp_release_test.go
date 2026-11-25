package engine

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// releaseRecorder 记录 ReleaseMCP 调用（运行收口/取消都必须调一次）。
type releaseRecorder struct {
	mu  sync.Mutex
	ids []string
}

func (r *releaseRecorder) release(_ context.Context, _ uint64, runID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, runID)
	return nil
}

func (r *releaseRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ids...)
}

// 运行成功后必须回收该运行签发的 MCP 端点（否则端点表只增不减）。
func TestFinalizeReleasesMCPEndpoints(t *testing.T) {
	e, _, _ := newTestEngine(t)
	rec := &releaseRecorder{}
	e.deps.ReleaseMCP = rec.release
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent"})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
	}}}
	run := startRun(t, e, def)
	for i := 0; i < 3; i++ {
		e.Advance(ctx, run.ID)
	}
	// 测试引擎关掉了 runLoop，收口由 finalizeRun 显式触发（生产由 runLoop 调）。
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	require.NoError(t, e.finalizeRun(ctx, got))
	require.Equal(t, []string{run.ID}, rec.snapshot(), "成功收口要回收端点")
}

// 取消收口同样回收：取消路径不走 OnRunFinal，端点不能跟着泄漏。
func TestCancelReleasesMCPEndpoints(t *testing.T) {
	e, _, _ := newTestEngine(t)
	rec := &releaseRecorder{}
	e.deps.ReleaseMCP = rec.release
	ctx := context.Background()
	run := &types.SopRun{
		TenantID: 1, Status: types.SopRunPaused,
		WorkflowSnapshot: types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}},
	}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodePending,
	}))
	require.NoError(t, e.Cancel(ctx, 1, run.ID))
	require.Equal(t, []string{run.ID}, rec.snapshot(), "取消收口也要回收端点")
}

// 清理失败不影响运行结论：ReleaseMCP 报错时运行仍是 succeeded。
func TestReleaseMCPFailureDoesNotAffectRun(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.ReleaseMCP = func(context.Context, uint64, string) error {
		return errReleaseBoom
	}
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent"})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
	}}}
	run := startRun(t, e, def)
	for i := 0; i < 3; i++ {
		e.Advance(ctx, run.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	require.NoError(t, e.finalizeRun(ctx, got))
}

type errRelease struct{}

func (errRelease) Error() string { return "release boom" }

var errReleaseBoom = errRelease{}

var _ executor.Provider = (*scriptedProvider)(nil)
