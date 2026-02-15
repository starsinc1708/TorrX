package search

import (
	"testing"

	"torrentstream/searchservice/internal/domain"
)

func TestBuildSearchCacheKeyIncludesFilters(t *testing.T) {
	base := domain.SearchRequest{
		Query:     "The Witcher",
		Limit:     50,
		Offset:    0,
		SortBy:    domain.SearchSortByRelevance,
		SortOrder: domain.SearchSortOrderDesc,
		Profile:   domain.DefaultSearchRankingProfile(),
	}

	with1080 := base
	with1080.Filters = domain.SearchFilters{Quality: []string{"1080p"}}

	with720 := base
	with720.Filters = domain.SearchFilters{Quality: []string{"720p"}}

	k1 := buildSearchCacheKey(with1080, []string{"rutracker"})
	k2 := buildSearchCacheKey(with720, []string{"rutracker"})
	if k1 == k2 {
		t.Fatalf("expected different cache keys for different filters, got %q", k1)
	}
}

func TestBuildSearchCacheKeyNormalizesFilterTokenOrder(t *testing.T) {
	base := domain.SearchRequest{
		Query:     "The Witcher",
		Limit:     50,
		Offset:    0,
		SortBy:    domain.SearchSortByRelevance,
		SortOrder: domain.SearchSortOrderDesc,
		Profile:   domain.DefaultSearchRankingProfile(),
	}

	a := base
	a.Filters = domain.SearchFilters{
		Quality:       []string{"1080p", "720p"},
		DubbingGroups: []string{"LostFilm", "NewStudio"},
		ContentType:   " SERIES ",
	}

	b := base
	b.Filters = domain.SearchFilters{
		Quality:       []string{"720p", "1080p"},
		DubbingGroups: []string{"newstudio", "lostfilm"},
		ContentType:   "series",
	}

	k1 := buildSearchCacheKey(a, []string{"rutracker", "1337x"})
	k2 := buildSearchCacheKey(b, []string{"1337x", "rutracker"})
	if k1 != k2 {
		t.Fatalf("expected cache keys to match after normalization\nk1=%q\nk2=%q", k1, k2)
	}
}

