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
// 并回报 pass；用于子流程产物导入用例。content 可在用例中途改写，模拟子运行
// 重跑后产出不同内容。
type childWriterProvider struct {
	kind, file, content string
}

func (c *childWriterProvider) Kind() string { return c.kind }

func (c *childWriterProvider) Prepare(ctx context.Context, n *types.SopNode, ws string) error {
	return nil
}

func (c *childWriterProvider) Execute(
	ctx context.Context, tb executor.TaskBook, o executor.ExecOptions,
) (executor.ExecResult, error) {
	if tb.OutDir != "" {
		body := c.content
		if body == "" {
			body = "# 子产物"
		}
		_ = os.WriteFile(filepath.Join(tb.OutDir, c.file), []byte(body), 0o644)
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

// artifactChildDef 造一个单 agent 节点、声明必需产物 out/child.md 的子模板定义。
func artifactChildDef() types.SopDefinition {
	return types.SopDefinition{Nodes: []types.SopNode{{
		ID: "c1", Name: "子步骤", Type: "agent",
		Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
		ArtifactOutputs: []types.SopArtifactOutput{{
			Name: "子产物", Path: "out/child.md", Required: true,
			Check: types.SopArtifactCheck{Type: "exists"},
		}},
	}}}
}

// 换子运行（返工/重试）后父节点产物必须整体换成新子运行的内容：
// 旧实现在 (node, attempt, path) 上做去重，Attempt 归零会撞上旧行、新内容导不进来。
func TestChildReworkReimportsNewContent(t *testing.T) {
	e, _, _ := newTestEngine(t)
	prov := &childWriterProvider{kind: "platform_agent", file: "child.md", content: "# A"}
	e.deps.Registry.Register(prov)
	ctx := context.Background()

	childTpl, _ := publishChildTemplate(t, e, artifactChildDef())
	parent := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "p1", Name: "子流程节点", Type: "sop", ChildTemplateID: childTpl.ID}},
	}}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, parent))
	require.NoError(t, e.TickOnce(ctx))
	_, err := e.Advance(ctx, parent.ID) // 拉起第一条子运行
	require.NoError(t, err)

	pnr, err := e.deps.Runs.GetNodeRun(ctx, 1, parent.ID, "p1")
	require.NoError(t, err)
	firstChild := pnr.ChildRunID
	require.NotEmpty(t, firstChild)
	require.NoError(t, e.TickOnce(ctx))
	drive(t, e, firstChild, 6)
	_, err = e.Advance(ctx, parent.ID) // 导入 A 并让父运行成功
	require.NoError(t, err)
	drive(t, e, parent.ID, 3)

	got, err := e.deps.Runs.GetRun(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	arts, err := e.deps.Runs.ListArtifacts(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.Len(t, arts, 1)
	firstSHA := arts[0].SHA256

	// 模拟返工（与 Retry 同构的落库状态）：节点回 pending、Attempt 归零、ChildRunID 清空。
	require.NoError(t, e.deps.Runs.ResetNodes(ctx, parent.ID, []string{"p1"}, nil))
	pnr, err = e.deps.Runs.GetNodeRun(ctx, 1, parent.ID, "p1")
	require.NoError(t, err)
	pnr.Attempt = 0
	require.NoError(t, e.deps.Runs.UpdateNodeRun(ctx, pnr))
	got.Status = types.SopRunRunning
	require.NoError(t, e.deps.Runs.UpdateRun(ctx, got))

	prov.content = "# B"
	_, err = e.Advance(ctx, parent.ID) // 旧子运行已终态 → 新建一条子运行并清掉旧产物
	require.NoError(t, err)
	pnr, err = e.deps.Runs.GetNodeRun(ctx, 1, parent.ID, "p1")
	require.NoError(t, err)
	require.NotEmpty(t, pnr.ChildRunID)
	require.NotEqual(t, firstChild, pnr.ChildRunID, "返工后必须是一条新的子运行")
	require.NoError(t, e.TickOnce(ctx))
	drive(t, e, pnr.ChildRunID, 6)
	_, err = e.Advance(ctx, parent.ID)
	require.NoError(t, err)
	drive(t, e, parent.ID, 3)

	arts, err = e.deps.Runs.ListArtifacts(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.Len(t, arts, 1, "旧子运行的产物行必须被替换，不得残留同路径旧行")
	require.NotEqual(t, firstSHA, arts[0].SHA256)
	data, err := os.ReadFile(filepath.Join(got.WorkspaceDir, "out", "c1", "child.md"))
	require.NoError(t, err)
	require.Equal(t, "# B", string(data), "父工作区产物必须是新子运行的内容")
}

// 拉起幂等：连调两次 startChildRun 只允许一条子运行（第二次接管残留/已建的那条）。
func TestChildStartIdempotentReusesExisting(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	childTpl, _ := publishChildTemplate(t, e, types.SopDefinition{Nodes: []types.SopNode{{
		ID: "c1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
	}}})
	parent := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "p1", Type: "sop", ChildTemplateID: childTpl.ID}},
	}}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, parent))
	require.NoError(t, e.TickOnce(ctx))
	run, err := e.deps.Runs.GetRun(ctx, 1, parent.ID)
	require.NoError(t, err)
	node := nodeByID(&run.WorkflowSnapshot, "p1")
	require.NotNil(t, node)
	nr, err := e.deps.Runs.GetNodeRun(ctx, 1, parent.ID, "p1")
	require.NoError(t, err)

	require.NoError(t, e.startChildRun(ctx, run, node, nr))
	first := nr.ChildRunID
	require.NotEmpty(t, first)
	require.NoError(t, e.startChildRun(ctx, run, node, nr))
	require.Equal(t, first, nr.ChildRunID, "第二次拉起必须复用同一条子运行")

	children, err := e.deps.Runs.ListChildRuns(ctx, parent.ID)
	require.NoError(t, err)
	require.Len(t, children, 1, "同一父节点任何时刻只能有一条子运行")
}

