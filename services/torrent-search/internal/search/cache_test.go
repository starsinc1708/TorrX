package search

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"torrentstream/searchservice/internal/domain"
)

// ---------------------------------------------------------------------------
// cacheLookup
// ---------------------------------------------------------------------------

func TestCacheLookupMissOnEmpty(t *testing.T) {
	svc := newTestService()
	_, found, needsRefresh := svc.cacheLookup("key", time.Now())
	if found || needsRefresh {
		t.Fatal("expected cache miss on empty cache")
	}
}

func TestCacheLookupHitFresh(t *testing.T) {
	svc := newTestService()
	resp := domain.SearchResponse{
		Query:      "test",
		TotalItems: 3,
		Items:      []domain.SearchResult{{Name: "A", InfoHash: "a1"}},
	}

	now := time.Now()
	svc.cacheStore("key", resp, now)

	got, found, needsRefresh := svc.cacheLookup("key", now.Add(time.Minute))
	if !found {
		t.Fatal("expected cache hit")
	}
	if needsRefresh {
		t.Fatal("expected no refresh needed for fresh entry")
	}
	if got.TotalItems != 3 || len(got.Items) != 1 {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func TestCacheLookupStaleReturnsDataAndRefreshFlag(t *testing.T) {
	svc := newTestService()
	resp := domain.SearchResponse{
		Query:      "test",
		TotalItems: 5,
		Items:      []domain.SearchResult{{Name: "B", InfoHash: "b1"}},
	}

	now := time.Now()
	svc.cacheStore("key", resp, now)

	// Jump past fresh TTL (6h) but within stale TTL (18h)
	staleTime := now.Add(7 * time.Hour)
	got, found, needsRefresh := svc.cacheLookup("key", staleTime)
	if !found {
		t.Fatal("expected stale hit")
	}
	if !needsRefresh {
		t.Fatal("expected refresh flag for stale entry")
	}
	if got.TotalItems != 5 {
		t.Fatalf("expected stale data returned, got %+v", got)
	}
}

func TestCacheLookupStaleOnlyFirstRefresh(t *testing.T) {
	svc := newTestService()
	resp := domain.SearchResponse{
		Query:      "test",
		TotalItems: 1,
		Items:      []domain.SearchResult{{Name: "C", InfoHash: "c1"}},
	}

	now := time.Now()
	svc.cacheStore("key", resp, now)

	staleTime := now.Add(7 * time.Hour)

	// First stale lookup triggers refresh
	_, _, needsRefresh1 := svc.cacheLookup("key", staleTime)
	if !needsRefresh1 {
		t.Fatal("first stale lookup should trigger refresh")
	}

	// Second stale lookup should NOT trigger refresh (sync.Once)
	_, found2, needsRefresh2 := svc.cacheLookup("key", staleTime.Add(time.Second))
	if !found2 {
		t.Fatal("expected stale hit on second lookup")
	}
	if needsRefresh2 {
		t.Fatal("second stale lookup should not trigger refresh (sync.Once)")
	}
}

func TestCacheLookupExpiredBeyondStale(t *testing.T) {
	svc := newTestService()
	resp := domain.SearchResponse{
		Query: "test",
		Items: []domain.SearchResult{{Name: "D", InfoHash: "d1"}},
	}

	now := time.Now()
	svc.cacheStore("key", resp, now)

	// Jump past stale TTL (18h)
	expired := now.Add(19 * time.Hour)
	_, found, _ := svc.cacheLookup("key", expired)
	if found {
		t.Fatal("expected miss for expired-beyond-stale entry")
	}
}

func TestCacheLookupClonesResponse(t *testing.T) {
	svc := newTestService()
	resp := domain.SearchResponse{
		Items: []domain.SearchResult{{Name: "Original", InfoHash: "x1"}},
	}

	now := time.Now()
	svc.cacheStore("key", resp, now)

	got, found, _ := svc.cacheLookup("key", now)
	if !found {
		t.Fatal("expected hit")
	}

	// Mutate the returned response
	got.Items[0].Name = "Mutated"

	// Original cache entry should be unchanged
	got2, found2, _ := svc.cacheLookup("key", now)
	if !found2 {
		t.Fatal("expected hit after mutation")
	}
	if got2.Items[0].Name != "Original" {
		t.Fatalf("cache entry was mutated: %s", got2.Items[0].Name)
	}
}

// ---------------------------------------------------------------------------
// cacheStore and trimCacheLocked
// ---------------------------------------------------------------------------

func TestCacheStoreAndRetrieve(t *testing.T) {
	svc := newTestService()
	resp := domain.SearchResponse{
		Query:      "hello",
		TotalItems: 2,
		Items: []domain.SearchResult{
			{Name: "R1", InfoHash: "h1", Seeders: 100},
			{Name: "R2", InfoHash: "h2", Seeders: 50},
		},
	}

	now := time.Now()
	svc.cacheStore("hello-key", resp, now)

	got, found, _ := svc.cacheLookup("hello-key", now)
	if !found {
		t.Fatal("expected hit")
	}
	if len(got.Items) != 2 || got.Items[0].Seeders != 100 {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestCacheTrimEvictsOldest(t *testing.T) {
	svc := newTestService()
	svc.warmerCfg.cacheMaxEntries = 3

	now := time.Now()
	for i := 0; i < 5; i++ {
		key := "key-" + string(rune('a'+i))
		resp := domain.SearchResponse{Query: key}
		svc.cacheStore(key, resp, now.Add(time.Duration(i)*time.Second))
	}

	svc.cacheMu.Lock()
	count := len(svc.cache)
	svc.cacheMu.Unlock()

	if count > 3 {
		t.Fatalf("expected max 3 entries, got %d", count)
	}

	// Oldest entries (a, b) should be evicted; newest (c, d, e) remain
	_, foundA, _ := svc.cacheLookup("key-a", now.Add(5*time.Second))
	_, foundE, _ := svc.cacheLookup("key-e", now.Add(5*time.Second))
	if foundA {
		t.Fatal("oldest entry 'a' should have been evicted")
	}
	if !foundE {
		t.Fatal("newest entry 'e' should still exist")
	}
}

func TestCacheTrimRemovesExpiredFirst(t *testing.T) {
	svc := newTestService()
	svc.warmerCfg.cacheMaxEntries = 10

	now := time.Now()

	// Store 3 entries
	for i := 0; i < 3; i++ {
		key := "exp-" + string(rune('a'+i))
		svc.cacheStore(key, domain.SearchResponse{Query: key}, now)
	}

	// Jump past stale TTL — all should be expired
	expired := now.Add(20 * time.Hour)
	svc.cacheStore("fresh", domain.SearchResponse{Query: "fresh"}, expired)

	svc.cacheMu.Lock()
	// After storing "fresh", trimCacheLocked should have removed expired entries
	count := len(svc.cache)
	svc.cacheMu.Unlock()

	if count != 1 {
		t.Fatalf("expected only 1 entry (fresh), got %d", count)
	}
}

// ---------------------------------------------------------------------------
// markPopular
// ---------------------------------------------------------------------------

func TestMarkPopularTracksHitCount(t *testing.T) {
	svc := newTestService()
	now := time.Now()

	req := domain.SearchRequest{Query: "popular test"}

	svc.markPopular("pop-key", req, []string{"a"}, now)
	svc.markPopular("pop-key", req, []string{"a"}, now.Add(time.Second))
	svc.markPopular("pop-key", req, []string{"a"}, now.Add(2*time.Second))

	svc.cacheMu.Lock()
	pop := svc.popular["pop-key"]
	svc.cacheMu.Unlock()

	if pop == nil {
		t.Fatal("expected popular entry")
	}
	if pop.hits != 3 {
		t.Fatalf("expected 3 hits, got %d", pop.hits)
	}
}

func TestMarkPopularIgnoresNonFirstPage(t *testing.T) {
	svc := newTestService()
	now := time.Now()

	req := domain.SearchRequest{Query: "page2", Offset: 50}
	svc.markPopular("page2-key", req, []string{"a"}, now)

	svc.cacheMu.Lock()
	_, exists := svc.popular["page2-key"]
	svc.cacheMu.Unlock()

	if exists {
		t.Fatal("should not track popularity for non-first-page requests")
	}
}

func TestMarkPopularTrimsExcess(t *testing.T) {
	svc := newTestService()
	svc.warmerCfg.popularMaxEntries = 3
	now := time.Now()

	// Pre-populate 4 entries directly to simulate accumulated popularity.
	svc.cacheMu.Lock()
	svc.popular["pk-a"] = &popularQuery{request: domain.SearchRequest{Query: "a"}, hits: 10, lastSeen: now}
	svc.popular["pk-b"] = &popularQuery{request: domain.SearchRequest{Query: "b"}, hits: 20, lastSeen: now}
	svc.popular["pk-c"] = &popularQuery{request: domain.SearchRequest{Query: "c"}, hits: 30, lastSeen: now}
	svc.popular["pk-d"] = &popularQuery{request: domain.SearchRequest{Query: "d"}, hits: 40, lastSeen: now}
	svc.cacheMu.Unlock()

	// Adding a 5th entry (pk-e, 1 hit) triggers trim from 5 → 3.
	// Sorted ascending by hits: pk-e(1) < pk-a(10) < pk-b(20) < pk-c(30) < pk-d(40).
	// The 2 least popular are removed: pk-e(1) and pk-a(10).
	svc.markPopular("pk-e", domain.SearchRequest{Query: "e"}, []string{"p"}, now.Add(time.Second))

	svc.cacheMu.Lock()
	count := len(svc.popular)
	_, hasA := svc.popular["pk-a"]
	_, hasB := svc.popular["pk-b"]
	_, hasC := svc.popular["pk-c"]
	_, hasD := svc.popular["pk-d"]
	svc.cacheMu.Unlock()

	if count != 3 {
		t.Fatalf("expected exactly 3 popular entries, got %d", count)
	}
	if hasA {
		t.Fatal("entry 'a' (10 hits) should have been trimmed")
	}
	// The top 3 by hits should remain: pk-b(20), pk-c(30), pk-d(40)
	if !hasB {
		t.Fatal("entry 'b' (20 hits) should remain")
	}
	if !hasC {
		t.Fatal("entry 'c' (30 hits) should remain")
	}
	if !hasD {
		t.Fatal("entry 'd' (40 hits) should remain")
	}
}

// ---------------------------------------------------------------------------
// runWarmCycle
// ---------------------------------------------------------------------------

func TestRunWarmCycleRefreshesPopularQueries(t *testing.T) {
	provider := &countingProvider{
		name:  "warm",
		items: []domain.SearchResult{{Name: "R", InfoHash: "w1"}},
	}
	svc := NewService([]Provider{provider}, 5*time.Second)
	svc.warmerCfg.warmTopQueries = 2
	svc.warmerCfg.warmInterval = time.Minute

	now := time.Now()

	// Populate cache and popularity
	req := domain.SearchRequest{Query: "warmable", Limit: 10}
	svc.Search(context.Background(), req, nil)

	// Should have 1 provider call
	if got := provider.hits.Load(); got != 1 {
		t.Fatalf("expected 1 initial call, got %d", got)
	}

	// Expire the cache entry so warmer considers it stale
	svc.cacheMu.Lock()
	for _, entry := range svc.cache {
		entry.expiresAt = now.Add(-time.Second) // expired
		entry.staleUntil = now.Add(time.Hour)   // still in stale window
	}
	svc.cacheMu.Unlock()

	// Run warm cycle
	svc.runWarmCycle(context.Background())

	// Should have 2 provider calls (1 initial + 1 warm)
	if got := provider.hits.Load(); got != 2 {
		t.Fatalf("expected 2 calls after warm cycle, got %d", got)
	}
}

func TestRunWarmCycleSkipsRecentlyWarmed(t *testing.T) {
	provider := &countingProvider{
		name:  "warm-skip",
		items: []domain.SearchResult{{Name: "R", InfoHash: "w2"}},
	}
	svc := NewService([]Provider{provider}, 5*time.Second)
	svc.warmerCfg.warmTopQueries = 5
	svc.warmerCfg.warmInterval = 10 * time.Minute

	req := domain.SearchRequest{Query: "recent-warm", Limit: 10}
	svc.Search(context.Background(), req, nil)

	// Expire cache
	now := time.Now()
	svc.cacheMu.Lock()
	for _, entry := range svc.cache {
		entry.expiresAt = now.Add(-time.Second)
		entry.staleUntil = now.Add(time.Hour)
	}
	// Mark as recently warmed (less than warmInterval/2 ago)
	for _, pop := range svc.popular {
		pop.lastWarm = now.Add(-time.Minute) // only 1 minute ago, interval/2 = 5 min
	}
	svc.cacheMu.Unlock()

	svc.runWarmCycle(context.Background())

	// Should NOT warm again (recently warmed)
	if got := provider.hits.Load(); got != 1 {
		t.Fatalf("expected only 1 call (no re-warm), got %d", got)
	}
}

func TestRunWarmCycleSkipsFreshCache(t *testing.T) {
	provider := &countingProvider{
		name:  "warm-fresh",
		items: []domain.SearchResult{{Name: "R", InfoHash: "w3"}},
	}
	svc := NewService([]Provider{provider}, 5*time.Second)
	svc.warmerCfg.warmTopQueries = 5

	req := domain.SearchRequest{Query: "still-fresh", Limit: 10}
	svc.Search(context.Background(), req, nil)

	// Cache is still fresh, warm cycle should skip
	svc.runWarmCycle(context.Background())

	if got := provider.hits.Load(); got != 1 {
		t.Fatalf("expected only 1 call (cache still fresh), got %d", got)
	}
}

func TestRunWarmCycleEmptyPopular(t *testing.T) {
	svc := newTestService()
	// Should not panic on empty popular map
	svc.runWarmCycle(context.Background())
}

// ---------------------------------------------------------------------------
// collectWarmSpecs
// ---------------------------------------------------------------------------

func TestCollectWarmSpecsLimitsToTopN(t *testing.T) {
	svc := newTestService()
	svc.warmerCfg.warmTopQueries = 2
	svc.warmerCfg.warmInterval = time.Minute

	now := time.Now()

	// Add 5 popular queries with different hit counts
	for i := 0; i < 5; i++ {
		key := "spec-" + string(rune('a'+i))
		svc.cacheMu.Lock()
		svc.popular[key] = &popularQuery{
			request:  domain.SearchRequest{Query: key},
			hits:     (i + 1) * 10,
			lastSeen: now,
		}
		// Add expired cache entries so they qualify for warming
		svc.cache[key] = &cachedSearchResponse{
			expiresAt:  now.Add(-time.Second),
			staleUntil: now.Add(time.Hour),
		}
		svc.cacheMu.Unlock()
	}

	specs := svc.collectWarmSpecs(now)
	if len(specs) > 2 {
		t.Fatalf("expected max 2 warm specs, got %d", len(specs))
	}
}

// ---------------------------------------------------------------------------
// WithCacheTTL option
// ---------------------------------------------------------------------------

func TestWithCacheTTLSetsCustomTTL(t *testing.T) {
	svc := NewService(nil, time.Second, WithCacheTTL(2*time.Hour))

	if svc.warmerCfg.cacheTTL != 2*time.Hour {
		t.Fatalf("expected cacheTTL=2h, got %v", svc.warmerCfg.cacheTTL)
	}
	if svc.warmerCfg.staleTTL != 6*time.Hour {
		t.Fatalf("expected staleTTL=6h (3x cacheTTL), got %v", svc.warmerCfg.staleTTL)
	}
}

func TestWithCacheTTLIgnoresZero(t *testing.T) {
	svc := NewService(nil, time.Second, WithCacheTTL(0))

	if svc.warmerCfg.cacheTTL != defaultCacheTTL {
		t.Fatalf("expected default cacheTTL, got %v", svc.warmerCfg.cacheTTL)
	}
}

// ---------------------------------------------------------------------------
// cloneSearchResponse
// ---------------------------------------------------------------------------

func TestCloneSearchResponseDeepCopies(t *testing.T) {
	pubTime := time.Now()
	original := domain.SearchResponse{
		Items: []domain.SearchResult{
			{
				Name:        "A",
				InfoHash:    "aaa",
				PublishedAt: &pubTime,
				Enrichment: domain.SearchEnrichment{
					Audio:       []string{"ru", "en"},
					Subtitles:   []string{"ru"},
					Screenshots: []string{"http://img/1.jpg"},
				},
			},
		},
		Providers: []domain.ProviderStatus{{Name: "test", OK: true}},
	}

	cloned := cloneSearchResponse(original)

	// Mutate original
	original.Items[0].Name = "MUTATED"
	original.Items[0].Enrichment.Audio[0] = "XX"
	original.Items[0].PublishedAt = nil
	original.Providers[0].Name = "MUTATED"

	if cloned.Items[0].Name != "A" {
		t.Fatal("clone was mutated (Name)")
	}
	if cloned.Items[0].Enrichment.Audio[0] != "ru" {
		t.Fatal("clone was mutated (Audio)")
	}
	if cloned.Items[0].PublishedAt == nil {
		t.Fatal("clone PublishedAt was mutated")
	}
	if cloned.Providers[0].Name != "test" {
		t.Fatal("clone Providers was mutated")
	}
}

func TestCloneSearchResponseNilSlices(t *testing.T) {
	original := domain.SearchResponse{
		Items:     nil,
		Providers: nil,
	}
	cloned := cloneSearchResponse(original)
	if cloned.Items != nil || cloned.Providers != nil {
		t.Fatal("expected nil slices in clone")
	}
}

// ---------------------------------------------------------------------------
// SearchStream caching integration
// ---------------------------------------------------------------------------

func TestSearchStreamHitsCacheFirst(t *testing.T) {
	provider := &countingProvider{
		name:  "stream-cache",
		items: []domain.SearchResult{{Name: "S", InfoHash: "s1"}},
	}
	svc := NewService([]Provider{provider}, 5*time.Second)

	req := domain.SearchRequest{Query: "stream-test", Limit: 10}

	// Populate cache via regular search
	svc.Search(context.Background(), req, nil)

	if got := provider.hits.Load(); got != 1 {
		t.Fatalf("expected 1 initial call, got %d", got)
	}

	// Stream should hit cache
	ch := svc.SearchStream(context.Background(), req, nil)
	var responses []domain.SearchResponse
	for resp := range ch {
		responses = append(responses, resp)
	}

	// Provider should NOT be called again
	if got := provider.hits.Load(); got != 1 {
		t.Fatalf("expected 1 call (stream should hit cache), got %d", got)
	}

	if len(responses) != 1 {
		t.Fatalf("expected 1 cached response, got %d", len(responses))
	}
	if !responses[0].Final {
		t.Fatal("cached stream response should be marked Final")
	}
}

func TestSearchStreamNoCacheBypassesCache(t *testing.T) {
	provider := &countingProvider{
		name:  "stream-nocache",
		items: []domain.SearchResult{{Name: "N", InfoHash: "n1"}},
	}
	svc := NewService([]Provider{provider}, 5*time.Second)

	req := domain.SearchRequest{Query: "stream-nocache", Limit: 10}

	// Populate cache
	svc.Search(context.Background(), req, nil)

	// Stream with NoCache
	noCacheReq := req
	noCacheReq.NoCache = true
	ch := svc.SearchStream(context.Background(), noCacheReq, nil)
	for range ch {
		// drain
	}

	if got := provider.hits.Load(); got != 2 {
		t.Fatalf("expected 2 calls (NoCache stream should bypass), got %d", got)
	}
}

// ---------------------------------------------------------------------------
// Concurrent cache access
// ---------------------------------------------------------------------------

func TestCacheConcurrentAccess(t *testing.T) {
	provider := &countingProvider{
		name:  "concurrent",
		items: []domain.SearchResult{{Name: "C", InfoHash: "c1"}},
	}
	svc := NewService([]Provider{provider}, 5*time.Second)

	req := domain.SearchRequest{Query: "concurrent", Limit: 10}

	// Populate cache
	svc.Search(context.Background(), req, nil)

	// Concurrent reads should all hit cache without panics
	var wg sync.WaitGroup
	var misses atomic.Int32
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Search(context.Background(), req, nil)
			if err != nil {
				misses.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := misses.Load(); got != 0 {
		t.Fatalf("expected 0 errors, got %d", got)
	}

	// Should be 1 provider call (all others hit cache)
	if got := provider.hits.Load(); got != 1 {
		t.Fatalf("expected 1 provider call, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// cacheClearRefreshing
// ---------------------------------------------------------------------------

func TestCacheClearRefreshingResetsFlag(t *testing.T) {
	svc := newTestService()
	now := time.Now()

	svc.cacheStore("key", domain.SearchResponse{Query: "test"}, now)

	svc.cacheMu.Lock()
	svc.cache["key"].refreshing = true
	svc.cacheMu.Unlock()

	svc.cacheClearRefreshing("key")

	svc.cacheMu.Lock()
	refreshing := svc.cache["key"].refreshing
	svc.cacheMu.Unlock()

	if refreshing {
		t.Fatal("expected refreshing to be cleared")
	}
}

func TestCacheClearRefreshingNonExistentKey(t *testing.T) {
	svc := newTestService()
	// Should not panic
	svc.cacheClearRefreshing("nonexistent")
}

// ---------------------------------------------------------------------------
// cacheStoreMemoryOnly
// ---------------------------------------------------------------------------

func TestCacheStoreMemoryOnlyDoesNotUseRedis(t *testing.T) {
	svc := newTestService()
	// No Redis backend configured, should store in memory only
	now := time.Now()
	resp := domain.SearchResponse{Query: "memonly", TotalItems: 1}

	svc.cacheStoreMemoryOnly("memonly-key", resp, now)

	got, found, _ := svc.cacheLookup("memonly-key", now)
	if !found {
		t.Fatal("expected hit from memory-only store")
	}
	if got.TotalItems != 1 {
		t.Fatalf("unexpected response: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newTestService() *Service {
	return NewService(nil, time.Second)
}
