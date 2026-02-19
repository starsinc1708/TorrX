package bittorrentindex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"torrentstream/searchservice/internal/domain"
)

// ---------------------------------------------------------------------------
// parseAPIItems
// ---------------------------------------------------------------------------

func TestParseAPIItems(t *testing.T) {
	payload := []byte(`[
		{"name":"Ubuntu ISO","info_hash":"ABCDEF1234567890ABCDEF1234567890ABCDEF12","size":"2147483648","seeders":"1200","leechers":"80","added":"1700000000"}
	]`)

	items, err := parseAPIItems(payload)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("unexpected items count: %d", len(items))
	}
}

func TestParseAPIItemsMultiple(t *testing.T) {
	payload := []byte(`[
		{"name":"Ubuntu ISO","info_hash":"AAAA","size":"100","seeders":"10","leechers":"5","added":"1700000000"},
		{"name":"Fedora ISO","info_hash":"BBBB","size":"200","seeders":"20","leechers":"10","added":"1700000001"}
	]`)
	items, err := parseAPIItems(payload)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Name != "Ubuntu ISO" {
		t.Fatalf("unexpected first item name: %s", items[0].Name)
	}
	if items[1].Name != "Fedora ISO" {
		t.Fatalf("unexpected second item name: %s", items[1].Name)
	}
}

func TestParseAPIItemsEmpty(t *testing.T) {
	payload := []byte(`[]`)
	items, err := parseAPIItems(payload)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}
}

func TestParseAPIItemsSingleObjectNoResults(t *testing.T) {
	// apibay returns {"name":"No results returned","info_hash":""} for no results
	payload := []byte(`{"name":"No results returned","info_hash":""}`)
	items, err := parseAPIItems(payload)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 items for single-object response, got %d", len(items))
	}
}

