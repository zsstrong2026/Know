package sop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// stubRunner 按序返回预置文本（多轮追问/编排用）。
type stubRunner struct {
	texts  []string
	calls  []AIRequest
	failOn int
}

func (r *stubRunner) Ask(_ context.Context, req AIRequest) (string, string, error) {
	r.calls = append(r.calls, req)
	if r.failOn > 0 && len(r.calls) == r.failOn {
		return "", "", context.DeadlineExceeded
	}
	idx := len(r.calls) - 1
	if idx >= len(r.texts) {
		idx = len(r.texts) - 1
	}
	return r.texts[idx], "sess-1", nil
}

// stubItemAPI 返回预置事项与附件。
type stubItemAPI struct {
	item *types.SopItem
	atts []types.SopItemAttachment
}

func (s stubItemAPI) Get(_ context.Context, _ uint64, _ string) (*types.SopItem, error) {
	if s.item == nil {
		return nil, context.Canceled
	}
	return s.item, nil
}

func (s stubItemAPI) ListAttachments(
	_ context.Context, _ uint64, _ string,
) ([]types.SopItemAttachment, error) {
	return s.atts, nil
}

// stubClarifications 记录澄清落库。
type stubClarifications struct{ added []types.SopItemClarification }

func (c *stubClarifications) AddClarification(_ context.Context, cl *types.SopItemClarification) error {
	c.added = append(c.added, *cl)
	return nil
}

func validDef() types.SopDefinition {
	return types.SopDefinition{
		Nodes: []types.SopNode{
			{ID: "n0", Name: "分析", Type: types.SopNodeTypeAgent},
			{ID: "n1", Name: "实现", Type: types.SopNodeTypeAgent},
		},
		Edges: []types.SopEdge{{ID: "e1", Source: "n0", Target: "n1"}},
	}
}

func TestApplyNodeOpsValidatesInventory(t *testing.T) {
	svc := NewAIService(&stubRunner{}, stubItemAPI{}, &stubClarifications{})
	inv := Inventory{
		Agents: []InventoryItem{{ID: "a1", Name: "工程师"}},
		CLIs:   []string{"zcode"},
		Models: []InventoryItem{{ID: "m1", Name: "默认"}},
		Skills: []string{"go-test"},
		MCPs:   []string{"know-kb"},
	}
	ops := `{"ops":[
		{"op":"set","node_id":"n1","fields":{"executor":{"kind":"platform_agent","agent_id":"a1"}}},
		{"op":"set","node_id":"n1","fields":{"executor":{"kind":"platform_agent","agent_id":"不存在"}}},
		{"op":"set","node_id":"nX","fields":{"name":"幽灵节点"}}
	]}`
	res, err := svc.ApplyNodeOps(context.Background(), validDef(), ops, inv)
	require.NoError(t, err)
	require.Len(t, res.Applied, 1)
	require.Len(t, res.Skipped, 2)
	require.Contains(t, res.Skipped[0].Reason, "执行角色不存在")
	require.Contains(t, res.Skipped[1].Reason, "节点不存在")
	require.Equal(t, "a1", res.Definition.Nodes[1].Executor.AgentID)
}

// 打回目标必须是上游；不是就跳过并说明。
func TestApplyNodeOpsRejectTargetMustBeUpstream(t *testing.T) {
	svc := NewAIService(&stubRunner{}, stubItemAPI{}, &stubClarifications{})
	ops := `{"ops":[{"op":"set","node_id":"n0","fields":{"on_failure":"reject","reject_to":"n1"}}]}`
	res, err := svc.ApplyNodeOps(context.Background(), validDef(), ops, Inventory{})
	require.NoError(t, err)
	require.Empty(t, res.Applied)
	require.Len(t, res.Skipped, 1)
	require.Contains(t, res.Skipped[0].Reason, "上游")

	ok := `{"ops":[{"op":"set","node_id":"n1","fields":{"on_failure":"reject","reject_to":"n0"}}]}`
	res, err = svc.ApplyNodeOps(context.Background(), validDef(), ok, Inventory{})
	require.NoError(t, err)
	require.Len(t, res.Applied, 1)
}

// gate_commands 永不被 AI 直写：末尾按定义重算。
func TestApplyNodeOpsIgnoresGateCommands(t *testing.T) {
	svc := NewAIService(&stubRunner{}, stubItemAPI{}, &stubClarifications{})
	def := validDef()
	def.Nodes[1].ArtifactOutputs = []types.SopArtifactOutput{{
		Name: "r", Path: "out/a.md", Check: types.SopArtifactCheck{Type: "exists"},
	}}
	ops := `{"ops":[{"op":"set","node_id":"n1","fields":{"gate_commands":["rm -rf /"],"name":"改名"}}]}`
	res, err := svc.ApplyNodeOps(context.Background(), def, ops, Inventory{})
	require.NoError(t, err)
	require.Equal(t, "改名", res.Definition.Nodes[1].Name)
	for _, cmd := range res.Definition.Nodes[1].GateCommands {
		require.NotContains(t, cmd, "rm -rf")
	}
	require.NotEmpty(t, res.Definition.Nodes[1].GateCommands, "派生命令按产物契约重算")
}

