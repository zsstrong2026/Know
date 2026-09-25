// Package topicsearch implements the topic-based web search data source connector.
//
// It searches the public web for user-defined topics, fetches the result pages,
// and ingests their text into a knowledge base: every topic is a selectable
// resource and every page is a knowledge entry whose external_id is the
// normalized URL, so re-running a sync skips pages whose body is unchanged and
// updates the ones that changed.
//
// Change detection has two levels (mirroring the RSS connector):
//   - search-result visible fields (title + snippet + published time) unchanged
//     → the page is not fetched at all;
//   - otherwise the page is fetched and its body hash is compared.
//
// Outbound requests go through the injected fetcher (web_fetch, SSRF-safe);
// blacklist filtering is owned by the injected WebSearchService, so the two
// implementations cannot drift apart.
package topicsearch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// Defaults and caps. Out-of-range numbers are clamped instead of rejected so a
// configuration edit can never make every scheduled sync fail.
const (
	defaultMaxResultsPerTopic = 10
	defaultMaxItemsPerRun     = 5
	defaultMinContentLength   = 200

	maxTopics             = 20
	maxResultsPerTopicCap = 50
	maxItemsPerRunCap     = 50
	maxRecencyDays        = 365
	maxBlacklistRules     = 200
	maxFileNameBytes      = 200
)

// trackingParams lists the common tracking parameters dropped from a normalized
// URL. Business parameters are always kept.
var trackingParams = map[string]struct{}{
	"gclid": {}, "fbclid": {}, "msclkid": {}, "yclid": {}, "spm": {}, "scm": {}, "_ga": {},
}

// Config is the topic_search configuration. Everything here is non-secret and
// lives in DataSourceConfig.Settings; the connector stores no credentials.
type Config struct {
	Topics             []string
	ProviderID         string
	MaxResultsPerTopic int
	MaxItemsPerRun     int
	MinContentLength   int
	ResultRecencyDays  int
	Blacklist          []string
}

// parseConfig parses and clamps the connector configuration. An empty topic
// list is an invalid configuration.
func parseConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	settings := config.Settings
	cfg := &Config{
		Topics:             settingStringList(settings, "topics"),
		ProviderID:         settingString(settings, "provider_id"),
		MaxResultsPerTopic: settingInt(settings, "max_results_per_topic", defaultMaxResultsPerTopic),
		MaxItemsPerRun:     settingInt(settings, "max_items_per_run", defaultMaxItemsPerRun),
		MinContentLength:   settingInt(settings, "min_content_length", defaultMinContentLength),
		ResultRecencyDays:  settingInt(settings, "result_recency_days", 0),
		Blacklist:          settingStringList(settings, "blacklist"),
	}
	cfg.normalize()
	if len(cfg.Topics) == 0 {
		return nil, fmt.Errorf("%w: topics is required", datasource.ErrInvalidConfig)
	}
	return cfg, nil
}

// normalize clamps numbers and caps the list fields.
func (c *Config) normalize() {
	c.Topics = capList(c.Topics, maxTopics)
	c.Blacklist = capList(c.Blacklist, maxBlacklistRules)
	c.MaxResultsPerTopic = clamp(c.MaxResultsPerTopic, 1, maxResultsPerTopicCap)
	c.MaxItemsPerRun = clamp(c.MaxItemsPerRun, 1, maxItemsPerRunCap)
	if c.MinContentLength < 0 {
		c.MinContentLength = 0
	}
	c.ResultRecencyDays = clamp(c.ResultRecencyDays, 0, maxRecencyDays)
}

// searchConfig builds the parameters handed to WebSearchService. IncludeDate is
// only enabled when a recency window is configured, so providers are not asked
// for date metadata unnecessarily.
func (c *Config) searchConfig() *types.WebSearchConfig {
	return &types.WebSearchConfig{
		MaxResults:  c.MaxResultsPerTopic,
		IncludeDate: c.ResultRecencyDays > 0,
		Blacklist:   c.Blacklist,
	}
}

