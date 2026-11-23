package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// OpencodeAdapter 是 opencode 的适配器。
//
// 特点：原生加载 agent-system.md（SystemPromptNative），配置整段走
// OPENCODE_CONFIG_CONTENT 环境变量（JSON 里用 ${SOP_CLI_API_KEY} 引用密钥），
// 输出 `run --format json` 的 JSONL（part / step_finish）。
type OpencodeAdapter struct{}

// Kind 返回 CLI 种类。
func (OpencodeAdapter) Kind() string { return "opencode" }

// BinaryName 返回可执行文件名。
func (OpencodeAdapter) BinaryName() string { return "opencode" }

// PreferStdin：opencode 的 prompt 是位置参数，不走 stdin。
func (OpencodeAdapter) PreferStdin() bool { return false }

// SystemPromptMode：opencode 原生读私有 HOME 里的 agent-system.md。
func (OpencodeAdapter) SystemPromptMode() string { return SystemPromptNative }

// BuildArgs：run --format json --thinking [-m model] [-s resume] <prompt>。
func (OpencodeAdapter) BuildArgs(tb executor.TaskBook, prompt, resumeID string) []string {
	args := []string{"run", "--format", "json", "--thinking"}
	if model := strings.TrimSpace(tb.Model); model != "" {
		args = append(args, "-m", model)
	}
	if id := strings.TrimSpace(resumeID); id != "" {
		args = append(args, "-s", id)
	}
	if p := strings.TrimSpace(prompt); p != "" {
		args = append(args, p)
	}
	return args
}

// ConfigFiles：opencode 的配置整段塞进环境变量，磁盘上不写文件。
func (OpencodeAdapter) ConfigFiles(executor.TaskBook) map[string]string { return nil }

// Env：OPENCODE_CONFIG_CONTENT 里 provider/apiKey 一律引用环境变量。
func (OpencodeAdapter) Env(tb executor.TaskBook) map[string]string {
	env := map[string]string{}
	model := strings.TrimSpace(tb.Model)
	if model == "" {
		model = "${SOP_CLI_MODEL}"
	}
	base := "${SOP_CLI_BASE_URL}"
	if tb.Credential != nil && strings.TrimSpace(tb.Credential.BaseURL) != "" {
		base = "${SOP_CLI_BASE_URL}"
	}
	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"model":   model,
		"provider": map[string]any{
			"sop": map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": "SOP CLI",
				"options": map[string]any{
					"apiKey":  "${SOP_CLI_API_KEY}",
					"baseURL": base,
				},
			},
		},
	}
	if raw, err := json.Marshal(cfg); err == nil {
		env["OPENCODE_CONFIG_CONTENT"] = string(raw)
	}
	if tb.Credential != nil {
		if key := strings.TrimSpace(tb.Credential.APIKey); key != "" {
			env["OPENCODE_API_KEY"] = key
		}
	}
	return env
}

