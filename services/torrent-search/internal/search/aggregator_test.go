package search

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"torrentstream/searchservice/internal/domain"
)

type fakeProvider struct {
	name  string
	items []domain.SearchResult
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) Info() domain.ProviderInfo {
	return domain.ProviderInfo{Name: p.name, Label: p.name, Kind: "test", Enabled: true}
}

func (p *fakeProvider) Search(ctx context.Context, request domain.SearchRequest) ([]domain.SearchResult, error) {
	_ = ctx
	_ = request
	return append([]domain.SearchResult(nil), p.items...), nil
}

type countingProvider struct {
	name  string
	items []domain.SearchResult
	hits  atomic.Int32
}

func (p *countingProvider) Name() string { return p.name }

func (p *countingProvider) Info() domain.ProviderInfo {
	return domain.ProviderInfo{Name: p.name, Label: p.name, Kind: "test", Enabled: true}
}

func (p *countingProvider) Search(ctx context.Context, request domain.SearchRequest) ([]domain.SearchResult, error) {
	_ = ctx
	_ = request
	p.hits.Add(1)
	return append([]domain.SearchResult(nil), p.items...), nil
}

type failingProvider struct {
	name string
	err  error
}

func (p *failingProvider) Name() string { return p.name }

func (p *failingProvider) Info() domain.ProviderInfo {
	return domain.ProviderInfo{Name: p.name, Label: p.name, Kind: "test", Enabled: true}
}

func (p *failingProvider) Search(ctx context.Context, request domain.SearchRequest) ([]domain.SearchResult, error) {
	return nil, p.err
}

type slowProvider struct {
	name    string
	items   []domain.SearchResult
	delay   time.Duration
	started atomic.Bool
}

func (p *slowProvider) Name() string { return p.name }

func (p *slowProvider) Info() domain.ProviderInfo {
	return domain.ProviderInfo{Name: p.name, Label: p.name, Kind: "test", Enabled: true}
}

func (p *slowProvider) Search(ctx context.Context, request domain.SearchRequest) ([]domain.SearchResult, error) {
	p.started.Store(true)
	select {
	case <-time.After(p.delay):
		return append([]domain.SearchResult(nil), p.items...), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ---------------------------------------------------------------------------
// Search — basic scenarios
// ---------------------------------------------------------------------------

func TestSearchDedupeSortAndPaginate(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{
			name: "first",
			items: []domain.SearchResult{
				{Name: "A", InfoHash: "1111", Seeders: 10},
				{Name: "B", InfoHash: "2222", Seeders: 5},
			},
		},
		&fakeProvider{
			name: "second",
			items: []domain.SearchResult{
				{Name: "A-dup", InfoHash: "1111", Seeders: 25},
				{Name: "C", InfoHash: "3333", Seeders: 1},
			},
		},
	}, 2*time.Second)

	response, err := service.Search(context.Background(), domain.SearchRequest{
		Query:     "ubuntu",
		Limit:     1,
		Offset:    1,
		SortBy:    domain.SearchSortBySeeders,
		SortOrder: domain.SearchSortOrderDesc,
	}, nil)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}

	if response.TotalItems != 3 {
		t.Fatalf("expected total 3, got %d", response.TotalItems)
	}
	if len(response.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(response.Items))
	}
	if response.Items[0].InfoHash != "2222" {
		t.Fatalf("unexpected item after pagination: %#v", response.Items[0])
	}
	if !response.HasMore {
		t.Fatalf("expected hasMore=true")
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{name: "test"},
	}, time.Second)

	_, err := service.Search(context.Background(), domain.SearchRequest{
		Query: "",
	}, nil)
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("expected ErrInvalidQuery, got %v", err)
	}
}

func TestSearchWhitespaceOnlyQuery(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{name: "test"},
	}, time.Second)

	_, err := service.Search(context.Background(), domain.SearchRequest{
		Query: "   ",
	}, nil)
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("expected ErrInvalidQuery, got %v", err)
	}
}

func TestSearchNegativeOffset(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{name: "test"},
	}, time.Second)

	_, err := service.Search(context.Background(), domain.SearchRequest{
		Query:  "test",
		Offset: -1,
	}, nil)
	if !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("expected ErrInvalidOffset, got %v", err)
	}
}

func TestSearchNoProviders(t *testing.T) {
	service := NewService(nil, time.Second)

	_, err := service.Search(context.Background(), domain.SearchRequest{
		Query: "test",
	}, nil)
	if !errors.Is(err, ErrNoProviders) {
		t.Fatalf("expected ErrNoProviders, got %v", err)
	}
}

