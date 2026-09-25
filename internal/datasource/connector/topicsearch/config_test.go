package topicsearch

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseConfigRequiresTopics(t *testing.T) {
	if _, err := parseConfig(makeConfig(map[string]interface{}{})); err == nil {
		t.Fatal("parseConfig() error = nil, want error for missing topics")
	}
	if _, err := parseConfig(makeConfig(map[string]interface{}{"topics": " , \n "})); err == nil {
		t.Fatal("parseConfig() error = nil, want error for blank topics")
	}
}

func TestParseConfigClampsOutOfRange(t *testing.T) {
	// 31 distinct topics: duplicate values would be deduped before the cap applies.
	distinct := make([]string, 0, 31)
	for i := 0; i < 31; i++ {
		distinct = append(distinct, fmt.Sprintf("topic-%d", i))
	}
	cfg, err := parseConfig(makeConfig(map[string]interface{}{
		"topics":                strings.Join(distinct, ","),
		"max_results_per_topic": 999,
		"max_items_per_run":     999,
		"min_content_length":    -5,
		"result_recency_days":   9999,
	}))
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if len(cfg.Topics) != maxTopics {
		t.Fatalf("topics = %d, want %d", len(cfg.Topics), maxTopics)
	}
	if cfg.MaxResultsPerTopic != maxResultsPerTopicCap {
		t.Fatalf("MaxResultsPerTopic = %d, want %d", cfg.MaxResultsPerTopic, maxResultsPerTopicCap)
	}
	if cfg.MaxItemsPerRun != maxItemsPerRunCap {
		t.Fatalf("MaxItemsPerRun = %d, want %d", cfg.MaxItemsPerRun, maxItemsPerRunCap)
	}
	if cfg.MinContentLength != 0 {
		t.Fatalf("MinContentLength = %d, want 0", cfg.MinContentLength)
	}
	if cfg.ResultRecencyDays != maxRecencyDays {
		t.Fatalf("ResultRecencyDays = %d, want %d", cfg.ResultRecencyDays, maxRecencyDays)
	}
}

func TestParseConfigDefaultsAndStringNumbers(t *testing.T) {
	cfg, err := parseConfig(makeConfig(map[string]interface{}{
		"topics":            "RAG, 知识库\nRAG", // deduped to 2 topics
		"max_items_per_run": "3",             // UI forms may send strings
	}))
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if len(cfg.Topics) != 2 || cfg.Topics[0] != "RAG" || cfg.Topics[1] != "知识库" {
		t.Fatalf("Topics = %v", cfg.Topics)
	}
	if cfg.MaxItemsPerRun != 3 {
		t.Fatalf("MaxItemsPerRun = %d, want 3", cfg.MaxItemsPerRun)
	}
	if cfg.MaxResultsPerTopic != defaultMaxResultsPerTopic {
		t.Fatalf("MaxResultsPerTopic = %d, want default %d", cfg.MaxResultsPerTopic, defaultMaxResultsPerTopic)
	}
	if cfg.MinContentLength != defaultMinContentLength {
		t.Fatalf("MinContentLength = %d, want default %d", cfg.MinContentLength, defaultMinContentLength)
	}
}

func TestParseConfigAcceptsStringArraySettings(t *testing.T) {
	cfg, err := parseConfig(makeConfig(map[string]interface{}{
		"topics":    []interface{}{"RAG", "  ", "知识库"},
		"blacklist": []interface{}{"*://*.spam.com/*"},
	}))
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if len(cfg.Topics) != 2 || len(cfg.Blacklist) != 1 {
		t.Fatalf("Topics = %v, Blacklist = %v", cfg.Topics, cfg.Blacklist)
	}
}

func TestSearchConfigCarriesBlacklistAndRecency(t *testing.T) {
	cfg, err := parseConfig(makeConfig(map[string]interface{}{
		"topics":                "RAG",
		"blacklist":             "*://*.spam.com/*",
		"result_recency_days":   7,
		"max_results_per_topic": 3,
	}))
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	sc := cfg.searchConfig()
	if sc.MaxResults != 3 || !sc.IncludeDate || len(sc.Blacklist) != 1 {
		t.Fatalf("searchConfig() = %+v", sc)
	}

	plain, err := parseConfig(makeConfig(map[string]interface{}{"topics": "RAG"}))
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if plain.searchConfig().IncludeDate {
		t.Fatal("IncludeDate = true, want false when the recency filter is off")
	}
}

