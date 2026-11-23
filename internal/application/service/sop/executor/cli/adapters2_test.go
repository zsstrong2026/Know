package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestKimiAdapter(t *testing.T) {
	a := KimiAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{}, "P", "sess-k"), " ")
	require.Contains(t, args, "--output-format stream-json")
	require.Contains(t, args, "-S sess-k")
	require.Contains(t, args, "-p P")

	cfg := a.ConfigFiles(executor.TaskBook{Credential: &types.SopCLICredential{APIKey: "k-secret"}})
	content, ok := cfg[".kimi-code/config.toml"]
	require.True(t, ok)
	require.NotContains(t, content, "k-secret")
	require.Contains(t, content, "SOP_CLI_API_KEY")

	evs, term := parseFixture(t, a, "testdata/kimi/stream.jsonl")
	require.True(t, term, "meta 帧是终态")
	require.Len(t, filterType(evs, executor.StreamEventTextDelta), 2)
	require.Equal(t, "sess-k", a.ResumeID(evs), "resume_hint 即续跑 id")
}

func TestPiAdapter(t *testing.T) {
	a := PiAdapter{}
	tb := executor.TaskBook{SystemPrompt: "你是工程师"}
	args := strings.Join(a.BuildArgs(tb, "P", "sess-p"), " ")
	require.Contains(t, args, "--mode json")
	require.Contains(t, args, "--session-id sess-p", "续跑就是不换会话 id")
	require.Contains(t, args, "--append-system-prompt", "原生系统提示文件")
	require.Equal(t, SystemPromptNative, a.SystemPromptMode())

	// 没有续跑 id 时自己生成一个稳定 uuid（同一轮内 BuildArgs 一致即可）
	generated := a.BuildArgs(tb, "P", "")
	require.Contains(t, strings.Join(generated, " "), "--session-id")

	cfg := a.ConfigFiles(executor.TaskBook{})
	require.Contains(t, cfg, ".pi/agent/models.json")
	require.NotContains(t, cfg[".pi/agent/models.json"], "sk-")

	evs, term := parseFixture(t, a, "testdata/pi/stream.jsonl")
	require.True(t, term, "agent_end 是终态")
	require.Len(t, filterType(evs, executor.StreamEventTextDelta), 2)
	require.Equal(t, "sess-p", a.ResumeID(evs))
}

func TestDshAdapter(t *testing.T) {
	a := DshAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{}, "P", ""), " ")
	require.Contains(t, args,
		"--profile headless --json --permission-mode danger-full-access --approval-policy never")

	evs, term := parseFixture(t, a, "testdata/dsh/stream.jsonl")
	require.True(t, term, "final 帧是终态")
	require.Len(t, filterType(evs, executor.StreamEventTextDelta), 2)
	result := filterType(evs, executor.StreamEventResult)
	require.Len(t, result, 1)
	require.Contains(t, string(result[0].Payload), `"status":"success"`, "completed 视为成功")

	// 非 completed 的 kind 判失败
	evs2, term2 := a.ParseLine(`{"type":"final","reason":{"kind":"cancelled"}}`)
	require.True(t, term2)
	require.Contains(t, string(evs2[0].Payload), `"status":"error"`)

	// 单事件正文截断 8KiB
	long := strings.Repeat("x", 9000)
	evs3, _ := a.ParseLine(`{"type":"delta","text":"` + long + `"}`)
	require.Len(t, evs3, 1)
	require.LessOrEqual(t, len(evs3[0].Text), dshEventTextMax)
}

func TestZcodeAdapter(t *testing.T) {
	a := ZcodeAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{}, "P", ""), " ")
	require.Contains(t, args, "--output-format stream-json --mode yolo --no-color")
	require.Contains(t, args, "--prompt P")
	require.Contains(t, args, "--disallowed-tools", "危险工具清单必须显式禁用")

	cfg := a.ConfigFiles(executor.TaskBook{Credential: &types.SopCLICredential{APIKey: "sk-zcode"}})
	content, ok := cfg[".zcode/cli/config.json"]
	require.True(t, ok)
	require.NotContains(t, content, "sk-zcode", "配置里只留变量引用")
	require.Contains(t, content, "SOP_CLI_API_KEY")

	evs, term := parseFixture(t, a, "testdata/zcode/stream.jsonl")
	require.True(t, term)
	require.Equal(t, "sess-z", a.ResumeID(evs))
}

// 四个适配器都能被 CLIProvider 包装。
func TestAdapters2CLIProviderKinds(t *testing.T) {
	for _, a := range []Adapter{KimiAdapter{}, PiAdapter{}, DshAdapter{}, ZcodeAdapter{}} {
		require.Equal(t, "cli:"+a.Kind(), (&CLIProvider{A: a}).Kind())
	}
}
