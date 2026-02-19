package dht

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
// extractMagnets
// ---------------------------------------------------------------------------

func TestExtractMagnets(t *testing.T) {
	htmlPayload := `
<html><body>
<a href="magnet:?xt=urn:btih:ABCDEF1234567890ABCDEF1234567890ABCDEF12&amp;dn=Ubuntu+ISO&amp;xl=2147483648">link</a>
</body></html>`

	magnets := extractMagnets(htmlPayload)
	if len(magnets) != 1 {
		t.Fatalf("expected 1 magnet, got %d", len(magnets))
	}
	if magnets[0] == "" {
		t.Fatal("empty magnet")
	}
}

func TestExtractMagnetsMultiple(t *testing.T) {
	html := `
<div>
<a href="magnet:?xt=urn:btih:AAAA1234567890ABCDEF1234567890ABCDEF1234&dn=First">first</a>
<a href="magnet:?xt=urn:btih:BBBB1234567890ABCDEF1234567890ABCDEF1234&dn=Second">second</a>
</div>`
	magnets := extractMagnets(html)
	if len(magnets) != 2 {
		t.Fatalf("expected 2 magnets, got %d", len(magnets))
	}
}

func TestExtractMagnetsNoMagnets(t *testing.T) {
	html := `<html><body><p>No torrents found</p></body></html>`
	magnets := extractMagnets(html)
	if len(magnets) != 0 {
		t.Fatalf("expected 0 magnets, got %d", len(magnets))
	}
}

func TestExtractMagnetsEmptyString(t *testing.T) {
	magnets := extractMagnets("")
	if len(magnets) != 0 {
		t.Fatalf("expected 0 magnets for empty string, got %d", len(magnets))
	}
}

func TestExtractMagnetsHTMLEntities(t *testing.T) {
	// HTML entities like &amp; should be unescaped
	html := `<a href="magnet:?xt=urn:btih:AAAA1234567890ABCDEF1234567890ABCDEF1234&amp;dn=Test&amp;tr=udp://tracker:1337">link</a>`
	magnets := extractMagnets(html)
	if len(magnets) != 1 {
		t.Fatalf("expected 1 magnet, got %d", len(magnets))
	}
	if strings.Contains(magnets[0], "&amp;") {
		t.Fatalf("expected HTML entities to be unescaped: %s", magnets[0])
	}
}

func TestExtractMagnetsHash32Chars(t *testing.T) {
	// 32-char base32 info hash
	html := `<a href="magnet:?xt=urn:btih:ABCDEFGHIJKLMNOPQRSTUVWXYZ234567&dn=Test">link</a>`
	magnets := extractMagnets(html)
	if len(magnets) != 1 {
		t.Fatalf("expected 1 magnet with 32-char hash, got %d", len(magnets))
	}
}

// ---------------------------------------------------------------------------
// magnetToResult
// ---------------------------------------------------------------------------

func TestMagnetToResult(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:ABCDEF1234567890ABCDEF1234567890ABCDEF12&dn=Ubuntu+ISO&xl=2147483648"
	result, ok := magnetToResult(magnet)
	if !ok {
		t.Fatal("expected parsed result")
	}
	if result.InfoHash == "" || result.Name == "" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.SizeBytes != 2147483648 {
		t.Fatalf("unexpected size: %d", result.SizeBytes)
	}
}

func TestMagnetToResultAllFields(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:abcdef1234567890abcdef1234567890abcdef12&dn=Test+Torrent&xl=1024"
	result, ok := magnetToResult(magnet)
	if !ok {
		t.Fatal("expected valid result")
	}
	if result.Name != "Test Torrent" {
		t.Fatalf("unexpected name: %s", result.Name)
	}
	if result.InfoHash != "abcdef1234567890abcdef1234567890abcdef12" {
		t.Fatalf("unexpected infoHash: %s", result.InfoHash)
	}
	if result.Magnet != magnet {
		t.Fatalf("expected original magnet to be preserved: %s", result.Magnet)
	}
	if result.SizeBytes != 1024 {
		t.Fatalf("unexpected size: %d", result.SizeBytes)
	}
	if result.Source != "dht" {
		t.Fatalf("unexpected source: %s", result.Source)
	}
	if result.Tracker != "btdig.com" {
		t.Fatalf("unexpected tracker: %s", result.Tracker)
	}
	if result.Seeders != 0 {
		t.Fatalf("expected seeders=0 for DHT, got: %d", result.Seeders)
	}
	if result.Leechers != 0 {
		t.Fatalf("expected leechers=0 for DHT, got: %d", result.Leechers)
	}
}

