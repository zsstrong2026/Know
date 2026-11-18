package insight

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// fakeChat 按脚本顺序返回响应，并记录每次调用的参数。
type fakeChat struct {
	responses []*types.ChatResponse
	opts      []*chat.ChatOptions
}

func (f *fakeChat) Chat(
	_ context.Context, _ []chat.Message, opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	f.opts = append(f.opts, opts)
	if len(f.responses) == 0 {
		return nil, errors.New("no scripted response")
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func (f *fakeChat) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not used")
}

func (f *fakeChat) GetModelName() string { return "fake" }
func (f *fakeChat) GetModelID() string   { return "fake" }

// 输出被 token 上限截断时必须换一条“只输出完整 JSON”的指令重试，而不是直接失败。
func TestCallChatJSONRetriesTruncatedOutput(t *testing.T) {
	model := &fakeChat{responses: []*types.ChatResponse{
		{Content: `{"conclusion":"写到一半`, FinishReason: "length"},
		{Content: `{"conclusion":"完整"}`, FinishReason: "stop"},
	}}
	var out reportOutput
	if err := callChatJSON(context.Background(), model, nil, &out); err != nil {
		t.Fatalf("truncated output should be retried, got error: %v", err)
	}
	if out.Conclusion != "完整" {
		t.Fatalf("conclusion = %q, want 完整", out.Conclusion)
	}
	if len(model.opts) != 2 {
		t.Fatalf("chat calls = %d, want 2", len(model.opts))
	}
	if model.opts[0].MaxCompletionTokens != jsonCompletionBudget {
		t.Fatalf("json budget = %d, want %d", model.opts[0].MaxCompletionTokens, jsonCompletionBudget)
	}
}

// 两次都被截断时要返回可识别的错误，供上层给出可重试的提示。
func TestCallChatJSONReportsTruncation(t *testing.T) {
	model := &fakeChat{responses: []*types.ChatResponse{
		{Content: `{"conclusion":"写到一半`, FinishReason: "length"},
		{Content: `{"conclusion":"还是写`, FinishReason: "length"},
	}}
	var out reportOutput
	err := callChatJSON(context.Background(), model, nil, &out)
	if !errors.Is(err, errInsightOutputTruncated) {
		t.Fatalf("want truncation error, got %v", err)
	}
}

// 追问的上下文必须严格限定为「这份报告 + 未排除的证据」，不能把被剔除的证据带进去。
func TestBuildReportAskMessagesScope(t *testing.T) {
	report := &types.InsightReport{
		Title:       "思维模式洞察报告",
		ContentJSON: types.JSON(`{"conclusion":"用行动换确定性"}`),
	}
	theme := &types.InsightTheme{Name: "思维模式"}
	evidence := []*types.InsightEvidence{
		{
			KnowledgeTitle: "Q3 复盘",
			ChunkIndex:     12,
			Dimension:      "决策偏好",
			EvidenceType:   "evidence",
			Quote:          "先把最小闭环跑出来",
		},
		{
			KnowledgeTitle: "已排除文档",
			ChunkIndex:     3,
			Dimension:      "决策偏好",
			EvidenceType:   "evidence",
			Quote:          "这条应该被过滤",
			Excluded:       true,
		},
	}
	history := []*types.InsightReportMessage{
		{Role: types.InsightMessageRoleUser, Content: "这条结论可靠吗？"},
		{Role: types.InsightMessageRoleAssistant, Content: "置信度中等。"},
	}

	messages := buildReportAskMessages(report, theme, evidence, history, "有反例吗？")
	if len(messages) != 4 {
		t.Fatalf("want 4 messages (system + 2 history + question), got %d", len(messages))
	}
	system := messages[0].Content
	if !strings.Contains(system, "先把最小闭环跑出来") {
		t.Fatal("system prompt should contain included evidence")
	}
	if strings.Contains(system, "这条应该被过滤") {
		t.Fatal("system prompt must not contain excluded evidence")
	}
	if !strings.Contains(system, "用行动换确定性") {
		t.Fatal("system prompt should contain report content")
	}
	if messages[1].Role != "user" || messages[2].Role != "assistant" {
		t.Fatalf("history roles mismatch: %s, %s", messages[1].Role, messages[2].Role)
	}
	if messages[3].Content != "有反例吗？" {
		t.Fatalf("last message should be the question, got %q", messages[3].Content)
	}
}

// 自我洞悉追问只能看到「当前说明书 + 主题报告摘要」，不得夹带未列出的内容。
func TestBuildSelfAskMessagesScope(t *testing.T) {
	content := types.JSON(`{"portrait":"重视确定性"}`)
	digests := []selfReportDigest{
		{ThemeName: "价值观", Title: "价值观洞察报告", CoverageNote: "覆盖 24 篇", Conclusion: "安全优先于机会"},
	}
	history := []*types.InsightSelfMessage{
		{Role: types.InsightMessageRoleUser, Content: "这个判断有依据吗？"},
		{Role: types.InsightMessageRoleAssistant, Content: "来自价值观主题的报告。"},
	}

	messages := buildSelfAskMessages(content, digests, history, "压力下会变吗？")
	if len(messages) != 4 {
		t.Fatalf("want 4 messages (system + 2 history + question), got %d", len(messages))
	}
	system := messages[0].Content
	if !strings.Contains(system, "重视确定性") || !strings.Contains(system, "安全优先于机会") {
		t.Fatal("system prompt should contain self content and report digest")
	}
	if messages[1].Role != "user" || messages[2].Role != "assistant" {
		t.Fatalf("history roles mismatch: %s, %s", messages[1].Role, messages[2].Role)
	}
	if messages[3].Content != "压力下会变吗？" {
		t.Fatalf("last message should be the question, got %q", messages[3].Content)
	}
}
