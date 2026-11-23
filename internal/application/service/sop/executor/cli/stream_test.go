package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// golden 流：assistant 正文/工具调用 + result 终态，归一化成统一事件。
func TestClaudeStreamNormalization(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "claude", "stream.jsonl"))
	require.NoError(t, err)

	var events []executor.StreamEvent
	terminalFrames := 0
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		evs, terminal := ClaudeAdapter{}.ParseLine(line)
		events = append(events, evs...)
		if terminal {
			terminalFrames++
		}
	}
	require.Equal(t, 1, terminalFrames, "只有 result 帧是终态")

	var texts, tools, results int
	var resultEv executor.StreamEvent
	for _, e := range events {
		switch e.Type {
		case executor.StreamEventTextDelta:
			texts++
		case executor.StreamEventToolCall:
			tools++
			require.Equal(t, "Write", e.ToolName)
		case executor.StreamEventResult:
			results++
			resultEv = e
		}
	}
	require.Equal(t, 1, tools)
	require.Equal(t, 1, results)
	require.GreaterOrEqual(t, texts, 1, "正文至少一条（result 帧补尾也算）")
	require.Contains(t, resultEv.Text, "实现完成")

	// 用量与续跑 id 都从终态帧里取
	require.Contains(t, string(resultEv.Payload), `"input_tokens":120`)
	require.Equal(t, "sess-abc", ClaudeAdapter{}.ResumeID(events))
}

// 无头参数：stream-json + verbose + resume；prompt 走 stdin 时不在 argv 里。
func TestClaudeBuildArgsHeadless(t *testing.T) {
	tb := executor.TaskBook{Model: "claude-x"}
	args := ClaudeAdapter{}.BuildArgs(tb, "", "sess-abc")
	joined := strings.Join(args, " ")
	require.Contains(t, joined, "--output-format stream-json")
	require.Contains(t, joined, "--verbose")
	require.Contains(t, joined, "--resume sess-abc")
	require.Contains(t, joined, "--model claude-x")
	require.True(t, ClaudeAdapter{}.PreferStdin())

	// argv 投递时正文在最后
	args = ClaudeAdapter{}.BuildArgs(executor.TaskBook{}, "任务正文", "")
	require.Equal(t, "任务正文", args[len(args)-1])
	require.NotContains(t, strings.Join(args, " "), "--resume")
}

// 配置文件只有变量引用；环境里才有明文（env-only）。
func TestClaudeCredentialEnvOnly(t *testing.T) {
	tb := executor.TaskBook{Credential: &types.SopCLICredential{
		CLIKind: "claude", APIKey: "sk-secret", BaseURL: "https://proxy.example.com",
	}}
	files := ClaudeAdapter{}.ConfigFiles(tb)
	require.Len(t, files, 1)
	for _, content := range files {
		require.NotContains(t, content, "sk-secret", "配置里绝不能有明文")
		require.Contains(t, content, "SOP_CLI_API_KEY")
	}
	env := ClaudeAdapter{}.Env(tb)
	require.Equal(t, "sk-secret", env["ANTHROPIC_API_KEY"])
	require.Equal(t, "https://proxy.example.com", env["ANTHROPIC_BASE_URL"])
}

// result 帧 is_error 时判失败；轮次用尽转人工。
func TestClaudeResultErrorAndFailureHint(t *testing.T) {
	evs, terminal := ClaudeAdapter{}.ParseLine(
		`{"type":"result","subtype":"error","is_error":true,"result":"模型被限流"}`)
	require.True(t, terminal)
	require.Contains(t, string(evs[len(evs)-1].Payload), `"status":"error"`)

	require.Equal(t, types.SopGateHuman,
		ClaudeAdapter{}.FailureHint("Error: max turns exceeded"))
	require.Empty(t, ClaudeAdapter{}.FailureHint("some other failure"))
}

// 非 JSON 行不该炸：忽略。
func TestClaudeParseLineIgnoresGarbage(t *testing.T) {
	for _, line := range []string{"", "   ", "not json", `{"type":"unknown"}`} {
		evs, terminal := ClaudeAdapter{}.ParseLine(line)
		require.Empty(t, evs)
		require.False(t, terminal)
	}
}

// ---- RunStream 行为 ----

// fakeAdapter 是「用 bash 跑一段脚本」的假 CLI：跨平台可靠（Windows 上不能把
// .sh 改名成 .exe 执行），同时沿用 ClaudeAdapter 的参数与解析语义。
type fakeAdapter struct {
	ClaudeAdapter
	script string
}

func (f fakeAdapter) BinaryName() string { return "bash" }

func (f fakeAdapter) BuildArgs(tb executor.TaskBook, prompt, resumeID string) []string {
	return append([]string{f.script}, f.ClaudeAdapter.BuildArgs(tb, prompt, resumeID)...)
}

// fakeBinary 写一个假 CLI 脚本并返回可直接用的适配器。
func fakeBinary(t *testing.T, script string) fakeAdapter {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-cli.sh")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/bash\n"+script+"\n"), 0o755))
	return fakeAdapter{script: path}
}

