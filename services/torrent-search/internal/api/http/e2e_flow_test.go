package apihttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"torrentstream/searchservice/internal/domain"
)

// fakeSearchWithMagnets returns results that include magnet links,
// simulating the real provider behavior where search results can be
// passed to the torrent-engine's POST /torrents endpoint.
type fakeSearchWithMagnets struct {
	fakeSearchService
}

func (f *fakeSearchWithMagnets) magnetResults(request domain.SearchRequest) []domain.SearchResult {
	return []domain.SearchResult{
		{
			Name:      "Sintel.2010.1080p.BluRay.x264",
			InfoHash:  "abc123def456789abc123def456789abc123def4",
			Magnet:    "magnet:?xt=urn:btih:abc123def456789abc123def456789abc123def4&dn=Sintel.2010.1080p.BluRay.x264",
			SizeBytes: 1_500_000_000,
			Seeders:   42,
			Leechers:  3,
			Source:    "bittorrent",
		},
		{
			Name:      "Sintel.2010.720p.WEB-DL",
			InfoHash:  "def456789abc123def456789abc123def456789ab",
			Magnet:    "magnet:?xt=urn:btih:def456789abc123def456789abc123def456789ab&dn=Sintel.2010.720p.WEB-DL",
			SizeBytes: 800_000_000,
			Seeders:   18,
			Leechers:  1,
			Source:    "dht",
		},
	}
}

func (f *fakeSearchWithMagnets) buildResponse(request domain.SearchRequest) domain.SearchResponse {
	items := f.magnetResults(request)
	return domain.SearchResponse{
		Query: request.Query,
		Items: items,
		Providers: []domain.ProviderStatus{
			{Name: "bittorrent", OK: true, Count: 1},
			{Name: "dht", OK: true, Count: 1},
		},
		ElapsedMS:  250,
		TotalItems: len(items),
		Limit:      request.Limit,
		Offset:     request.Offset,
	}
}

func (f *fakeSearchWithMagnets) Search(ctx context.Context, request domain.SearchRequest, providers []string) (domain.SearchResponse, error) {
	f.callCount++
	f.lastProviders = append([]string(nil), providers...)
	f.lastRequest = request
	return f.buildResponse(request), nil
}

func (f *fakeSearchWithMagnets) SearchStream(ctx context.Context, request domain.SearchRequest, providers []string) <-chan domain.SearchResponse {
	f.callCount++
	f.lastProviders = append([]string(nil), providers...)
	f.lastRequest = request
	ch := make(chan domain.SearchResponse, 1)
	resp := f.buildResponse(request)
	resp.Final = true
	ch <- resp
	close(ch)
	return ch
}

// TestE2ESearchReturnsAddableResults validates that search results include
// the magnet link and name fields required by the torrent-engine's
// POST /torrents endpoint to add a torrent to the catalog.
func TestE2ESearchReturnsAddableResults(t *testing.T) {
	fake := &fakeSearchWithMagnets{}
	server := NewServer(fake)

	req := httptest.NewRequest(http.MethodGet, "/search?q=sintel&providers=bittorrent,dht", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var resp domain.SearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.Items) == 0 {
		t.Fatalf("search returned no results")
	}

	// Every result should have a magnet link that the engine can use
	for i, item := range resp.Items {
		if item.Magnet == "" {
			t.Errorf("item[%d] %q: missing magnet link", i, item.Name)
		}
		if !strings.HasPrefix(item.Magnet, "magnet:?") {
			t.Errorf("item[%d] %q: magnet should start with magnet:?, got %q", i, item.Name, item.Magnet)
		}
		if item.Name == "" {
			t.Errorf("item[%d]: missing name", i)
		}
		if item.InfoHash == "" {
			t.Errorf("item[%d] %q: missing info hash", i, item.Name)
		}
	}

	// Verify the search was called with correct provider dedup
	if len(fake.lastProviders) != 2 {
		t.Fatalf("providers = %v, want [bittorrent dht]", fake.lastProviders)
	}
}

// TestE2ESearchStreamReturnsAddableResults validates that SSE streaming
// search also returns results with magnet links.
func TestE2ESearchStreamReturnsAddableResults(t *testing.T) {
	fake := &fakeSearchWithMagnets{}
	server := NewServer(fake)

	req := httptest.NewRequest(http.MethodGet, "/search/stream?q=sintel&providers=bittorrent", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	body := rec.Body.String()
	// SSE events should contain magnet links in the JSON data
	if !strings.Contains(body, "magnet:?xt=urn:btih:") {
		t.Fatalf("SSE stream should contain magnet links in results")
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("SSE stream should end with done event")
	}
}

// TestE2ESearchProvidesEnoughDataForCatalogDisplay validates that search
// results contain all fields the frontend needs to display in the SearchPage
// result cards before the user clicks "Add to catalog".
func TestE2ESearchProvidesEnoughDataForCatalogDisplay(t *testing.T) {
	fake := &fakeSearchWithMagnets{}
	server := NewServer(fake)

	req := httptest.NewRequest(http.MethodGet, "/search?q=sintel", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var resp domain.SearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for i, item := range resp.Items {
		// Required for display
		if item.Name == "" {
			t.Errorf("item[%d]: name required for display", i)
		}
		if item.SizeBytes <= 0 {
			t.Errorf("item[%d] %q: sizeBytes required for display", i, item.Name)
		}
		if item.Seeders < 0 {
			t.Errorf("item[%d] %q: seeders should be non-negative", i, item.Name)
		}
		if item.Source == "" {
			t.Errorf("item[%d] %q: source required for provider badge", i, item.Name)
		}
		// Required for "Add to catalog" button
		if item.Magnet == "" {
			t.Errorf("item[%d] %q: magnet required for add-to-catalog", i, item.Name)
		}
	}

	// Verify provider status is returned for the UI provider badges
	if len(resp.Providers) == 0 {
		t.Fatalf("provider status required for UI badges")
	}
	for _, p := range resp.Providers {
		if p.Name == "" {
			t.Errorf("provider status missing name")
		}
	}
}
