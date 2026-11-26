package topicsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Compile-time proof that *Connector satisfies the datasource.Connector interface.
var _ datasource.Connector = (*Connector)(nil)

// searchRunner is the connector's minimal dependency on web search
// (satisfied by interfaces.WebSearchService). 上游 0.9.1 把搜索能力收敛到
// Provider 解析后的 WebClient，这里只依赖该服务的 Resolve。
type searchRunner interface {
	Resolve(ctx context.Context, providerID string) (interfaces.WebClient, error)
}

// runSearch resolves the provider client and runs one query.
func (c *Connector) runSearch(
	ctx context.Context, providerID string, cfg *types.WebSearchConfig, query string,
) ([]*types.WebSearchResult, error) {
	client, err := c.search.Resolve(ctx, providerID)
	if err != nil {
		return nil, err
	}
	req := types.WebSearchRequest{Query: query}
	if cfg != nil {
		req.MaxResults = cfg.MaxResults
		req.IncludeDate = cfg.IncludeDate
	}
	return client.Search(ctx, req)
}

// defaultProviderResolver resolves a tenant's default search provider
// (satisfied by interfaces.WebSearchProviderRepository, which is only used for
// its GetDefault method here).
type defaultProviderResolver interface {
	GetDefault(ctx context.Context, tenantID uint64) (*types.WebSearchProviderEntity, error)
}

// pageFetcher fetches a page's readable text (satisfied by *web_fetch.Fetcher).
type pageFetcher interface {
	Fetch(ctx context.Context, rawURL string) (string, error)
}

// Connector implements datasource.Connector: it searches the public web by topic
// and writes the fetched page text into a knowledge base.
type Connector struct {
	search    searchRunner
	providers defaultProviderResolver
	fetcher   pageFetcher
}

// NewConnector creates the connector. All three dependencies are minimal
// interfaces so unit tests can inject fakes.
func NewConnector(search searchRunner, providers defaultProviderResolver, fetcher pageFetcher) *Connector {
	return &Connector{search: search, providers: providers, fetcher: fetcher}
}

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeTopicSearch }

// Validate checks the configuration and the search path: topics present →
// provider resolvable → one live search probe.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseConfig(config)
	if err != nil {
		return err
	}
	providerID, err := c.resolveProviderID(ctx, cfg)
	if err != nil {
		return err
	}
	if _, err := c.runSearch(ctx, providerID, cfg.searchConfig(), cfg.Topics[0]); err != nil {
		return fmt.Errorf("web search probe failed: %w", err)
	}
	return nil
}

// ListResources exposes every configured topic as a selectable resource.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	if parentID != "" {
		return []types.Resource{}, nil
	}
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	out := make([]types.Resource, 0, len(cfg.Topics))
	for _, topic := range cfg.Topics {
		out = append(out, types.Resource{
			ExternalID:  topic,
			Type:        "topic",
			Name:        topic,
			Description: "按该主题搜索公网并入库相关网页",
		})
	}
	return out, nil
}

// ResolveResourceAncestors has nothing to do: topics are a flat list.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	return []string{}, nil
}

// resolveProviderID picks the search provider for this run: an explicitly
// configured one wins, otherwise the tenant's default.
func (c *Connector) resolveProviderID(ctx context.Context, cfg *Config) (string, error) {
	if cfg.ProviderID != "" {
		return cfg.ProviderID, nil
	}
	if c.providers == nil {
		return "", fmt.Errorf("%w: 缺少默认 Provider 解析器", datasource.ErrInvalidConfig)
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("%w: 上下文缺少租户信息，无法解析默认 Web 搜索 Provider", datasource.ErrInvalidConfig)
	}
	entity, err := c.providers.GetDefault(ctx, tenantID)
	if err != nil {
		return "", fmt.Errorf("resolve default web search provider: %w", err)
	}
	if entity == nil || strings.TrimSpace(entity.ID) == "" {
		return "", fmt.Errorf(
			"%w: 未配置默认 Web 搜索 Provider，请在设置中将其设为默认，或在本数据源指定 provider_id",
			datasource.ErrInvalidConfig,
		)
	}
	return entity.ID, nil
}

// topicCursor is this connector's incremental cursor: pages keyed by normalized URL.
type topicCursor struct {
	LastSyncTime time.Time              `json:"last_sync_time"`
	Pages        map[string]cursorEntry `json:"pages,omitempty"`
}

