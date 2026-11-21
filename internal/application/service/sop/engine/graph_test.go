package engine

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func def3() *types.SopDefinition {
	return &types.SopDefinition{
		Nodes: []types.SopNode{
			{ID: "n1", Name: "分析", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a1"}},
			{ID: "n2", Name: "实现", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a1"}, OnFailure: "reject", RejectTo: "n1"},
			{ID: "n3", Name: "验收", Type: "approval"},
		},
		Edges: []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2", When: "success"}, {ID: "e2", Source: "n2", Target: "n3", When: "success"}},
	}
}

func containsSub(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestValidateDefinitionOK(t *testing.T) {
	if errs := ValidateDefinition(def3(), false); len(errs) != 0 {
		t.Fatalf("valid def rejected: %v", errs)
	}
}

func TestValidateDefinitionCycle(t *testing.T) {
	d := def3()
	d.Edges = append(d.Edges, types.SopEdge{ID: "e3", Source: "n3", Target: "n1", When: "success"})
	errs := ValidateDefinition(d, false)
	if len(errs) == 0 || !containsSub(errs, "环") {
		t.Fatalf("cycle not detected: %v", errs)
	}
}

func TestValidateRejectToMustBeAncestor(t *testing.T) {
	d := def3()
	d.Nodes[1].RejectTo = "n3" // 下游不可作打回目标
	errs := ValidateDefinition(d, false)
	if !containsSub(errs, "上游") {
		t.Fatalf("reject_to downstream not rejected: %v", errs)
	}
}

func TestValidateRejectMissingTargetPlainWords(t *testing.T) {
	d := def3()
	d.Nodes[1].RejectTo = ""
	errs := ValidateDefinition(d, false)
	if !containsSub(errs, "失败后选了打回上游，但没选打回哪一步") {
		t.Fatalf("plain-word error missing: %v", errs)
	}
}

func TestValidateArtifactPathEscape(t *testing.T) {
	d := def3()
	d.Nodes[0].ArtifactOutputs = []types.SopArtifactOutput{{Name: "x", Path: "../evil.md", Required: true, Check: types.SopArtifactCheck{Type: "exists"}}}
	if !containsSub(ValidateDefinition(d, false), "工作区") {
		t.Fatal("path escape not rejected")
	}
}

func TestValidateChildSOPNoSelfReference(t *testing.T) {
	d := def3()
	d.Nodes[0].Type = "sop"
	d.Nodes[0].ChildTemplateID = "self"
	if !containsSub(ValidateDefinitionWithSelf(d, "self", false), "递归") {
		t.Fatal("self-referencing child SOP not rejected")
	}
}

func TestDescendantsAndAncestors(t *testing.T) {
	d := def3()
	desc := Descendants(d, "n1")
	if !desc["n2"] || !desc["n3"] || desc["n1"] {
		t.Fatalf("descendants wrong: %v", desc)
	}
	anc := Ancestors(d, "n3")
	if !anc["n1"] || !anc["n2"] {
		t.Fatalf("ancestors wrong: %v", anc)
	}
}

func TestTopoLayers(t *testing.T) {
	layers := TopoLayers(def3())
	if len(layers) != 3 || layers[0][0] != "n1" || layers[2][0] != "n3" {
		t.Fatalf("layers wrong: %v", layers)
	}
}

// ---- 补充用例：空定义 / 重复 id / 悬空连线 / 自环与双节点环 / 孤立步骤 / 执行器完整性 ----

func TestValidateEmptyDefinition(t *testing.T) {
	for name, def := range map[string]*types.SopDefinition{
		"nil":           nil,
		"no nodes":      {Edges: []types.SopEdge{}},
		"nil with edge": {Edges: []types.SopEdge{{ID: "e1", Source: "x", Target: "y"}}},
	} {
		errs := ValidateDefinition(def, false)
		if len(errs) == 0 || !containsSub(errs, "步骤") {
			t.Fatalf("%s: empty definition not rejected: %v", name, errs)
		}
	}
}

func TestValidateDuplicateNodeIDs(t *testing.T) {
	d := def3()
	d.Nodes[2].ID = "n2"
	errs := ValidateDefinition(d, false)
	if !containsSub(errs, "重复") {
		t.Fatalf("duplicate node id not rejected: %v", errs)
	}
}

func TestValidateEmptyNodeID(t *testing.T) {
	d := def3()
	d.Nodes[2].ID = ""
	if !containsSub(ValidateDefinition(d, false), "编号") {
		t.Fatal("empty node id not rejected")
	}
}

func TestValidateDanglingEdge(t *testing.T) {
	d := def3()
	d.Edges = append(d.Edges, types.SopEdge{ID: "e9", Source: "n3", Target: "ghost"})
	errs := ValidateDefinition(d, false)
	if !containsSub(errs, "连线指向了不存在的步骤") || !containsSub(errs, "ghost") {
		t.Fatalf("dangling edge not rejected: %v", errs)
	}
	d = def3()
	d.Edges[0].Source = "ghost"
	if !containsSub(ValidateDefinition(d, false), "连线指向了不存在的步骤") {
		t.Fatal("dangling source not rejected")
	}
}

func TestValidateSelfLoopCycleNamesNodes(t *testing.T) {
	d := def3()
	d.Edges = append(d.Edges, types.SopEdge{ID: "e9", Source: "n2", Target: "n2"})
	errs := ValidateDefinition(d, false)
	if !containsSub(errs, "环") {
		t.Fatalf("self loop not detected: %v", errs)
	}
	// 报错要带步骤名，不能只有内部 id。
	if !containsSub(errs, "实现") {
		t.Fatalf("cycle message should name nodes: %v", errs)
	}
}

func TestValidateTwoNodeCycleNamesNodes(t *testing.T) {
	d := &types.SopDefinition{
		Nodes: []types.SopNode{
			{ID: "a", Name: "写需求"},
			{ID: "b", Name: "写方案"},
		},
		Edges: []types.SopEdge{
			{ID: "e1", Source: "a", Target: "b"},
			{ID: "e2", Source: "b", Target: "a"},
		},
	}
	errs := ValidateDefinition(d, false)
	if !containsSub(errs, "环") || !containsSub(errs, "写需求") || !containsSub(errs, "写方案") {
		t.Fatalf("2-node cycle not described by names: %v", errs)
	}
}

func TestValidateIsolatedNode(t *testing.T) {
	d := def3()
	d.Nodes = append(d.Nodes, types.SopNode{ID: "n4", Name: "游离"})
	if !containsSub(ValidateDefinition(d, false), "游离") {
		t.Fatal("isolated node not reported")
	}
	// 单节点流程没有连线也是合法的。
	if errs := ValidateDefinition(&types.SopDefinition{Nodes: []types.SopNode{{ID: "only", Name: "一步"}}}, false); len(errs) != 0 {
		t.Fatalf("single node def rejected: %v", errs)
	}
}

func TestValidateIncompleteExecutor(t *testing.T) {
	d := def3()
	d.Nodes[0].Executor = types.SopNodeExecutor{Kind: "platform_agent"} // 缺 agent_id
	d.Nodes[1].Executor = types.SopNodeExecutor{Kind: "cli"}            // 缺 cli_kind

	errs := ValidateDefinition(d, false)
	if !containsSub(errs, "执行角色") || !containsSub(errs, "CLI") {
		t.Fatalf("incomplete executor not rejected: %v", errs)
	}
	if errs := ValidateDefinition(d, true); len(errs) != 0 {
		t.Fatalf("allowIncompleteExecutor=true should pass: %v", errs)
	}

	// 空 kind：既没选角色也没选 CLI。
	d2 := def3()
	d2.Nodes[0].Executor = types.SopNodeExecutor{}
	if !containsSub(ValidateDefinition(d2, false), "还没选执行角色/CLI") {
		t.Fatal("empty executor kind not reported")
	}
}

func TestValidateChildSOPMissingTemplate(t *testing.T) {
	d := def3()
	d.Nodes[2].Type = types.SopNodeTypeSOP
	if !containsSub(ValidateDefinition(d, false), "子流程步骤还没选模板") &&
		!containsSub(ValidateDefinition(d, false), "还没选模板") {
		t.Fatal("sop node without child template not rejected")
	}
	// 非自身模板 id 正常通过。
	d.Nodes[2].ChildTemplateID = "tpl-other"
	if errs := ValidateDefinitionWithSelf(d, "self", false); len(errs) != 0 {
		t.Fatalf("valid child sop rejected: %v", errs)
	}
}

func TestValidateArtifactPathForms(t *testing.T) {
	bad := []string{"/abs/out.md", `C:\abs\out.md`, "out/../../evil.md", `out\..\..\evil.md`, "..", "../"}
	for _, p := range bad {
		d := def3()
		d.Nodes[0].ArtifactOutputs = []types.SopArtifactOutput{{Name: "x", Path: p}}
		if !containsSub(ValidateDefinition(d, false), "工作区") {
			t.Fatalf("path %q not rejected", p)
		}
	}
	good := []string{"out/a.md", "report.md", `out\a.md`}
	for _, p := range good {
		d := def3()
		d.Nodes[0].ArtifactOutputs = []types.SopArtifactOutput{{Name: "x", Path: p}}
		if errs := ValidateDefinition(d, false); len(errs) != 0 {
			t.Fatalf("path %q wrongly rejected: %v", p, errs)
		}
	}
}

func TestUpstreamsDownstreams(t *testing.T) {
	d := def3()
	if got := Upstreams(d, "n2"); len(got) != 1 || got[0] != "n1" {
		t.Fatalf("upstreams = %v", got)
	}
	if got := Downstreams(d, "n2"); len(got) != 1 || got[0] != "n3" {
		t.Fatalf("downstreams = %v", got)
	}
	if got := Upstreams(d, "ghost"); len(got) != 0 {
		t.Fatalf("unknown node upstreams = %v", got)
	}
	if got := Descendants(d, "ghost"); len(got) != 0 {
		t.Fatalf("unknown node descendants = %v", got)
	}
}

func TestHasCycle(t *testing.T) {
	if HasCycle(def3()) {
		t.Fatal("acyclic def reported as cycle")
	}
	d := def3()
	d.Edges = append(d.Edges, types.SopEdge{ID: "e3", Source: "n3", Target: "n1"})
	if !HasCycle(d) {
		t.Fatal("cycle not detected")
	}
	// 同一条依赖连两遍（编辑器重复连线）不是环。
	dup := def3()
	dup.Edges = append(dup.Edges, types.SopEdge{ID: "e9", Source: "n1", Target: "n2"})
	if HasCycle(dup) {
		t.Fatal("duplicate parallel edge wrongly reported as cycle")
	}
	if layers := TopoLayers(dup); len(layers) != 3 {
		t.Fatalf("duplicate parallel edge changed layers: %v", layers)
	}
}

func TestTopoLayersParallelAndLeftover(t *testing.T) {
	d := &types.SopDefinition{
		Nodes: []types.SopNode{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}},
		Edges: []types.SopEdge{
			{ID: "e1", Source: "a", Target: "c"},
			{ID: "e2", Source: "b", Target: "c"},
			{ID: "e3", Source: "c", Target: "d"},
		},
	}
	layers := TopoLayers(d)
	if len(layers) != 3 || len(layers[0]) != 2 || layers[1][0] != "c" || layers[2][0] != "d" {
		t.Fatalf("layers = %v", layers)
	}
	// 有环时环内节点进最后一层，不丢节点。
	d.Edges = append(d.Edges, types.SopEdge{ID: "e4", Source: "d", Target: "a"})
	layers = TopoLayers(d)
	seen := map[string]bool{}
	for _, l := range layers {
		for _, id := range l {
			seen[id] = true
		}
	}
	if len(seen) != 4 {
		t.Fatalf("cycle layers dropped nodes: %v", layers)
	}
	if TopoLayers(nil) != nil {
		t.Fatal("nil def should yield nil layers")
	}
}

func TestValidateArtifactCheckCompleteness(t *testing.T) {
	d := def3()
	d.Nodes[0].ArtifactOutputs = []types.SopArtifactOutput{
		{Name: "PRD", Path: "out/p.md", Check: types.SopArtifactCheck{Type: "contains"}},
		{Name: "自定义", Path: "out/x", Check: types.SopArtifactCheck{Type: "command"}},
	}
	errs := ValidateDefinition(d, false)
	if !containsSub(errs, "没写要包含什么") || !containsSub(errs, "没写要跑的命令") {
		t.Fatalf("incomplete checks not reported: %v", errs)
	}
	// 空路径的 exists 检查无法执行，同样报错。
	d = def3()
	d.Nodes[0].ArtifactOutputs = []types.SopArtifactOutput{{Name: "空路径", Path: "  ", Check: types.SopArtifactCheck{Type: "exists"}}}
	if !containsSub(ValidateDefinition(d, false), "还没填产物路径") &&
		!containsSub(ValidateDefinition(d, false), "路径") {
		t.Fatalf("empty artifact path not reported: %v", ValidateDefinition(d, false))
	}
}

func TestTopoSort(t *testing.T) {
	if got := TopoSort(def3()); strings.Join(got, ",") != "n1,n2,n3" {
		t.Fatalf("toposort = %v", got)
	}
	if TopoSort(nil) != nil {
		t.Fatal("nil def should yield nil")
	}
}

func TestValidateDeterministic(t *testing.T) {
	d := def3()
	d.Nodes = append(d.Nodes,
		types.SopNode{ID: "", Name: "无编号"},
		types.SopNode{ID: "n1", Name: "重复编号"},
	)
	d.Edges = append(d.Edges, types.SopEdge{ID: "e9", Source: "n3", Target: "ghost"})
	first := strings.Join(ValidateDefinition(d, false), "|")
	for i := 0; i < 5; i++ {
		if got := strings.Join(ValidateDefinition(d, false), "|"); got != first {
			t.Fatalf("non-deterministic: %q vs %q", first, got)
		}
	}
}
