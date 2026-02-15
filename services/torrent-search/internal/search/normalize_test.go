package search

import (
	"testing"

	"torrentstream/searchservice/internal/domain"
)

func TestParseTitleMetaExtractsYearAndEpisodeData(t *testing.T) {
	meta := parseTitleMeta("Человек-Паук 2 сезон 1 серия 3 (2019) WEB-DL 1080p")
	if meta.year != 2019 {
		t.Fatalf("expected year=2019, got %d", meta.year)
	}
	if meta.season != 1 {
		t.Fatalf("expected season=1, got %d", meta.season)
	}
	if meta.episode != 3 {
		t.Fatalf("expected episode=3, got %d", meta.episode)
	}
	if _, ok := meta.tokenSet["chelovek"]; !ok {
		t.Fatalf("expected transliterated token in set")
	}
}

func TestBuildTitleDedupeKeyNormalizesReleaseNoise(t *testing.T) {
	first := buildTitleDedupeKey(domain.SearchResult{Name: "The.Rookie.S01E02.1080p.WEB-DL"})
	second := buildTitleDedupeKey(domain.SearchResult{Name: "The Rookie season 1 episode 2 720p"})
	if first == "" || second == "" {
		t.Fatalf("dedupe keys must not be empty")
	}
	if first != second {
		t.Fatalf("expected same dedupe key, got %q vs %q", first, second)
	}
}

func TestRelevanceScorePrefersMatchingEpisode(t *testing.T) {
	query := parseTitleMeta("The Rookie S01E02")
	profile := domain.DefaultSearchRankingProfile()
	match := relevanceScoreForResult(query, profile, domain.SearchResult{Name: "The Rookie S01E02 1080p", Seeders: 20})
	mismatch := relevanceScoreForResult(query, profile, domain.SearchResult{Name: "The Rookie S01E08 1080p", Seeders: 20})
	if match <= mismatch {
		t.Fatalf("expected matching episode score > mismatch, got %.2f <= %.2f", match, mismatch)
	}
}

func TestEnrichSearchResultExtractsMetadata(t *testing.T) {
	item := enrichSearchResult(domain.SearchResult{Name: "Dark.S01E01.1080p.WEB-DL.x265.RUS.ENG.SUB"})
	if !item.Enrichment.IsSeries {
		t.Fatalf("expected series=true")
	}
	if item.Enrichment.Season != 1 || item.Enrichment.Episode != 1 {
		t.Fatalf("unexpected season/episode: %#v", item.Enrichment)
	}
	if item.Enrichment.Quality == "" {
		t.Fatalf("quality is empty")
	}
	if len(item.Enrichment.Audio) == 0 {
		t.Fatalf("audio hints are empty")
	}
	if len(item.Enrichment.Subtitles) == 0 {
		t.Fatalf("subtitle hints are empty")
	}
}

func TestEnrichSearchResultDetectsCyrillicLanguageHints(t *testing.T) {
	item := enrichSearchResult(domain.SearchResult{Name: "Тьма.S01E01.1080p.WEB-DL.Русский.Английский.Субтитры"})
	if len(item.Enrichment.Audio) == 0 {
		t.Fatalf("audio hints are empty")
	}
	if len(item.Enrichment.Subtitles) == 0 {
		t.Fatalf("subtitle hints are empty")
	}
	hasRU := false
	hasEN := false
	for _, value := range item.Enrichment.Audio {
		switch value {
		case "RU":
			hasRU = true
		case "EN":
			hasEN = true
		}
	}
	if !hasRU || !hasEN {
		t.Fatalf("expected RU and EN in audio hints, got %#v", item.Enrichment.Audio)
	}
}