func TestParseAPIItemsInvalidJSON(t *testing.T) {
	payload := []byte(`not json`)
	_, err := parseAPIItems(payload)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseAPIItemsHTMLResponse(t *testing.T) {
	payload := []byte(`<html><body>error</body></html>`)
	_, err := parseAPIItems(payload)
	if err == nil {
		t.Fatal("expected error for HTML response")
	}
}

// ---------------------------------------------------------------------------
// toResult
// ---------------------------------------------------------------------------

func TestToResultBuildsMagnet(t *testing.T) {
	provider := NewProvider(Config{})
	result, ok := provider.toResult(apiItem{
		Name:     "Ubuntu ISO",
		InfoHash: "ABCDEF1234567890ABCDEF1234567890ABCDEF12",
		Size:     "2147483648",
		Seeders:  "1200",
		Leechers: "80",
		Added:    "1700000000",
	})
	if !ok {
		t.Fatal("expected valid result")
	}
	if result.InfoHash == "" || result.Magnet == "" {
		t.Fatalf("expected infoHash and magnet, got %#v", result)
	}
	if result.Seeders != 1200 {
		t.Fatalf("unexpected seeders: %d", result.Seeders)
	}
}

func TestToResultAllFields(t *testing.T) {
	provider := NewProvider(Config{})
	result, ok := provider.toResult(apiItem{
		ID:       "12345",
		Name:     "Test Torrent",
		InfoHash: "abcdef1234567890abcdef1234567890abcdef12",
		Size:     "1073741824",
		Seeders:  "500",
		Leechers: "25",
		Added:    "1700000000",
	})
	if !ok {
		t.Fatal("expected valid result")
	}
	if result.Name != "Test Torrent" {
		t.Fatalf("unexpected name: %s", result.Name)
	}
	if result.InfoHash != "abcdef1234567890abcdef1234567890abcdef12" {
		t.Fatalf("unexpected infoHash: %s", result.InfoHash)
	}
	if !strings.HasPrefix(result.Magnet, "magnet:?xt=urn:btih:") {
		t.Fatalf("unexpected magnet format: %s", result.Magnet)
	}
	if result.SizeBytes != 1073741824 {
		t.Fatalf("unexpected size: %d", result.SizeBytes)
	}
	if result.Seeders != 500 {
		t.Fatalf("unexpected seeders: %d", result.Seeders)
	}
	if result.Leechers != 25 {
		t.Fatalf("unexpected leechers: %d", result.Leechers)
	}
	if result.Source != "piratebay" {
		t.Fatalf("unexpected source: %s", result.Source)
	}
	if result.Tracker != "thepiratebay.org" {
		t.Fatalf("unexpected tracker: %s", result.Tracker)
	}
	if result.PublishedAt == nil {
		t.Fatal("expected publishedAt to be set")
	}
	if result.PageURL == "" {
		t.Fatal("expected pageURL to be set")
	}
}

func TestToResultEmptyInfoHash(t *testing.T) {
	provider := NewProvider(Config{})
	_, ok := provider.toResult(apiItem{
		Name:     "Test",
		InfoHash: "",
	})
	if ok {
		t.Fatal("expected false for empty infoHash")
	}
}

func TestToResultEmptyName(t *testing.T) {
	provider := NewProvider(Config{})
	_, ok := provider.toResult(apiItem{
		Name:     "",
		InfoHash: "abcdef1234567890abcdef1234567890abcdef12",
	})
	if ok {
		t.Fatal("expected false for empty name")
	}
}

func TestToResultNoResultsSentinel(t *testing.T) {
	provider := NewProvider(Config{})
	_, ok := provider.toResult(apiItem{
		Name:     "No results returned",
		InfoHash: "0000000000000000000000000000000000000000",
	})
	if ok {
		t.Fatal("expected false for 'no results returned' sentinel")
	}
}

func TestToResultNoResultsSentinelCaseInsensitive(t *testing.T) {
	provider := NewProvider(Config{})
	_, ok := provider.toResult(apiItem{
		Name:     "NO RESULTS RETURNED for your query",
		InfoHash: "0000000000000000000000000000000000000000",
	})
	if ok {
		t.Fatal("expected false for case-insensitive 'no results returned'")
	}
}

func TestToResultInvalidSeeders(t *testing.T) {
	provider := NewProvider(Config{})
	result, ok := provider.toResult(apiItem{
		Name:     "Test",
		InfoHash: "abcdef1234567890abcdef1234567890abcdef12",
		Seeders:  "not_a_number",
	})
	if !ok {
		t.Fatal("expected result even with bad seeders")
	}
	if result.Seeders != 0 {
		t.Fatalf("expected 0 seeders for invalid string, got %d", result.Seeders)
	}
}

func TestToResultInvalidSize(t *testing.T) {
	provider := NewProvider(Config{})
	result, ok := provider.toResult(apiItem{
		Name:     "Test",
		InfoHash: "abcdef1234567890abcdef1234567890abcdef12",
		Size:     "abc",
	})
	if !ok {
		t.Fatal("expected result even with bad size")
	}
	if result.SizeBytes != 0 {
		t.Fatalf("expected 0 sizeBytes for invalid string, got %d", result.SizeBytes)
	}
}

func TestToResultZeroTimestamp(t *testing.T) {
	provider := NewProvider(Config{})
	result, ok := provider.toResult(apiItem{
		Name:     "Test",
		InfoHash: "abcdef1234567890abcdef1234567890abcdef12",
		Added:    "0",
	})
	if !ok {
		t.Fatal("expected valid result")
	}
	if result.PublishedAt != nil {
		t.Fatal("expected nil publishedAt for zero timestamp")
	}
}

func TestToResultNegativeTimestamp(t *testing.T) {
	provider := NewProvider(Config{})
	result, ok := provider.toResult(apiItem{
		Name:     "Test",
		InfoHash: "abcdef1234567890abcdef1234567890abcdef12",
		Added:    "-100",
	})
	if !ok {
		t.Fatal("expected valid result")
	}
	if result.PublishedAt != nil {
		t.Fatal("expected nil publishedAt for negative timestamp")
	}
}

func TestToResultEmptyTimestamp(t *testing.T) {
	provider := NewProvider(Config{})
	result, ok := provider.toResult(apiItem{
		Name:     "Test",
		InfoHash: "abcdef1234567890abcdef1234567890abcdef12",
		Added:    "",
	})
	if !ok {
		t.Fatal("expected valid result")
	}
	if result.PublishedAt != nil {
		t.Fatal("expected nil publishedAt for empty timestamp")
	}
}

func TestToResultPageURLWithID(t *testing.T) {
	provider := NewProvider(Config{Endpoint: "https://apibay.org/q.php"})
	result, ok := provider.toResult(apiItem{
		ID:       "12345",
		Name:     "Test",
		InfoHash: "abcdef1234567890abcdef1234567890abcdef12",
	})
	if !ok {
		t.Fatal("expected valid result")
	}
	if !strings.Contains(result.PageURL, "t.php") || !strings.Contains(result.PageURL, "id=12345") {
		t.Fatalf("unexpected pageURL: %s", result.PageURL)
	}
}

func TestToResultPageURLNoID(t *testing.T) {
	provider := NewProvider(Config{})
	result, ok := provider.toResult(apiItem{
		Name:     "Test",
		InfoHash: "abcdef1234567890abcdef1234567890abcdef12",
	})
	if !ok {
		t.Fatal("expected valid result")
	}
	if result.PageURL != "" {
		t.Fatalf("expected empty pageURL when no ID, got: %s", result.PageURL)
	}
}

func TestToResultMagnetContainsTrackers(t *testing.T) {
	trackers := []string{"udp://tracker1:1337", "udp://tracker2:6969"}
	provider := NewProvider(Config{Trackers: trackers})
	result, ok := provider.toResult(apiItem{
		Name:     "Test",
		InfoHash: "abcdef1234567890abcdef1234567890abcdef12",
	})
	if !ok {
		t.Fatal("expected valid result")
	}
	if !strings.Contains(result.Magnet, "tr=") {
		t.Fatalf("expected magnet to contain trackers: %s", result.Magnet)
	}
}

func TestToResultInfoHashNormalized(t *testing.T) {
	provider := NewProvider(Config{})
	result, ok := provider.toResult(apiItem{
		Name:     "Test",
		InfoHash: "ABCDEF1234567890ABCDEF1234567890ABCDEF12",
	})
	if !ok {
		t.Fatal("expected valid result")
	}
	if result.InfoHash != "abcdef1234567890abcdef1234567890abcdef12" {
		t.Fatalf("expected lowercase infoHash, got: %s", result.InfoHash)
	}
}

// ---------------------------------------------------------------------------
// atoi / atoi64 / parseUnixTS helpers
// ---------------------------------------------------------------------------

func TestAtoi(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"123", 123},
		{"0", 0},
		{"-5", -5},
		{"", 0},
		{"abc", 0},
		{" 42 ", 42},
	}
	for _, tc := range cases {
		got := atoi(tc.input)
		if got != tc.want {
			t.Errorf("atoi(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestAtoi64(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		{"1073741824", 1073741824},
		{"0", 0},
		{"-1", -1},
		{"", 0},
		{"abc", 0},
		{" 999 ", 999},
	}
	for _, tc := range cases {
		got := atoi64(tc.input)
		if got != tc.want {
			t.Errorf("atoi64(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestParseUnixTS(t *testing.T) {
	cases := []struct {
		input string
		isNil bool
	}{
		{"1700000000", false},
		{"0", true},
		{"-1", true},
		{"", true},
		{"abc", true},
		{" 1700000000 ", false},
	}
	for _, tc := range cases {
		got := parseUnixTS(tc.input)
		if tc.isNil && got != nil {
			t.Errorf("parseUnixTS(%q) = %v, expected nil", tc.input, got)
		}
		if !tc.isNil && got == nil {
			t.Errorf("parseUnixTS(%q) = nil, expected non-nil", tc.input)
		}
	}
}

// ---------------------------------------------------------------------------
// NewProvider defaults
// ---------------------------------------------------------------------------

func TestNewProviderDefaults(t *testing.T) {
	p := NewProvider(Config{})
	if p.endpoint != defaultEndpoint {
		t.Fatalf("expected default endpoint %s, got %s", defaultEndpoint, p.endpoint)
	}
	if p.userAgent != defaultUserAgent {
		t.Fatalf("expected default userAgent %s, got %s", defaultUserAgent, p.userAgent)
	}
	if len(p.trackers) != len(defaultTrackers) {
		t.Fatalf("expected %d default trackers, got %d", len(defaultTrackers), len(p.trackers))
	}
	if p.client == nil {
		t.Fatal("expected non-nil HTTP client")
	}
}

func TestNewProviderCustomConfig(t *testing.T) {
	client := &http.Client{Timeout: 5 * time.Second}
	trackers := []string{"udp://custom:1337"}
	p := NewProvider(Config{
		Endpoint:  "https://custom.api/q.php",
		UserAgent: "custom-agent/2.0",
		Trackers:  trackers,
		Client:    client,
	})
	if p.endpoint != "https://custom.api/q.php" {
		t.Fatalf("unexpected endpoint: %s", p.endpoint)
	}
	if p.userAgent != "custom-agent/2.0" {
		t.Fatalf("unexpected userAgent: %s", p.userAgent)
	}
	if len(p.trackers) != 1 || p.trackers[0] != "udp://custom:1337" {
		t.Fatalf("unexpected trackers: %v", p.trackers)
	}
	if p.client != client {
		t.Fatal("expected custom client")
	}
}

func TestNewProviderWhitespaceEndpoint(t *testing.T) {
	p := NewProvider(Config{Endpoint: "  "})
	if p.endpoint != defaultEndpoint {
		t.Fatalf("expected default endpoint for whitespace input, got %s", p.endpoint)
	}
}

// ---------------------------------------------------------------------------
// Name / Info
// ---------------------------------------------------------------------------

func TestProviderName(t *testing.T) {
	p := NewProvider(Config{})
	if p.Name() != "piratebay" {
		t.Fatalf("unexpected name: %s", p.Name())
	}
}

func TestProviderInfo(t *testing.T) {
	p := NewProvider(Config{})
	info := p.Info()
	if info.Name != "piratebay" {
		t.Fatalf("unexpected info name: %s", info.Name)
	}
	if info.Label != "The Pirate Bay" {
		t.Fatalf("unexpected label: %s", info.Label)
	}
	if info.Kind != "index" {
		t.Fatalf("unexpected kind: %s", info.Kind)
	}
	if !info.Enabled {
		t.Fatal("expected enabled=true")
	}
}

// ---------------------------------------------------------------------------
// Search (integration with httptest)
// ---------------------------------------------------------------------------

func TestSearchHappyPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "ubuntu" {
			t.Errorf("unexpected query: %s", r.URL.Query().Get("q"))
		}
		if r.Header.Get("User-Agent") != "test-agent" {
			t.Errorf("unexpected user-agent: %s", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"id":"1","name":"Ubuntu 22.04","info_hash":"aaaa1234567890abcdef1234567890abcdef1234","size":"2000000000","seeders":"500","leechers":"20","added":"1700000000"},
			{"id":"2","name":"Ubuntu 20.04","info_hash":"bbbb1234567890abcdef1234567890abcdef1234","size":"1800000000","seeders":"300","leechers":"10","added":"1700000001"}
		]`))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL, UserAgent: "test-agent"})
	results, err := p.Search(context.Background(), domain.SearchRequest{Query: "ubuntu"})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Name != "Ubuntu 22.04" {
		t.Fatalf("unexpected first result name: %s", results[0].Name)
	}
	if results[0].Seeders != 500 {
		t.Fatalf("unexpected seeders: %d", results[0].Seeders)
	}
	if results[0].Magnet == "" {
		t.Fatal("expected magnet link")
	}
	if results[0].Source != "piratebay" {
		t.Fatalf("unexpected source: %s", results[0].Source)
	}
}

func TestSearchRespectsLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"name":"A","info_hash":"aaaa1234567890abcdef1234567890abcdef1234","size":"100","seeders":"1","leechers":"0"},
			{"name":"B","info_hash":"bbbb1234567890abcdef1234567890abcdef1234","size":"200","seeders":"2","leechers":"0"},
			{"name":"C","info_hash":"cccc1234567890abcdef1234567890abcdef1234","size":"300","seeders":"3","leechers":"0"}
		]`))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL})
	results, err := p.Search(context.Background(), domain.SearchRequest{Query: "test", Limit: 2})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (limit=2), got %d", len(results))
	}
}

