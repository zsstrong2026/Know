package engine

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestCompileGateCommands(t *testing.T) {
	n := &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{
		{Name: "报告", Path: "out/a.md", Check: types.SopArtifactCheck{Type: "exists"}},
		{Name: "PRD", Path: "out/prd.md", Check: types.SopArtifactCheck{Type: "contains", Keyword: "验收"}},
		{Name: "自定义", Path: "out/x", Check: types.SopArtifactCheck{Type: "command", Command: "test -f out/x"}},
	}, GateExtra: "echo one && echo two"}
	cmds := CompileGateCommands(n)
	want := []string{`test -s "out/a.md"`, `grep -q -- "验收" "out/prd.md"`, "test -f out/x", "echo one", "echo two"}
	if len(cmds) != len(want) {
		t.Fatalf("got %v want %v", cmds, want)
	}
	for i := range want {
		if cmds[i] != want[i] {
			t.Fatalf("cmd[%d]=%q want %q", i, cmds[i], want[i])
		}
	}
}

func TestJoinGateScript(t *testing.T) {
	s := JoinGateScript([]string{"test -s a", "grep -q k b"})
	if s != "set -e\ntest -s a\ngrep -q k b" {
		t.Fatalf("script wrong: %q", s)
	}
	if JoinGateScript(nil) != "" {
		t.Fatal("empty cmds should yield empty script")
	}
}

func TestCompileGateCommandsEachCheckType(t *testing.T) {
	tests := []struct {
		name string
		node *types.SopNode
		want []string
	}{
		{
			name: "空 type 按 exists 处理",
			node: &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "out/a.md"}}},
			want: []string{`test -s "out/a.md"`},
		},
		{
			name: "显式 exists",
			node: &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "a.md", Check: types.SopArtifactCheck{Type: types.SopArtifactCheckExists}}}},
			want: []string{`test -s "a.md"`},
		},
		{
			name: "contains 关键词加引号",
			node: &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "p.md", Check: types.SopArtifactCheck{Type: types.SopArtifactCheckContains, Keyword: "验收 标准"}}}},
			want: []string{`grep -q -- "验收 标准" "p.md"`},
		},
		{
			name: "contains 含 $ 与反引号被转义，不会被 shell 展开",
			node: &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "p.md", Check: types.SopArtifactCheck{Type: "contains", Keyword: "$HOME `id`"}}}},
			want: []string{`grep -q -- "\$HOME \` + "`id\\`" + `" "p.md"`},
		},
		{
			name: "contains 关键词含双引号被转义",
			node: &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "p.md", Check: types.SopArtifactCheck{Type: "contains", Keyword: `say "hi"`}}}},
			want: []string{`grep -q -- "say \"hi\"" "p.md"`},
		},
		{
			// 关键词以 - 开头（markdown 待办项）会被 grep 当成选项，必须用 -- 分隔。
			name: "contains 关键词以 - 开头不当作选项",
			node: &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "out.md", Check: types.SopArtifactCheck{Type: "contains", Keyword: "- [ ]"}}}},
			want: []string{`grep -q -- "- [ ]" "out.md"`},
		},
		{
			name: "contains 关键词含 && 不会被拆成两条命令",
			node: &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "out.md", Check: types.SopArtifactCheck{Type: "contains", Keyword: "a && rm -rf b"}}}},
			want: []string{`grep -q -- "a && rm -rf b" "out.md"`},
		},
		{
			name: "contains 关键词含引号 $ 反引号 && 全部被引用",
			node: &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "out.md", Check: types.SopArtifactCheck{Type: "contains", Keyword: "say \"x\" $HOME `id` && y"}}}},
			want: []string{`grep -q -- "say \"x\" \$HOME \` + "`id\\`" + ` && y" "out.md"`},
		},
		{
			// 产物路径也必须引用：空格、$ 都不能让命令断开或被展开。
			name: "exists 路径含空格被引用",
			node: &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "out/需求 分析.md", Check: types.SopArtifactCheck{Type: "exists"}}}},
			want: []string{`test -s "out/需求 分析.md"`},
		},
		{
			name: "exists 路径含 $ 被转义",
			node: &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "out/$draft.md", Check: types.SopArtifactCheck{Type: "exists"}}}},
			want: []string{`test -s "out/\$draft.md"`},
		},
		{
			name: "contains 路径含空格被引用",
			node: &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "out/需求 分析.md", Check: types.SopArtifactCheck{Type: "contains", Keyword: "验收"}}}},
			want: []string{`grep -q -- "验收" "out/需求 分析.md"`},
		},
		{
			name: "contains 路径含 $ 被转义",
			node: &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "out/$draft.md", Check: types.SopArtifactCheck{Type: "contains", Keyword: "- [x]"}}}},
			want: []string{`grep -q -- "- [x]" "out/\$draft.md"`},
		},
		{
			name: "command 原样保留",
			node: &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "x", Check: types.SopArtifactCheck{Type: "command", Command: "node check.js --strict"}}}},
			want: []string{"node check.js --strict"},
		},
		{
			name: "command 为空白则跳过",
			node: &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "x", Check: types.SopArtifactCheck{Type: "command", Command: "   "}}}},
			want: nil,
		},
		{
			name: "gate_extra 空片段被忽略",
			node: &types.SopNode{GateExtra: "  &&   && echo ok &&  "},
			want: []string{"echo ok"},
		},
		{
			name: "gate_extra 保留内部空白",
			node: &types.SopNode{GateExtra: " grep -q k  out/a.md  && echo done"},
			want: []string{"grep -q k  out/a.md", "echo done"},
		},
		{name: "nil 节点", node: nil, want: nil},
		{name: "无产物无额外校验", node: &types.SopNode{}, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CompileGateCommands(tc.node)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("cmd[%d]=%q want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestCompileGateCommandsDoesNotMutateNode(t *testing.T) {
	n := &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{{Path: "a.md", Check: types.SopArtifactCheck{Type: "exists"}}}, GateExtra: "echo x"}
	_ = CompileGateCommands(n)
	if len(n.GateCommands) != 0 {
		t.Fatalf("compiler must not write derived field itself: %v", n.GateCommands)
	}
	if n.GateExtra != "echo x" || len(n.ArtifactOutputs) != 1 {
		t.Fatalf("compiler mutated node: %+v", n)
	}
}

