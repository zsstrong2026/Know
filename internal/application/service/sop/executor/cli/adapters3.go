package cli

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// CodebuddyAdapter 是 codebuddy 的适配器。
//
// 特点：headless 模式从标准输入读 prompt（PreferStdin=true），输出自有 JSON 帧；
// 这里统一翻译成基准事件（text_delta / tool_call / result）。
type CodebuddyAdapter struct{}

// Kind 返回 CLI 种类。
func (CodebuddyAdapter) Kind() string { return "codebuddy" }

// BinaryName 返回可执行文件名。
func (CodebuddyAdapter) BinaryName() string { return "codebuddy" }

// PreferStdin：codebuddy headless 从 stdin 读 prompt（大任务书不必挤命令行）。
func (CodebuddyAdapter) PreferStdin() bool { return true }

// SystemPromptMode：无系统提示文件参数，规则包进正文。
func (CodebuddyAdapter) SystemPromptMode() string { return SystemPromptWrap }

// BuildArgs：headless + JSON 事件流；prompt 走 stdin 时 argv 不带正文。
func (CodebuddyAdapter) BuildArgs(_ executor.TaskBook, prompt, resumeID string) []string {
	args := []string{"--headless", "--output-format", "json-stream", "--no-color"}
	if id := strings.TrimSpace(resumeID); id != "" {
		args = append(args, "--session", id)
	}
	if p := strings.TrimSpace(prompt); p != "" {
		args = append(args, "--prompt", p)
	}
	return args
}

// ConfigFiles：配置里 apiKey 只引用环境变量。
func (CodebuddyAdapter) ConfigFiles(executor.TaskBook) map[string]string {
	return map[string]string{
		".codebuddy/config.json": "{\n  \"apiKey\": \"${SOP_CLI_API_KEY}\",\n" +
			"  \"baseUrl\": \"${SOP_CLI_BASE_URL}\",\n  \"model\": \"${SOP_CLI_MODEL}\"\n}\n",
	}
}

// Env：凭据走环境变量。
func (CodebuddyAdapter) Env(tb executor.TaskBook) map[string]string {
	env := map[string]string{}
	if tb.Credential == nil {
		return env
	}
	if key := strings.TrimSpace(tb.Credential.APIKey); key != "" {
		env["CODEBUDDY_API_KEY"] = key
	}
	return env
}