func TestSearchUnknownProvider(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{name: "testprov"},
	}, time.Second)

	_, err := service.Search(context.Background(), domain.SearchRequest{
		Query: "test",
	}, []string{"nonexistent"})
	if !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("expected ErrUnknownProvider, got %v", err)
	}
}

func TestSearchSelectSpecificProvider(t *testing.T) {
	provA := &countingProvider{
		name:  "prova",
		items: []domain.SearchResult{{Name: "A", InfoHash: "aaa"}},
	}
	provB := &countingProvider{
		name:  "provb",
		items: []domain.SearchResult{{Name: "B", InfoHash: "bbb"}},
	}
	service := NewService([]Provider{provA, provB}, time.Second)

	response, err := service.Search(context.Background(), domain.SearchRequest{
		Query: "test",
		Limit: 10,
	}, []string{"prova"})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(response.Items) != 1 || response.Items[0].InfoHash != "aaa" {
		t.Fatalf("expected only prova results, got %v", response.Items)
	}
	if provA.hits.Load() != 1 {
		t.Fatalf("expected provA to be called once")
	}
	if provB.hits.Load() != 0 {
		t.Fatalf("expected provB to NOT be called")
	}
}

// ---------------------------------------------------------------------------
// Search — fan-out and error handling
// ---------------------------------------------------------------------------

func TestSearchFanOutToMultipleProviders(t *testing.T) {
	providers := make([]Provider, 5)
	for i := range providers {
		providers[i] = &fakeProvider{
			name:  fmt.Sprintf("prov%d", i),
			items: []domain.SearchResult{{Name: fmt.Sprintf("Item%d", i), InfoHash: fmt.Sprintf("hash%d", i)}},
		}
	}
	service := NewService(providers, 2*time.Second)

	response, err := service.Search(context.Background(), domain.SearchRequest{
		Query: "test",
		Limit: 50,
	}, nil)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if response.TotalItems != 5 {
		t.Fatalf("expected 5 total items from 5 providers, got %d", response.TotalItems)
	}
	if len(response.Providers) != 5 {
		t.Fatalf("expected 5 provider statuses, got %d", len(response.Providers))
	}
}

func TestSearchProviderFailureDoesNotBlockOthers(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{
			name:  "good",
			items: []domain.SearchResult{{Name: "Result", InfoHash: "abc"}},
		},
		&failingProvider{
			name: "bad",
			err:  fmt.Errorf("parse error: invalid JSON"),
		},
	}, 2*time.Second)

	response, err := service.Search(context.Background(), domain.SearchRequest{
		Query: "test",
		Limit: 10,
	}, nil)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("expected 1 result from good provider, got %d", len(response.Items))
	}

	// Check provider statuses
	badFound := false
	for _, ps := range response.Providers {
		if ps.Name == "bad" {
			badFound = true
			if ps.OK {
				t.Fatal("expected bad provider to have OK=false")
			}
			if ps.Error == "" {
				t.Fatal("expected bad provider to have error message")
			}
		}
	}
	if !badFound {
		t.Fatal("expected bad provider in statuses")
	}
}

func TestSearchContextTimeout(t *testing.T) {
	service := NewService([]Provider{
		&slowProvider{
			name:  "slow",
			items: []domain.SearchResult{{Name: "Slow", InfoHash: "slow"}},
			delay: 5 * time.Second,
		},
	}, 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	response, err := service.Search(ctx, domain.SearchRequest{
		Query: "test",
		Limit: 10,
	}, nil)
	// May or may not error, but shouldn't hang
	_ = err
	_ = response
}

// ---------------------------------------------------------------------------
// Search — deduplication
// ---------------------------------------------------------------------------

func TestSearchDedupesByInfoHash(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{
			name: "prov1",
			items: []domain.SearchResult{
				{Name: "Ubuntu Desktop", InfoHash: "abc123", Seeders: 100},
			},
		},
		&fakeProvider{
			name: "prov2",
			items: []domain.SearchResult{
				{Name: "Ubuntu Desktop ISO", InfoHash: "abc123", Seeders: 50},
			},
		},
	}, 2*time.Second)

	response, err := service.Search(context.Background(), domain.SearchRequest{
		Query: "ubuntu",
		Limit: 50,
	}, nil)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if response.TotalItems != 1 {
		t.Fatalf("expected 1 item (deduped), got %d", response.TotalItems)
	}
}

