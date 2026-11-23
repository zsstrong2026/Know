package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// resumeProvider 记录任务书里的人工答复与续跑会话，返回通过。
type resumeProvider struct {
	kind       string
	lastAnswer string
	lastResume string
	calls      int
}

func (r *resumeProvider) Kind() string { return r.kind }

func (r *resumeProvider) Prepare(_ context.Context, _ *types.SopNode, _ string) error { return nil }

func (r *resumeProvider) Execute(
	_ context.Context, tb executor.TaskBook, _ executor.ExecOptions,
) (executor.ExecResult, error) {
	r.calls++
	r.lastResume = tb.ResumeSessionID
	if strings.Contains(tb.Prompt, "用方案A") {
		r.lastAnswer = "用方案A"
	}
	return executor.ExecResult{Output: "done", GateResult: types.SopGatePass, SessionID: tb.ResumeSessionID}, nil
}

func (r *resumeProvider) Cancel(_ context.Context) error { return nil }

// stubPrompts 记录答复写入挂起提示的调用。
type stubPrompts struct {
	answers []string
	err     error
}

func (s *stubPrompts) Answer(_ context.Context, _ uint64, _, answer string) error {
	s.answers = append(s.answers, answer)
	return s.err
}

func newPendingInputRun(t *testing.T, e *Engine) *types.SopRun {
	t.Helper()
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "n1", Type: "agent", CarryOver: true,
		Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
	}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunWaiting, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1",
		Status: types.SopNodePendingInput, SessionID: "sess1",
	}))
	return run
}

func TestAnswerPromptResumesPendingInput(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := newPendingInputRun(t, e)
	prompts := &stubPrompts{}
	e.deps.Prompts = prompts
	sp := &resumeProvider{kind: "platform_agent"}
	e.deps.Registry.Register(sp)

	require.NoError(t, e.AnswerPrompt(ctx, 1, run.ID, "n1", "用方案A"))
	require.Equal(t, []string{"用方案A"}, prompts.answers, "答复要先写进挂起提示")
	require.Equal(t, answerSource, e.loadRunHumanContextSource(t, run.ID))

	nr, err := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.NoError(t, err)
	require.Equal(t, types.SopNodeRunning, nr.Status, "答复后交还 Advance 续跑")
	got, err := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunRunning, got.Status)

	for i := 0; i < 3; i++ {
		if _, err := e.Advance(ctx, run.ID); err != nil {
			t.Fatalf("advance: %v", err)
		}
	}
	got, err = e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	require.Equal(t, "用方案A", sp.lastAnswer, "答复经 HumanContext 注入任务书")
	require.Equal(t, "sess1", sp.lastResume, "carry 会话续跑")
}

// loadRunHumanContextSource 断言人工上下文里出现了答复来源（避免测试自己拼 map）。
func (e *Engine) loadRunHumanContextSource(t *testing.T, runID string) string {
	t.Helper()
	run, err := e.deps.Runs.GetRun(context.Background(), 0, runID)
	require.NoError(t, err)
	for _, h := range run.HumanContext {
		if h.Source == answerSource {
			return h.Source
		}
	}
	return ""
}

func TestAnswerPromptRejectsNonPendingInput(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := newPendingInputRun(t, e)
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	nr.Status = types.SopNodeRunning
	require.NoError(t, e.deps.Runs.UpdateNodeRun(ctx, nr))
	require.Error(t, e.AnswerPrompt(ctx, 1, run.ID, "n1", "x"))
}

func TestAnswerPromptRejectsEmptyAnswer(t *testing.T) {
	e, _, _ := newTestEngine(t)
	run := newPendingInputRun(t, e)
	require.Error(t, e.AnswerPrompt(context.Background(), 1, run.ID, "n1", "  "))
}

func TestAnswerPromptRejectsTerminalRun(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := newPendingInputRun(t, e)
	run.Status = types.SopRunSucceeded
	require.NoError(t, e.deps.Runs.UpdateRun(ctx, run))
	require.Error(t, e.AnswerPrompt(ctx, 1, run.ID, "n1", "x"))
}

func TestAnswerPromptKeepsPendingInputWhenPromptWriteFails(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := newPendingInputRun(t, e)
	e.deps.Prompts = &stubPrompts{err: context.DeadlineExceeded}
	require.Error(t, e.AnswerPrompt(ctx, 1, run.ID, "n1", "x"))
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodePendingInput, nr.Status, "答复没落库就不能把节点放走")
}

func TestResumeNodeKeepsSessionWhenCarryOver(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := newPendingInputRun(t, e)
	sp := &resumeProvider{kind: "platform_agent"}
	e.deps.Registry.Register(sp)

	require.NoError(t, e.ResumeNode(ctx, 1, run.ID, "n1"))
	nr, err := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.NoError(t, err)
	require.Equal(t, types.SopNodePending, nr.Status)
	require.Equal(t, "sess1", nr.SessionID, "carry_over 节点保留会话")
	require.Empty(t, nr.ResumeSessionID, "没有挂起快照时不走 Resume")

	for i := 0; i < 3; i++ {
		_, _ = e.Advance(ctx, run.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	require.Equal(t, 1, sp.calls)
}

func TestResumeNodeDropsSessionWhenNotCarryOver(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "n1", Type: "agent", CarryOver: false,
		Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
	}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunWaiting, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodePendingInput, SessionID: "sess1",
	}))
	require.NoError(t, e.ResumeNode(ctx, 1, run.ID, "n1"))
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Empty(t, nr.SessionID, "不 carry 的节点重开会话")
}
