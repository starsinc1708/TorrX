package apihttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"torrentstream/searchservice/internal/domain"
)

type fakeSearchService struct {
	lastProviders []string
	lastRequest   domain.SearchRequest
	callCount     int
}

type fakeProviderSettingsService struct {
	items []domain.ProviderRuntimeConfig
}

func (f *fakeSearchService) Search(ctx context.Context, request domain.SearchRequest, providers []string) (domain.SearchResponse, error) {
	_ = ctx
	f.callCount++
	f.lastProviders = append([]string(nil), providers...)
	f.lastRequest = request
	statusName := "fake"
	if len(providers) > 0 {
		statusName = providers[0]
	}
	return domain.SearchResponse{
		Query: request.Query,
		Items: []domain.SearchResult{
			{Name: request.Query + "-result", Source: "fake"},
		},
		Providers: []domain.ProviderStatus{
			{Name: statusName, OK: true, Count: 1},
		},
		ElapsedMS:  3,
		TotalItems: 1,
		Limit:      request.Limit,
		Offset:     request.Offset,
		SortBy:     request.SortBy,
		SortOrder:  request.SortOrder,
	}, nil
}

func (f *fakeSearchService) Providers() []domain.ProviderInfo {
	return []domain.ProviderInfo{
		{Name: "bittorrent", Label: "BitTorrent Index", Kind: "index", Enabled: true},
		{Name: "dht", Label: "DHT Index", Kind: "dht", Enabled: true},
	}
}

func (f *fakeSearchService) SearchStream(ctx context.Context, request domain.SearchRequest, providers []string) <-chan domain.SearchResponse {
	_ = ctx
	ch := make(chan domain.SearchResponse, 1)
	f.callCount++
	f.lastProviders = append([]string(nil), providers...)
	f.lastRequest = request
	statusName := "fake"
	if len(providers) > 0 {
		statusName = providers[0]
	}
	ch <- domain.SearchResponse{
		Query: request.Query,
		Items: []domain.SearchResult{
			{Name: request.Query + "-result", Source: "fake"},
		},
		Providers: []domain.ProviderStatus{
			{Name: statusName, OK: true, Count: 1},
		},
		ElapsedMS:  3,
		TotalItems: 1,
		Limit:      request.Limit,
		Offset:     request.Offset,
		SortBy:     request.SortBy,
		SortOrder:  request.SortOrder,
		Final:      true,
	}
	close(ch)
	return ch
}

func (f *fakeSearchService) ProviderDiagnostics() []domain.ProviderDiagnostics {
	return []domain.ProviderDiagnostics{
		{Name: "bittorrent", Label: "BitTorrent Index", Kind: "index", Enabled: true, LastLatencyMS: 120},
		{Name: "dht", Label: "DHT Index", Kind: "dht", Enabled: true, LastLatencyMS: 80},
	}
}

func (f *fakeProviderSettingsService) ListProviderConfigs() []domain.ProviderRuntimeConfig {
	return append([]domain.ProviderRuntimeConfig(nil), f.items...)
}

func (f *fakeProviderSettingsService) UpdateProviderConfig(ctx context.Context, patch domain.ProviderRuntimePatch) (domain.ProviderRuntimeConfig, error) {
	_ = ctx
	for index := range f.items {
		if f.items[index].Name != patch.Name {
			continue
		}
		if patch.Endpoint != nil {
			f.items[index].Endpoint = *patch.Endpoint
		}
		if patch.ProxyURL != nil {
			f.items[index].ProxyURL = *patch.ProxyURL
		}
		if patch.APIKey != nil {
			f.items[index].HasAPIKey = strings.TrimSpace(*patch.APIKey) != ""
			if f.items[index].HasAPIKey {
				f.items[index].APIKeyPreview = "****"
			} else {
				f.items[index].APIKeyPreview = ""
			}
		}
		f.items[index].Configured = f.items[index].Endpoint != "" && f.items[index].HasAPIKey
		return f.items[index], nil
	}
	return domain.ProviderRuntimeConfig{}, errors.New("unknown provider")
}

func (f *fakeProviderSettingsService) AutoDetectProviderConfig(ctx context.Context, name string) (domain.ProviderRuntimeConfig, error) {
	_ = ctx
	for index := range f.items {
		if f.items[index].Name != name {
			continue
		}
		f.items[index].HasAPIKey = true
		f.items[index].APIKeyPreview = "auto"
		f.items[index].Configured = f.items[index].Endpoint != ""
		return f.items[index], nil
	}
	return domain.ProviderRuntimeConfig{}, errors.New("unknown provider")
}