func TestMagnetToResultNoName(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:abcdef1234567890abcdef1234567890abcdef12"
	result, ok := magnetToResult(magnet)
	if !ok {
		t.Fatal("expected valid result")
	}
	if !strings.HasPrefix(result.Name, "DHT result ") {
		t.Fatalf("expected fallback name starting with 'DHT result ', got: %s", result.Name)
	}
}

func TestMagnetToResultNoSize(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:abcdef1234567890abcdef1234567890abcdef12&dn=Test"
	result, ok := magnetToResult(magnet)
	if !ok {
		t.Fatal("expected valid result")
	}
	if result.SizeBytes != 0 {
		t.Fatalf("expected 0 size when xl missing, got: %d", result.SizeBytes)
	}
}

func TestMagnetToResultInvalidSize(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:abcdef1234567890abcdef1234567890abcdef12&dn=Test&xl=notanumber"
	result, ok := magnetToResult(magnet)
	if !ok {
		t.Fatal("expected valid result")
	}
	if result.SizeBytes != 0 {
		t.Fatalf("expected 0 for invalid xl, got: %d", result.SizeBytes)
	}
}

func TestMagnetToResultNoInfoHash(t *testing.T) {
	magnet := "magnet:?dn=Test"
	_, ok := magnetToResult(magnet)
	if ok {
		t.Fatal("expected false for magnet without info hash")
	}
}

func TestMagnetToResultEmptyInfoHash(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:&dn=Test"
	_, ok := magnetToResult(magnet)
	if ok {
		t.Fatal("expected false for empty info hash")
	}
}

func TestMagnetToResultInvalidURL(t *testing.T) {
	_, ok := magnetToResult("not a url at all ://")
	if ok {
		t.Fatal("expected false for invalid URL")
	}
}

func TestMagnetToResultNotMagnetScheme(t *testing.T) {
	_, ok := magnetToResult("http://example.com?xt=urn:btih:abcdef1234")
	if ok {
		t.Fatal("expected false for non-magnet scheme")
	}
}

func TestMagnetToResultEmptyString(t *testing.T) {
	_, ok := magnetToResult("")
	if ok {
		t.Fatal("expected false for empty magnet")
	}
}

func TestMagnetToResultWhitespace(t *testing.T) {
	magnet := "  magnet:?xt=urn:btih:abcdef1234567890abcdef1234567890abcdef12&dn=Test  "
	result, ok := magnetToResult(magnet)
	if !ok {
		t.Fatal("expected valid result (whitespace trimmed)")
	}
	if result.Name != "Test" {
		t.Fatalf("unexpected name: %s", result.Name)
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
	if p.client == nil {
		t.Fatal("expected non-nil HTTP client")
	}
}

func TestNewProviderCustomConfig(t *testing.T) {
	client := &http.Client{Timeout: 3 * time.Second}
	p := NewProvider(Config{
		Endpoint:  "https://custom.btdig.com/search",
		UserAgent: "custom-agent/1.0",
		Client:    client,
	})
	if p.endpoint != "https://custom.btdig.com/search" {
		t.Fatalf("unexpected endpoint: %s", p.endpoint)
	}
	if p.userAgent != "custom-agent/1.0" {
		t.Fatalf("unexpected userAgent: %s", p.userAgent)
	}
	if p.client != client {
		t.Fatal("expected custom client")
	}
}

func TestNewProviderWhitespaceEndpoint(t *testing.T) {
	p := NewProvider(Config{Endpoint: "   "})
	if p.endpoint != defaultEndpoint {
		t.Fatalf("expected default endpoint for whitespace, got %s", p.endpoint)
	}
}

// ---------------------------------------------------------------------------
// Name / Info
// ---------------------------------------------------------------------------

func TestProviderName(t *testing.T) {
	p := NewProvider(Config{})
	if p.Name() != "dht" {
		t.Fatalf("unexpected name: %s", p.Name())
	}
}

func TestProviderInfo(t *testing.T) {
	p := NewProvider(Config{})
	info := p.Info()
	if info.Name != "dht" {
		t.Fatalf("unexpected info name: %s", info.Name)
	}
	if info.Label != "DHT Index" {
		t.Fatalf("unexpected label: %s", info.Label)
	}
	if info.Kind != "dht" {
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
		w.Write([]byte(`<html><body>
<a href="magnet:?xt=urn:btih:aaaa1234567890abcdef1234567890abcdef1234&dn=Ubuntu+22.04&xl=2000000000">Ubuntu 22.04</a>
<a href="magnet:?xt=urn:btih:bbbb1234567890abcdef1234567890abcdef1234&dn=Ubuntu+20.04&xl=1800000000">Ubuntu 20.04</a>
</body></html>`))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL})
	results, err := p.Search(context.Background(), domain.SearchRequest{Query: "ubuntu"})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Source != "dht" {
		t.Fatalf("unexpected source: %s", results[0].Source)
	}
	if results[0].Seeders != 0 {
		t.Fatalf("expected seeders=0 for DHT results, got: %d", results[0].Seeders)
	}
}

