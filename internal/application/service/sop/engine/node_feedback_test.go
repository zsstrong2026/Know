package engine

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// promptRecorder 记录每次下发的任务书，供断言「返工意见」是否残留。
type promptRecorder struct {
	kind    string
	steps   []executor.ExecResult
	mu      sync.Mutex
	i       int
	prompts []string
}

func (p *promptRecorder) Kind() string { return p.kind }

func (p *promptRecorder) Prepare(context.Context, *types.SopNode, string) error { return nil }

func (p *promptRecorder) Execute(
	_ context.Context, tb executor.TaskBook, _ executor.ExecOptions,
) (executor.ExecResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prompts = append(p.prompts, tb.Prompt)
	if p.i >= len(p.steps) {
		return executor.ExecResult{GateResult: types.SopGatePass}, nil
	}
	res := p.steps[p.i]
	p.i++
	return res, nil
}

func (p *promptRecorder) Cancel(context.Context) error { return nil }

// 返工一轮后通过：Feedback 必须清空，否则下一次重跑/重试会把上一轮的
// 「返工意见」当成新指令再喂给执行器，误导模型。
func TestPassClearsStaleFeedback(t *testing.T) {
	e, _, _ := newTestEngine(t)
	rec := &promptRecorder{kind: "platform_agent", steps: []executor.ExecResult{
		{Output: "v1", GateResult: types.SopGateRework, GateReason: "缺少异常态"},
		{Output: "v2", GateResult: types.SopGatePass},
	}}
	e.deps.Registry.Register(rec)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
		OnFailure: "loop", MaxAttempts: 3, CarryOver: true,
	}}}
	run := startRun(t, e, def)
	for i := 0; i < 4; i++ {
		e.Advance(ctx, run.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)

	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodeSucceeded, nr.Status)
	require.Equal(t, types.SopGatePass, nr.GateResult, "门禁结论要保留 pass")
	require.Empty(t, nr.Feedback, "成功后不得残留返工意见")
	require.Empty(t, nr.GateReason, "成功后不得残留门禁原因")

	require.Len(t, rec.prompts, 2, "应正好跑两轮：返工一次、通过一次")
	require.NotContains(t, rec.prompts[0], "返工意见", "首轮不该有返工意见")
	require.Contains(t, rec.prompts[1], "返工意见", "返工轮必须带上一轮意见")

	// 成功后按落库状态重渲任务书：不得再出现「返工意见」小节。
	rerender := RenderTaskBook(TaskBookInput{Node: def.Nodes[0], Feedback: nr.Feedback})
	require.NotContains(t, rerender, "返工意见")
}