// A→B→A 环：沿祖先链上溯发现子模板已在链上即拒（发布校验只看模板自身，看不到跨模板环）。
func TestChildRecursionCycleRejected(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	bDraft := &types.SopTemplate{TenantID: 1, Name: "B", Status: "draft",
		Definition: types.SopDefinition{Nodes: []types.SopNode{{ID: "b1", Name: "B 的子流程", Type: "sop"}}}}
	require.NoError(t, e.deps.Templates.Create(ctx, bDraft))
	aDraft := &types.SopTemplate{TenantID: 1, Name: "A", Status: "draft",
		Definition: types.SopDefinition{Nodes: []types.SopNode{{
			ID: "a1", Name: "A 的子流程", Type: "sop", ChildTemplateID: bDraft.ID,
			OnFailure: types.SopOnFailureFail,
		}}}}
	require.NoError(t, e.deps.Templates.Create(ctx, aDraft))
	// B 的子流程指回 A，形成 A→B→A。
	bDraft.Definition.Nodes[0].ChildTemplateID = aDraft.ID
	require.NoError(t, e.deps.Templates.Update(ctx, bDraft))
	_, err := e.deps.Templates.Publish(ctx, 1, bDraft.ID, "u1")
	require.NoError(t, err)
	aRev, err := e.deps.Templates.Publish(ctx, 1, aDraft.ID, "u1")
	require.NoError(t, err)

	parent := &types.SopRun{TenantID: 1, Status: types.SopRunQueued,
		TemplateID: aDraft.ID, WorkflowSnapshot: aRev.Definition}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, parent))
	require.NoError(t, e.TickOnce(ctx))
	_, err = e.Advance(ctx, parent.ID) // 拉起 B
	require.NoError(t, err)
	children, err := e.deps.Runs.ListChildRuns(ctx, parent.ID)
	require.NoError(t, err)
	require.Len(t, children, 1)
	childID := children[0].ID

	require.NoError(t, e.TickOnce(ctx))
	drive(t, e, childID, 6) // B 的 sop 节点要引用 A —— 必须被祖先链拦下
	childGot, err := e.deps.Runs.GetRun(ctx, 1, childID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunFailed, childGot.Status)
	require.Contains(t, childGot.Error, "递归")

	drive(t, e, parent.ID, 6)
	got, err := e.deps.Runs.GetRun(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunFailed, got.Status)
}

// 子运行的 ParentRunID 与父运行不符（串号数据）时节点直接失败，不导入任何产物。
func TestChildRunOwnershipMismatchFailsNode(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	foreign := &types.SopRun{TenantID: 1, Status: types.SopRunRunning, ParentRunID: "someone-else"}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, foreign))

	parent := &types.SopRun{TenantID: 1, Status: types.SopRunRunning, WorkflowSnapshot: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "p1", Type: "sop", ChildTemplateID: "tpl"}},
	}}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, parent))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: parent.ID, NodeID: "p1",
		Status: types.SopNodeRunning, ChildRunID: foreign.ID,
	}))

	_, err := e.Advance(ctx, parent.ID)
	require.NoError(t, err)
	got, err := e.deps.Runs.GetRun(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunFailed, got.Status)
	require.Contains(t, got.Error, "归属")

	arts, err := e.deps.Runs.ListArtifacts(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.Empty(t, arts)
}

// ChildRunID 指向不存在的运行：返回带上下文的错误，绝不能是 %w 包 nil 的乱码。
func TestReconcileMissingChildRunPlainError(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	parent := &types.SopRun{TenantID: 1, Status: types.SopRunRunning, WorkflowSnapshot: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "p1", Type: "sop", ChildTemplateID: "tpl"}},
	}}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, parent))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: parent.ID, NodeID: "p1",
		Status: types.SopNodeRunning, ChildRunID: "gone",
	}))

	_, err := e.Advance(ctx, parent.ID)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "%!w")
	require.Contains(t, err.Error(), "读取子流程运行失败")
}

// 取消父运行必须向下传播：子运行一并取消，不能在父运行收口后继续烧 token。
func TestCancelPropagatesToChildRun(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	childTpl, _ := publishChildTemplate(t, e, types.SopDefinition{Nodes: []types.SopNode{{
		ID: "c1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
	}}})
	parent := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "p1", Type: "sop", ChildTemplateID: childTpl.ID}},
	}}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, parent))
	require.NoError(t, e.TickOnce(ctx))
	_, err := e.Advance(ctx, parent.ID)
	require.NoError(t, err)
	pnr, err := e.deps.Runs.GetNodeRun(ctx, 1, parent.ID, "p1")
	require.NoError(t, err)
	require.NotEmpty(t, pnr.ChildRunID)

	require.NoError(t, e.Cancel(ctx, 1, parent.ID))
	childGot, err := e.deps.Runs.GetRun(ctx, 1, pnr.ChildRunID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunCancelled, childGot.Status, "子运行必须随父运行一起取消")
	require.NotNil(t, childGot.FinishedAt)
	got, err := e.deps.Runs.GetRun(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunCancelled, got.Status)
}
