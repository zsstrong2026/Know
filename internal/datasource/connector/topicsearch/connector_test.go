package topicsearch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// longBody is longer than the default min_content_length (200 runes).
var longBody = strings.Repeat("WeKnora 主题网络搜索正文。", 30)

// fakeSearch is a fake searchRunner returning canned results per topic.
type fakeSearch struct {
	results      map[string][]*types.WebSearchResult
	errs         map[string]error
	calls        []string
	lastConfig   *types.WebSearchConfig
	lastProvider string
}

func (f *fakeSearch) Search(
	_ context.Context, providerID string, cfg *types.WebSearchConfig, query string,
) ([]*types.WebSearchResult, error) {
	f.calls = append(f.calls, query)
	f.lastConfig = cfg
	f.lastProvider = providerID
	if err := f.errs[query]; err != nil {
		return nil, err
	}
	return f.results[query], nil
}

// fakeProviders is a fake defaultProviderResolver.
type fakeProviders struct {
	defaultProvider *types.WebSearchProviderEntity
	err             error
	tenantSeen      uint64
}

func (f *fakeProviders) GetDefault(_ context.Context, tenantID uint64) (*types.WebSearchProviderEntity, error) {
	f.tenantSeen = tenantID
	return f.defaultProvider, f.err
}

// fakeFetcher is a fake pageFetcher returning canned page text per URL.
type fakeFetcher struct {
	pages map[string]string
	errs  map[string]error
	urls  []string
}

func (f *fakeFetcher) Fetch(_ context.Context, rawURL string) (string, error) {
	f.urls = append(f.urls, rawURL)
	if err := f.errs[rawURL]; err != nil {
		return "", err
	}
	body, ok := f.pages[rawURL]
	if !ok {
		return "", errors.New("unexpected url: " + rawURL)
	}
	return body, nil
}

// tenantCtx builds a context carrying a tenant ID, which the connector needs to
// resolve the default web search provider.
func tenantCtx() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1001))
}

// makeConfig builds a connector configuration.
func makeConfig(settings map[string]interface{}) *types.DataSourceConfig {
	return &types.DataSourceConfig{Type: types.ConnectorTypeTopicSearch, Settings: settings}
}

// makeResult builds one search result.
func makeResult(title, url, snippet string) *types.WebSearchResult {
	return &types.WebSearchResult{Title: title, URL: url, Snippet: snippet}
}

// defaultProviders returns a fake resolver with a default provider.
func defaultProviders() *fakeProviders {
	return &fakeProviders{defaultProvider: &types.WebSearchProviderEntity{ID: "p-1", Name: "默认搜索"}}
}

// cursorPages decodes the generic cursor back into this connector's page states.
func cursorPages(t *testing.T, cursor *types.SyncCursor) map[string]cursorEntry {
	t.Helper()
	decoded := decodeCursor(context.Background(), cursor)
	if decoded == nil {
		return nil
	}
	return decoded.Pages
}

func TestValidateRejectsMissingTopics(t *testing.T) {
	c := NewConnector(&fakeSearch{}, defaultProviders(), &fakeFetcher{})
	err := c.Validate(tenantCtx(), makeConfig(map[string]interface{}{}))
	if !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("Validate() error = %v, want ErrInvalidConfig", err)
	}
}

func TestValidateRequiresDefaultProvider(t *testing.T) {
	search := &fakeSearch{}
	c := NewConnector(search, &fakeProviders{}, &fakeFetcher{})
	err := c.Validate(tenantCtx(), makeConfig(map[string]interface{}{"topics": "RAG"}))
	if !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("Validate() error = %v, want ErrInvalidConfig", err)
	}
	if len(search.calls) != 0 {
		t.Fatalf("search must not run without a provider, calls = %v", search.calls)
	}
}

