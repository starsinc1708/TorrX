package search

import (
	"testing"

	"torrentstream/searchservice/internal/domain"
)

func TestExpandedQueryForProviderAddsRusWhenRUPrefEnabled(t *testing.T) {
	profile := domain.DefaultSearchRankingProfile()
	profile.PreferredAudio = []string{"RU"}

	got := expandedQueryForProvider("dark 1 season", "1337x", profile)
	if got != "dark 1 season russian" {
		t.Fatalf("expected expanded query, got %q", got)
	}
}

func TestExpandedQueryForProviderDoesNotAlterRuTrackerQuery(t *testing.T) {
	profile := domain.DefaultSearchRankingProfile()
	profile.PreferredAudio = []string{"ru"}

	got := expandedQueryForProvider("тьма 1 сезон", "rutracker", profile)
	if got != "тьма 1 сезон" {
		t.Fatalf("expected query to stay unchanged, got %q", got)
	}
}

func TestExpandedQueryForProviderDoesNotDuplicateLanguageToken(t *testing.T) {
	profile := domain.DefaultSearchRankingProfile()
	profile.PreferredAudio = []string{"ru"}

	got := expandedQueryForProvider("dark 1 season rus", "piratebay", profile)
	if got != "dark 1 season rus" {
		t.Fatalf("expected query to stay unchanged, got %q", got)
	}
}

func TestExpandedQueryForProviderNoChangeWithoutLanguagePreference(t *testing.T) {
	profile := domain.DefaultSearchRankingProfile()
	profile.PreferredAudio = nil
	profile.PreferredSubtitles = nil

	got := expandedQueryForProvider("dark 1 season", "1337x", profile)
	if got != "dark 1 season" {
		t.Fatalf("expected query to stay unchanged, got %q", got)
	}
}