// 增删节点/连线。
func TestApplyNodeOpsGraphEdits(t *testing.T) {
	svc := NewAIService(&stubRunner{}, stubItemAPI{}, &stubClarifications{})
	ops := `{"ops":[
		{"op":"add_node","node_id":"n2","fields":{"name":"验收","type":"agent"}},
		{"op":"add_edge","from_id":"n1","to_id":"n2"},
		{"op":"remove_edge","edge_id":"e1"},
		{"op":"remove_node","node_id":"n0"}
	]}`
	res, err := svc.ApplyNodeOps(context.Background(), validDef(), ops, Inventory{})
	require.NoError(t, err)
	require.Len(t, res.Applied, 4)
	require.Len(t, res.Definition.Nodes, 2, "删掉 n0、加了 n2")
	require.Len(t, res.Definition.Edges, 1, "删 e1、加 n1→n2")
	require.Equal(t, "n1", res.Definition.Edges[0].Source)

	// 重复 id 被拒
	dup := `{"ops":[{"op":"add_node","node_id":"n1","fields":{"name":"重名"}}]}`
	res, err = svc.ApplyNodeOps(context.Background(), validDef(), dup, Inventory{})
	require.NoError(t, err)
	require.Contains(t, res.Skipped[0].Reason, "已存在")
}

// 草稿解析：归一化 + 派生命令 + warnings，状态恒为 draft。
func TestParseFlowDraftDraftOnly(t *testing.T) {
	svc := NewAIService(&stubRunner{}, stubItemAPI{}, &stubClarifications{})
	raw := "```json\n{\"nodes\":[{\"id\":\"n1\",\"name\":\"分析\",\"type\":\"agent\",\"task_prompt\":\"x\"}],\"edges\":[]}\n```"
	draft, err := svc.ParseFlowDraft(context.Background(), raw)
	require.NoError(t, err)
	require.Equal(t, "draft", draft.Status)
	require.Len(t, draft.Definition.Nodes, 1)
	require.Equal(t, 3, draft.Definition.Nodes[0].MaxAttempts, "归一化补默认值")
	require.Empty(t, draft.Warnings, "草稿允许缺执行器，不应报错")

	// 结构性错误（打回缺目标）以警告形式返回给画布，而不是让整份草稿失败。
	bad := `{"nodes":[{"id":"n1","name":"坏","type":"agent","on_failure":"reject"}]}`
	draft, err = svc.ParseFlowDraft(context.Background(), bad)
	require.NoError(t, err)
	require.NotEmpty(t, draft.Warnings)
	require.Contains(t, draft.Warnings[0], "打回哪一步")
}

// 拆解：解析 JSON 数组，过滤空标题。
func TestBreakdownParsesSubItems(t *testing.T) {
	runner := &stubRunner{texts: []string{
		`[{"title":"子事项1","description":"x"},{"title":"  ","description":"空"}]`,
	}}
	svc := NewAIService(runner, stubItemAPI{}, &stubClarifications{})
	items, err := svc.Breakdown(context.Background(), 1, "p1", ItemDraft{Title: "大需求"})
	require.NoError(t, err)
	require.Len(t, items, 1, "空标题被过滤")
	require.Equal(t, "子事项1", items[0].Title)
	require.Contains(t, runner.calls[0].System, "JSON 数组")
}

// 澄清生成：问句夹推测答案，落库为 open/ai。
func TestGenerateClarificationsPersistsOpen(t *testing.T) {
	runner := &stubRunner{texts: []string{
		`[{"question":"目标用户是谁？","guess":"内部研发"},{"question":"上线时间？","guess":""}]`,
	}}
	store := &stubClarifications{}
	item := &types.SopItem{ID: "i1", No: "TP-1", Title: "需求", Status: "todo"}
	svc := NewAIService(runner, stubItemAPI{item: item}, store)

	out, err := svc.GenerateClarifications(context.Background(), 1, "i1")
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.Contains(t, out[0].Question, "推测答案：内部研发")
	require.NotContains(t, out[1].Question, "推测答案", "没有推测就不夹")
	require.Len(t, store.added, 2)
	for _, c := range store.added {
		require.Equal(t, types.SopClarificationStatusOpen, c.Status)
		require.Equal(t, "ai", c.Source)
	}
}

// 分析会话：返回会话 id；上下文包含事项字段与附件文本。
func TestAnalyzeItemBuildsContext(t *testing.T) {
	runner := &stubRunner{texts: []string{"分析正文"}}
	item := &types.SopItem{ID: "i1", No: "TP-1", Title: "登录改造", Status: "in_progress", Description: "支持 SSO"}
	svc := NewAIService(runner, stubItemAPI{item: item, atts: []types.SopItemAttachment{
		{Name: "需求.md", MediaType: "text/markdown", TextContent: "# SSO 需求"},
	}}, &stubClarifications{})

	sessionID, err := svc.AnalyzeItem(context.Background(), 1, "i1")
	require.NoError(t, err)
	require.Equal(t, "sess-1", sessionID)
	prompt := runner.calls[0].Prompt
	require.Contains(t, prompt, "TP-1")
	require.Contains(t, prompt, "登录改造")
	require.Contains(t, prompt, "SSO 需求")
}

// 编排模式：非法 mode 给固定白话错误（handler 映射 400）。
func TestOrchestrateModeValidation(t *testing.T) {
	_, err := OrchestrateSystemPrompt("bogus")
	require.ErrorIs(t, err, ErrSopAIModeInvalid)
	flow, err := OrchestrateSystemPrompt(OrchestrateModeFlow)
	require.NoError(t, err)
	require.Contains(t, flow, "gate_commands")
	node, err := OrchestrateSystemPrompt(OrchestrateModeNode)
	require.NoError(t, err)
	require.Contains(t, node, "清单")
}

// 模型输出常带代码块/前缀废话：提取器要能剥干净。
func TestExtractJSONToleratesMarkdown(t *testing.T) {
	require.JSONEq(t, `{"a":1}`, extractJSON("好的，如下：\n```json\n{\"a\":1}\n```\n希望有帮助"))
	require.JSONEq(t, `[{"b":2}]`, extractJSON("结果：[{\"b\":2}] 以上"))
}