// cursorEntry records the change-detection state of one page.
type cursorEntry struct {
	Signal string `json:"signal,omitempty"`
	Hash   string `json:"hash,omitempty"`
	Topic  string `json:"topic,omitempty"`
	At     string `json:"at,omitempty"`
}

// decodeCursor restores this connector's cursor from the generic one.
// An unparseable cursor is treated as a full sync (returns nil).
func decodeCursor(ctx context.Context, cursor *types.SyncCursor) *topicCursor {
	if cursor == nil || cursor.ConnectorCursor == nil {
		return nil
	}
	raw, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil {
		logger.Warnf(ctx, "[TopicSearch] marshal connector cursor: %v", err)
		return nil
	}
	var prev topicCursor
	if err := json.Unmarshal(raw, &prev); err != nil {
		logger.Warnf(ctx, "[TopicSearch] unmarshal connector cursor: %v", err)
		return nil
	}
	return &prev
}

// encodeCursor converts this connector's cursor back into the generic one.
func encodeCursor(cursor *topicCursor) *types.SyncCursor {
	if cursor == nil {
		return nil
	}
	raw, err := json.Marshal(cursor)
	if err != nil {
		return nil
	}
	var generic map[string]interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil
	}
	return &types.SyncCursor{
		LastSyncTime:    cursor.LastSyncTime,
		ConnectorCursor: generic,
	}
}

// FetchAll performs a full sync: the cursor is ignored, pages are refetched
// (the per-run quota and minimum content length still apply).
func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

// FetchIncremental returns only new pages and pages whose body changed.
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	prev := decodeCursor(ctx, cursor)
	items, next, err := c.walk(ctx, config, config.ResourceIDs, prev, true)
	if err != nil && next == nil {
		return nil, nil, err
	}
	return items, encodeCursor(next), err
}

// walk is the shared implementation behind FetchAll / FetchIncremental.
//
// With incremental set, change detection runs in two levels: when the search
// result's visible fields (signal) are unchanged the page is not fetched at all;
// otherwise it is fetched and its body hash compared. The new cursor records
// every page processed this run (including ones judged stable or too short);
// pages left unprocessed (for example because the per-run quota was reached) are
// deliberately omitted so the next run picks them up.
func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *topicCursor,
	incremental bool,
) ([]types.FetchedItem, *topicCursor, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, err
	}
	providerID, err := c.resolveProviderID(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	if c.fetcher == nil {
		return nil, nil, fmt.Errorf("%w: 缺少网页抓取器", datasource.ErrInvalidConfig)
	}

	topics := cfg.topicsFor(resourceIDs)
	newCursor := &topicCursor{
		LastSyncTime: time.Now().UTC(),
		Pages:        map[string]cursorEntry{},
	}

	var (
		out          []types.FetchedItem
		warnings     []string
		seenURLs     = map[string]struct{}{}
		produced     int
		failedTopics int
	)

	for _, topic := range topics {
		if produced >= cfg.MaxItemsPerRun {
			break
		}
		results, searchErr := c.runSearch(ctx, providerID, cfg.searchConfig(), topic)
		if searchErr != nil {
			failedTopics++
			logger.Warnf(ctx, "[TopicSearch] search topic %q failed: %v", topic, searchErr)
			warnings = append(warnings, fmt.Sprintf("topic %q: %v", topic, searchErr))
			continue
		}

		for _, result := range results {
			if produced >= cfg.MaxItemsPerRun {
				break
			}
			if result == nil || strings.TrimSpace(result.URL) == "" {
				continue
			}
			normURL, normErr := normalizeURL(result.URL)
			if normErr != nil {
				logger.Warnf(ctx, "[TopicSearch] skip invalid url %q: %v", result.URL, normErr)
				continue
			}
			// 上游 0.9.1 起搜索请求不再携带黑名单，数据源级规则改由本连接器
			// 在抓取前过滤，保持原有行为。
			if matchesAnyBlacklistRule(normURL, cfg.Blacklist) {
				continue
			}
			if _, dup := seenURLs[normURL]; dup {
				continue
			}
			seenURLs[normURL] = struct{}{}
			if !cfg.withinRecency(result.PublishedAt, time.Now().UTC()) {
				continue
			}

			signal := signalFingerprint(result)
			var prevEntry *cursorEntry
			if incremental && prev != nil {
				if entry, ok := prev.Pages[normURL]; ok {
					prevEntry = &entry
				}
			}
			// Level 1: search-visible fields unchanged → no fetch, carry state forward.
			if prevEntry != nil && prevEntry.Signal != "" && prevEntry.Signal == signal {
				newCursor.Pages[normURL] = *prevEntry
				continue
			}

			content, fetchErr := c.fetcher.Fetch(ctx, result.URL)
			if fetchErr != nil {
				// No cursor entry: the next run retries this page.
				logger.Warnf(ctx, "[TopicSearch] fetch %s failed: %v", result.URL, fetchErr)
				warnings = append(warnings, fmt.Sprintf("page %s: %v", result.URL, fetchErr))
				continue
			}
			content = strings.TrimSpace(content)
			hash := contentFingerprint(content)
			entry := cursorEntry{
				Signal: signal,
				Hash:   hash,
				Topic:  topic,
				At:     time.Now().UTC().Format(time.RFC3339),
			}

			// Level 2: body unchanged → skip, but refresh the signal so the next
			// run can short-circuit at level 1 again.
			if prevEntry != nil && prevEntry.Hash != "" && prevEntry.Hash == hash {
				newCursor.Pages[normURL] = entry
				continue
			}
			// Too short to be worth ingesting: record it anyway so it is not
			// refetched on every run.
			if utf8.RuneCountInString(content) < cfg.MinContentLength {
				newCursor.Pages[normURL] = entry
				logger.Infof(ctx, "[TopicSearch] skip short page %s (%d chars)",
					result.URL, utf8.RuneCountInString(content))
				continue
			}

			newCursor.Pages[normURL] = entry
			out = append(out, buildItem(result, normURL, topic, providerID, content))
			produced++
		}
	}

	if len(topics) > 0 && failedTopics == len(topics) {
		return nil, newCursor, fmt.Errorf("all topics failed: %s", strings.Join(warnings, "; "))
	}
	if len(warnings) > 0 {
		return out, newCursor, &datasource.PartialFetchError{Details: warnings}
	}
	return out, newCursor, nil
}