func TestValidateProbesSearchWithDefaultProvider(t *testing.T) {
	search := &fakeSearch{results: map[string][]*types.WebSearchResult{
		"RAG": {makeResult("A", "https://example.com/a", "s")},
	}}
	providers := defaultProviders()
	c := NewConnector(search, providers, &fakeFetcher{})
	if err := c.Validate(tenantCtx(), makeConfig(map[string]interface{}{"topics": "RAG"})); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if search.lastProvider != "p-1" {
		t.Fatalf("providerID = %q, want p-1", search.lastProvider)
	}
	if providers.tenantSeen != 1001 {
		t.Fatalf("tenantID = %d, want 1001", providers.tenantSeen)
	}
}

func TestValidateUsesExplicitProvider(t *testing.T) {
	search := &fakeSearch{results: map[string][]*types.WebSearchResult{"RAG": nil}}
	providers := &fakeProviders{} // no default provider configured
	c := NewConnector(search, providers, &fakeFetcher{})
	cfg := makeConfig(map[string]interface{}{"topics": "RAG", "provider_id": "p-explicit"})
	if err := c.Validate(tenantCtx(), cfg); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if search.lastProvider != "p-explicit" {
		t.Fatalf("providerID = %q, want p-explicit", search.lastProvider)
	}
}

func TestValidateSurfacesSearchFailure(t *testing.T) {
	search := &fakeSearch{errs: map[string]error{"RAG": errors.New("provider down")}}
	c := NewConnector(search, defaultProviders(), &fakeFetcher{})
	err := c.Validate(tenantCtx(), makeConfig(map[string]interface{}{"topics": "RAG"}))
	if err == nil || !strings.Contains(err.Error(), "provider down") {
		t.Fatalf("Validate() error = %v, want wrapped search failure", err)
	}
}

func TestListResourcesExposesTopics(t *testing.T) {
	c := NewConnector(&fakeSearch{}, defaultProviders(), &fakeFetcher{})
	resources, err := c.ListResources(tenantCtx(), makeConfig(map[string]interface{}{"topics": "RAG\n知识库"}), "")
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	if len(resources) != 2 || resources[0].ExternalID != "RAG" || resources[0].Type != "topic" {
		t.Fatalf("resources = %+v", resources)
	}
	nested, err := c.ListResources(tenantCtx(), makeConfig(map[string]interface{}{"topics": "RAG"}), "RAG")
	if err != nil {
		t.Fatalf("ListResources(parentID) error = %v", err)
	}
	if len(nested) != 0 {
		t.Fatalf("nested resources = %+v, want empty (flat structure)", nested)
	}
}

func TestResolveResourceAncestorsIsEmpty(t *testing.T) {
	c := NewConnector(&fakeSearch{}, defaultProviders(), &fakeFetcher{})
	got, err := c.ResolveResourceAncestors(tenantCtx(), makeConfig(map[string]interface{}{"topics": "RAG"}), []string{"RAG"})
	if err != nil || len(got) != 0 {
		t.Fatalf("ResolveResourceAncestors() = %v, %v; want empty slice and nil error", got, err)
	}
}

// timePtr returns a time pointer, for building results with publish dates.
func timePtr(t time.Time) *time.Time { return &t }

func TestFetchNewItemsProduced(t *testing.T) {
	search := &fakeSearch{results: map[string][]*types.WebSearchResult{
		"RAG": {
			makeResult("A 文章", "https://Example.com/post/1?utm_source=x", "s1"),
			makeResult("B 文章", "https://example.com/post/2", "s2"),
		},
	}}
	fetcher := &fakeFetcher{pages: map[string]string{
		"https://Example.com/post/1?utm_source=x": longBody,
		"https://example.com/post/2":              longBody + "b",
	}}
	c := NewConnector(search, defaultProviders(), fetcher)

	items, cursor, err := c.FetchIncremental(tenantCtx(), makeConfig(map[string]interface{}{"topics": "RAG"}), nil)
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].ExternalID != "https://example.com/post/1" {
		t.Fatalf("ExternalID = %q, want normalized URL", items[0].ExternalID)
	}
	if items[0].URL != "https://Example.com/post/1?utm_source=x" {
		t.Fatalf("URL = %q, want original URL", items[0].URL)
	}
	if !strings.HasSuffix(items[0].FileName, ".md") {
		t.Fatalf("FileName = %q, want .md suffix", items[0].FileName)
	}
	if items[0].Metadata["channel"] != types.ChannelTopicSearch {
		t.Fatalf("channel = %q, want %q", items[0].Metadata["channel"], types.ChannelTopicSearch)
	}
	if items[0].Metadata["topic"] != "RAG" || items[0].Metadata["search_provider"] != "p-1" {
		t.Fatalf("metadata = %v", items[0].Metadata)
	}
	if pages := cursorPages(t, cursor); len(pages) != 2 {
		t.Fatalf("cursor pages = %v, want 2 entries", pages)
	}
}

