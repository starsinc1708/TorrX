package search

import (
	"strings"
	"testing"
	"time"

	"torrentstream/searchservice/internal/domain"
)

// ---------------------------------------------------------------------------
// parseTitleMeta
// ---------------------------------------------------------------------------

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

func TestParseTitleMetaS01E02Format(t *testing.T) {
	meta := parseTitleMeta("The.Rookie.S01E02.1080p")
	if meta.season != 1 {
		t.Fatalf("expected season=1, got %d", meta.season)
	}
	if meta.episode != 2 {
		t.Fatalf("expected episode=2, got %d", meta.episode)
	}
}

func TestParseTitleMeta3x05Format(t *testing.T) {
	meta := parseTitleMeta("Breaking.Bad.3x05.720p")
	if meta.season != 3 {
		t.Fatalf("expected season=3, got %d", meta.season)
	}
	if meta.episode != 5 {
		t.Fatalf("expected episode=5, got %d", meta.episode)
	}
}

func TestParseTitleMetaEmptyInput(t *testing.T) {
	meta := parseTitleMeta("")
	if meta.normalized != "" {
		t.Fatalf("expected empty normalized for empty input, got %q", meta.normalized)
	}
	if meta.year != 0 || meta.season != 0 || meta.episode != 0 {
		t.Fatalf("expected zero values for empty input")
	}
}

func TestParseTitleMetaWhitespaceOnly(t *testing.T) {
	meta := parseTitleMeta("   ")
	if meta.normalized != "" {
		t.Fatalf("expected empty normalized, got %q", meta.normalized)
	}
}

func TestParseTitleMetaStripsStopwords(t *testing.T) {
	meta := parseTitleMeta("Dark 1080p WEB-DL x265 2020")
	if _, ok := meta.tokenSet["1080p"]; ok {
		t.Fatal("1080p should be stripped as stopword")
	}
	if _, ok := meta.tokenSet["x265"]; ok {
		t.Fatal("x265 should be stripped as stopword")
	}
	if _, ok := meta.tokenSet["dark"]; !ok {
		t.Fatal("dark should be in token set")
	}
}

func TestParseTitleMetaMultipleYears(t *testing.T) {
	meta := parseTitleMeta("Batman (1989) vs Batman (2022)")
	if meta.year != 2022 {
		t.Fatalf("expected latest year 2022, got %d", meta.year)
	}
}

func TestParseTitleMetaNoYear(t *testing.T) {
	meta := parseTitleMeta("Ubuntu ISO")
	if meta.year != 0 {
		t.Fatalf("expected year=0, got %d", meta.year)
	}
}

func TestParseTitleMetaTransliterateCyrillicTokens(t *testing.T) {
	meta := parseTitleMeta("Тьма")
	if _, ok := meta.tokenSet["тьма"]; !ok {
		t.Fatal("expected Cyrillic token in set")
	}
	// Check for transliterated version
	found := false
	for token := range meta.tokenSet {
		if !strings.ContainsRune(token, 'т') && token != "тьма" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected transliterated Latin token in set")
	}
}

func TestParseTitleMetaYoReplacedWithE(t *testing.T) {
	meta := parseTitleMeta("Ёлки")
	// ё should be replaced with е
	if _, ok := meta.tokenSet["елки"]; !ok {
		t.Fatal("expected ё replaced with е in token set")
	}
}

// ---------------------------------------------------------------------------
// extractYear / extractSeasonEpisode
// ---------------------------------------------------------------------------