func (f *fakeProviderSettingsService) AutoDetectAllProviderConfigs(ctx context.Context) ([]domain.ProviderRuntimeConfig, map[string]string) {
	_ = ctx
	items := make([]domain.ProviderRuntimeConfig, 0, len(f.items))
	for index := range f.items {
		f.items[index].HasAPIKey = true
		f.items[index].APIKeyPreview = "auto"
		f.items[index].Configured = f.items[index].Endpoint != ""
		items = append(items, f.items[index])
	}
	return items, nil
}

func (f *fakeProviderSettingsService) GetFlareSolverrSettings(ctx context.Context) (domain.FlareSolverrSettings, error) {
	_ = ctx
	return domain.FlareSolverrSettings{
		DefaultURL: "http://flaresolverr:8191/",
		URL:        "http://flaresolverr:8191/",
		Providers: []domain.FlareSolverrProviderStatus{
			{Provider: "jackett", Configured: true, URL: "http://flaresolverr:8191/"},
			{Provider: "prowlarr", Configured: true, URL: "http://flaresolverr:8191/"},
		},
	}, nil
}

func (f *fakeProviderSettingsService) ApplyFlareSolverr(ctx context.Context, url string, provider string) (domain.FlareSolverrApplyResponse, error) {
	_ = ctx
	_ = provider
	return domain.FlareSolverrApplyResponse{
		URL: url,
		Results: []domain.FlareSolverrApplyResult{
			{Provider: "jackett", OK: true, Status: "applied", Message: "FlareSolverr linked successfully"},
			{Provider: "prowlarr", OK: true, Status: "applied", Message: "FlareSolverr linked successfully"},
		},
	}, nil
}

