package cli

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// truncateBytes 按字节截断并补省略号（总长不超过 max）。
func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "…"
}

// KimiAdapter 是 kimi（Kimi Code CLI）的适配器。
//
// 特点：配置文件是 TOML（`.kimi-code/config.toml`，密钥引用环境变量），
// 输出是「整消息帧 + meta 收尾帧」，续跑 id 在 meta.session.resume_hint。
type KimiAdapter struct{}

// Kind 返回 CLI 种类。
func (KimiAdapter) Kind() string { return "kimi" }

// BinaryName 返回可执行文件名。
func (KimiAdapter) BinaryName() string { return "kimi" }

// PreferStdin：kimi 的 prompt 走 -p 参数。
func (KimiAdapter) PreferStdin() bool { return false }

// SystemPromptMode：kimi 无系统提示文件参数，规则包进正文。
func (KimiAdapter) SystemPromptMode() string { return SystemPromptWrap }

// BuildArgs：-p <prompt> --output-format stream-json [-S 会话 id]。
func (KimiAdapter) BuildArgs(tb executor.TaskBook, prompt, resumeID string) []string {
	args := []string{"--output-format", "stream-json"}
	if id := strings.TrimSpace(resumeID); id != "" {
		args = append(args, "-S", id)
	}
	if p := strings.TrimSpace(prompt); p != "" {
		args = append(args, "-p", p)
	}
	return args
}

// ConfigFiles：TOML 里 api_key 引用环境变量（磁盘上永无明文）。
func (KimiAdapter) ConfigFiles(executor.TaskBook) map[string]string {
	return map[string]string{
		".kimi-code/config.toml": "[provider]\napi_key = \"${SOP_CLI_API_KEY}\"\n" +
			"base_url = \"${SOP_CLI_BASE_URL}\"\nmodel = \"${SOP_CLI_MODEL}\"\n",
	}
}

// Env：凭据同时以 KIMI_API_KEY 注入（CLI 优先读配置里的引用，环境变量兜底）。
func (KimiAdapter) Env(tb executor.TaskBook) map[string]string {
	env := map[string]string{}
	if tb.Credential == nil {
		return env
	}
	if key := strings.TrimSpace(tb.Credential.APIKey); key != "" {
		env["KIMI_API_KEY"] = key
	}
	if base := strings.TrimSpace(tb.Credential.BaseURL); base != "" {
		env["KIMI_BASE_URL"] = base
	}
	return env
}

