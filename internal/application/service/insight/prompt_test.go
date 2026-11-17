package insight

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

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