// ParseLine：codebuddy 自有帧 → 基准事件。
//
// 帧形如 {"type":"message","role":"assistant","content":"..."} /
// {"type":"tool","name":"write","input":{...}} / {"type":"done","status":"success"}。
func (a CodebuddyAdapter) ParseLine(line string) ([]executor.StreamEvent, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return nil, false
	}
	var frame struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content string          `json:"content"`
		Name    string          `json:"name"`
		Input   json.RawMessage `json:"input"`
		Status  string          `json:"status"`
		Reason  string          `json:"reason"`
		Session string          `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		return nil, false
	}
	now := time.Now()
	switch frame.Type {
	case "message":
		if frame.Content == "" {
			return nil, false
		}
		return []executor.StreamEvent{{Type: executor.StreamEventTextDelta, Text: frame.Content, At: now}}, false
	case "tool":
		return []executor.StreamEvent{{
			Type: executor.StreamEventToolCall, ToolName: frame.Name, Payload: frame.Input, At: now,
		}}, false
	case "done", "result":
		status := strings.ToLower(strings.TrimSpace(frame.Status))
		if status == "" {
			status = "success"
		}
		payload := map[string]any{"status": status, "reason": frame.Reason, "session_id": frame.Session}
		return []executor.StreamEvent{{
			Type: executor.StreamEventResult, Payload: marshalPayload(payload), At: now,
		}}, true
	default:
		return nil, false
	}
}

// ResumeID：done 帧的 session_id（首版 codebuddy 不保证可续，取到就用）。
func (a CodebuddyAdapter) ResumeID(events []executor.StreamEvent) string {
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
func (a CodebuddyAdapter) FailureHint(stderr string) string {
	if containsAnyFold(stderr, "max turns", "quota", "rate limit") {
		return types.SopGateHuman
	}
	return ""
}

// CodexAdapter 是 codex（OpenAI Codex CLI）的适配器。
//
// 特点：`codex exec --json` 输出事件流；配置 `.codex/config.toml` 引用环境变量；
// 首版不续跑（返工走新会话 + carry_over 的产物）。
type CodexAdapter struct{}

// Kind 返回 CLI 种类。
func (CodexAdapter) Kind() string { return "codex" }

// BinaryName 返回可执行文件名。
func (CodexAdapter) BinaryName() string { return "codex" }

// PreferStdin：codex exec 的 prompt 是位置参数。
func (CodexAdapter) PreferStdin() bool { return false }

// SystemPromptMode：codex 没有系统提示文件参数，规则包进正文。
func (CodexAdapter) SystemPromptMode() string { return SystemPromptWrap }

// BuildArgs：exec --json [--model] [--full-auto] <prompt>。
//
// --full-auto 是无人值守必需的「不弹确认」档位（等价于 workspace-write + 免审批）。
func (CodexAdapter) BuildArgs(tb executor.TaskBook, prompt, resumeID string) []string {
	args := []string{"exec", "--json", "--full-auto"}
	if model := strings.TrimSpace(tb.Model); model != "" {
		args = append(args, "--model", model)
	}
	if p := strings.TrimSpace(prompt); p != "" {
		args = append(args, p)
	}
	// resumeID 有意忽略：codex 首版不续跑。
	_ = resumeID
	return args
}

// ConfigFiles：config.toml 里引用环境变量。
func (CodexAdapter) ConfigFiles(executor.TaskBook) map[string]string {
	return map[string]string{
		".codex/config.toml": "api_key = \"${SOP_CLI_API_KEY}\"\n" +
			"base_url = \"${SOP_CLI_BASE_URL}\"\nmodel = \"${SOP_CLI_MODEL}\"\n" +
			"approval_policy = \"never\"\n",
	}
}

// Env：凭据走环境变量。
func (CodexAdapter) Env(tb executor.TaskBook) map[string]string {
	env := map[string]string{}
	if tb.Credential == nil {
		return env
	}
	if key := strings.TrimSpace(tb.Credential.APIKey); key != "" {
		env["OPENAI_API_KEY"] = key
	}
	if base := strings.TrimSpace(tb.Credential.BaseURL); base != "" {
		env["OPENAI_BASE_URL"] = base
	}
	return env
}

// ParseLine：codex exec 的 JSON 事件 → 基准事件。
//
// 帧形如 {"type":"item.completed","item":{"type":"agent_message","text":"…"}} /
// {"type":"turn.completed","usage":{…}} / {"type":"error","message":"…"}。
func (a CodexAdapter) ParseLine(line string) ([]executor.StreamEvent, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return nil, false
	}
	var frame struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Item    struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
		Usage *struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		return nil, false
	}
	now := time.Now()
	switch frame.Type {
	case "item.completed", "item.updated":
		if frame.Item.Type == "agent_message" && frame.Item.Text != "" {
			return []executor.StreamEvent{{
				Type: executor.StreamEventTextDelta, Text: frame.Item.Text, At: now,
			}}, false
		}
		if frame.Item.Type == "command_execution" {
			return []executor.StreamEvent{{
				Type: executor.StreamEventToolCall, ToolName: "shell", At: now,
			}}, false
		}
		return nil, false
	case "turn.completed", "turn.failed":
		status := "success"
		reason := ""
		if frame.Type == "turn.failed" || strings.TrimSpace(frame.Message) != "" {
			status, reason = "error", frame.Message
		}
		payload := map[string]any{"status": status, "reason": reason}
		if frame.Usage != nil {
			payload["usage"] = map[string]any{
				"input_tokens": frame.Usage.InputTokens, "output_tokens": frame.Usage.OutputTokens,
			}
		}
		return []executor.StreamEvent{{
			Type: executor.StreamEventResult, Payload: marshalPayload(payload), At: now,
		}}, true
	case "error":
		return []executor.StreamEvent{{
			Type: executor.StreamEventError, Text: frame.Message, At: now,
		}}, false
	default:
		return nil, false
	}
}

// ResumeID：首版 codex 不续跑。
func (CodexAdapter) ResumeID([]executor.StreamEvent) string { return "" }

// FailureHint：额度/审批问题转人工。
func (a CodexAdapter) FailureHint(stderr string) string {
	if containsAnyFold(stderr, "quota", "rate limit", "usage limit") {
		return types.SopGateHuman
	}
	return ""
}