func TestSearchDedupeKeepsHigherSeeders(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{
			name: "prov1",
			items: []domain.SearchResult{
				{Name: "Ubuntu", InfoHash: "abc123", Seeders: 10},
			},
		},
		&fakeProvider{
			name: "prov2",
			items: []domain.SearchResult{
				{Name: "Ubuntu", InfoHash: "abc123", Seeders: 100},
			},
		},
	}, 2*time.Second)

	response, err := service.Search(context.Background(), domain.SearchRequest{
		Query:     "ubuntu",
		Limit:     50,
		SortBy:    domain.SearchSortBySeeders,
		SortOrder: domain.SearchSortOrderDesc,
	}, nil)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if response.TotalItems != 1 {
		t.Fatalf("expected 1 item, got %d", response.TotalItems)
	}
	if response.Items[0].Seeders != 100 {
		t.Fatalf("expected higher seeders version to be kept, got %d", response.Items[0].Seeders)
	}
}

// ---------------------------------------------------------------------------
// Search — limit/offset/hasMore
// ---------------------------------------------------------------------------

func TestSearchDefaultLimit(t *testing.T) {
	items := make([]domain.SearchResult, 60)
	for i := range items {
		items[i] = domain.SearchResult{Name: fmt.Sprintf("Item%d", i), InfoHash: fmt.Sprintf("hash%d", i)}
	}
	service := NewService([]Provider{
		&fakeProvider{name: "test", items: items},
	}, 2*time.Second)

	response, err := service.Search(context.Background(), domain.SearchRequest{
		Query: "test",
	}, nil)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(response.Items) > 50 {
		t.Fatalf("expected at most 50 items (default limit), got %d", len(response.Items))
	}
	if response.TotalItems != 60 {
		t.Fatalf("expected totalItems=60, got %d", response.TotalItems)
	}
	if !response.HasMore {
		t.Fatal("expected HasMore=true with 60 items and limit 50")
	}
}

func TestSearchMaxLimit(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{name: "test", items: []domain.SearchResult{{Name: "A", InfoHash: "a"}}},
	}, time.Second)

	response, err := service.Search(context.Background(), domain.SearchRequest{
		Query: "test",
		Limit: 9999,
	}, nil)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if response.Limit != 200 {
		t.Fatalf("expected limit capped at 200, got %d", response.Limit)
	}
}

func TestSearchOffsetBeyondResults(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{
			name:  "test",
			items: []domain.SearchResult{{Name: "A", InfoHash: "a"}},
		},
	}, time.Second)

	response, err := service.Search(context.Background(), domain.SearchRequest{
		Query:  "test",
		Limit:  10,
		Offset: 100,
	}, nil)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(response.Items) != 0 {
		t.Fatalf("expected 0 items when offset > total, got %d", len(response.Items))
	}
	if response.HasMore {
		t.Fatal("expected HasMore=false when offset > total")
	}
}

// ---------------------------------------------------------------------------
// Search — response metadata
// ---------------------------------------------------------------------------

func TestSearchResponseMetadata(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{
			name:  "test",
			items: []domain.SearchResult{{Name: "A", InfoHash: "a"}},
		},
	}, time.Second)

	response, err := service.Search(context.Background(), domain.SearchRequest{
		Query:     "ubuntu",
		Limit:     25,
		Offset:    0,
		SortBy:    domain.SearchSortBySeeders,
		SortOrder: domain.SearchSortOrderAsc,
	}, nil)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if response.Query != "ubuntu" {
		t.Fatalf("expected query='ubuntu', got %q", response.Query)
	}
	if response.Limit != 25 {
		t.Fatalf("expected limit=25, got %d", response.Limit)
	}
	if response.SortBy != domain.SearchSortBySeeders {
		t.Fatalf("expected sortBy=seeders, got %v", response.SortBy)
	}
	if response.SortOrder != domain.SearchSortOrderAsc {
		t.Fatalf("expected sortOrder=asc, got %v", response.SortOrder)
	}
	if response.ElapsedMS < 0 {
		t.Fatalf("expected non-negative elapsedMS, got %d", response.ElapsedMS)
	}
}

// ---------------------------------------------------------------------------
// Providers / Aliases
// ---------------------------------------------------------------------------

func TestProvidersSorted(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{name: "zeta"},
		&fakeProvider{name: "alpha"},
	}, time.Second)

	providers := service.Providers()
	if len(providers) != 2 {
		t.Fatalf("unexpected providers count: %d", len(providers))
	}
	if providers[0].Name != "alpha" || providers[1].Name != "zeta" {
		t.Fatalf("unexpected order: %#v", providers)
	}
}

func TestProviderAliasesResolve(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{
			name: "piratebay",
			items: []domain.SearchResult{
				{Name: "A", InfoHash: "hash-a", Seeders: 10},
			},
		},
	}, time.Second)

	response, err := service.Search(context.Background(), domain.SearchRequest{
		Query: "test",
		Limit: 10,
	}, []string{"bittorrent"})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("unexpected items count: %d", len(response.Items))
	}
	if len(service.Providers()) != 1 {
		t.Fatalf("providers list should not duplicate aliases")
	}
}