func TestIncrementalSkipsUnchangedWithoutFetching(t *testing.T) {
	search := &fakeSearch{results: map[string][]*types.WebSearchResult{
		"RAG": {makeResult("A", "https://example.com/a", "s")},
	}}
	settings := map[string]interface{}{"topics": "RAG"}

	firstFetcher := &fakeFetcher{pages: map[string]string{"https://example.com/a": longBody}}
	_, cursor, err := NewConnector(search, defaultProviders(), firstFetcher).
		FetchIncremental(tenantCtx(), makeConfig(settings), nil)
	if err != nil {
		t.Fatalf("first fetch error = %v", err)
	}

	secondFetcher := &fakeFetcher{pages: map[string]string{"https://example.com/a": longBody}}
	items, next, err := NewConnector(search, defaultProviders(), secondFetcher).
		FetchIncremental(tenantCtx(), makeConfig(settings), cursor)
	if err != nil {
		t.Fatalf("second fetch error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %d, want 0", len(items))
	}
	if len(secondFetcher.urls) != 0 {
		t.Fatalf("fetcher called with %v, want no fetch when the signal is unchanged", secondFetcher.urls)
	}
	if pages := cursorPages(t, next); len(pages) != 1 {
		t.Fatalf("cursor must carry forward the unchanged page, got %v", pages)
	}
}

func TestIncrementalRefetchesWhenSignalChangesButBodySame(t *testing.T) {
	settings := map[string]interface{}{"topics": "RAG"}
	first := &fakeSearch{results: map[string][]*types.WebSearchResult{
		"RAG": {makeResult("A", "https://example.com/a", "旧摘要")},
	}}
	_, cursor, err := NewConnector(first, defaultProviders(), &fakeFetcher{
		pages: map[string]string{"https://example.com/a": longBody},
	}).FetchIncremental(tenantCtx(), makeConfig(settings), nil)
	if err != nil {
		t.Fatalf("first fetch error = %v", err)
	}

	second := &fakeSearch{results: map[string][]*types.WebSearchResult{
		"RAG": {makeResult("A", "https://example.com/a", "新摘要")},
	}}
	fetcher := &fakeFetcher{pages: map[string]string{"https://example.com/a": longBody}}
	items, next, err := NewConnector(second, defaultProviders(), fetcher).
		FetchIncremental(tenantCtx(), makeConfig(settings), cursor)
	if err != nil {
		t.Fatalf("second fetch error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %d, want 0 (body unchanged)", len(items))
	}
	if len(fetcher.urls) != 1 {
		t.Fatalf("fetcher calls = %v, want 1 refetch after the signal changed", fetcher.urls)
	}
	wantSignal := signalFingerprint(second.results["RAG"][0])
	if got := cursorPages(t, next)["https://example.com/a"].Signal; got != wantSignal {
		t.Fatalf("cursor signal = %q, want refreshed %q", got, wantSignal)
	}
}

