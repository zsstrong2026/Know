package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// parseFixture 逐行跑 ParseLine，聚合事件与终态标记。
func parseFixture(t *testing.T, a Adapter, rel string) ([]executor.StreamEvent, bool) {
	t.Helper()
	body, err := os.ReadFile(filepath.FromSlash(rel))
	require.NoError(t, err)
	var events []executor.StreamEvent
	terminal := false
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		evs, term := a.ParseLine(line)
		events = append(events, evs...)
		terminal = terminal || term
	}
	return events, terminal
}

// filterType 取某类型的事件。
func filterType(events []executor.StreamEvent, typ string) []executor.StreamEvent {
	var out []executor.StreamEvent
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func TestOpencodeAdapter(t *testing.T) {
	a := OpencodeAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{Model: "m1"}, "P", "sess-1"), " ")
	require.Contains(t, args, "run --format json --thinking")
	require.Contains(t, args, "-m m1")
	require.Contains(t, args, "-s sess-1")
	require.Equal(t, SystemPromptNative, a.SystemPromptMode(), "opencode 原生加载 agent-system.md")
	require.False(t, a.PreferStdin())

	env := a.Env(executor.TaskBook{Credential: &types.SopCLICredential{APIKey: "k"}})
	cfg := env["OPENCODE_CONFIG_CONTENT"]
	require.Contains(t, cfg, "${SOP_CLI_API_KEY}", "配置里只留变量引用")
	require.NotContains(t, cfg, `"k"`, "明文密钥不得进配置")

	evs, term := parseFixture(t, a, "testdata/opencode/stream.jsonl")
	require.True(t, term, "step_finish 是终态")
	texts := filterType(evs, executor.StreamEventTextDelta)
	require.Len(t, texts, 2)
	require.Equal(t, "先读代码", texts[0].Text)
	require.Len(t, filterType(evs, executor.StreamEventToolCall), 1)
	require.Equal(t, "sess-1", a.ResumeID(evs))
	require.Equal(t, types.SopGateHuman, a.FailureHint("quota exceeded"))
}

func TestCursorAdapter(t *testing.T) {
	a := CursorAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{Model: "m1"}, "P", "sess-2"), " ")
	require.Contains(t, args,
		"-p --output-format stream-json --stream-partial-output --trust --force --approve-mcps")
	require.Contains(t, args, "--model m1")
	require.Contains(t, args, "--resume sess-2")
	require.Equal(t, "cursor-agent", a.Kind())
	require.Equal(t, "cursor-agent", a.BinaryName())

	env := a.Env(executor.TaskBook{Credential: &types.SopCLICredential{APIKey: "k"}})
	require.Equal(t, "k", env["CURSOR_API_KEY"])

	evs, term := parseFixture(t, a, "testdata/cursor/stream.jsonl")
	require.True(t, term, "result 是终态")
	// 两条 delta + result 帧补的最终文本
	require.GreaterOrEqual(t, len(filterType(evs, executor.StreamEventTextDelta)), 2)
	require.Equal(t, "sess-2", a.ResumeID(evs))
}

func TestAgyAdapter(t *testing.T) {
	a := AgyAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{}, "P", ""), " ")
	require.Contains(t, args,
		"--output-format stream-json --dangerously-skip-permissions --disable-slash-commands -p")

	cfg := a.ConfigFiles(executor.TaskBook{Credential: &types.SopCLICredential{APIKey: "sk-real-key"}})
	content, ok := cfg[".gemini/antigravity-cli/settings.json"]
	require.True(t, ok)
	require.NotContains(t, content, "sk-real-key", "明文 Key 不落配置")
	require.Contains(t, content, "SOP_CLI_API_KEY")

	env := a.Env(executor.TaskBook{Credential: &types.SopCLICredential{APIKey: "sk-real-key"}})
	require.Equal(t, "sk-real-key", env["GEMINI_API_KEY"])

	evs, term := parseFixture(t, a, "testdata/agy/stream.jsonl")
	require.True(t, term, "event=result 是终态")
	texts := filterType(evs, executor.StreamEventTextDelta)
	require.GreaterOrEqual(t, len(texts), 2)
	require.Empty(t, a.ResumeID(evs), "agy 不支持续跑")
}

// 三个适配器都能被 CLIProvider 包装成合法注册表键。
func TestAdaptersCLIProviderKinds(t *testing.T) {
	for _, a := range []Adapter{OpencodeAdapter{}, CursorAdapter{}, AgyAdapter{}} {
		p := &CLIProvider{A: a}
		require.Equal(t, "cli:"+a.Kind(), p.Kind())
	}
	require.Equal(t, "cli:opencode", RegistryKey(OpencodeAdapter{}))
	require.Equal(t, "cli:cursor-agent", RegistryKey(CursorAdapter{}))
	require.Equal(t, "cli:agy", RegistryKey(AgyAdapter{}))
}
