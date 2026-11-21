package types

import (
	"encoding/json"
	"testing"
	"time"
)

// 新建 run 的零值兜底：状态默认 queued、补 uuid/时间戳、JSON 列补空集合，
// 避免 serializer 把 nil 写成 NULL 而与列默认 '{}' / '[]' 不一致。
func TestSopRunBeforeCreateDefaults(t *testing.T) {
	r := &SopRun{TenantID: 1}
	if err := r.BeforeCreate(nil); err != nil {
		t.Fatalf("before create: %v", err)
	}
	if r.ID == "" {
		t.Fatal("id must be assigned")
	}
	if r.Status != SopRunQueued {
		t.Fatalf("status = %q, want %q", r.Status, SopRunQueued)
	}
	if r.Input == nil {
		t.Fatal("input must not be nil")
	}
	if r.HumanContext == nil {
		t.Fatal("human_context must be empty slice, not nil")
	}
	if r.WorkflowSnapshot.Nodes == nil || r.WorkflowSnapshot.Edges == nil || r.WorkflowSnapshot.InputSchema == nil {
		t.Fatalf("workflow snapshot slices must be empty, got %+v", r.WorkflowSnapshot)
	}
	raw, err := json.Marshal(r.HumanContext)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != "[]" {
		t.Fatalf("human_context JSON = %s, want []", raw)
	}
}

// 显式状态不被兜底覆盖（create 时直接写明 succeeded 等历史态）。
func TestSopRunBeforeCreateKeepsExplicitStatus(t *testing.T) {
	r := &SopRun{TenantID: 1, Status: SopRunSucceeded}
	if err := r.BeforeCreate(nil); err != nil {
		t.Fatalf("before create: %v", err)
	}
	if r.Status != SopRunSucceeded {
		t.Fatalf("status = %q, want %q", r.Status, SopRunSucceeded)
	}
}

func TestSopRunJSONTags(t *testing.T) {
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	r := SopRun{
		ID: "r1", TenantID: 1, TemplateID: "t1", RevisionID: "rev1",
		ProjectID: "p1", ItemID: "i1", ParentRunID: "pr1",
		TriggerKind: SopRunTriggerManual, TriggerRefID: "ref",
		Status: SopRunQueued, Input: map[string]any{"k": "v"},
		HumanContext: []SopHumanContextEntry{{At: at, Source: "human", NodeID: "n1", Text: "补充"}},
		Usage:        SopUsage{InputTokens: 10, OutputTokens: 20, CostUSD: 0.5},
		WorkspaceDir: "/ws", Error: "e", ExecutionDurationMs: 1200,
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{"id", "tenant_id", "template_id", "revision_id", "workflow_snapshot",
		"project_id", "item_id", "parent_run_id", "trigger_kind", "trigger_ref_id", "status",
		"input", "human_context", "project_context", "workspace_dir", "lease_until", "heartbeat_at",
		"cancel_requested", "pause_requested", "usage", "execution_duration_ms", "error",
		"started_at", "finished_at"}
	for _, key := range want {
		if _, ok := back[key]; !ok {
			t.Fatalf("missing JSON key %q in %s", key, raw)
		}
	}
	usage, ok := back["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage must be object: %v", back["usage"])
	}
	if usage["input_tokens"].(float64) != 10 || usage["output_tokens"].(float64) != 20 || usage["cost_usd"].(float64) != 0.5 {
		t.Fatalf("usage keys wrong: %v", usage)
	}
	hc := back["human_context"].([]any)[0].(map[string]any)
	if hc["source"] != "human" || hc["node_id"] != "n1" || hc["text"] != "补充" {
		t.Fatalf("human context keys wrong: %v", hc)
	}
}

func TestSopNodeRunBeforeCreateDefaults(t *testing.T) {
	n := &SopNodeRun{TenantID: 1, RunID: "r1", NodeID: "n1"}
	if err := n.BeforeCreate(nil); err != nil {
		t.Fatalf("before create: %v", err)
	}
	if n.ID == "" {
		t.Fatal("id must be assigned")
	}
	if n.Status != SopNodePending {
		t.Fatalf("status = %q, want %q", n.Status, SopNodePending)
	}
}

// 状态取值是前后端契约（frontend RunStatus 7 态 / NodeStatus 8 态），不得漂移。
func TestSopStatusConstantValues(t *testing.T) {
	runs := map[string]string{
		SopRunQueued: "queued", SopRunRunning: "running", SopRunWaiting: "waiting",
		SopRunSucceeded: "succeeded", SopRunFailed: "failed",
		SopRunCancelled: "cancelled", SopRunPaused: "paused",
	}
	for got, want := range runs {
		if got != want {
			t.Fatalf("run status constant %q != %q", got, want)
		}
	}
	nodes := map[string]string{
		SopNodePending: "pending", SopNodeRunning: "running",
		SopNodeWaitingApproval: "waiting_approval", SopNodePendingInput: "pending_input",
		SopNodeSucceeded: "succeeded", SopNodeFailed: "failed",
		SopNodeCancelled: "cancelled", SopNodeSkipped: "skipped",
	}
	for got, want := range nodes {
		if got != want {
			t.Fatalf("node status constant %q != %q", got, want)
		}
	}
	gates := map[string]string{
		SopGatePass: "pass", SopGateRework: "rework", SopGateFail: "fail", SopGateHuman: "human",
	}
	for got, want := range gates {
		if got != want {
			t.Fatalf("gate constant %q != %q", got, want)
		}
	}
}

// 活跃/终态集是 CountActiveRuns 与统计口径的唯一来源。
func TestSopRunStatusSets(t *testing.T) {
	for _, s := range []string{SopRunQueued, SopRunRunning, SopRunWaiting, SopRunPaused} {
		if !IsSopRunActive(s) {
			t.Fatalf("%s must be active", s)
		}
	}
	for _, s := range []string{SopRunSucceeded, SopRunFailed, SopRunCancelled} {
		if IsSopRunActive(s) {
			t.Fatalf("%s must not be active", s)
		}
		if !IsSopRunFinished(s) {
			t.Fatalf("%s must be finished", s)
		}
	}
	if IsSopRunFinished(SopRunRunning) {
		t.Fatal("running must not be finished")
	}
}

func TestSopArtifactBeforeCreateDefaults(t *testing.T) {
	a := &SopArtifact{TenantID: 1, RunID: "r1", NodeID: "n1", Name: "报告", Path: "out/a.md"}
	if err := a.BeforeCreate(nil); err != nil {
		t.Fatalf("before create: %v", err)
	}
	if a.ID == "" {
		t.Fatal("id must be assigned")
	}
	if a.Attempt != 1 {
		t.Fatalf("attempt = %d, want 1", a.Attempt)
	}
}
