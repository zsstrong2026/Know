package cli

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// ClaudeAdapter 是 claude（Claude Code 无头模式）适配器——也是 10 种 CLI 的基准实现：
// 其它适配器照它的形状写，差异只在参数与输出解析。
//
// 输出协议：`--output-format stream-json` 的 JSONL，关键帧 type = system/assistant/result。
type ClaudeAdapter struct{}

// Kind 返回 CLI 种类。
func (ClaudeAdapter) Kind() string { return "claude" }

// BinaryName 返回可执行文件名。
func (ClaudeAdapter) BinaryName() string { return "claude" }

// PreferStdin：claude 支持从标准输入读 prompt（大 prompt 走 stdin，避开命令行上限）。
func (ClaudeAdapter) PreferStdin() bool { return true }

// SystemPromptMode：claude 没有「指定系统提示文件」的参数，只能把规则包进正文。
func (ClaudeAdapter) SystemPromptMode() string { return SystemPromptWrap }

// BuildArgs 组装无头参数。
//
// `-p` 是 headless 入口；stream-json 需要同时给 `--verbose`。prompt 走 stdin 时
// argv 不再带正文（否则同一段文字被读两遍）。
func (ClaudeAdapter) BuildArgs(tb executor.TaskBook, prompt, resumeID string) []string {
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
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

// ConfigFiles：settings.json 里只留变量引用（磁盘上永远没有明文密钥）。
func (ClaudeAdapter) ConfigFiles(_ executor.TaskBook) map[string]string {
	return map[string]string{
		".claude/settings.json": "{\n  \"env\": {\n    \"ANTHROPIC_API_KEY\": \"${SOP_CLI_API_KEY}\"\n  }\n}\n",
	}
}

// Env：claude 属「环境变量类」，直接把凭据映射成 ANTHROPIC_* 变量。
func (ClaudeAdapter) Env(tb executor.TaskBook) map[string]string {
	env := map[string]string{}
	if tb.Credential == nil {
		return env
	}
	if key := strings.TrimSpace(tb.Credential.APIKey); key != "" {
		env["ANTHROPIC_API_KEY"] = key
	}
	if base := strings.TrimSpace(tb.Credential.BaseURL); base != "" {
		env["ANTHROPIC_BASE_URL"] = base
	}
	return env
}

// ParseLine 把一行 stream-json 解析成统一事件。
func (a ClaudeAdapter) ParseLine(line string) ([]executor.StreamEvent, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return nil, false
	}
	var frame struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
		Session string `json:"session_id"`
		Usage   *struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
		TotalCostUSD float64 `json:"total_cost_usd"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		return nil, false
	}
	now := time.Now()
	switch frame.Type {
	case "assistant":
		var events []executor.StreamEvent
		for _, block := range frame.Message.Content {
			switch block.Type {
			case "text":
				if block.Text != "" {
					events = append(events, executor.StreamEvent{
						Type: executor.StreamEventTextDelta, Text: block.Text, At: now,
					})
				}
			case "tool_use":
				events = append(events, executor.StreamEvent{
					Type: executor.StreamEventToolCall, ToolName: block.Name,
					Payload: block.Input, At: now,
				})
			}
		}
		return events, false
	case "user":
		// 工具结果帧：把结果回灌给前端时间线。
		var events []executor.StreamEvent
		for _, block := range frame.Message.Content {
			if block.Type == "tool_result" {
				events = append(events, executor.StreamEvent{
					Type: executor.StreamEventToolResult, Text: block.Text, At: now,
				})
			}
		}
		return events, false
	case "result":
		status := "success"
		reason := ""
		if frame.IsError || strings.EqualFold(frame.Subtype, "error") {
			status = "error"
			reason = strings.TrimSpace(frame.Result)
		}
		payload := map[string]any{"status": status, "reason": reason, "session_id": frame.Session}
		if frame.Usage != nil {
			payload["usage"] = map[string]any{
				"input_tokens": frame.Usage.InputTokens, "output_tokens": frame.Usage.OutputTokens,
			}
		}
		if frame.TotalCostUSD > 0 {
			payload["total_cost_usd"] = frame.TotalCostUSD
		}
		events := []executor.StreamEvent{{
			Type: executor.StreamEventResult, Text: frame.Result,
			Payload: marshalPayload(payload), At: now,
		}}
		if frame.Result != "" {
			// 终态帧的 result 是整段最终答案：前面若只有片段，正文可能缺尾，这里补一次。
			events = append([]executor.StreamEvent{{
				Type: executor.StreamEventTextDelta, Text: "\n" + frame.Result, At: now,
			}}, events...)
		}
		return events, true
	default:
		// system(init) 等帧不产生用户可见事件。
		return nil, false
	}
}

// ResumeID 从帧里取 session_id（result 帧优先，其次 init 帧的 system_id）。
func (a ClaudeAdapter) ResumeID(events []executor.StreamEvent) string {
	for _, ev := range events {
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

// FailureHint：轮次/预算用尽这类「重跑也没用」的失败要转人工。
func (a ClaudeAdapter) FailureHint(stderr string) string {
	if containsAnyFold(stderr, "max turns", "turn limit", "maximum number of turns") {
		return types.SopGateHuman
	}
	return ""
}