func TestSearchDefaultLimit(t *testing.T) {
	// Generate 60 items (more than default limit of 50)
	items := make([]string, 60)
	for i := range items {
		hash := strings.Repeat("a", 39) + string(rune('a'+i%26))
		items[i] = `{"name":"Item ` + string(rune('A'+i%26)) + `","info_hash":"` + hash + `","size":"100","seeders":"1","leechers":"0"}`
	}
	payload := "[" + strings.Join(items, ",") + "]"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(payload))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL})
	results, err := p.Search(context.Background(), domain.SearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) > 50 {
		t.Fatalf("expected at most 50 results (default limit), got %d", len(results))
	}
}

func TestSearchNoResults(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":"0","name":"No results returned","info_hash":"0000000000000000000000000000000000000000","size":"0","seeders":"0","leechers":"0","added":"0","status":"member","category":"100","imdb":""}]`))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL})
	results, err := p.Search(context.Background(), domain.SearchRequest{Query: "nonexistent"})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for 'no results returned', got %d", len(results))
	}
}

func TestSearchEmptyArray(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL})
	results, err := p.Search(context.Background(), domain.SearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestSearchHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL})
	_, err := p.Search(context.Background(), domain.SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected error to contain status code: %v", err)
	}
}

func TestSearchHTTP403(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("blocked"))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL})
	_, err := p.Search(context.Background(), domain.SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error for HTTP 403")
	}
}