// ParseLine：整消息帧取 text 作为正文；meta.session.resume_hint 是终态与续跑 id。
func (a KimiAdapter) ParseLine(line string) ([]executor.StreamEvent, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return nil, false
	}
	var frame struct {
		Type    string `json:"type"`
		Content string `json:"content"`
		Text    string `json:"text"`
		Meta    struct {
			Session struct {
				ResumeHint string `json:"resume_hint"`
			} `json:"session"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		return nil, false
	}
	now := time.Now()
	if frame.Type == "meta" || frame.Meta.Session.ResumeHint != "" {
		payload := map[string]any{"session_id": frame.Meta.Session.ResumeHint, "status": "success"}
		return []executor.StreamEvent{{
			Type: executor.StreamEventResult, Payload: marshalPayload(payload), At: now,
		}}, true
	}
	text := firstNonEmpty(frame.Content, frame.Text)
	if text == "" {
		return nil, false
	}
	return []executor.StreamEvent{{Type: executor.StreamEventTextDelta, Text: text, At: now}}, false
}

// ResumeID：meta.session.resume_hint。
func (a KimiAdapter) ResumeID(events []executor.StreamEvent) string {
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
func (a KimiAdapter) FailureHint(stderr string) string {
	if containsAnyFold(stderr, "max turns", "quota", "rate limit") {
		return types.SopGateHuman
	}
	return ""
}

// PiAdapter 是 pi（pi-coding-agent）的适配器。
//
// 特点：`--mode json` 事件流；`--append-system-prompt <文件>` 是原生系统提示入口；
// `--session-id <uuid>` 显式指定会话（续跑就是不换 id）。
type PiAdapter struct{}

// Kind 返回 CLI 种类。
func (PiAdapter) Kind() string { return "pi" }

// BinaryName 返回可执行文件名。
func (PiAdapter) BinaryName() string { return "pi" }

// PreferStdin：pi 的 prompt 是位置参数。
func (PiAdapter) PreferStdin() bool { return false }

// SystemPromptMode：pi 原生支持追加系统提示文件。
func (PiAdapter) SystemPromptMode() string { return SystemPromptNative }

// BuildArgs：--mode json --session-id <id> [--thinking] [--append-system-prompt <文件>] <prompt>。
func (PiAdapter) BuildArgs(tb executor.TaskBook, prompt, resumeID string) []string {
	sessionID := strings.TrimSpace(resumeID)
	if sessionID == "" {
		sessionID = newUUID()
	}
	args := []string{"--mode", "json", "--session-id", sessionID}
	if tb.SystemPrompt != "" {
		// 私有 HOME 里的 agent-system.md（RunStream 已写好）。
		args = append(args, "--append-system-prompt",
			"$HOME/agent-system.md")
	}
	if p := strings.TrimSpace(prompt); p != "" {
		args = append(args, p)
	}
	return args
}

// newUUID 生成会话 id（pi 要求调用方给一个稳定 id 才能续跑）。
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ConfigFiles：models.json 里 apiKey 引用环境变量。
func (PiAdapter) ConfigFiles(executor.TaskBook) map[string]string {
	return map[string]string{
		".pi/agent/models.json": "{\n  \"provider\": {\n    \"apiKey\": \"${SOP_CLI_API_KEY}\",\n" +
			"    \"baseURL\": \"${SOP_CLI_BASE_URL}\",\n    \"model\": \"${SOP_CLI_MODEL}\"\n  }\n}\n",
	}
}

// Env：凭据走环境变量。
func (PiAdapter) Env(tb executor.TaskBook) map[string]string {
	env := map[string]string{}
	if tb.Credential == nil {
		return env
	}
	if key := strings.TrimSpace(tb.Credential.APIKey); key != "" {
		env["PI_API_KEY"] = key
	}
	return env
}

// ParseLine：message_update → text_delta；agent_end → 终态。
func (a PiAdapter) ParseLine(line string) ([]executor.StreamEvent, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return nil, false
	}
	var frame struct {
		Type      string `json:"type"`
		Text      string `json:"text"`
		SessionID string `json:"session_id"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		return nil, false
	}
	now := time.Now()
	switch frame.Type {
	case "message_update", "message_delta":
		if frame.Text == "" {
			return nil, false
		}
		return []executor.StreamEvent{{Type: executor.StreamEventTextDelta, Text: frame.Text, At: now}}, false
	case "agent_end":
		status := "success"
		reason := ""
		if strings.TrimSpace(frame.Error) != "" {
			status, reason = "error", frame.Error
		}
		payload := map[string]any{"status": status, "reason": reason, "session_id": frame.SessionID}
		return []executor.StreamEvent{{
			Type: executor.StreamEventResult, Payload: marshalPayload(payload), At: now,
		}}, true
	default:
		return nil, false
	}
}

