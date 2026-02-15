package search

import (
	"context"
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