func TestProviderAliases1337x(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{
			name:  "1337x",
			items: []domain.SearchResult{{Name: "A", InfoHash: "a"}},
		},
	}, time.Second)

	response, err := service.Search(context.Background(), domain.SearchRequest{
		Query: "test",
		Limit: 10,
	}, []string{"x1337"})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("expected 1 item via alias x1337, got %d", len(response.Items))
	}
}

// ---------------------------------------------------------------------------
// Search — caching
// ---------------------------------------------------------------------------

func TestSearchCachesPopularQuery(t *testing.T) {
	provider := &countingProvider{
		name: "cached",
		items: []domain.SearchResult{
			{Name: "Ubuntu", InfoHash: "abc", Seeders: 10},
		},
	}
	service := NewService([]Provider{provider}, 2*time.Second)

	request := domain.SearchRequest{
		Query:     "ubuntu",
		Limit:     10,
		Offset:    0,
		SortBy:    domain.SearchSortByRelevance,
		SortOrder: domain.SearchSortOrderDesc,
	}

	if _, err := service.Search(context.Background(), request, nil); err != nil {
		t.Fatalf("first search failed: %v", err)
	}
	if _, err := service.Search(context.Background(), request, nil); err != nil {
		t.Fatalf("second search failed: %v", err)
	}

	if got := provider.hits.Load(); got != 1 {
		t.Fatalf("expected provider call count 1 (cached), got %d", got)
	}
}

func TestSearchNoCacheBypassesCache(t *testing.T) {
	provider := &countingProvider{
		name:  "nocache",
		items: []domain.SearchResult{{Name: "A", InfoHash: "a"}},
	}
	service := NewService([]Provider{provider}, 2*time.Second)

	request := domain.SearchRequest{
		Query: "test",
		Limit: 10,
	}

	// First call populates cache
	service.Search(context.Background(), request, nil)

	// Second call with NoCache should hit provider again
	noCacheReq := request
	noCacheReq.NoCache = true
	service.Search(context.Background(), noCacheReq, nil)

	if got := provider.hits.Load(); got != 2 {
		t.Fatalf("expected 2 calls with NoCache, got %d", got)
	}
}

func TestSearchCacheDisabled(t *testing.T) {
	provider := &countingProvider{
		name:  "nocache",
		items: []domain.SearchResult{{Name: "A", InfoHash: "a"}},
	}
	service := NewService([]Provider{provider}, 2*time.Second, WithCacheDisabled(true))

	request := domain.SearchRequest{
		Query: "test",
		Limit: 10,
	}

	service.Search(context.Background(), request, nil)
	service.Search(context.Background(), request, nil)

	if got := provider.hits.Load(); got != 2 {
		t.Fatalf("expected 2 calls with cache disabled, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// NewService
// ---------------------------------------------------------------------------

func TestNewServiceNilProviders(t *testing.T) {
	service := NewService(nil, time.Second)
	if service == nil {
		t.Fatal("expected non-nil service even with nil providers")
	}
}

func TestNewServiceDefaultTimeout(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{name: "test"},
	}, 0)
	if service.timeout != 15*time.Second {
		t.Fatalf("expected default timeout 15s, got %v", service.timeout)
	}
}

func TestNewServiceSkipsNilProviders(t *testing.T) {
	service := NewService([]Provider{
		nil,
		&fakeProvider{name: "valid"},
		nil,
	}, time.Second)
	providers := service.Providers()
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider (skipping nils), got %d", len(providers))
	}
}

// ---------------------------------------------------------------------------
// resolveProviders
// ---------------------------------------------------------------------------

func TestResolveProvidersAllWhenNoneSpecified(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{name: "alpha"},
		&fakeProvider{name: "beta"},
	}, time.Second)

	selected, err := service.resolveProviders(nil)
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(selected) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(selected))
	}
}

func TestResolveProvidersSortedAlphabetically(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{name: "zeta"},
		&fakeProvider{name: "alpha"},
	}, time.Second)

	selected, err := service.resolveProviders(nil)
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if selected[0].Name() != "alpha" {
		t.Fatalf("expected alpha first, got %s", selected[0].Name())
	}
}

func TestResolveProvidersDeduplicates(t *testing.T) {
	service := NewService([]Provider{
		&fakeProvider{name: "test"},
	}, time.Second)

	selected, err := service.resolveProviders([]string{"test", "test", "TEST"})
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(selected) != 1 {
		t.Fatalf("expected 1 provider (deduped), got %d", len(selected))
	}
}