func TestExtractYearValidCases(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"Movie 2024", 2024},
		{"Movie (1999) HD", 1999},
		{"2000 A Space Odyssey", 2000},
		{"No year here", 0},
		{"1800 is not a movie year", 0},
	}
	for _, tc := range cases {
		got := extractYear(tc.input)
		if got != tc.want {
			t.Errorf("extractYear(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestExtractSeasonEpisodeFormats(t *testing.T) {
	cases := []struct {
		input   string
		season  int
		episode int
	}{
		{"S02E03", 2, 3},
		{"s1e10", 1, 10},
		{"S 3 E 5", 3, 5},
		{"3x05", 3, 5},
		{"12x100", 12, 100},
		{"season 2", 2, 0},
		{"сезон 3 серия 5", 3, 5},
		{"no episode data", 0, 0},
	}
	for _, tc := range cases {
		s, e := extractSeasonEpisode(strings.ToLower(tc.input))
		if s != tc.season || e != tc.episode {
			t.Errorf("extractSeasonEpisode(%q) = (%d,%d), want (%d,%d)", tc.input, s, e, tc.season, tc.episode)
		}
	}
}

// ---------------------------------------------------------------------------
// isResolutionToken
// ---------------------------------------------------------------------------

func TestIsResolutionToken(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"1080p", true},
		{"720p", true},
		{"480p", true},
		{"2160p", true},
		{"360p", true},
		{"abcp", false},
		{"1080", false},
		{"p", false},
		{"10p", true},
	}
	for _, tc := range cases {
		got := isResolutionToken(tc.input)
		if got != tc.want {
			t.Errorf("isResolutionToken(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// transliterateCyrillic
// ---------------------------------------------------------------------------

func TestTransliterateCyrillic(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"привет", "privet"},
		{"матрица", "matritsa"},
		{"hello", "hello"},
		{"", ""},
		{"mix микс", "mix miks"},
	}
	for _, tc := range cases {
		got := transliterateCyrillic(tc.input)
		if got != tc.want {
			t.Errorf("transliterateCyrillic(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// dedupeKey
// ---------------------------------------------------------------------------

func TestDedupeKeyUsesInfoHashFirst(t *testing.T) {
	item := domain.SearchResult{
		Name:     "Test",
		InfoHash: "ABCDEF1234567890",
		Magnet:   "magnet:?xt=urn:btih:ABCDEF1234567890",
	}
	key := dedupeKey(item)
	if !strings.HasPrefix(key, "hash:") {
		t.Fatalf("expected hash: prefix, got %q", key)
	}
}

func TestDedupeKeyFallsBackToMagnet(t *testing.T) {
	item := domain.SearchResult{
		Name:   "Test",
		Magnet: "magnet:?xt=urn:btih:ABCDEF1234567890",
	}
	key := dedupeKey(item)
	if !strings.HasPrefix(key, "hash:") {
		t.Fatalf("expected hash from magnet extraction, got %q", key)
	}
}

func TestDedupeKeyFallsBackToTitle(t *testing.T) {
	item := domain.SearchResult{
		Name:      "The.Rookie.S01E02.1080p",
		SizeBytes: 2 * 1024 * 1024 * 1024,
	}
	key := dedupeKey(item)
	if strings.HasPrefix(key, "hash:") {
		t.Fatalf("expected title-based key, got %q", key)
	}
	if key == "" {
		t.Fatal("expected non-empty key")
	}
}

func TestDedupeKeyNormalizesInfoHash(t *testing.T) {
	a := dedupeKey(domain.SearchResult{InfoHash: "ABCDEF1234"})
	b := dedupeKey(domain.SearchResult{InfoHash: "abcdef1234"})
	if a != b {
		t.Fatalf("expected same dedupe key for different case, got %q vs %q", a, b)
	}
}

func TestDedupeKeySameHashDifferentName(t *testing.T) {
	a := dedupeKey(domain.SearchResult{Name: "A", InfoHash: "abc123"})
	b := dedupeKey(domain.SearchResult{Name: "B", InfoHash: "abc123"})
	if a != b {
		t.Fatalf("expected same dedupe key for same hash, got %q vs %q", a, b)
	}
}

// ---------------------------------------------------------------------------
// buildTitleDedupeKey
// ---------------------------------------------------------------------------

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

func TestBuildTitleDedupeKeyEmptyName(t *testing.T) {
	key := buildTitleDedupeKey(domain.SearchResult{Name: ""})
	if key != "" {
		t.Fatalf("expected empty key for empty name, got %q", key)
	}
}

func TestBuildTitleDedupeKeyIncludesSize(t *testing.T) {
	withSize := buildTitleDedupeKey(domain.SearchResult{Name: "Test Movie 2024", SizeBytes: 4 * 1024 * 1024 * 1024})
	withoutSize := buildTitleDedupeKey(domain.SearchResult{Name: "Test Movie 2024", SizeBytes: 0})
	if withSize == withoutSize {
		t.Fatalf("expected different keys for different sizes")
	}
}

// ---------------------------------------------------------------------------
// shouldReplace
// ---------------------------------------------------------------------------

func TestShouldReplaceHigherSeeders(t *testing.T) {
	queryMeta := parseTitleMeta("test")
	profile := domain.DefaultSearchRankingProfile()
	existing := domain.SearchResult{Name: "Test", Seeders: 10, InfoHash: "aaa"}
	candidate := domain.SearchResult{Name: "Test", Seeders: 100, InfoHash: "aaa"}
	if !shouldReplace(existing, candidate, queryMeta, profile) {
		t.Fatal("expected candidate with more seeders to replace existing")
	}
}

func TestShouldReplaceCandidateHasInfoHash(t *testing.T) {
	queryMeta := parseTitleMeta("test")
	profile := domain.DefaultSearchRankingProfile()
	existing := domain.SearchResult{Name: "Test", Seeders: 10}
	candidate := domain.SearchResult{Name: "Test", Seeders: 10, InfoHash: "abc123"}
	if !shouldReplace(existing, candidate, queryMeta, profile) {
		t.Fatal("expected candidate with infoHash to replace existing without")
	}
}

func TestShouldReplaceCandidateHasMagnet(t *testing.T) {
	queryMeta := parseTitleMeta("test")
	profile := domain.DefaultSearchRankingProfile()
	existing := domain.SearchResult{Name: "Test", Seeders: 10}
	candidate := domain.SearchResult{Name: "Test", Seeders: 10, Magnet: "magnet:?xt=urn:btih:abc"}
	if !shouldReplace(existing, candidate, queryMeta, profile) {
		t.Fatal("expected candidate with magnet to replace existing without")
	}
}

func TestShouldNotReplaceWorse(t *testing.T) {
	queryMeta := parseTitleMeta("test")
	profile := domain.DefaultSearchRankingProfile()
	existing := domain.SearchResult{Name: "Test", Seeders: 100, InfoHash: "aaa", Magnet: "m"}
	candidate := domain.SearchResult{Name: "Test", Seeders: 5}
	if shouldReplace(existing, candidate, queryMeta, profile) {
		t.Fatal("expected candidate with fewer seeders and no hash to NOT replace")
	}
}

// ---------------------------------------------------------------------------
// sortResults
// ---------------------------------------------------------------------------

func TestSortResultsBySeeders(t *testing.T) {
	items := []domain.SearchResult{
		{Name: "A", Seeders: 10},
		{Name: "B", Seeders: 100},
		{Name: "C", Seeders: 50},
	}
	queryMeta := parseTitleMeta("test")
	profile := domain.DefaultSearchRankingProfile()
	sortResults(items, domain.SearchSortBySeeders, domain.SearchSortOrderDesc, queryMeta, profile)
	if items[0].Seeders != 100 {
		t.Fatalf("expected first item to have most seeders, got %d", items[0].Seeders)
	}
	if items[2].Seeders != 10 {
		t.Fatalf("expected last item to have fewest seeders, got %d", items[2].Seeders)
	}
}

func TestSortResultsBySize(t *testing.T) {
	items := []domain.SearchResult{
		{Name: "A", SizeBytes: 1000},
		{Name: "B", SizeBytes: 5000},
		{Name: "C", SizeBytes: 2000},
	}
	queryMeta := parseTitleMeta("test")
	profile := domain.DefaultSearchRankingProfile()
	sortResults(items, domain.SearchSortBySizeBytes, domain.SearchSortOrderDesc, queryMeta, profile)
	if items[0].SizeBytes != 5000 {
		t.Fatalf("expected first item to be largest, got %d", items[0].SizeBytes)
	}
}

func TestSortResultsByPublished(t *testing.T) {
	now := time.Now()
	old := now.Add(-72 * time.Hour)
	mid := now.Add(-24 * time.Hour)
	items := []domain.SearchResult{
		{Name: "A", PublishedAt: &old},
		{Name: "B", PublishedAt: &now},
		{Name: "C", PublishedAt: &mid},
	}
	queryMeta := parseTitleMeta("test")
	profile := domain.DefaultSearchRankingProfile()
	sortResults(items, domain.SearchSortByPublished, domain.SearchSortOrderDesc, queryMeta, profile)
	if items[0].Name != "B" {
		t.Fatalf("expected newest first, got %s", items[0].Name)
	}
}

func TestSortResultsByRelevanceDefault(t *testing.T) {
	items := []domain.SearchResult{
		{Name: "Unrelated Movie 2020", Seeders: 500},
		{Name: "Ubuntu 22.04 Desktop", Seeders: 50},
	}
	queryMeta := parseTitleMeta("Ubuntu")
	profile := domain.DefaultSearchRankingProfile()
	sortResults(items, domain.SearchSortByRelevance, domain.SearchSortOrderDesc, queryMeta, profile)
	if items[0].Name != "Ubuntu 22.04 Desktop" {
		t.Fatalf("expected more relevant result first, got %s", items[0].Name)
	}
}

func TestSortResultsAscending(t *testing.T) {
	items := []domain.SearchResult{
		{Name: "A", Seeders: 100},
		{Name: "B", Seeders: 10},
		{Name: "C", Seeders: 50},
	}
	queryMeta := parseTitleMeta("test")
	profile := domain.DefaultSearchRankingProfile()
	sortResults(items, domain.SearchSortBySeeders, domain.SearchSortOrderAsc, queryMeta, profile)
	if items[0].Seeders != 10 {
		t.Fatalf("expected ascending: first item seeders=10, got %d", items[0].Seeders)
	}
}

// ---------------------------------------------------------------------------
// applyFilters
// ---------------------------------------------------------------------------

func TestApplyFiltersNoFilters(t *testing.T) {
	items := []domain.SearchResult{{Name: "A"}, {Name: "B"}}
	result := applyFilters(items, domain.SearchFilters{})
	if len(result) != 2 {
		t.Fatalf("expected 2 items with no filters, got %d", len(result))
	}
}

func TestApplyFiltersQuality(t *testing.T) {
	items := []domain.SearchResult{
		{Name: "A", Enrichment: domain.SearchEnrichment{Quality: "1080p"}},
		{Name: "B", Enrichment: domain.SearchEnrichment{Quality: "720p"}},
		{Name: "C", Enrichment: domain.SearchEnrichment{Quality: "1080p"}},
	}
	result := applyFilters(items, domain.SearchFilters{Quality: []string{"1080p"}})
	if len(result) != 2 {
		t.Fatalf("expected 2 items matching 1080p, got %d", len(result))
	}
}

func TestApplyFiltersMinSeeders(t *testing.T) {
	items := []domain.SearchResult{
		{Name: "A", Seeders: 5},
		{Name: "B", Seeders: 50},
		{Name: "C", Seeders: 100},
	}
	result := applyFilters(items, domain.SearchFilters{MinSeeders: 10})
	if len(result) != 2 {
		t.Fatalf("expected 2 items with >= 10 seeders, got %d", len(result))
	}
}

func TestApplyFiltersContentTypeSeries(t *testing.T) {
	items := []domain.SearchResult{
		{Name: "Movie", Enrichment: domain.SearchEnrichment{IsSeries: false, ContentType: "movie"}},
		{Name: "Show", Enrichment: domain.SearchEnrichment{IsSeries: true, ContentType: "series"}},
	}
	result := applyFilters(items, domain.SearchFilters{ContentType: "series"})
	if len(result) != 1 || result[0].Name != "Show" {
		t.Fatalf("expected only series, got %v", result)
	}
}

func TestApplyFiltersContentTypeMovie(t *testing.T) {
	items := []domain.SearchResult{
		{Name: "Movie", Enrichment: domain.SearchEnrichment{IsSeries: false, ContentType: "movie"}},
		{Name: "Show", Enrichment: domain.SearchEnrichment{IsSeries: true, ContentType: "series"}},
	}
	result := applyFilters(items, domain.SearchFilters{ContentType: "movie"})
	if len(result) != 1 || result[0].Name != "Movie" {
		t.Fatalf("expected only movies, got %v", result)
	}
}

func TestApplyFiltersYearRange(t *testing.T) {
	items := []domain.SearchResult{
		{Name: "A", Enrichment: domain.SearchEnrichment{Year: 2019}},
		{Name: "B", Enrichment: domain.SearchEnrichment{Year: 2022}},
		{Name: "C", Enrichment: domain.SearchEnrichment{Year: 2024}},
	}
	result := applyFilters(items, domain.SearchFilters{YearFrom: 2020, YearTo: 2023})
	if len(result) != 1 || result[0].Name != "B" {
		t.Fatalf("expected only 2022 result, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// detectQuality
// ---------------------------------------------------------------------------

func TestDetectQuality(t *testing.T) {
	cases := []struct {
		input    string
		contains string
	}{
		{"movie 2160p hdr", "2160p"},
		{"movie 1080p web-dl h264", "1080p"},
		{"movie 720p bluray x265", "720p"},
		{"movie 480p dvdrip", "480p"},
		{"movie", ""},
	}
	for _, tc := range cases {
		got := detectQuality(tc.input)
		if tc.contains == "" {
			if got != "" {
				t.Errorf("detectQuality(%q) = %q, want empty", tc.input, got)
			}
		} else if !strings.Contains(got, tc.contains) {
			t.Errorf("detectQuality(%q) = %q, expected to contain %q", tc.input, got, tc.contains)
		}
	}
}

func TestDetectQualityMultiParts(t *testing.T) {
	got := detectQuality("movie 1080p bluray x265")
	if !strings.Contains(got, "1080p") || !strings.Contains(got, "BluRay") || !strings.Contains(got, "H.265") {
		t.Errorf("detectQuality: expected multi-part quality, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// detectSourceType
// ---------------------------------------------------------------------------

func TestDetectSourceType(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"movie remux", "Remux"},
		{"movie bluray", "BluRay"},
		{"movie web-dl", "WEB-DL"},
		{"movie webrip", "WEBRip"},
		{"movie dvdrip", "DVDRip"},
		{"plain movie", ""},
	}
	for _, tc := range cases {
		got := detectSourceType(tc.input)
		if got != tc.want {
			t.Errorf("detectSourceType(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// detectHDR
// ---------------------------------------------------------------------------

func TestDetectHDR(t *testing.T) {
	cases := []struct {
		input string
		hdr   bool
		dv    bool
	}{
		{"movie hdr10 ", true, false},
		{"movie dolby vision", false, true},
		{"movie hdr10+ dolby vision", true, true},
		{"plain movie", false, false},
	}
	for _, tc := range cases {
		hdr, dv := detectHDR(tc.input)
		if hdr != tc.hdr || dv != tc.dv {
			t.Errorf("detectHDR(%q) = (%v,%v), want (%v,%v)", tc.input, hdr, dv, tc.hdr, tc.dv)
		}
	}
}

// ---------------------------------------------------------------------------
// detectAudioChannels
// ---------------------------------------------------------------------------

func TestDetectAudioChannels(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"movie 7.1 atmos", "7.1"},
		{"movie 5.1 ac3", "5.1"},
		{"movie stereo", "2.0"},
		{"movie", ""},
	}
	for _, tc := range cases {
		got := detectAudioChannels(tc.input)
		if got != tc.want {
			t.Errorf("detectAudioChannels(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// detectContentType
// ---------------------------------------------------------------------------

func TestDetectContentType(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"dark s01e01 1080p", "series"},
		{"attack on titan anime", "anime"},
		{"inception 2010 1080p", "movie"},
	}
	for _, tc := range cases {
		meta := parseTitleMeta(tc.input)
		got := detectContentType(strings.ToLower(tc.input), meta)
		if got != tc.want {
			t.Errorf("detectContentType(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// enrichSearchResult
// ---------------------------------------------------------------------------

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

func TestEnrichSearchResultDetectsQualityComponents(t *testing.T) {
	item := enrichSearchResult(domain.SearchResult{Name: "Movie.2024.2160p.Remux.DTS.HDR10"})
	if !strings.Contains(item.Enrichment.Quality, "2160p") {
		t.Fatalf("expected 2160p in quality, got %q", item.Enrichment.Quality)
	}
	if item.Enrichment.SourceType != "Remux" {
		t.Fatalf("expected Remux source type, got %q", item.Enrichment.SourceType)
	}
	if !item.Enrichment.HDR {
		t.Fatal("expected HDR=true")
	}
}

func TestEnrichSearchResultDetectsMovie(t *testing.T) {
	item := enrichSearchResult(domain.SearchResult{Name: "Inception 2010 1080p BluRay"})
	if item.Enrichment.IsSeries {
		t.Fatal("expected IsSeries=false for movie")
	}
	if item.Enrichment.ContentType != "movie" {
		t.Fatalf("expected contentType=movie, got %q", item.Enrichment.ContentType)
	}
}

func TestEnrichSearchResultDescription(t *testing.T) {
	item := enrichSearchResult(domain.SearchResult{
		Name:      "Test 1080p WEB-DL RUS",
		SizeBytes: 2 * 1024 * 1024 * 1024,
		Source:    "piratebay",
	})
	if item.Enrichment.Description == "" {
		t.Fatal("expected non-empty description")
	}
}

// ---------------------------------------------------------------------------
// relevanceScoreForResult
// ---------------------------------------------------------------------------

func TestRelevanceScorePrefersMatchingEpisode(t *testing.T) {
	query := parseTitleMeta("The Rookie S01E02")
	profile := domain.DefaultSearchRankingProfile()
	match := relevanceScoreForResult(query, profile, domain.SearchResult{Name: "The Rookie S01E02 1080p", Seeders: 20})
	mismatch := relevanceScoreForResult(query, profile, domain.SearchResult{Name: "The Rookie S01E08 1080p", Seeders: 20})
	if match <= mismatch {
		t.Fatalf("expected matching episode score > mismatch, got %.2f <= %.2f", match, mismatch)
	}
}

func TestRelevanceScorePrefersMatchingYear(t *testing.T) {
	query := parseTitleMeta("Movie 2024")
	profile := domain.DefaultSearchRankingProfile()
	match := relevanceScoreForResult(query, profile, domain.SearchResult{Name: "Movie 2024 1080p", Seeders: 20})
	mismatch := relevanceScoreForResult(query, profile, domain.SearchResult{Name: "Movie 2020 1080p", Seeders: 20})
	if match <= mismatch {
		t.Fatalf("expected matching year score > mismatch, got %.2f <= %.2f", match, mismatch)
	}
}

func TestRelevanceScorePrefersFullTokenMatch(t *testing.T) {
	query := parseTitleMeta("Breaking Bad")
	profile := domain.DefaultSearchRankingProfile()
	full := relevanceScoreForResult(query, profile, domain.SearchResult{Name: "Breaking Bad 1080p", Seeders: 10})
	partial := relevanceScoreForResult(query, profile, domain.SearchResult{Name: "Breaking something else", Seeders: 10})
	if full <= partial {
		t.Fatalf("expected full match > partial, got %.2f <= %.2f", full, partial)
	}
}

func TestRelevanceScoreHigherSeedersBoost(t *testing.T) {
	query := parseTitleMeta("Test")
	profile := domain.DefaultSearchRankingProfile()
	highSeed := relevanceScoreForResult(query, profile, domain.SearchResult{Name: "Test", Seeders: 1000})
	lowSeed := relevanceScoreForResult(query, profile, domain.SearchResult{Name: "Test", Seeders: 1})
	if highSeed <= lowSeed {
		t.Fatalf("expected higher seeders to boost score, got %.2f <= %.2f", highSeed, lowSeed)
	}
}

func TestRelevanceScoreHasInfoHashBonus(t *testing.T) {
	query := parseTitleMeta("Test")
	profile := domain.DefaultSearchRankingProfile()
	withHash := relevanceScoreForResult(query, profile, domain.SearchResult{Name: "Test", InfoHash: "abc123"})
	withoutHash := relevanceScoreForResult(query, profile, domain.SearchResult{Name: "Test"})
	if withHash <= withoutHash {
		t.Fatalf("expected infoHash bonus, got %.2f <= %.2f", withHash, withoutHash)
	}
}

// ---------------------------------------------------------------------------
// normalizeInfoHash / extractInfoHashFromMagnet
// ---------------------------------------------------------------------------

func TestNormalizeInfoHash(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"ABCDEF1234", "abcdef1234"},
		{"urn:btih:ABCDEF1234", "abcdef1234"},
		{"", ""},
		{" ABCDEF1234 ", "abcdef1234"},
	}
	for _, tc := range cases {
		got := normalizeInfoHash(tc.input)
		if got != tc.want {
			t.Errorf("normalizeInfoHash(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestExtractInfoHashFromMagnet(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"magnet:?xt=urn:btih:ABCDEF1234&dn=Test", "abcdef1234"},
		{"magnet:?xt=urn:btih:abc123", "abc123"},
		{"", ""},
		{"not a magnet", ""},
		{"magnet:?dn=Test", ""},
	}
	for _, tc := range cases {
		got := extractInfoHashFromMagnet(tc.input)
		if got != tc.want {
			t.Errorf("extractInfoHashFromMagnet(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// humanBytes
// ---------------------------------------------------------------------------

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KiB"},
		{1048576, "1.0 MiB"},
		{1073741824, "1.0 GiB"},
		{1099511627776, "1.0 TiB"},
		{-1, "0 B"},
	}
	for _, tc := range cases {
		got := humanBytes(tc.input)
		if got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// uniqueStrings
// ---------------------------------------------------------------------------

func TestUniqueStrings(t *testing.T) {
	cases := []struct {
		input []string
		want  int
	}{
		{[]string{"a", "b", "a"}, 2},
		{[]string{"A", "a"}, 1},
		{[]string{}, 0},
		{nil, 0},
		{[]string{"", " ", "a"}, 1},
	}
	for _, tc := range cases {
		got := uniqueStrings(tc.input)
		if len(got) != tc.want {
			t.Errorf("uniqueStrings(%v) has %d items, want %d", tc.input, len(got), tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// compareTime
// ---------------------------------------------------------------------------

func TestCompareTime(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Hour)
	if compareTime(nil, nil) != 0 {
		t.Fatal("nil,nil should be 0")
	}
	if compareTime(nil, &now) != -1 {
		t.Fatal("nil should be less than non-nil")
	}
	if compareTime(&now, nil) != 1 {
		t.Fatal("non-nil should be greater than nil")
	}
	if compareTime(&now, &later) != -1 {
		t.Fatal("earlier should be less than later")
	}
	if compareTime(&later, &now) != 1 {
		t.Fatal("later should be greater than earlier")
	}
}