func TestSearchContextCancelled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	p := NewProvider(Config{Endpoint: ts.URL})
	_, err := p.Search(ctx, domain.SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestSearchInvalidEndpoint(t *testing.T) {
	p := NewProvider(Config{Endpoint: "://bad-url"})
	_, err := p.Search(context.Background(), domain.SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error for invalid endpoint")
	}
}

func TestSearchUnreachableEndpoint(t *testing.T) {
	p := NewProvider(Config{
		Endpoint: "http://192.0.2.1:1/q.php", // TEST-NET, unreachable
		Client:   &http.Client{Timeout: 100 * time.Millisecond},
	})
	_, err := p.Search(context.Background(), domain.SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error for unreachable endpoint")
	}
}

func TestSearchSkipsInvalidItems(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"name":"Valid","info_hash":"aaaa1234567890abcdef1234567890abcdef1234","size":"100","seeders":"1","leechers":"0"},
			{"name":"","info_hash":"bbbb1234567890abcdef1234567890abcdef1234","size":"200","seeders":"2","leechers":"0"},
			{"name":"Also Valid","info_hash":"cccc1234567890abcdef1234567890abcdef1234","size":"300","seeders":"3","leechers":"0"}
		]`))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL})
	results, err := p.Search(context.Background(), domain.SearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 valid results (skipping empty name), got %d", len(results))
	}
}

func TestSearchQueryEncoded(t *testing.T) {
	var capturedQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("q")
		w.Write([]byte(`[]`))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL})
	p.Search(context.Background(), domain.SearchRequest{Query: "hello world & test"})
	if capturedQuery != "hello world & test" {
		t.Fatalf("unexpected query encoding: %s", capturedQuery)
	}
}

func TestSearchTrimsQueryWhitespace(t *testing.T) {
	var capturedQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("q")
		w.Write([]byte(`[]`))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL})
	p.Search(context.Background(), domain.SearchRequest{Query: "  ubuntu  "})
	if capturedQuery != "ubuntu" {
		t.Fatalf("expected trimmed query, got: %q", capturedQuery)
	}
}

// ---------------------------------------------------------------------------
// Interface compliance
// ---------------------------------------------------------------------------

func TestProviderImplementsInterface(t *testing.T) {
	var _ interface {
		Name() string
		Info() domain.ProviderInfo
		Search(context.Context, domain.SearchRequest) ([]domain.SearchResult, error)
	} = (*Provider)(nil)
}
