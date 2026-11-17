package insight

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// 脱敏词表要按用户输入顺序保留，去空、去重，并限制单条长度。
func TestNormalizePrivacy(t *testing.T) {
	long := strings.Repeat("长", types.InsightPrivacyMaxTermRunes+5)
	got := normalizePrivacy(&types.InsightPrivacy{
		Persons:            []string{" 张三 ", "", "张三", long},
		Organizations:      nil,
		Projects:           []string{"星河计划"},
		HideEvidenceQuotes: true,
	})

	if len(got.Persons) != 2 {
		t.Fatalf("want 2 persons after dedupe/trim, got %d", len(got.Persons))
	}
	if got.Persons[0] != "张三" {
		t.Fatalf("order should follow input, got %q", got.Persons[0])
	}
	if len([]rune(got.Persons[1])) != types.InsightPrivacyMaxTermRunes {
		t.Fatalf("term should be truncated to %d runes", types.InsightPrivacyMaxTermRunes)
	}
	if got.Organizations == nil || len(got.Organizations) != 0 {
		t.Fatal("empty groups must stay non-nil for stable JSON output")
	}
	if !got.HideEvidenceQuotes || got.Projects[0] != "星河计划" {
		t.Fatal("other fields should be preserved")
	}
}

// 未保存过时 repo 返回空结构；归一化后必须是空数组而不是 nil，
// 否则 JSON 会输出 null，前端遍历词表时会抛错。
func TestNormalizePrivacyEmpty(t *testing.T) {
	got := normalizePrivacy(&types.InsightPrivacy{})
	if got.Persons == nil || got.Organizations == nil || got.Projects == nil {
		t.Fatal("empty privacy must serialize with empty arrays instead of null")
	}
}

// 超过上限的条目直接截断，不报错。
func TestNormalizePrivacyTermLimit(t *testing.T) {
	terms := make([]string, 0, types.InsightPrivacyMaxTerms+10)
	for i := 0; i < types.InsightPrivacyMaxTerms+10; i++ {
		terms = append(terms, fmt.Sprintf("term-%d", i))
	}
	got := normalizeTerms(terms)
	if len(got) != types.InsightPrivacyMaxTerms {
		t.Fatalf("want %d terms, got %d", types.InsightPrivacyMaxTerms, len(got))
	}
}
