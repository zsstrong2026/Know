package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// childWriterProvider 写一个自定义文件名的产物（子模板声明的 out/child.md），
// 并回报 pass；用于子流程产物导入用例。
type childWriterProvider struct{ kind, file string }

func (c *childWriterProvider) Kind() string { return c.kind }

func (c *childWriterProvider) Prepare(ctx context.Context, n *types.SopNode, ws string) error {
	return nil
}

func (c *childWriterProvider) Execute(
	ctx context.Context, tb executor.TaskBook, o executor.ExecOptions,
) (executor.ExecResult, error) {
	if tb.OutDir != "" {
		_ = os.WriteFile(filepath.Join(tb.OutDir, c.file), []byte("# 子产物"), 0o644)
	}
	return executor.ExecResult{Output: "子步骤完成", GateResult: types.SopGatePass}, nil
}

func (c *childWriterProvider) Cancel(ctx context.Context) error { return nil }

// publishChildTemplate 建一个已发布的单 agent 节点子模板，可选带 required 产物。
func publishChildTemplate(
	t *testing.T, e *Engine, def types.SopDefinition,
) (*types.SopTemplate, *types.SopTemplateRevision) {
	t.Helper()
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "子流程", Status: "draft", Definition: def}
	require.NoError(t, e.deps.Templates.Create(ctx, tpl))
	rev, err := e.deps.Templates.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	tpl.PublishedRevisionID = rev.ID
	return tpl, rev
}

// drive 反复推进直到运行到达终态（或轮次用尽）。
func drive(t *testing.T, e *Engine, runID string, rounds int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < rounds; i++ {
		_, err := e.Advance(ctx, runID)
		require.NoError(t, err)
		got, err := e.deps.Runs.GetRun(ctx, 0, runID)
		require.NoError(t, err)
		if isTerminalRun(got.Status) {
			return
		}
	}
}

func TestChildSOPRunImportsArtifacts(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&childWriterProvider{kind: "platform_agent", file: "child.md"})
	ctx := context.Background()

	childDef := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "c1", Name: "子步骤", Type: "agent",
		Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
		ArtifactOutputs: []types.SopArtifactOutput{{
			Name: "子产物", Path: "out/child.md", Required: true,
			Check: types.SopArtifactCheck{Type: "exists"},
		}},
	}}}
	childTpl, rev := publishChildTemplate(t, e, childDef)

	proj := &types.SopProject{TenantID: 1, Name: "P"}
	require.NoError(t, e.deps.Projects.Create(ctx, proj))

	parentDef := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "p1", Name: "子流程节点", Type: "sop", ChildTemplateID: childTpl.ID,
	}}}
	parent := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: parentDef, ProjectID: proj.ID}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, parent))
	require.NoError(t, e.TickOnce(ctx)) // 抢占 parent

	_, err := e.Advance(ctx, parent.ID) // 启动子 run
	require.NoError(t, err)
	pnr, err := e.deps.Runs.GetNodeRun(ctx, 1, parent.ID, "p1")
	require.NoError(t, err)
	require.Equal(t, types.SopNodeRunning, pnr.Status)
	require.NotEmpty(t, pnr.ChildRunID)

	child, err := e.deps.Runs.GetRun(ctx, 1, pnr.ChildRunID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunTriggerChildRun, child.TriggerKind)
	require.Equal(t, parent.ID, child.ParentRunID)
	require.Equal(t, rev.ID, child.RevisionID)
	require.Equal(t, proj.ID, child.ProjectID)

	require.NoError(t, e.TickOnce(ctx)) // 抢占子 run
	drive(t, e, child.ID, 6)
	childGot, err := e.deps.Runs.GetRun(ctx, 0, child.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunSucceeded, childGot.Status)

	drive(t, e, parent.ID, 6)
	got, err := e.deps.Runs.GetRun(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunSucceeded, got.Status)

	arts, err := e.deps.Runs.ListArtifacts(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.Len(t, arts, 1)
	require.Equal(t, "p1", arts[0].NodeID)
	require.NotEmpty(t, arts[0].SHA256)
	// 子 run 产物按同路径回传父工作区（节点目录口径 out/<子节点>/）。
	require.Equal(t, "out/c1/child.md", arts[0].Path)
	_, err = os.Stat(filepath.Join(got.WorkspaceDir, "out", "c1", "child.md"))
	require.NoError(t, err)

	// 父节点输出汇总了子节点的成功输出。
	pnr, err = e.deps.Runs.GetNodeRun(ctx, 1, parent.ID, "p1")
	require.NoError(t, err)
	require.Contains(t, pnr.Output, "子步骤完成")
	require.Equal(t, types.SopGatePass, pnr.GateResult)
}

func TestChildSOPFailurePropagates(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	childTpl, _ := publishChildTemplate(t, e, types.SopDefinition{Nodes: []types.SopNode{{
		ID: "c1", Type: "agent",
		Executor:  types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
		OnFailure: "fail",
	}}})
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent",
		steps: []executor.ExecResult{{GateResult: types.SopGateFail, GateReason: "子流程失败"}}})

	parent := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "p1", Type: "sop", ChildTemplateID: childTpl.ID, OnFailure: "fail"}},
	}}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, parent))
	require.NoError(t, e.TickOnce(ctx))
	pnr, err := e.deps.Runs.GetNodeRun(ctx, 1, parent.ID, "p1")
	require.NoError(t, err)

	_, err = e.Advance(ctx, parent.ID)
	require.NoError(t, err)
	pnr, err = e.deps.Runs.GetNodeRun(ctx, 1, parent.ID, "p1")
	require.NoError(t, err)
	require.NotEmpty(t, pnr.ChildRunID)

	require.NoError(t, e.TickOnce(ctx))
	drive(t, e, pnr.ChildRunID, 6)
	child, err := e.deps.Runs.GetRun(ctx, 0, pnr.ChildRunID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunFailed, child.Status)

	drive(t, e, parent.ID, 6)
	got, err := e.deps.Runs.GetRun(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunFailed, got.Status)
	require.Contains(t, got.Error, "子流程执行失败")
}

func TestChildTemplateUnpublishedPlainError(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	draft := &types.SopTemplate{TenantID: 1, Name: "草稿", Status: "draft"}
	require.NoError(t, e.deps.Templates.Create(ctx, draft))

	parent := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "p1", Type: "sop", ChildTemplateID: draft.ID}},
	}}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, parent))
	require.NoError(t, e.TickOnce(ctx))
	_, err := e.Advance(ctx, parent.ID)
	require.NoError(t, err)

	got, err := e.deps.Runs.GetRun(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunFailed, got.Status)
	require.Contains(t, got.Error, "尚未发布")
}

// 子模板与父运行同源时拒绝：运行期兜底防递归（发布校验之外的第二道）。
func TestChildSelfReferenceRejected(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "p1", Type: "sop", ChildTemplateID: "placeholder",
	}}}
	tpl, _ := publishChildTemplate(t, e, def)
	// 把节点的子模板指向父运行自己的模板。
	run := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, TemplateID: tpl.ID,
		WorkflowSnapshot: types.SopDefinition{Nodes: []types.SopNode{{
			ID: "p1", Type: "sop", ChildTemplateID: tpl.ID,
		}}}}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.TickOnce(ctx))
	_, err := e.Advance(ctx, run.ID)
	require.NoError(t, err)

	got, err := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunFailed, got.Status)
	require.Contains(t, got.Error, "递归")
}