// 无 result 帧且 exit 0：结论留空，交门禁三段裁定。
func TestRunStreamNoResultFrameExitZeroDefersToGate(t *testing.T) {
	a := fakeBinary(t, `echo '{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}'
exit 0`)
	res, err := RunStream(context.Background(), a,
		executor.TaskBook{NodeID: "n1", WorkspaceRoot: t.TempDir(), Attempt: 1,
			Timeout: 20 * time.Second}, nil)
	require.NoError(t, err)
	require.Empty(t, res.GateResult, "无终态帧时交门禁裁定")
	require.Contains(t, res.Output, "hi")
}

// result 帧 success：结论 pass，正文与用量都带上。
func TestRunStreamResultFramePasses(t *testing.T) {
	a := fakeBinary(t, `echo '{"type":"assistant","message":{"content":[{"type":"text","text":"做完了"}]}}'
echo '{"type":"result","subtype":"success","session_id":"s-1","result":"完成","usage":{"input_tokens":5,"output_tokens":3}}'
exit 0`)
	res, err := RunStream(context.Background(), a,
		executor.TaskBook{NodeID: "n1", WorkspaceRoot: t.TempDir(), Attempt: 1,
			Timeout: 20 * time.Second}, nil)
	require.NoError(t, err)
	require.Equal(t, types.SopGatePass, res.GateResult)
	require.Equal(t, "s-1", res.ResumeSessionID)
	require.Equal(t, int64(5), res.Usage.InputTokens)
	require.Equal(t, int64(3), res.Usage.OutputTokens)
}

// 非 0 退出且 stderr 出现轮次用尽：转人工（重跑没用）。
func TestRunStreamMaxTurnsGoesHuman(t *testing.T) {
	a := fakeBinary(t, `echo "error: max turns exceeded" >&2
exit 1`)
	res, err := RunStream(context.Background(), a,
		executor.TaskBook{NodeID: "n1", WorkspaceRoot: t.TempDir(), Attempt: 1,
			Timeout: 20 * time.Second}, nil)
	require.NoError(t, err)
	require.Equal(t, types.SopGateHuman, res.GateResult)
	require.Contains(t, res.GateReason, "人工")
}

// 普通非 0 退出：打回并附退出码与 stderr 尾部。
func TestRunStreamNonZeroExitReworks(t *testing.T) {
	a := fakeBinary(t, `echo "boom: 编译失败" >&2
exit 4`)
	res, err := RunStream(context.Background(), a,
		executor.TaskBook{NodeID: "n1", WorkspaceRoot: t.TempDir(), Attempt: 1,
			Timeout: 20 * time.Second}, nil)
	require.NoError(t, err)
	require.Equal(t, types.SopGateRework, res.GateResult)
	require.Contains(t, res.GateReason, "退出码 4")
	require.Contains(t, res.GateReason, "编译失败")
}

// 超时：打回并说明超时（不是执行器错误）。
func TestRunStreamTimeoutReworks(t *testing.T) {
	a := fakeBinary(t, `sleep 30`)
	start := time.Now()
	res, err := RunStream(context.Background(), a,
		executor.TaskBook{NodeID: "n1", WorkspaceRoot: t.TempDir(), Attempt: 1,
			Timeout: 800 * time.Millisecond}, nil)
	require.NoError(t, err)
	require.Less(t, time.Since(start), 30*time.Second)
	require.Equal(t, types.SopGateRework, res.GateResult)
	require.Contains(t, res.GateReason, "超时")
}

// 私有 HOME：子进程看到 HOME 指向工作区内的私有目录。
func TestRunStreamUsesPrivateHome(t *testing.T) {
	a := fakeBinary(t, `echo "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"$HOME\"}]}}"`)
	root := t.TempDir()
	res, err := RunStream(context.Background(), a,
		executor.TaskBook{NodeID: "n1", WorkspaceRoot: root, Attempt: 1,
			Timeout: 20 * time.Second}, nil)
	require.NoError(t, err)
	require.Contains(t, strings.ReplaceAll(res.Output, "\\", "/"), ".sop/private-home/claude")
}

// Prepare：PATH 上没有该 CLI 时给白话安装指引。
func TestCLIProviderPrepareMissingBinaryPlainWords(t *testing.T) {
	p := &CLIProvider{A: ClaudeAdapter{}}
	t.Setenv("PATH", t.TempDir())
	err := p.Prepare(context.Background(), &types.SopNode{}, t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "未检测到")
	require.Contains(t, err.Error(), "PATH")
	require.Contains(t, err.Error(), "claude")
}

// Kind 用 cli: 前缀（注册表键口径）。
func TestCLIProviderKind(t *testing.T) {
	p := &CLIProvider{A: ClaudeAdapter{}}
	require.Equal(t, "cli:claude", p.Kind())
	require.Equal(t, "cli:claude", RegistryKey(ClaudeAdapter{}))
}
