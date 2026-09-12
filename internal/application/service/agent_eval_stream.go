package service

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

type sseEvent struct {
	ResponseType        string          `json:"response_type"`
	Content             string          `json:"content"`
	KnowledgeReferences []sseReference  `json:"knowledge_references"`
	ToolCalls           []sseToolCall   `json:"tool_calls"`
	Data                json.RawMessage `json:"data"`
	Raw                 []byte          `json:"-"`
}

type sseReference struct {
	Content string `json:"content"`
}

type sseToolCall struct {
	ToolName   string `json:"tool_name"`
	ToolCallID string `json:"tool_call_id"`
}

type agentEvalStreamCollector struct {
	Answer         string
	ReferenceCount int
	ToolCalls      []types.AgentEvalToolCall
	EndedNormally  bool
	ErrorMessage   string
	BlockedBy      string
}

func parseSSEData(line string) (sseEvent, bool) {
	if !strings.HasPrefix(line, "data:") {
		return sseEvent{}, false
	}
	chunk := strings.TrimSpace(line[len("data:"):])
	if chunk == "" || chunk == "[DONE]" {
		return sseEvent{}, false
	}
	var evt sseEvent
	if err := json.Unmarshal([]byte(chunk), &evt); err != nil {
		return sseEvent{}, false
	}
	evt.Raw = []byte(chunk)
	return evt, true
}

func (c *agentEvalStreamCollector) Apply(evt sseEvent) bool {
	switch evt.ResponseType {
	case "answer":
		c.Answer += evt.Content
	case "references":
		for _, ref := range evt.KnowledgeReferences {
			if ref.Content != "" {
				c.ReferenceCount++
			}
		}
	case "tool_call":
		var nested sseToolCall
		if len(evt.Data) > 0 && json.Unmarshal(evt.Data, &nested) == nil && nested.ToolName != "" {
			c.ToolCalls = append(c.ToolCalls, types.AgentEvalToolCall{ToolName: nested.ToolName, ToolCallID: nested.ToolCallID})
		}
		for _, call := range evt.ToolCalls {
			if call.ToolName == "" {
				continue
			}
			c.ToolCalls = append(c.ToolCalls, types.AgentEvalToolCall{ToolName: call.ToolName, ToolCallID: call.ToolCallID})
		}
	case "error":
		if evt.Content != "" {
			c.ErrorMessage = evt.Content
		} else {
			c.ErrorMessage = truncateRunes(string(evt.Raw), 500)
		}
		return true
	case "tool_approval_required", "mcp_oauth_required":
		c.BlockedBy = evt.ResponseType
		return true
	case "complete":
		c.EndedNormally = true
		return true
	}
	return false
}

func (c *agentEvalStreamCollector) Observation() agentEvalObservation {
	names := make([]string, 0, len(c.ToolCalls))
	for _, call := range c.ToolCalls {
		names = append(names, call.ToolName)
	}
	return agentEvalObservation{
		Answer:         c.Answer,
		ReferenceCount: c.ReferenceCount,
		ToolNames:      names,
		EndedNormally:  c.EndedNormally,
		ErrorMessage:   c.ErrorMessage,
	}
}

func consumeAgentEvalSSE(r io.Reader, collector *agentEvalStreamCollector) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		evt, ok := parseSSEData(scanner.Text())
		if !ok {
			continue
		}
		if collector.Apply(evt) {
			return nil
		}
	}
	return scanner.Err()
}

func parseJudgeOutput(text string) (float64, string, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return 0, "", errors.New("judge output contains no JSON object")
	}
	var out struct {
		Score  float64 `json:"score"`
		Reason string  `json:"reason"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &out); err != nil {
		return 0, "", err
	}
	return out.Score, out.Reason, nil
}
