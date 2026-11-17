package insight

import (
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// 状态标志必须把每个已知状态恰好归入「进行中」或「已结束」之一。
func TestStatusFlagsPartition(t *testing.T) {
	all := []string{
		types.InsightStatusPending,
		types.InsightStatusStage1,
		types.InsightStatusWaitingConfirm,
		types.InsightStatusStage2,
		types.InsightStatusDone,
		types.InsightStatusFailed,
		types.InsightStatusCancelled,
	}
	for _, status := range all {
		running, terminal := isRunningStatus(status), isTerminalStatus(status)
		if running == terminal {
			t.Fatalf("status %q: running=%v terminal=%v, want exactly one", status, running, terminal)
		}
	}
	if isRunningStatus("unknown") || isTerminalStatus("unknown") {
		t.Fatal("unknown status must be neither running nor terminal")
	}
}

// 小样固定取最近 20 篇；全量按 max_docs 截断，且不超过上限。
func TestSelectAnalyzedDocsLimits(t *testing.T) {
	docs := make([]*types.Knowledge, 0, 25)
	base := time.Now()
	for i := 0; i < 25; i++ {
		docs = append(docs, &types.Knowledge{ID: string(rune('a' + i)), UpdatedAt: base.Add(time.Duration(i) * time.Hour)})
	}

	sample := selectAnalyzedDocs(docs, types.StartInsightRunRequest{Sample: true})
	if len(sample) != types.InsightSampleDocLimit {
		t.Fatalf("sample size = %d, want %d", len(sample), types.InsightSampleDocLimit)
	}
	if sample[0].ID != "y" {
		t.Fatalf("sample should start from the newest doc, got %q", sample[0].ID)
	}

	full := selectAnalyzedDocs(docs, types.StartInsightRunRequest{MaxDocs: 300})
	if len(full) != len(docs) {
		t.Fatalf("full size = %d, want %d", len(full), len(docs))
	}

	// 未显式给 max_docs 时按全量处理，而不是退回 20 篇小样。
	unspecified := selectAnalyzedDocs(docs, types.StartInsightRunRequest{})
	if len(unspecified) != len(docs) {
		t.Fatalf("unspecified max_docs size = %d, want %d", len(unspecified), len(docs))
	}

	// max_docs 超过上限时按上限截断，而不是直接使用请求值。
	many := make([]*types.Knowledge, 0, types.InsightMaxDocLimit+5)
	for i := 0; i < types.InsightMaxDocLimit+5; i++ {
		many = append(many, &types.Knowledge{ID: "k", UpdatedAt: base})
	}
	capped := selectAnalyzedDocs(many, types.StartInsightRunRequest{MaxDocs: types.InsightMaxDocLimit + 100})
	if len(capped) != types.InsightMaxDocLimit {
		t.Fatalf("capped size = %d, want %d", len(capped), types.InsightMaxDocLimit)
	}
}

// 维度名称去空白、scale 缺省补 0-100，数量限制在 1–8。
func TestNormalizeDimensions(t *testing.T) {
	if _, err := normalizeDimensions(nil); err == nil {
		t.Fatal("empty dimensions should be rejected")
	}
	if _, err := normalizeDimensions(make([]types.InsightDimension, 9)); err == nil {
		t.Fatal("more than 8 dimensions should be rejected")
	}
	longName := strings.Repeat("维", types.InsightDimensionMaxLen+1)
	if _, err := normalizeDimensions([]types.InsightDimension{{Name: longName}}); err == nil {
		t.Fatal("too long dimension name should be rejected")
	}

	got, err := normalizeDimensions([]types.InsightDimension{{Name: "  决策质量 ", Criteria: " 是否有备选方案 "}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0].Name != "决策质量" || got[0].Scale != "0-100" || got[0].Criteria != "是否有备选方案" {
		t.Fatalf("unexpected normalized dimension: %+v", got[0])
	}
}

// 截断必须按字符而不是字节，避免汉字被切成乱码。
func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("你好世界", 2); got != "你好" {
		t.Fatalf("got %q, want %q", got, "你好")
	}
	if got := truncateRunes("你好", 0); got != "你好" {
		t.Fatalf("limit 0 should keep original, got %q", got)
	}
}

// 模型返回值可能是任意字符串，类型与置信度必须收敛到固定枚举。
func TestNormalizeEvidenceEnums(t *testing.T) {
	cases := map[string]string{
		" evidence ": "evidence",
		"EVIDENCE":   "evidence",
		"inference":  "inference",
		"猜测":         "inference",
		"":           "inference",
	}
	for input, want := range cases {
		if got := normalizeEvidenceType(input); got != want {
			t.Fatalf("normalizeEvidenceType(%q) = %q, want %q", input, got, want)
		}
	}
	for input, want := range map[string]string{" 高 ": "高", "低": "低", "中": "中", "high": "中"} {
		if got := normalizeConfidence(input); got != want {
			t.Fatalf("normalizeConfidence(%q) = %q, want %q", input, got, want)
		}
	}
}

// 图片与音视频按类型或后缀都应被识别为附件。
func TestIsAttachment(t *testing.T) {
	if !isAttachment(&types.Knowledge{FileType: "image"}) {
		t.Fatal("image file type should be an attachment")
	}
	if !isAttachment(&types.Knowledge{FileName: "会议录音.MP3"}) {
		t.Fatal("audio extension should be an attachment")
	}
	if isAttachment(&types.Knowledge{FileName: "季度复盘.pdf"}) {
		t.Fatal("pdf should not be treated as an attachment")
	}
}