func TestDecideGate(t *testing.T) {
	tests := []struct {
		name       string
		ev         GateEvidence
		wantVerdit string
		wantSub    string
	}{
		{
			name:       "执行器报 pass 直接采纳",
			ev:         GateEvidence{Reported: types.SopGatePass},
			wantVerdit: types.SopGatePass,
		},
		{
			name:       "执行器报 rework 带原因",
			ev:         GateEvidence{Reported: types.SopGateRework, Reason: "缺少异常态"},
			wantVerdit: types.SopGateRework,
			wantSub:    "缺少异常态",
		},
		{
			name:       "执行器报 fail",
			ev:         GateEvidence{Reported: types.SopGateFail, Reason: "编译失败"},
			wantVerdit: types.SopGateFail,
			wantSub:    "编译失败",
		},
		{
			name:       "执行器报 human",
			ev:         GateEvidence{Reported: types.SopGateHuman, Reason: "需要人判断"},
			wantVerdit: types.SopGateHuman,
			wantSub:    "需要人判断",
		},
		{
			name:       "缺失上报 + 缺必需产物",
			ev:         GateEvidence{MissingArtifacts: []string{"out/a.md", "out/b.md"}},
			wantVerdit: types.SopGateRework,
			wantSub:    "缺少必需产物：out/a.md、out/b.md",
		},
		{
			name:       "缺失上报 + 脚本失败",
			ev:         GateEvidence{ScriptRan: true, ScriptOutput: "grep: no match"},
			wantVerdit: types.SopGateRework,
			wantSub:    "检查没通过：grep: no match",
		},
		{
			name:       "缺失上报 + 脚本通过",
			ev:         GateEvidence{ScriptRan: true, ScriptOK: true},
			wantVerdit: types.SopGatePass,
		},
		{
			name:       "缺失上报 + 没有可跑命令",
			ev:         GateEvidence{},
			wantVerdit: types.SopGatePass,
		},
		{
			name:       "未知上报值按缺失处理",
			ev:         GateEvidence{Reported: "weird"},
			wantVerdit: types.SopGatePass,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict, reason := DecideGate(tc.ev)
			if verdict != tc.wantVerdit {
				t.Fatalf("verdict = %q want %q", verdict, tc.wantVerdit)
			}
			if tc.wantSub != "" && !strings.Contains(reason, tc.wantSub) {
				t.Fatalf("reason = %q, want substring %q", reason, tc.wantSub)
			}
			if strings.Contains(reason, "门禁") {
				t.Fatalf("user-facing reason must avoid jargon: %q", reason)
			}
		})
	}
}

func TestDecideGateClipsLongOutput(t *testing.T) {
	long := strings.Repeat("错", 2000)
	verdict, reason := DecideGate(GateEvidence{ScriptRan: true, ScriptOutput: long})
	if verdict != types.SopGateRework {
		t.Fatalf("verdict = %q", verdict)
	}
	if n := len([]rune(reason)); n > GateReasonClipRunes+40 {
		t.Fatalf("reason not clipped: %d runes", n)
	}
	if !strings.HasSuffix(reason, "…") {
		t.Fatalf("clipped reason should end with ellipsis: %q", reason[len(reason)-10:])
	}
}

func TestRefreshDerived(t *testing.T) {
	d := def3()
	d.Nodes[0].ArtifactOutputs = []types.SopArtifactOutput{
		{Name: "报告", Path: "out/a.md", Check: types.SopArtifactCheck{Type: "exists"}},
		{Name: "PRD", Path: "out/p.md", Check: types.SopArtifactCheck{Type: "contains", Keyword: "验收"}},
	}
	d.Nodes[0].GateCommands = []string{"stale"}
	d.Nodes[0].GateExtra = "echo ok && true"

	RefreshDerived(d)
	want := []string{`test -s "out/a.md"`, `grep -q -- "验收" "out/p.md"`, "echo ok", "true"}
	if len(d.Nodes[0].GateCommands) != len(want) {
		t.Fatalf("gate_commands = %v want %v", d.Nodes[0].GateCommands, want)
	}
	for i := range want {
		if d.Nodes[0].GateCommands[i] != want[i] {
			t.Fatalf("gate_commands[%d] = %q want %q", i, d.Nodes[0].GateCommands[i], want[i])
		}
	}
	if d.Nodes[1].GateCommands == nil {
		t.Fatal("nodes without checks should get empty (non-nil) gate_commands for stable JSON")
	}
	if len(d.Nodes[1].GateCommands) != 0 {
		t.Fatalf("node without checks should have empty gate_commands: %v", d.Nodes[1].GateCommands)
	}
	// 幂等 + nil 安全。
	RefreshDerived(d)
	RefreshDerived(nil)
	if len(d.Nodes[0].GateCommands) != len(want) {
		t.Fatalf("RefreshDerived not idempotent: %v", d.Nodes[0].GateCommands)
	}
}