func TestSearchMissingQuery(t *testing.T) {
	server := NewServer(nil)
	req := httptest.NewRequest(http.MethodGet, "/search", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestSearchParsesProviders(t *testing.T) {
	fake := &fakeSearchService{}
	server := NewServer(fake)
	req := httptest.NewRequest(http.MethodGet, "/search?q=ubuntu&providers=dht,bittorrent,dht&offset=5&sortBy=seeders&sortOrder=asc", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if len(fake.lastProviders) != 2 || fake.lastProviders[0] != "dht" || fake.lastProviders[1] != "bittorrent" {
		t.Fatalf("unexpected providers: %#v", fake.lastProviders)
	}
	if fake.lastRequest.Offset != 5 {
		t.Fatalf("unexpected offset: %d", fake.lastRequest.Offset)
	}
	if fake.lastRequest.SortBy != domain.SearchSortBySeeders {
		t.Fatalf("unexpected sortBy: %s", fake.lastRequest.SortBy)
	}
	if fake.lastRequest.SortOrder != domain.SearchSortOrderAsc {
		t.Fatalf("unexpected sortOrder: %s", fake.lastRequest.SortOrder)
	}

	var payload domain.SearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.TotalItems != 1 {
		t.Fatalf("unexpected total items: %d", payload.TotalItems)
	}
}

func TestSearchProvidersEndpoint(t *testing.T) {
	fake := &fakeSearchService{}
	server := NewServer(fake)
	req := httptest.NewRequest(http.MethodGet, "/search/providers", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload struct {
		Items []domain.ProviderInfo `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("unexpected items count: %d", len(payload.Items))
	}
}

func TestSearchProvidersHealthEndpoint(t *testing.T) {
	fake := &fakeSearchService{}
	server := NewServer(fake)
	req := httptest.NewRequest(http.MethodGet, "/search/providers/health", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload struct {
		Items []domain.ProviderDiagnostics `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("unexpected items count: %d", len(payload.Items))
	}
}

func TestSearchProviderTestEndpoint(t *testing.T) {
	fake := &fakeSearchService{}
	server := NewServer(fake)
	req := httptest.NewRequest(http.MethodGet, "/search/providers/test?provider=dht&q=ubuntu", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload struct {
		Provider string `json:"provider"`
		OK       bool   `json:"ok"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Provider != "dht" {
		t.Fatalf("unexpected provider: %s", payload.Provider)
	}
	if !payload.OK {
		t.Fatalf("expected ok=true")
	}
}

func TestSearchParsesRankingProfile(t *testing.T) {
	fake := &fakeSearchService{}
	server := NewServer(fake)
	req := httptest.NewRequest(http.MethodGet, "/search?q=dark&freshnessWeight=0&seedersWeight=2.5&qualityWeight=3&languageWeight=1.5&sizeWeight=0.25&preferSeries=1&preferredAudio=ru,en&preferredSubtitles=ru&targetSizeBytes=2147483648", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	profile := fake.lastRequest.Profile
	if profile.FreshnessWeight != 0 {
		t.Fatalf("expected FreshnessWeight=0, got %.2f", profile.FreshnessWeight)
	}
	if profile.SeedersWeight != 2.5 {
		t.Fatalf("expected SeedersWeight=2.5, got %.2f", profile.SeedersWeight)
	}
	if !profile.PreferSeries {
		t.Fatalf("expected PreferSeries=true")
	}
	if len(profile.PreferredAudio) != 2 {
		t.Fatalf("unexpected PreferredAudio: %#v", profile.PreferredAudio)
	}
}

func TestSearchStreamSendsPhases(t *testing.T) {
	fake := &fakeSearchService{}
	server := NewServer(fake)
	req := httptest.NewRequest(http.MethodGet, "/search/stream?q=ubuntu&providers=piratebay,dht", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !containsAll(body, []string{"event: bootstrap", "event: update", "event: done"}) {
		t.Fatalf("unexpected stream body: %s", body)
	}
	if fake.callCount < 1 {
		t.Fatalf("expected at least 1 SearchStream call, got %d", fake.callCount)
	}
}

func TestProviderSettingsEndpoint(t *testing.T) {
	fake := &fakeSearchService{}
	settings := &fakeProviderSettingsService{
		items: []domain.ProviderRuntimeConfig{
			{Name: "jackett", Label: "Jackett (Torznab)", Endpoint: "http://jackett:9117/api/v2.0/indexers/all/results/torznab/api"},
		},
	}
	server := NewServer(fake, WithProviderSettings(settings))

	getReq := httptest.NewRequest(http.MethodGet, "/search/settings/providers", nil)
	getRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected GET 200, got %d", getRec.Code)
	}

	patchReq := httptest.NewRequest(http.MethodPatch, "/search/settings/providers", strings.NewReader(`{"provider":"jackett","apiKey":"abc1234567890abc","proxyUrl":"http://127.0.0.1:8080"}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("expected PATCH 200, got %d", patchRec.Code)
	}

	var payload domain.ProviderRuntimeConfig
	if err := json.Unmarshal(patchRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.HasAPIKey {
		t.Fatalf("expected hasApiKey=true")
	}
	if payload.ProxyURL == "" {
		t.Fatalf("expected proxy url to be updated")
	}
}

func TestProviderSettingsAutodetectEndpoint(t *testing.T) {
	fake := &fakeSearchService{}
	settings := &fakeProviderSettingsService{
		items: []domain.ProviderRuntimeConfig{
			{Name: "jackett", Label: "Jackett (Torznab)", Endpoint: "http://jackett:9117/api/v2.0/indexers/all/results/torznab/api"},
			{Name: "prowlarr", Label: "Prowlarr (Torznab)", Endpoint: "http://prowlarr:9696/1/api"},
		},
	}
	server := NewServer(fake, WithProviderSettings(settings))

	req := httptest.NewRequest(http.MethodPost, "/search/settings/providers/autodetect", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload struct {
		Items   []domain.ProviderRuntimeConfig `json:"items"`
		Results []struct {
			Provider string `json:"provider"`
			OK       bool   `json:"ok"`
			Status   string `json:"status"`
			Method   string `json:"method"`
			Message  string `json:"message"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(payload.Items))
	}
	if len(payload.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(payload.Results))
	}
	if !payload.Items[0].HasAPIKey || !payload.Items[1].HasAPIKey {
		t.Fatalf("expected auto-detected api keys for all providers")
	}
}

func TestFlareSolverrSettingsEndpoints(t *testing.T) {
	fake := &fakeSearchService{}
	settings := &fakeProviderSettingsService{
		items: []domain.ProviderRuntimeConfig{
			{Name: "jackett", Label: "Jackett (Torznab)"},
			{Name: "prowlarr", Label: "Prowlarr (Torznab)"},
		},
	}
	server := NewServer(fake, WithProviderSettings(settings))

	getReq := httptest.NewRequest(http.MethodGet, "/search/settings/flaresolverr", nil)
	getRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected GET 200, got %d", getRec.Code)
	}

	postReq := httptest.NewRequest(http.MethodPost, "/search/settings/flaresolverr", strings.NewReader(`{"url":"http://flaresolverr:8191/"}`))
	postReq.Header.Set("Content-Type", "application/json")
	postRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("expected POST 200, got %d", postRec.Code)
	}

	var payload domain.FlareSolverrApplyResponse
	if err := json.Unmarshal(postRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.URL == "" || len(payload.Results) == 0 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func containsAll(value string, required []string) bool {
	for _, part := range required {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