func TestIncrementalProducesWhenBodyChanges(t *testing.T) {
	settings := map[string]interface{}{"topics": "RAG"}
	search := &fakeSearch{results: map[string][]*types.WebSearchResult{
		"RAG": {makeResult("A", "https://example.com/a", "旧摘要")},
	}}
	_, cursor, err := NewConnector(search, defaultProviders(), &fakeFetcher{
		pages: map[string]string{"https://example.com/a": longBody},
	}).FetchIncremental(tenantCtx(), makeConfig(settings), nil)
	if err != nil {
		t.Fatalf("first fetch error = %v", err)
	}

	changed := longBody + "新增段落"
	search2 := &fakeSearch{results: map[string][]*types.WebSearchResult{
		"RAG": {makeResult("A", "https://example.com/a", "新摘要")},
	}}
	items, _, err := NewConnector(search2, defaultProviders(), &fakeFetcher{
		pages: map[string]string{"https://example.com/a": changed},
	}).FetchIncremental(tenantCtx(), makeConfig(settings), cursor)
	if err != nil {
		t.Fatalf("second fetch error = %v", err)
	}
	if len(items) != 1 || items[0].ExternalID != "https://example.com/a" {
		t.Fatalf("items = %+v, want 1 item with the same ExternalID", items)
	}
}

func TestShortPageSkippedButRecorded(t *testing.T) {
	search := &fakeSearch{results: map[string][]*types.WebSearchResult{
		"RAG": {makeResult("A", "https://example.com/a", "s")},
	}}
	fetcher := &fakeFetcher{pages: map[string]string{"https://example.com/a": "太短"}}
	c := NewConnector(search, defaultProviders(), fetcher)

	items, cursor, err := c.FetchIncremental(tenantCtx(), makeConfig(map[string]interface{}{"topics": "RAG"}), nil)
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %d, want 0", len(items))
	}
	if _, ok := cursorPages(t, cursor)["https://example.com/a"]; !ok {
		t.Fatal("short page must be recorded in the cursor to avoid refetching every run")
	}
}

func TestFetchFailureNotRecordedInCursor(t *testing.T) {
	search := &fakeSearch{results: map[string][]*types.WebSearchResult{
		"RAG": {makeResult("A", "https://example.com/a", "s")},
	}}
	fetcher := &fakeFetcher{errs: map[string]error{"https://example.com/a": errors.New("boom")}}
	c := NewConnector(search, defaultProviders(), fetcher)

	items, cursor, err := c.FetchIncremental(tenantCtx(), makeConfig(map[string]interface{}{"topics": "RAG"}), nil)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("error = %v, want PartialFetchError", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %d, want 0", len(items))
	}
	if _, ok := cursorPages(t, cursor)["https://example.com/a"]; ok {
		t.Fatal("failed page must not be recorded in the cursor, so the next run retries it")
	}
}

func TestQuotaLimitsProducedItems(t *testing.T) {
	search := &fakeSearch{results: map[string][]*types.WebSearchResult{
		"RAG": {
			makeResult("A", "https://example.com/a", "s"),
			makeResult("B", "https://example.com/b", "s"),
			makeResult("C", "https://example.com/c", "s"),
		},
	}}
	fetcher := &fakeFetcher{pages: map[string]string{
		"https://example.com/a": longBody,
		"https://example.com/b": longBody,
		"https://example.com/c": longBody,
	}}
	c := NewConnector(search, defaultProviders(), fetcher)

	items, cursor, err := c.FetchIncremental(tenantCtx(), makeConfig(map[string]interface{}{
		"topics": "RAG", "max_items_per_run": 2,
	}), nil)
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if len(fetcher.urls) != 2 {
		t.Fatalf("fetcher calls = %v, want 2 (stop at quota)", fetcher.urls)
	}
	pages := cursorPages(t, cursor)
	if len(pages) != 2 {
		t.Fatalf("cursor pages = %v, want only the processed pages", pages)
	}
	if _, ok := pages["https://example.com/c"]; ok {
		t.Fatal("unprocessed page must not be recorded, so the next run picks it up")
	}
}

func TestBlacklistPassedToSearchProvider(t *testing.T) {
	search := &fakeSearch{results: map[string][]*types.WebSearchResult{"RAG": nil}}
	c := NewConnector(search, defaultProviders(), &fakeFetcher{})
	_, _, err := c.FetchIncremental(tenantCtx(), makeConfig(map[string]interface{}{
		"topics":    "RAG",
		"blacklist": "*://*.spam.com/*\n/example\\.(net|org)/",
	}), nil)
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	if search.lastConfig == nil || len(search.lastConfig.Blacklist) != 2 {
		t.Fatalf("blacklist = %v, want 2 rules forwarded to the search service", search.lastConfig)
	}
}

