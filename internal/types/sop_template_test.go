package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeSopDefinitionDefaults(t *testing.T) {
	def := &SopDefinition{Nodes: []SopNode{{ID: "n1", Name: "分析", Type: "agent",
		Executor: SopNodeExecutor{Kind: "platform_agent", AgentID: "a1"}}}}
	NormalizeSopDefinition(def)
	n := def.Nodes[0]
	if n.MaxAttempts != 3 || n.MaxReworks != 2 || n.LoopMaxIterations != 3 || n.TimeoutMinutes != 60 {
		t.Fatalf("budget defaults wrong: %+v", n)
	}
	if !n.CarryOver || n.OnFailure != "loop" {
		t.Fatalf("carry_over/on_failure defaults wrong: %+v", n)
	}
	if len(n.MCP) != 1 || n.MCP[0] != "know-kb" || n.Executor.KBScope != "project" {
		t.Fatalf("mcp/kb_scope defaults wrong: %+v", n)
	}
}

func TestNormalizeKeepsExplicitValues(t *testing.T) {
	def := &SopDefinition{Nodes: []SopNode{{ID: "n1", Type: "agent", MaxAttempts: 5, OnFailure: "reject",
		MCP: []string{}, Skills: []string{"a"}, Executor: SopNodeExecutor{Kind: "cli", CLIKind: "zcode", KBScope: "all"}}}}
	NormalizeSopDefinition(def)
	n := def.Nodes[0]
	if n.MaxAttempts != 5 || n.OnFailure != "reject" || n.Executor.KBScope != "all" {
		t.Fatalf("explicit values overwritten: %+v", n)
	}
	if n.MCP == nil || len(n.MCP) != 0 {
		t.Fatal("explicit empty mcp must stay empty (not reset to know-kb)")
	}
	if n.Skills == nil || len(n.Skills) != 1 {
		t.Fatal("skills subset must be preserved")
	}
}

// 已配置完整的节点上显式关掉「保留上轮产物」必须保留：carry_over 默认 true
// 只对「完全未配置」的节点生效（见 NormalizeSopDefinition 注释）。
func TestNormalizeKeepsExplicitCarryOverFalse(t *testing.T) {
	def := &SopDefinition{Nodes: []SopNode{{ID: "n1", Type: "agent", OnFailure: "loop",
		MaxAttempts: 3, MaxReworks: 2, LoopMaxIterations: 3, TimeoutMinutes: 60,
		MCP: []string{"know-kb"}, CarryOver: false}}}
	NormalizeSopDefinition(def)
	if def.Nodes[0].CarryOver {
		t.Fatal("explicit carry_over=false must be preserved")
	}
}

func TestNormalizeArtifactCheckDefaultExists(t *testing.T) {
	def := &SopDefinition{Nodes: []SopNode{{ID: "n1", Type: "agent",
		ArtifactOutputs: []SopArtifactOutput{{Name: "报告", Path: "out/a.md", Required: true}}}}}
	NormalizeSopDefinition(def)
	if got := def.Nodes[0].ArtifactOutputs[0].Check.Type; got != "exists" {
		t.Fatalf("artifact check type default = %q, want exists", got)
	}
}

// definition 的 JSON 字段名是 engine/executor/handler/前端的共享契约，改名必须失败。
func TestSopDefinitionJSONContract(t *testing.T) {
	def := SopDefinition{
		Nodes: []SopNode{{
			ID: "n1", Name: "分析", Type: SopNodeTypeAgent,
			Executor:        SopNodeExecutor{Kind: SopExecutorPlatformAgent, AgentID: "a1", KBScope: "project"},
			Skills:          nil, // nil=角色全部，必须序列化成 null 而非 []
			MCP:             []string{"know-kb"},
			ArtifactOutputs: []SopArtifactOutput{{Name: "报告", Path: "out/a.md", MediaType: "text/markdown", Required: true, Check: SopArtifactCheck{Type: "exists"}}},
			OnFailure:       SopOnFailureLoop,
			MaxAttempts:     3, MaxReworks: 2, LoopMaxIterations: 3, CarryOver: true, TimeoutMinutes: 60,
			Position: SopPosition{X: 1, Y: 2},
		}},
		Edges:               []SopEdge{{ID: "e1", Source: "n0", Target: "n1", When: "success"}},
		InputSchema:         []string{"title"},
		OnSuccessItemStatus: "in_review",
	}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{
		`"nodes"`, `"edges"`, `"input_schema"`, `"on_success_item_status"`,
		`"executor"`, `"agent_id"`, `"kb_scope"`, `"task_prompt"`, `"task_refs"`,
		`"extra_rules"`, `"artifact_outputs"`, `"gate_commands"`, `"gate_extra"`,
		`"on_failure"`, `"reject_to"`, `"max_attempts"`, `"max_reworks"`,
		`"loop_max_iterations"`, `"carry_over"`, `"serial_group"`, `"timeout_minutes"`,
		`"position"`, `"media_type"`, `"required"`, `"check"`, `"when"`,
		`"x"`, `"y"`, `"skills":null`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("definition JSON 缺少契约字段 %s:\n%s", key, raw)
		}
	}
	// 可选字段零值不出现在 JSON 里（omitempty）
	for _, key := range []string{`"script"`, `"approver_hint"`, `"child_template_id"`, `"auto_ingest"`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("可选字段 %s 的空值不应序列化:\n%s", key, raw)
		}
	}
	// role_id 不可省略（null 表示「仅任务书」，是有语义的取值）
	if !strings.Contains(string(raw), `"role_id":null`) {
		t.Fatalf("executor.role_id 必须显式序列化为 null:\n%s", raw)
	}
}