func TestSearchDeduplicates(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Same info hash twice
		w.Write([]byte(`<html><body>
<a href="magnet:?xt=urn:btih:aaaa1234567890abcdef1234567890abcdef1234&dn=Duplicate+1">link1</a>
<a href="magnet:?xt=urn:btih:aaaa1234567890abcdef1234567890abcdef1234&dn=Duplicate+2">link2</a>
<a href="magnet:?xt=urn:btih:bbbb1234567890abcdef1234567890abcdef1234&dn=Different">link3</a>
</body></html>`))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL})
	results, err := p.Search(context.Background(), domain.SearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (dedup by info hash), got %d", len(results))
	}
}

func TestSearchRespectsLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>
<a href="magnet:?xt=urn:btih:aaaa1234567890abcdef1234567890abcdef1234&dn=A">A</a>
<a href="magnet:?xt=urn:btih:bbbb1234567890abcdef1234567890abcdef1234&dn=B">B</a>
<a href="magnet:?xt=urn:btih:cccc1234567890abcdef1234567890abcdef1234&dn=C">C</a>
</body></html>`))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL})
	results, err := p.Search(context.Background(), domain.SearchRequest{Query: "test", Limit: 1})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result (limit=1), got %d", len(results))
	}
}

func TestSearchNoResults(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><p>No results found</p></body></html>`))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL})
	results, err := p.Search(context.Background(), domain.SearchRequest{Query: "nonexistent"})
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
		w.Write([]byte("internal error"))
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

func TestSearchContextCancelled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := NewProvider(Config{Endpoint: ts.URL})
	_, err := p.Search(ctx, domain.SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestSearchInvalidEndpoint(t *testing.T) {
	p := NewProvider(Config{Endpoint: "://bad"})
	_, err := p.Search(context.Background(), domain.SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error for invalid endpoint")
	}
}

func TestSearchUnreachableEndpoint(t *testing.T) {
	p := NewProvider(Config{
		Endpoint: "http://192.0.2.1:1/search",
		Client:   &http.Client{Timeout: 100 * time.Millisecond},
	})
	_, err := p.Search(context.Background(), domain.SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error for unreachable endpoint")
	}
}

func TestSearchSetsQueryAndOrderParams(t *testing.T) {
	var capturedURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Write([]byte(`<html></html>`))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL})
	p.Search(context.Background(), domain.SearchRequest{Query: "linux"})
	if !strings.Contains(capturedURL, "q=linux") {
		t.Fatalf("expected q=linux in URL: %s", capturedURL)
	}
	if !strings.Contains(capturedURL, "order=0") {
		t.Fatalf("expected order=0 in URL: %s", capturedURL)
	}
}

func TestSearchSetsUserAgent(t *testing.T) {
	var capturedUA string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUA = r.Header.Get("User-Agent")
		w.Write([]byte(`<html></html>`))
	}))
	defer ts.Close()

	p := NewProvider(Config{Endpoint: ts.URL, UserAgent: "custom-ua/1.0"})
	p.Search(context.Background(), domain.SearchRequest{Query: "test"})
	if capturedUA != "custom-ua/1.0" {
		t.Fatalf("expected custom user-agent, got: %s", capturedUA)
	}
}

func TestSearchTrimsQuery(t *testing.T) {
	var capturedQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("q")
		w.Write([]byte(`<html></html>`))
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