// ResumeID：agent_end 的 session_id。
func (a PiAdapter) ResumeID(events []executor.StreamEvent) string {
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

// FailureHint：轮次用尽转人工。
func (a PiAdapter) FailureHint(stderr string) string {
	if containsAnyFold(stderr, "max turns", "iteration limit") {
		return types.SopGateHuman
	}
	return ""
}

// dshEventTextMax 是 dsh 单事件正文上限：它会把整段 diff 塞进一个事件。
const dshEventTextMax = 8192

// DshAdapter 是 dsh（DeepSeek CLI）的适配器。
//
// 特点：headless profile + 免审批（危险模式，仅在隔离工作区里用）；
// 事件正文可能极长（整段 diff），按 8KiB 截断再直播。
type DshAdapter struct{}

// Kind 返回 CLI 种类。
func (DshAdapter) Kind() string { return "dsh" }

// BinaryName 返回可执行文件名。
func (DshAdapter) BinaryName() string { return "dsh" }

// PreferStdin：dsh 的 prompt 是位置参数。
func (DshAdapter) PreferStdin() bool { return false }

// SystemPromptMode：dsh 无系统提示文件参数，规则包进正文。
func (DshAdapter) SystemPromptMode() string { return SystemPromptWrap }

// BuildArgs：headless + json + 免审批（无人值守必须）。
func (DshAdapter) BuildArgs(tb executor.TaskBook, prompt, resumeID string) []string {
	args := []string{
		"--profile", "headless", "--json",
		"--permission-mode", "danger-full-access", "--approval-policy", "never",
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

// ConfigFiles：模型 patch 里引用环境变量。
func (DshAdapter) ConfigFiles(executor.TaskBook) map[string]string {
	return map[string]string{
		".dsh/config.json": "{\n  \"provider\": {\n    \"apiKey\": \"${SOP_CLI_API_KEY}\",\n" +
			"    \"baseUrl\": \"${SOP_CLI_BASE_URL}\"\n  }\n}\n",
	}
}

// Env：凭据走环境变量。
func (DshAdapter) Env(tb executor.TaskBook) map[string]string {
	env := map[string]string{}
	if tb.Credential == nil {
		return env
	}
	if key := strings.TrimSpace(tb.Credential.APIKey); key != "" {
		env["DEEPSEEK_API_KEY"] = key
	}
	if base := strings.TrimSpace(tb.Credential.BaseURL); base != "" {
		env["DEEPSEEK_BASE_URL"] = base
	}
	return env
}

// ParseLine：delta/event 正文；final 帧按 reason.kind 判成败。
func (a DshAdapter) ParseLine(line string) ([]executor.StreamEvent, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return nil, false
	}
	var frame struct {
		Type   string `json:"type"`
		Event  string `json:"event"`
		Text   string `json:"text"`
		Reason struct {
			Kind string `json:"kind"`
		} `json:"reason"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		return nil, false
	}
	now := time.Now()
	kind := firstNonEmpty(frame.Type, frame.Event)
	switch kind {
	case "final":
		status := "success"
		reason := strings.TrimSpace(frame.Reason.Kind)
		if reason != "" && reason != "completed" {
			status = "error"
		}
		payload := map[string]any{"status": status, "reason": reason}
		return []executor.StreamEvent{{
			Type: executor.StreamEventResult, Payload: marshalPayload(payload), At: now,
		}}, true
	default:
		if frame.Text == "" {
			return nil, false
		}
		// 单事件正文封顶：dsh 会把整段 diff 塞进来，直播与日志都不该被一段撑爆。
		// 截断按字节、且带上省略号也不超上限：直播端按字节限流，多出的 3 个字节
		// （"…" 的 UTF-8 长度）会让断言与限流失去意义。
		text := frame.Text
		if len(text) > dshEventTextMax {
			text = truncateBytes(text, dshEventTextMax)
		}
		return []executor.StreamEvent{{Type: executor.StreamEventTextDelta, Text: text, At: now}}, false
	}
}

// ResumeID：dsh 的 final 帧不带 id，续跑走 --resume（由 RunStream 原样回传入参）。
func (DshAdapter) ResumeID([]executor.StreamEvent) string { return "" }

// FailureHint：额度/权限问题转人工。
func (a DshAdapter) FailureHint(stderr string) string {
	if containsAnyFold(stderr, "insufficient balance", "rate limit", "permission") {
		return types.SopGateHuman
	}
	return ""
}

// ZcodeAdapter 是 zcode 的适配器（标准 stream-json，与 claude 同形）。
type ZcodeAdapter struct{}

// Kind 返回 CLI 种类。
func (ZcodeAdapter) Kind() string { return "zcode" }

// BinaryName 返回可执行文件名。
func (ZcodeAdapter) BinaryName() string { return "zcode" }

// PreferStdin：zcode 的 prompt 走 --prompt 参数。
func (ZcodeAdapter) PreferStdin() bool { return false }

// SystemPromptMode：zcode 无系统提示文件参数，规则包进正文。
func (ZcodeAdapter) SystemPromptMode() string { return SystemPromptWrap }

// zcodeDisallowedTools 是平台侧禁止 zcode 调用的危险工具（无人值守下不允许
// 自己开交互式终端/改权限）。
var zcodeDisallowedTools = []string{"bash", "shell", "terminal"}

// BuildArgs：--prompt <p> --output-format stream-json --mode yolo --no-color
// --disallowed-tools <清单>。
func (ZcodeAdapter) BuildArgs(tb executor.TaskBook, prompt, resumeID string) []string {
	args := []string{"--output-format", "stream-json", "--mode", "yolo", "--no-color"}
	if p := strings.TrimSpace(prompt); p != "" {
		args = append(args, "--prompt", p)
	}
	if id := strings.TrimSpace(resumeID); id != "" {
		args = append(args, "--resume", id)
	}
	if model := strings.TrimSpace(tb.Model); model != "" {
		args = append(args, "--model", model)
	}
	if len(zcodeDisallowedTools) > 0 {
		args = append(args, "--disallowed-tools", strings.Join(zcodeDisallowedTools, ","))
	}
	return args
}

// ConfigFiles：T27 交付的 RenderZcodeConfig（只留变量引用）。
func (ZcodeAdapter) ConfigFiles(tb executor.TaskBook) map[string]string {
	return map[string]string{".zcode/cli/config.json": RenderZcodeConfig(tb.Credential)}
}

// Env：凭据走环境变量。
func (ZcodeAdapter) Env(tb executor.TaskBook) map[string]string {
	return CredentialEnv(tb.Credential)
}

// ParseLine：标准 stream-json，直接复用 claude 的翻译（同形协议）。
func (a ZcodeAdapter) ParseLine(line string) ([]executor.StreamEvent, bool) {
	return ClaudeAdapter{}.ParseLine(line)
}

// ResumeID：result 帧 session_id。
func (a ZcodeAdapter) ResumeID(events []executor.StreamEvent) string {
	return ClaudeAdapter{}.ResumeID(events)
}

// FailureHint：轮次用尽转人工。
func (a ZcodeAdapter) FailureHint(stderr string) string {
	return ClaudeAdapter{}.FailureHint(stderr)
}