// topicsFor returns the topics to sync this run: an empty resourceIDs means all
// topics, otherwise the intersection in configured order.
func (c *Config) topicsFor(resourceIDs []string) []string {
	if len(resourceIDs) == 0 {
		return c.Topics
	}
	selected := make(map[string]struct{}, len(resourceIDs))
	for _, id := range resourceIDs {
		if id = strings.TrimSpace(id); id != "" {
			selected[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(c.Topics))
	for _, topic := range c.Topics {
		if _, ok := selected[topic]; ok {
			out = append(out, topic)
		}
	}
	return out
}

// withinRecency filters search results by the configured freshness window.
// A disabled window or a result without a publish time is always kept.
func (c *Config) withinRecency(publishedAt *time.Time, now time.Time) bool {
	if c.ResultRecencyDays <= 0 || publishedAt == nil || publishedAt.IsZero() {
		return true
	}
	return publishedAt.After(now.AddDate(0, 0, -c.ResultRecencyDays))
}

// contentFingerprint hashes page text for incremental change detection.
func contentFingerprint(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "h:" + hex.EncodeToString(sum[:])[:16]
}

// signalFingerprint hashes the search-result visible fields: when it is
// unchanged the page does not need to be fetched at all.
func signalFingerprint(result *types.WebSearchResult) string {
	if result == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(result.Title))
	b.WriteByte('\n')
	b.WriteString(strings.TrimSpace(result.Snippet))
	b.WriteByte('\n')
	if result.PublishedAt != nil && !result.PublishedAt.IsZero() {
		b.WriteString(result.PublishedAt.UTC().Format(time.RFC3339))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "s:" + hex.EncodeToString(sum[:])[:16]
}

// normalizeURL produces the stable key used for deduplication: lower-cased
// scheme/host, no fragment, tracking parameters dropped, query sorted by key.
// Non-http(s) URLs and URLs without a host are rejected.
func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("missing host")
	}
	u.Scheme = scheme
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	u.RawQuery = normalizeQuery(u.Query())
	return u.String(), nil
}

// normalizeQuery drops tracking parameters. url.Values.Encode sorts by key, so
// the result is stable regardless of the original parameter order.
func normalizeQuery(values url.Values) string {
	filtered := make(url.Values, len(values))
	for key, v := range values {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") {
			continue
		}
		if _, ok := trackingParams[lower]; ok {
			continue
		}
		filtered[key] = v
	}
	return filtered.Encode()
}

// sanitizeFileName strips characters that are invalid in file names and
// truncates at a UTF-8 boundary (mirrors the RSS connector).
func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "untitled"
	}
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
		"\n", " ", "\r", " ", "\t", " ",
	)
	result := strings.TrimSpace(replacer.Replace(name))
	if result == "" {
		return "untitled"
	}
	if len(result) > maxFileNameBytes {
		result = result[:maxFileNameBytes]
		for len(result) > 0 {
			r, size := utf8.DecodeLastRuneInString(result)
			if r != utf8.RuneError || size != 1 {
				break
			}
			result = result[:len(result)-1]
		}
	}
	return result
}

// settingString reads a string setting; non-strings and missing keys yield "".
func settingString(settings map[string]interface{}, key string) string {
	if settings == nil {
		return ""
	}
	s, ok := settings[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// settingInt leniently parses a numeric setting: JSON numbers and numeric
// strings are accepted, anything else falls back to the default.
func settingInt(settings map[string]interface{}, key string, def int) int {
	if settings == nil {
		return def
	}
	switch v := settings[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return def
		}
		return parsed
	default:
		return def
	}
}

// settingStringList accepts both a newline/comma separated string and an array
// of strings, which is what UI forms and API clients respectively produce.
func settingStringList(settings map[string]interface{}, key string) []string {
	if settings == nil {
		return nil
	}
	switch v := settings[key].(type) {
	case string:
		return splitList(v)
	case []string:
		return dedupeStrings(v)
	case []interface{}:
		items := make([]string, 0, len(v))
		for _, raw := range v {
			if s, ok := raw.(string); ok {
				items = append(items, s)
			}
		}
		return dedupeStrings(items)
	default:
		return nil
	}
}

// splitList splits on newlines and commas, then dedupes while preserving order.
func splitList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ','
	})
	return dedupeStrings(fields)
}

// dedupeStrings trims, drops blanks, and dedupes while preserving first-seen order.
func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func capList(values []string, max int) []string {
	if len(values) <= max {
		return values
	}
	return values[:max]
}