// ParseLine：part.type=text → text_delta；part.type=tool → tool_call；
// step_finish 是终态（带 session_id 与用量）。
func (a OpencodeAdapter) ParseLine(line string) ([]executor.StreamEvent, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return nil, false
	}
	var frame struct {
		Type string `json:"type"`
		Part struct {
			Type string          `json:"type"`
			Text string          `json:"text"`
			Tool string          `json:"tool"`
			Args json.RawMessage `json:"args"`
		} `json:"part"`
		SessionID string `json:"session_id"`
		Usage     *struct {
			Input  int64 `json:"input"`
			Output int64 `json:"output"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		return nil, false
	}
	now := time.Now()
	switch frame.Type {
	case "part":
		switch frame.Part.Type {
		case "text":
			if frame.Part.Text == "" {
				return nil, false
			}
			return []executor.StreamEvent{{Type: executor.StreamEventTextDelta, Text: frame.Part.Text, At: now}}, false
		case "tool":
			return []executor.StreamEvent{{
				Type: executor.StreamEventToolCall, ToolName: frame.Part.Tool,
				Payload: frame.Part.Args, At: now,
			}}, false
		}
		return nil, false
	case "step_finish":
		payload := map[string]any{"session_id": frame.SessionID}
		if frame.Usage != nil {
			payload["usage"] = map[string]any{
				"input_tokens": frame.Usage.Input, "output_tokens": frame.Usage.Output,
			}
		}
		return []executor.StreamEvent{{
			Type: executor.StreamEventResult, Payload: marshalPayload(payload), At: now,
		}}, true
	default:
		return nil, false
	}
}

// ResumeID：step_finish 的 session_id。
func (a OpencodeAdapter) ResumeID(events []executor.StreamEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Type != executor.StreamEventResult || len(ev.Payload) == 0 {
			continue
		}
		var payload struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(ev.Payload, &payload) == nil && payload.SessionID != "" {
			return payload.SessionID
		}
	}
	return ""
}

// FailureHint：额度/轮次用尽转人工。
func (a OpencodeAdapter) FailureHint(stderr string) string {
	if containsAnyFold(stderr, "max turns", "quota exceeded", "context length") {
		return types.SopGateHuman
	}
	return ""
}

// CursorAdapter 是 cursor-agent 的适配器。
//
// 特点：`--stream-partial-output` 输出 timestamp_ms 增量帧；凭据直接用
// CURSOR_API_KEY 环境变量；result 帧终止。
type CursorAdapter struct{}

// Kind 返回 CLI 种类（注册表键里的名字与二进制同名）。
func (CursorAdapter) Kind() string { return "cursor-agent" }

// BinaryName 返回可执行文件名。
func (CursorAdapter) BinaryName() string { return "cursor-agent" }

// PreferStdin：cursor-agent 的 prompt 是位置参数。
func (CursorAdapter) PreferStdin() bool { return false }

// SystemPromptMode：cursor 没有系统提示文件参数，规则包进正文。
func (CursorAdapter) SystemPromptMode() string { return SystemPromptWrap }

// BuildArgs：无头 + 部分流 + 免确认（无人值守必须 --force/--approve-mcps）。
func (CursorAdapter) BuildArgs(tb executor.TaskBook, prompt, resumeID string) []string {
	args := []string{
		"-p", "--output-format", "stream-json", "--stream-partial-output",
		"--trust", "--force", "--approve-mcps",
	}
	if model := strings.TrimSpace(tb.Model); model != "" {
		args = append(args, "--model", model)
	}
	if id := strings.TrimSpace(resumeID); id != "" {
		args = append(args, "--resume", id)
	}
	if p := strings.TrimSpace(prompt); p != "" {
		args = append(args, p)
	}
	return args
}

// ConfigFiles：cursor 配置走环境变量，无需文件。
func (CursorAdapter) ConfigFiles(executor.TaskBook) map[string]string { return nil }

// Env：凭据映射到 CURSOR_API_KEY。
func (CursorAdapter) Env(tb executor.TaskBook) map[string]string {
	env := map[string]string{}
	if tb.Credential == nil {
		return env
	}
	if key := strings.TrimSpace(tb.Credential.APIKey); key != "" {
		env["CURSOR_API_KEY"] = key
	}
	if base := strings.TrimSpace(tb.Credential.BaseURL); base != "" {
		env["CURSOR_API_BASE_URL"] = base
	}
	return env
}

// ParseLine：delta.text 是正文增量；result 帧终态。
func (a CursorAdapter) ParseLine(line string) ([]executor.StreamEvent, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return nil, false
	}
	var frame struct {
		Type      string `json:"type"`
		Timestamp int64  `json:"timestamp_ms"`
		Delta     struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
		Text      string `json:"text"`
		SessionID string `json:"session_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		return nil, false
	}
	now := time.Now()
	if frame.Delta.Text != "" {
		return []executor.StreamEvent{{Type: executor.StreamEventTextDelta, Text: frame.Delta.Text, At: now}}, false
	}
	if frame.Type == "result" {
		status := strings.ToLower(strings.TrimSpace(frame.Status))
		if status == "" {
			status = "success"
		}
		payload := map[string]any{"status": status, "session_id": frame.SessionID}
		var events []executor.StreamEvent
		if frame.Text != "" {
			events = append(events, executor.StreamEvent{
				Type: executor.StreamEventTextDelta, Text: frame.Text, At: now,
			})
		}
		events = append(events, executor.StreamEvent{
			Type: executor.StreamEventResult, Text: frame.Text,
			Payload: marshalPayload(payload), At: now,
		})
		return events, true
	}
	return nil, false
}