func TestRecencyFilterSkipsOldResults(t *testing.T) {
	now := time.Now().UTC()
	oldResult := makeResult("旧", "https://example.com/old", "s")
	oldResult.PublishedAt = timePtr(now.AddDate(0, 0, -30))
	freshResult := makeResult("新", "https://example.com/new", "s")
	freshResult.PublishedAt = timePtr(now.AddDate(0, 0, -1))

	search := &fakeSearch{results: map[string][]*types.WebSearchResult{
		"RAG": {oldResult, freshResult, makeResult("无日期", "https://example.com/undated", "s")},
	}}
	fetcher := &fakeFetcher{pages: map[string]string{
		"https://example.com/new":     longBody,
		"https://example.com/undated": longBody,
	}}
	c := NewConnector(search, defaultProviders(), fetcher)

	items, _, err := c.FetchIncremental(tenantCtx(), makeConfig(map[string]interface{}{
		"topics": "RAG", "result_recency_days": 7,
	}), nil)
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (fresh + undated)", len(items))
	}
	for _, u := range fetcher.urls {
		if u == "https://example.com/old" {
			t.Fatal("out-of-window result must not be fetched")
		}
	}
}

func TestPartialTopicFailureReturnsPartialError(t *testing.T) {
	search := &fakeSearch{
		results: map[string][]*types.WebSearchResult{
			"B": {makeResult("B", "https://example.com/b", "s")},
		},
		errs: map[string]error{"A": errors.New("search down")},
	}
	fetcher := &fakeFetcher{pages: map[string]string{"https://example.com/b": longBody}}
	c := NewConnector(search, defaultProviders(), fetcher)

	items, _, err := c.FetchIncremental(tenantCtx(), makeConfig(map[string]interface{}{"topics": "A\nB"}), nil)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("error = %v, want PartialFetchError", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1 from the healthy topic", len(items))
	}
}

func TestAllTopicsFailedReturnsError(t *testing.T) {
	search := &fakeSearch{errs: map[string]error{
		"A": errors.New("down-a"),
		"B": errors.New("down-b"),
	}}
	c := NewConnector(search, defaultProviders(), &fakeFetcher{})
	_, _, err := c.FetchIncremental(tenantCtx(), makeConfig(map[string]interface{}{"topics": "A\nB"}), nil)
	if err == nil || !strings.Contains(err.Error(), "all topics failed") {
		t.Fatalf("error = %v, want an all-topics-failed error", err)
	}
	var partial *datasource.PartialFetchError
	if errors.As(err, &partial) {
		t.Fatal("all-topic failure must be a hard error, not PartialFetchError")
	}
}

func TestSameURLAcrossTopicsDeduped(t *testing.T) {
	shared := makeResult("共享", "https://example.com/shared", "s")
	search := &fakeSearch{results: map[string][]*types.WebSearchResult{
		"A": {shared},
		"B": {shared},
	}}
	fetcher := &fakeFetcher{pages: map[string]string{"https://example.com/shared": longBody}}
	c := NewConnector(search, defaultProviders(), fetcher)

	items, _, err := c.FetchIncremental(tenantCtx(), makeConfig(map[string]interface{}{"topics": "A\nB"}), nil)
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1 (deduped across topics)", len(items))
	}
}

func TestFetchAllIgnoresCursor(t *testing.T) {
	search := &fakeSearch{results: map[string][]*types.WebSearchResult{
		"RAG": {makeResult("A", "https://example.com/a", "s")},
	}}
	fetcher := &fakeFetcher{pages: map[string]string{"https://example.com/a": longBody}}
	c := NewConnector(search, defaultProviders(), fetcher)

	items, err := c.FetchAll(tenantCtx(), makeConfig(map[string]interface{}{"topics": "RAG"}), []string{"RAG"})
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
}
