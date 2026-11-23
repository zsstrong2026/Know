package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestCodebuddyAdapter(t *testing.T) {
	a := CodebuddyAdapter{}
	require.True(t, a.PreferStdin(), "headless 从 stdin 读 prompt")
	args := strings.Join(a.BuildArgs(executor.TaskBook{}, "", ""), " ")
	require.Contains(t, args, "--headless")
	require.Contains(t, args, "json-stream")
	// 有 prompt（argv 投递）时才出现 --prompt
	withPrompt := strings.Join(a.BuildArgs(executor.TaskBook{}, "P", ""), " ")
	require.Contains(t, withPrompt, "--prompt P")

	cfg := a.ConfigFiles(executor.TaskBook{})
	require.NotContains(t, cfg[".codebuddy/config.json"], "sk-")
	require.Contains(t, cfg[".codebuddy/config.json"], "SOP_CLI_API_KEY")

	evs, term := parseFixture(t, a, "testdata/codebuddy/stream.jsonl")
	require.True(t, term, "done 是终态")
	require.Len(t, filterType(evs, executor.StreamEventTextDelta), 1)
	require.Len(t, filterType(evs, executor.StreamEventToolCall), 1)
	require.Len(t, filterType(evs, executor.StreamEventResult), 1)
	require.Equal(t, "sess-cb", a.ResumeID(evs))
}

func TestCodexAdapter(t *testing.T) {
	a := CodexAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{Model: "gpt-x"}, "P", "ignored"), " ")
	require.Contains(t, args, "exec --json")
	require.Contains(t, args, "--full-auto", "无人值守必须免审批")
	require.Contains(t, args, "--model gpt-x")
	require.NotContains(t, args, "ignored", "首版不续跑：resume 参数被忽略")

	cfg := a.ConfigFiles(executor.TaskBook{})
	require.Contains(t, cfg, ".codex/config.toml")
	require.Contains(t, cfg[".codex/config.toml"], "approval_policy = \"never\"")

	evs, term := parseFixture(t, a, "testdata/codex/stream.jsonl")
	require.True(t, term, "turn.completed 是终态")
	require.Len(t, filterType(evs, executor.StreamEventTextDelta), 1)
	require.Len(t, filterType(evs, executor.StreamEventToolCall), 1)
	result := filterType(evs, executor.StreamEventResult)
	require.Len(t, result, 1)
	require.Contains(t, string(result[0].Payload), `"status":"success"`)
	require.Contains(t, string(result[0].Payload), `"input_tokens":11`)
	require.Empty(t, a.ResumeID(evs))
}

// 失败帧：turn.failed 判 error；error 事件单独成帧。
func TestCodexFailureFrames(t *testing.T) {
	a := CodexAdapter{}
	evs, term := a.ParseLine(`{"type":"turn.failed","message":"usage limit reached"}`)
	require.True(t, term)
	require.Contains(t, string(evs[0].Payload), `"status":"error"`)
	require.Equal(t, types.SopGateHuman, a.FailureHint("usage limit reached"))

	errEvs, errTerm := a.ParseLine(`{"type":"error","message":"network"}`)
	require.False(t, errTerm)
	require.Equal(t, executor.StreamEventError, errEvs[0].Type)
}

// 两个适配器都能被 CLIProvider 包装。
func TestAdapters3CLIProviderKinds(t *testing.T) {
	for _, a := range []Adapter{CodebuddyAdapter{}, CodexAdapter{}} {
		require.Equal(t, "cli:"+a.Kind(), (&CLIProvider{A: a}).Kind())
	}
	require.Equal(t, "cli:codebuddy", RegistryKey(CodebuddyAdapter{}))
	require.Equal(t, "cli:codex", RegistryKey(CodexAdapter{}))
}