func TestTopicsForSelection(t *testing.T) {
	cfg := &Config{Topics: []string{"A", "B", "C"}}
	if got := cfg.topicsFor(nil); len(got) != 3 {
		t.Fatalf("topicsFor(nil) = %v, want all topics", got)
	}
	if got := cfg.topicsFor([]string{"C"}); len(got) != 1 || got[0] != "C" {
		t.Fatalf("topicsFor([C]) = %v", got)
	}
	if got := cfg.topicsFor([]string{"X"}); len(got) != 0 {
		t.Fatalf("topicsFor([X]) = %v, want empty", got)
	}
}

func TestNormalizeURLStableKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://Example.com/Post?b=2&a=1", "https://example.com/Post?a=1&b=2"},
		{"https://example.com/Post?a=1&b=2#section-2", "https://example.com/Post?a=1&b=2"},
		{"https://example.com/Post?utm_source=x&utm_medium=y&keep=1", "https://example.com/Post?keep=1"},
		{"https://example.com/Post?fbclid=abc&gclid=def", "https://example.com/Post"},
		{"http://example.com/", "http://example.com/"},
	}
	for _, tc := range cases {
		got, err := normalizeURL(tc.in)
		if err != nil {
			t.Fatalf("normalizeURL(%q) error = %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("normalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeURLRejectsNonHTTP(t *testing.T) {
	for _, raw := range []string{"", "   ", "ftp://example.com/a", "javascript:alert(1)", "/relative/path"} {
		if got, err := normalizeURL(raw); err == nil {
			t.Fatalf("normalizeURL(%q) = %q, want error", raw, got)
		}
	}
}

func TestFingerprintsAreStableAndDistinct(t *testing.T) {
	if contentFingerprint("abc") != contentFingerprint("abc") {
		t.Fatal("contentFingerprint is not stable")
	}
	if contentFingerprint("abc") == contentFingerprint("abd") {
		t.Fatal("contentFingerprint collides for different content")
	}
	a := makeResult("标题", "https://example.com/a", "摘要")
	b := makeResult("标题", "https://example.com/a", "摘要")
	if signalFingerprint(a) != signalFingerprint(b) {
		t.Fatal("signalFingerprint is not stable")
	}
	c := makeResult("标题", "https://example.com/a", "摘要变了")
	if signalFingerprint(a) == signalFingerprint(c) {
		t.Fatal("signalFingerprint should change when the snippet changes")
	}
}

func TestWithinRecency(t *testing.T) {
	now := time.Now().UTC()
	cfg := &Config{ResultRecencyDays: 7}
	old := now.AddDate(0, 0, -30)
	recent := now.AddDate(0, 0, -1)
	if cfg.withinRecency(&old, now) {
		t.Fatal("old result should be filtered out")
	}
	if !cfg.withinRecency(&recent, now) {
		t.Fatal("recent result should be kept")
	}
	if !cfg.withinRecency(nil, now) {
		t.Fatal("result without a publish date should be kept")
	}
	unlimited := &Config{}
	if !unlimited.withinRecency(&old, now) {
		t.Fatal("recency filter off should keep everything")
	}
}

func TestSanitizeFileName(t *testing.T) {
	if got := sanitizeFileName("   "); got != "untitled" {
		t.Fatalf("sanitizeFileName(blank) = %q", got)
	}
	got := sanitizeFileName(`a/b\c:d*e?f"g<h>i|j`)
	if strings.ContainsAny(got, `/\:*?"<>|`) {
		t.Fatalf("sanitizeFileName left invalid chars: %q", got)
	}
	long := strings.Repeat("字", 400)
	if len(sanitizeFileName(long)) > maxFileNameBytes {
		t.Fatal("sanitizeFileName did not truncate by byte budget")
	}
}
