package types

import (
	"strings"
	"testing"
)

func TestCanTransitSopItem(t *testing.T) {
	allowed := [][2]string{
		{"backlog", "todo"}, {"todo", "in_progress"}, {"in_progress", "in_review"},
		{"in_review", "done"}, {"done", "closed"}, {"in_progress", "blocked"},
		{"blocked", "in_progress"}, {"closed", "todo"}, {"cancelled", "todo"},
		{"todo", "cancelled"},
	}
	for _, p := range allowed {
		if !CanTransitSopItem(p[0], p[1]) {
			t.Fatalf("%s -> %s should be allowed", p[0], p[1])
		}
	}
	for _, p := range [][2]string{{"backlog", "done"}, {"done", "backlog"}, {"closed", "in_progress"}, {"cancelled", "done"}} {
		if CanTransitSopItem(p[0], p[1]) {
			t.Fatalf("%s -> %s should be rejected", p[0], p[1])
		}
	}
	if !CanTransitSopItem("todo", "todo") {
		t.Fatal("same status must be a no-op allowed")
	}
}

func TestDeriveExecState(t *testing.T) {
	cases := []struct {
		item, run string
		hasRun    bool
		want      string
	}{
		{"done", "", false, "closed"},
		{"closed", "failed", true, "closed"},
		{"todo", "", false, "none"},
		{"todo", "cancelled", true, "none"},
		{"in_progress", "running", true, "running"},
		{"in_progress", "paused", true, "running"},
		{"in_progress", "waiting", true, "intervene"},
		{"in_progress", "failed", true, "intervene"},
		{"in_review", "succeeded", true, "pr"},
	}
	for _, c := range cases {
		if got := DeriveExecState(c.item, c.run, c.hasRun); got != c.want {
			t.Fatalf("item=%s run=%s hasRun=%v: got %s want %s", c.item, c.run, c.hasRun, got, c.want)
		}
	}
}

func TestSopSourceLabel(t *testing.T) {
	want := map[string]string{
		"customer": "客户反馈", "internal": "平台新建", "data": "数据洞察",
		"competitor": "竞品分析", "strategy": "战略规划", "other": "其他",
	}
	for k, v := range want {
		if got := SopSourceLabel(k); got != v {
			t.Fatalf("source %s: got %s want %s", k, got, v)
		}
	}
	if SopSourceLabel("unknown") != "其他" {
		t.Fatal("unknown source must fall back to 其他")
	}
}

func TestDeriveProgress(t *testing.T) {
	done, total := DeriveProgress([]string{"succeeded", "skipped", "running", "pending"})
	if done != 2 || total != 4 {
		t.Fatalf("got %d/%d want 2/4", done, total)
	}
}

// 空状态列表与未知状态是最容易写崩的两个边界：前者必须返回 0/0，
// 后者只计总数不计完成，且当前环节若落在未知状态上有确定的回退。
func TestDeriveProgressEmptyAndUnknown(t *testing.T) {
	if done, total := DeriveProgress(nil); done != 0 || total != 0 {
		t.Fatalf("nil statuses: got %d/%d want 0/0", done, total)
	}
	if done, total := DeriveProgress([]string{"weird", "succeeded"}); done != 1 || total != 2 {
		t.Fatalf("unknown status: got %d/%d want 1/2", done, total)
	}
}

func TestDeriveCurrentNode(t *testing.T) {
	cases := []struct {
		name             string
		item             string
		running, waiting []string
		failed           []string
		want             string
	}{
		{"failed wins over waiting and running", "in_progress", []string{"n1"}, []string{"n2"}, []string{"n3"}, "n3"},
		{"waiting wins over running", "in_progress", []string{"n1"}, []string{"n2"}, nil, "n2"},
		{"running when nothing blocks", "in_progress", []string{"n1"}, nil, nil, "n1"},
		{"第一项被选中", "in_progress", []string{"n1", "n5"}, nil, nil, "n1"},
		{"done without node runs", "done", nil, nil, nil, "已完成"},
		{"closed without node runs", "closed", nil, nil, nil, "已关闭"},
	}
	for _, c := range cases {
		if got := DeriveCurrentNode(c.item, c.running, c.waiting, c.failed); got != c.want {
			t.Fatalf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// 执行情况五态文案与五态键必须一一对应，前端按同一份中文展示。
func TestSopExecStateLabels(t *testing.T) {
	want := map[string]string{
		"none": "尚未执行", "running": "正在推进", "intervene": "需要介入",
		"pr": "待处理 PR", "closed": "已闭环",
	}
	if len(SopExecStateLabels) != len(want) {
		t.Fatalf("exec state label count: got %d want %d", len(SopExecStateLabels), len(want))
	}
	for k, v := range want {
		if got := SopExecStateLabels[k]; got != v {
			t.Fatalf("exec state %s: got %q want %q", k, got, v)
		}
	}
}

// 状态机引入未知状态时两侧都必须拒绝，避免脏数据被当成合法迁移。
func TestCanTransitSopItemUnknownStatus(t *testing.T) {
	if CanTransitSopItem("weird", "todo") {
		t.Fatal("unknown from status must be rejected")
	}
	if CanTransitSopItem("todo", "weird") {
		t.Fatal("unknown to status must be rejected")
	}
}

// 列表派生用的节点引用格式由 types 统一约定，解析必须容忍缺分隔符的脏数据。
func TestParseSopNodeRef(t *testing.T) {
	status, name := ParseSopNodeRef("running|实现")
	if status != "running" || name != "实现" {
		t.Fatalf("got %q/%q want running/实现", status, name)
	}
	status, name = ParseSopNodeRef("succeeded")
	if status != "succeeded" || name != "" {
		t.Fatalf("no separator: got %q/%q want succeeded/\"\"", status, name)
	}
}

func TestSopItemStatusPath(t *testing.T) {
	// backlog 不能直接到 done：必须经过 todo→in_progress→in_review
	path := SopItemStatusPath(SopItemStatusBacklog, SopItemStatusDone)
	if got := strings.Join(path, ">"); got != "todo>in_progress>done" {
		t.Fatalf("backlog→done path = %q", got)
	}
	if p := SopItemStatusPath(SopItemStatusInReview, SopItemStatusDone); len(p) != 1 || p[0] != SopItemStatusDone {
		t.Fatalf("in_review→done = %v", p)
	}
	if p := SopItemStatusPath(SopItemStatusDone, SopItemStatusDone); p != nil {
		t.Fatalf("same status path should be nil, got %v", p)
	}
	if p := SopItemStatusPath("bogus", SopItemStatusDone); p != nil {
		t.Fatalf("unknown from should be nil, got %v", p)
	}
}