// buildItem assembles a fetched page into a knowledge entry.
func buildItem(
	result *types.WebSearchResult, normURL, topic, providerID, content string,
) types.FetchedItem {
	title := strings.TrimSpace(result.Title)
	if title == "" {
		title = fallbackTitle(result.URL)
	}
	updatedAt := time.Now().UTC()
	metadata := map[string]string{
		"channel":         types.ChannelTopicSearch,
		"topic":           topic,
		"source_url":      result.URL,
		"search_provider": providerID,
	}
	if result.PublishedAt != nil && !result.PublishedAt.IsZero() {
		updatedAt = *result.PublishedAt
		metadata["published_at"] = result.PublishedAt.UTC().Format(time.RFC3339)
	}
	return types.FetchedItem{
		ExternalID:       normURL,
		Title:            title,
		Content:          []byte(content),
		ContentType:      "text/markdown",
		FileName:         sanitizeFileName(title) + ".md",
		URL:              result.URL,
		UpdatedAt:        updatedAt,
		SourceResourceID: topic,
		Metadata:         metadata,
	}
}

// fallbackTitle builds a readable title from the URL when the search result
// carries none.
func fallbackTitle(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "untitled"
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return u.Host
	}
	return u.Host + "/" + path
}

// matchesAnyBlacklistRule reports whether rawURL matches any of the rules.
func matchesAnyBlacklistRule(rawURL string, rules []string) bool {
	for _, rule := range rules {
		if matchesBlacklistRule(rawURL, rule) {
			return true
		}
	}
	return false
}

// matchesBlacklistRule 支持两种写法：以 / 包裹的正则（/example\.(net|org)/）
// 与通配符（*://*.example.com/*）。
func matchesBlacklistRule(rawURL, rule string) bool {
	if strings.HasPrefix(rule, "/") && strings.HasSuffix(rule, "/") && len(rule) > 1 {
		matched, err := regexp.MatchString(rule[1:len(rule)-1], rawURL)
		if err != nil {
			logger.Warnf(context.Background(), "[TopicSearch] invalid blacklist regex %q: %v", rule, err)
			return false
		}
		return matched
	}
	pattern := "^" + strings.ReplaceAll(rule, "*", ".*") + "$"
	matched, err := regexp.MatchString(pattern, rawURL)
	if err != nil {
		logger.Warnf(context.Background(), "[TopicSearch] invalid blacklist rule %q: %v", rule, err)
		return false
	}
	return matched
}