// ResumeID：result 帧的 session_id。
func (a CursorAdapter) ResumeID(events []executor.StreamEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Type != executor.StreamEventResult || len(ev.Payload) == 0 {
			continue
		}
		var payload struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(ev.Payload, &payload) == nil && payload.SessionID != "" {
			return payload.SessionID
		}
	}
	return ""
}

// FailureHint：轮次/额度用尽转人工。
func (a CursorAdapter) FailureHint(stderr string) string {
	if containsAnyFold(stderr, "max turns", "rate limit", "quota") {
		return types.SopGateHuman
	}
	return ""
}

// AgyAdapter 是 antigravity（agy）的适配器。
//
// 特点：凭据写 settings.json（**引用环境变量**）+ GEMINI_API_KEY 环境变量；
// 输出 event 帧（init/step_update/result）；不支持续跑。
type AgyAdapter struct{}

// Kind 返回 CLI 种类。
func (AgyAdapter) Kind() string { return "agy" }

// BinaryName 返回可执行文件名。
func (AgyAdapter) BinaryName() string { return "agy" }

// PreferStdin：agy 的 prompt 是位置参数。
func (AgyAdapter) PreferStdin() bool { return false }

// SystemPromptMode：agy 没有系统提示文件参数，规则包进正文。
func (AgyAdapter) SystemPromptMode() string { return SystemPromptWrap }

// BuildArgs：流式 + 跳过权限确认 + 关闭斜杠命令（无人值守）。
func (AgyAdapter) BuildArgs(tb executor.TaskBook, prompt, resumeID string) []string {
	args := []string{
		"--output-format", "stream-json", "--dangerously-skip-permissions",
		"--disable-slash-commands", "-p",
	}
	if model := strings.TrimSpace(tb.Model); model != "" {
		args = append(args, "--model", model)
	}
	// agy 不支持会话续跑：resumeID 非空时忽略（RunStream 也不会塞进 argv）。
	if p := strings.TrimSpace(prompt); p != "" {
		args = append(args, p)
	}
	return args
}

// ConfigFiles：settings.json 里只留变量引用。
func (AgyAdapter) ConfigFiles(executor.TaskBook) map[string]string {
	return map[string]string{
		".gemini/antigravity-cli/settings.json": fmt.Sprintf(
			"{\n  \"apiKey\": \"%s\"\n}\n", "${SOP_CLI_API_KEY}"),
	}
}

// Env：GEMINI_API_KEY 走环境变量。
func (AgyAdapter) Env(tb executor.TaskBook) map[string]string {
	env := map[string]string{}
	if tb.Credential == nil {
		return env
	}
	if key := strings.TrimSpace(tb.Credential.APIKey); key != "" {
		env["GEMINI_API_KEY"] = key
	}
	if base := strings.TrimSpace(tb.Credential.BaseURL); base != "" {
		env["GEMINI_API_BASE_URL"] = base
	}
	return env
}

// ParseLine：step_update.text → text_delta；result 帧终态（按 status 判成败）。
func (a AgyAdapter) ParseLine(line string) ([]executor.StreamEvent, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return nil, false
	}
	var frame struct {
		Event  string `json:"event"`
		Text   string `json:"text"`
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		return nil, false
	}
	now := time.Now()
	switch frame.Event {
	case "step_update":
		if frame.Text == "" {
			return nil, false
		}
		return []executor.StreamEvent{{Type: executor.StreamEventTextDelta, Text: frame.Text, At: now}}, false
	case "result":
		status := strings.ToLower(strings.TrimSpace(frame.Status))
		if status == "" {
			status = "success"
		}
		payload := map[string]any{"status": status, "reason": frame.Reason}
		events := []executor.StreamEvent{{
			Type: executor.StreamEventResult, Text: frame.Text,
			Payload: marshalPayload(payload), At: now,
		}}
		if frame.Text != "" {
			events = append([]executor.StreamEvent{{
				Type: executor.StreamEventTextDelta, Text: frame.Text, At: now,
			}}, events...)
		}
		return events, true
	default:
		return nil, false
	}
}

// ResumeID：agy 不支持续跑。
func (AgyAdapter) ResumeID([]executor.StreamEvent) string { return "" }

// FailureHint：轮次用尽转人工。
func (a AgyAdapter) FailureHint(stderr string) string {
	if containsAnyFold(stderr, "max turns", "permission denied") {
		return types.SopGateHuman
	}
	return ""
}
