package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestScriptSuccessWritesArtifact(t *testing.T) {
	p := NewScriptProvider()
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "out"), 0o755))
	tb := TaskBook{NodeID: "n1", WorkspaceRoot: ws, OutDir: filepath.Join(ws, "out"),
		Script: "echo hello > out/s.txt", Timeout: 30 * time.Second, Attempt: 1}
	var lines []string
	res, err := p.Execute(context.Background(), tb, ExecOptions{Emit: func(e StreamEvent) {
		if e.Type == StreamEventTextDelta {
			lines = append(lines, e.Text)
		}
	}})
	require.NoError(t, err)
	require.Equal(t, types.SopGatePass, res.GateResult)
	body, _ := os.ReadFile(filepath.Join(ws, "out", "s.txt"))
	require.Contains(t, string(body), "hello")
}

func TestScriptNonZeroExitMeansRework(t *testing.T) {
	p := NewScriptProvider()
	tb := TaskBook{NodeID: "n1", WorkspaceRoot: t.TempDir(), Script: "echo 出错 >&2; exit 3", Timeout: 30 * time.Second}
	res, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.NoError(t, err) // 退出码非 0 不是执行器错误，是门禁打回
	require.Equal(t, types.SopGateRework, res.GateResult)
	require.Contains(t, res.GateReason, "退出码 3")
	require.Contains(t, res.GateReason, "出错")
}

func TestScriptEnvInjection(t *testing.T) {
	p := NewScriptProvider()
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "out"), 0o755))
	tb := TaskBook{NodeID: "n1", WorkspaceRoot: ws, Script: "echo $MY_TOKEN > out/env.txt",
		Env: map[string]string{"MY_TOKEN": "secret123"}, Timeout: 30 * time.Second}
	_, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.NoError(t, err)
	body, _ := os.ReadFile(filepath.Join(ws, "out", "env.txt"))
	require.Contains(t, string(body), "secret123")
}

func TestScriptTimeoutKilled(t *testing.T) {
	p := NewScriptProvider()
	tb := TaskBook{NodeID: "n1", WorkspaceRoot: t.TempDir(), Script: "sleep 30", Timeout: 300 * time.Millisecond}
	start := time.Now()
	res, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.Less(t, time.Since(start), 10*time.Second)
	require.NoError(t, err)
	require.Equal(t, types.SopGateRework, res.GateResult)
	require.Contains(t, res.GateReason, "超时")
}

// 流式日志：标准输出逐行交给 Emit，同一份文本也落到 logs/<node>-attempt-N.log。
func TestScriptStreamsAndWritesLog(t *testing.T) {
	p := NewScriptProvider()
	ws := t.TempDir()
	tb := TaskBook{NodeID: "n1", WorkspaceRoot: ws, Attempt: 2, Timeout: 30 * time.Second,
		Script: "echo 第一行; echo 第二行 >&2"}
	var got []string
	_, err := p.Execute(context.Background(), tb, ExecOptions{Emit: func(e StreamEvent) {
		if e.Type == StreamEventTextDelta {
			got = append(got, e.Text)
		}
	}})
	require.NoError(t, err)
	joined := strings.Join(got, "")
	require.Contains(t, joined, "第一行")
	require.Contains(t, joined, "第二行")

	logPath := filepath.Join(ws, "logs", "n1-attempt-2.log")
	data, readErr := os.ReadFile(logPath)
	require.NoError(t, readErr)
	require.Contains(t, string(data), "第一行")
}

// 输出尾部作为摘要进 Output，超过上限时按尾部截断。
func TestScriptOutputIsTailSummary(t *testing.T) {
	p := NewScriptProvider()
	tb := TaskBook{NodeID: "n1", WorkspaceRoot: t.TempDir(), Timeout: 30 * time.Second,
		Script: "for i in $(seq 1 400); do echo line-$i; done"}
	res, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.NoError(t, err)
	require.LessOrEqual(t, len(res.Output), scriptOutputTail, "摘要按上限截断")
	require.Contains(t, res.Output, "line-400", "保留尾部")
}

func TestScriptPrepareIsNoop(t *testing.T) {
	p := NewScriptProvider()
	require.NoError(t, p.Prepare(context.Background(), &types.SopNode{}, t.TempDir()))
	require.Equal(t, ProviderKindScript, p.Kind())
}

func TestScriptEmptyScriptFails(t *testing.T) {
	p := NewScriptProvider()
	_, err := p.Execute(context.Background(), TaskBook{NodeID: "n1", WorkspaceRoot: t.TempDir()}, ExecOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "脚本")
}

func TestScriptCancelKillsRun(t *testing.T) {
	p := NewScriptProvider()
	ws := t.TempDir()
	done := make(chan struct{})
	var execErr error
	var res ExecResult
	go func() {
		defer close(done)
		res, execErr = p.Execute(context.Background(), TaskBook{
			NodeID: "n1", WorkspaceRoot: ws, Script: "sleep 30", Timeout: 60 * time.Second,
		}, ExecOptions{})
	}()
	require.Eventually(t, func() bool { return p.running() }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, p.Cancel(context.Background()))
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Cancel 没有终止脚本")
	}
	require.NoError(t, execErr)
	require.Equal(t, types.SopGateRework, res.GateResult)
	require.Contains(t, res.GateReason, "取消")
}
