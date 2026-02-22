package apihttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"torrentstream/searchservice/internal/domain"
	"torrentstream/searchservice/internal/providers/tmdb"
	"torrentstream/searchservice/internal/search"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type SearchService interface {
	Search(ctx context.Context, request domain.SearchRequest, providers []string) (domain.SearchResponse, error)
	SearchStream(ctx context.Context, request domain.SearchRequest, providers []string) <-chan domain.SearchResponse
	Providers() []domain.ProviderInfo
	ProviderDiagnostics() []domain.ProviderDiagnostics
}

type ProviderSettingsService interface {
	ListProviderConfigs() []domain.ProviderRuntimeConfig
	UpdateProviderConfig(ctx context.Context, patch domain.ProviderRuntimePatch) (domain.ProviderRuntimeConfig, error)
	AutoDetectProviderConfig(ctx context.Context, name string) (domain.ProviderRuntimeConfig, error)
	AutoDetectAllProviderConfigs(ctx context.Context) ([]domain.ProviderRuntimeConfig, map[string]string)
	GetFlareSolverrSettings(ctx context.Context) (domain.FlareSolverrSettings, error)
	ApplyFlareSolverr(ctx context.Context, url string, provider string) (domain.FlareSolverrApplyResponse, error)
}

type TMDBSuggestService interface {
	SearchMulti(ctx context.Context, query string, lang string) ([]tmdb.SearchResult, error)
	Enabled() bool
}

type Server struct {
	search   SearchService
	settings ProviderSettingsService
	tmdb     TMDBSuggestService
	logger   *slog.Logger
}

type providerAutodetectResult struct {
	Provider string `json:"provider"`
	OK       bool   `json:"ok"`
	Status   string `json:"status"`
	Method   string `json:"method,omitempty"`
	Message  string `json:"message"`
}

const maxQueryLength = 500

type ServerOption func(*Server)

func WithLogger(logger *slog.Logger) ServerOption {
	return func(s *Server) {
		s.logger = logger
	}
}

func WithTMDB(tmdb TMDBSuggestService) ServerOption {
	return func(s *Server) {
		s.tmdb = tmdb
	}
}

func WithProviderSettings(settings ProviderSettingsService) ServerOption {
	return func(s *Server) {
		s.settings = settings
	}
}

func NewServer(searchService SearchService, options ...ServerOption) *Server {
	server := &Server{
		search: searchService,
		logger: slog.Default(),
	}
	for _, option := range options {
		if option != nil {
			option(server)
		}
	}
	if server.logger == nil {
		server.logger = slog.Default()
	}
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/search/providers", s.handleProviders)
	mux.HandleFunc("/search/providers/health", s.handleProvidersHealth)
	mux.HandleFunc("/search/providers/test", s.handleProviderTest)
	mux.HandleFunc("/search/settings/providers", s.handleProviderSettings)
	mux.HandleFunc("/search/settings/providers/autodetect", s.handleProviderSettingsAutodetect)
	mux.HandleFunc("/search/settings/flaresolverr", s.handleFlareSolverrSettings)
	mux.HandleFunc("/search/stream", s.handleSearchStream)
	mux.HandleFunc("/search/suggest", s.handleSearchSuggest)
	mux.HandleFunc("/search/image", s.handleImageProxy)
	mux.HandleFunc("/search", s.handleSearch)
	traced := otelhttp.NewHandler(loggingMiddleware(s.logger, mux), "torrent-search",
		otelhttp.WithFilter(func(r *http.Request) bool {
			p := r.URL.Path
			return p != "/metrics" && p != "/health"
		}),
	)
	return recoveryMiddleware(s.logger, rateLimitMiddleware(50, 100, metricsMiddleware(traced)))
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"timestamp": time.Now().UTC(),
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/search" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.search == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "search service is not configured")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "query is required")
		return
	}
	if len(query) > maxQueryLength {
		writeError(w, http.StatusBadRequest, "invalid_request", "query too long (max 500 characters)")
		return
	}
	limit, err := parsePositiveInt(r, "limit", 50)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid limit")
		return
	}
	offset, err := parseNonNegativeInt(r, "offset", 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid offset")
		return
	}

	providers := parseCSV(r.URL.Query().Get("providers"))
	sortBy := domain.NormalizeSortBy(strings.TrimSpace(r.URL.Query().Get("sortBy")))
	sortOrder := domain.NormalizeSortOrder(strings.TrimSpace(r.URL.Query().Get("sortOrder")))
	profile, err := parseRankingProfile(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	filters := parseSearchFilters(r)
	noCache := parseOptionalBool(r.URL.Query().Get("nocache")) || parseOptionalBool(r.URL.Query().Get("noCache"))

	response, err := s.search.Search(r.Context(), domain.SearchRequest{
		Query:     query,
		Limit:     limit,
		Offset:    offset,
		SortBy:    sortBy,
		SortOrder: sortOrder,
		Profile:   profile,
		Filters:   filters,
		NoCache:   noCache,
	}, providers)
	if err != nil {
		s.logger.Warn("search request failed",
			slog.String("query", truncate(query, 80)),
			slog.Any("providers", providers),
			slog.String("error", err.Error()),
		)
		switch {
		case errors.Is(err, search.ErrInvalidQuery):
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		case errors.Is(err, search.ErrInvalidOffset):
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		case errors.Is(err, search.ErrUnknownProvider):
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		case errors.Is(err, search.ErrNoProviders):
			writeError(w, http.StatusServiceUnavailable, "service_unavailable", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "search failed")
		}
		return
	}

	failedProviders := make([]string, 0, len(response.Providers))
	for _, providerStatus := range response.Providers {
		if !providerStatus.OK {
			failedProviders = append(failedProviders, providerStatus.Name)
		}
	}
	s.logger.Info("search completed",
		slog.String("query", truncate(query, 80)),
		slog.Any("providers", providers),
		slog.Int("totalItems", response.TotalItems),
		slog.Int64("elapsedMs", response.ElapsedMS),
		slog.Int("failedProviders", len(failedProviders)),
	)
	if len(failedProviders) > 0 {
		s.logger.Warn("search providers partially failed",
			slog.String("query", truncate(query, 80)),
			slog.Any("failedProviders", failedProviders),
		)
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleSearchStream(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/search/stream" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.search == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "search service is not configured")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "streaming is not supported")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "query is required")
		return
	}
	if len(query) > maxQueryLength {
		writeError(w, http.StatusBadRequest, "invalid_request", "query too long (max 500 characters)")
		return
	}
	limit, err := parsePositiveInt(r, "limit", 50)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid limit")
		return
	}
	offset, err := parseNonNegativeInt(r, "offset", 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid offset")
		return
	}
	providers := parseCSV(r.URL.Query().Get("providers"))
	sortBy := domain.NormalizeSortBy(strings.TrimSpace(r.URL.Query().Get("sortBy")))
	sortOrder := domain.NormalizeSortOrder(strings.TrimSpace(r.URL.Query().Get("sortOrder")))
	profile, err := parseRankingProfile(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	filters := parseSearchFilters(r)
	noCache := parseOptionalBool(r.URL.Query().Get("nocache")) || parseOptionalBool(r.URL.Query().Get("noCache"))

	request := domain.SearchRequest{
		Query:     query,
		Limit:     limit,
		Offset:    offset,
		SortBy:    sortBy,
		SortOrder: sortOrder,
		Profile:   profile,
		Filters:   filters,
		NoCache:   noCache,
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	if err := writeSSEEvent(w, flusher, "bootstrap", map[string]any{
		"phase":  "bootstrap",
		"final":  false,
		"query":  query,
		"status": "started",
	}); err != nil {
		return // Client disconnected
	}

	ch := s.search.SearchStream(r.Context(), request, providers)
	for response := range ch {
		select {
		case <-r.Context().Done():
			return // Client disconnected
		default:
		}
		if response.Error != "" {
			_ = writeSSEEvent(w, flusher, "error", map[string]any{
				"message": response.Error,
			})
			_ = writeSSEEvent(w, flusher, "done", map[string]any{"final": true})
			return
		}
		response.Phase = "update"
		if err := writeSSEEvent(w, flusher, "update", response); err != nil {
			return // Client disconnected
		}
	}

	_ = writeSSEEvent(w, flusher, "done", map[string]any{"final": true})
}

func (s *Server) handleSearchSuggest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/search/suggest" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.tmdb == nil || !s.tmdb.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}

	lang := strings.TrimSpace(r.URL.Query().Get("lang"))
	if lang == "" {
		lang = "ru-RU"
	}

	results, err := s.tmdb.SearchMulti(r.Context(), query, lang)
	if err != nil {
		s.logger.Warn("tmdb suggest failed", slog.String("query", truncate(query, 60)), slog.String("error", err.Error()))
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}

	type suggestion struct {
		ID        int     `json:"id"`
		Title     string  `json:"title"`
		Year      int     `json:"year,omitempty"`
		Poster    string  `json:"poster,omitempty"`
		MediaType string  `json:"mediaType"`
		Rating    float64 `json:"rating,omitempty"`
	}

	items := make([]suggestion, 0, len(results))
	for _, r := range results {
		title := r.Title
		if title == "" {
			title = r.Name
		}
		if title == "" {
			continue
		}
		poster := ""
		if r.PosterPath != "" {
			poster = "https://image.tmdb.org/t/p/w92" + r.PosterPath
		}
		year := 0
		date := r.ReleaseDate
		if date == "" {
			date = r.FirstAirDate
		}
		if len(date) >= 4 {
			for _, c := range date[:4] {
				if c >= '0' && c <= '9' {
					year = year*10 + int(c-'0')
				}
			}
		}
		items = append(items, suggestion{
			ID:        r.ID,
			Title:     title,
			Year:      year,
			Poster:    poster,
			MediaType: r.MediaType,
			Rating:    r.VoteAverage,
		})
		if len(items) >= 8 {
			break
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/search/providers" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.search == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "search service is not configured")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": s.search.Providers(),
	})
}

func (s *Server) handleProvidersHealth(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/search/providers/health" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.search == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "search service is not configured")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"checkedAt": time.Now().UTC(),
		"items":     s.search.ProviderDiagnostics(),
	})
}

func (s *Server) handleProviderTest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/search/providers/test" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.search == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "search service is not configured")
		return
	}

	provider := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("provider")))
	if provider == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "provider is required")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		query = "spider man"
	}
	limit, err := parsePositiveInt(r, "limit", 10)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid limit")
		return
	}
	if limit > 50 {
		limit = 50
	}

	startedAt := time.Now()
	response, err := s.search.Search(r.Context(), domain.SearchRequest{
		Query:   query,
		Limit:   limit,
		Offset:  0,
		NoCache: true,
	}, []string{provider})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"provider":  provider,
			"query":     query,
			"ok":        false,
			"elapsedMs": time.Since(startedAt).Milliseconds(),
			"error":     err.Error(),
		})
		return
	}

	var providerStatus domain.ProviderStatus
	for _, status := range response.Providers {
		if strings.EqualFold(status.Name, provider) {
			providerStatus = status
			break
		}
	}
	sample := make([]string, 0, 3)
	for _, item := range response.Items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		sample = append(sample, truncate(name, 120))
		if len(sample) >= 3 {
			break
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"provider":  provider,
		"query":     query,
		"ok":        providerStatus.OK,
		"count":     providerStatus.Count,
		"elapsedMs": response.ElapsedMS,
		"error":     providerStatus.Error,
		"sample":    sample,
	})
}

func (s *Server) handleProviderSettings(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/search/settings/providers" {
		http.NotFound(w, r)
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "provider settings service is not configured")
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"items": s.settings.ListProviderConfigs(),
		})
	case http.MethodPatch:
		var payload struct {
			Provider string  `json:"provider"`
			Endpoint *string `json:"endpoint"`
			APIKey   *string `json:"apiKey"`
			ProxyURL *string `json:"proxyUrl"`
		}
		if err := decodeJSONBody(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		provider := strings.ToLower(strings.TrimSpace(payload.Provider))
		if provider == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "provider is required")
			return
		}
		item, err := s.settings.UpdateProviderConfig(r.Context(), domain.ProviderRuntimePatch{
			Name:     provider,
			Endpoint: payload.Endpoint,
			APIKey:   payload.APIKey,
			ProxyURL: payload.ProxyURL,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleProviderSettingsAutodetect(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/search/settings/providers/autodetect" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "provider settings service is not configured")
		return
	}
	beforeItems := s.settings.ListProviderConfigs()
	beforeByProvider := make(map[string]domain.ProviderRuntimeConfig, len(beforeItems))
	for _, item := range beforeItems {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		if name == "" {
			continue
		}
		beforeByProvider[name] = item
	}

	var payload struct {
		Provider string `json:"provider"`
	}
	if err := decodeJSONBody(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	provider := strings.ToLower(strings.TrimSpace(payload.Provider))
	if provider != "" {
		item, err := s.settings.AutoDetectProviderConfig(r.Context(), provider)
		before := beforeByProvider[provider]
		result := buildProviderAutodetectResult(provider, before, item, err)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"items":   []domain.ProviderRuntimeConfig{item},
				"results": []providerAutodetectResult{result},
				"errors": []map[string]string{
					{
						"provider": provider,
						"error":    err.Error(),
					},
				},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":   []domain.ProviderRuntimeConfig{item},
			"results": []providerAutodetectResult{result},
		})
		return
	}

	items, errorsByProvider := s.settings.AutoDetectAllProviderConfigs(r.Context())
	results := make([]providerAutodetectResult, 0, len(items))
	for _, item := range items {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		before := beforeByProvider[name]
		var detectErr error
		if message, ok := errorsByProvider[name]; ok && strings.TrimSpace(message) != "" {
			detectErr = errors.New(message)
		}
		results = append(results, buildProviderAutodetectResult(name, before, item, detectErr))
	}
	errorsList := make([]map[string]string, 0, len(errorsByProvider))
	for providerName, errMsg := range errorsByProvider {
		errorsList = append(errorsList, map[string]string{
			"provider": providerName,
			"error":    errMsg,
		})
	}
	sort.Slice(errorsList, func(i, j int) bool {
		return errorsList[i]["provider"] < errorsList[j]["provider"]
	})
	payloadOut := map[string]any{
		"items":   items,
		"results": results,
	}
	if len(errorsList) > 0 {
		payloadOut["errors"] = errorsList
	}
	writeJSON(w, http.StatusOK, payloadOut)
}

func (s *Server) handleFlareSolverrSettings(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/search/settings/flaresolverr" {
		http.NotFound(w, r)
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "provider settings service is not configured")
		return
	}

	switch r.Method {
	case http.MethodGet:
		payload, err := s.settings.GetFlareSolverrSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, payload)
	case http.MethodPost:
		var payload struct {
			URL      string `json:"url"`
			Provider string `json:"provider"`
		}
		if err := decodeJSONBody(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		response, err := s.settings.ApplyFlareSolverr(r.Context(), payload.URL, payload.Provider)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, response)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func buildProviderAutodetectResult(
	provider string,
	before domain.ProviderRuntimeConfig,
	after domain.ProviderRuntimeConfig,
	detectErr error,
) providerAutodetectResult {
	name := strings.ToLower(strings.TrimSpace(provider))
	method := autodetectMethodHint(name)
	if detectErr != nil {
		return providerAutodetectResult{
			Provider: name,
			OK:       false,
			Status:   "error",
			Method:   method,
			Message:  detectErr.Error(),
		}
	}
	if !before.HasAPIKey && after.HasAPIKey {
		return providerAutodetectResult{
			Provider: name,
			OK:       true,
			Status:   "detected",
			Method:   method,
			Message:  "API key detected automatically",
		}
	}
	if after.HasAPIKey {
		return providerAutodetectResult{
			Provider: name,
			OK:       true,
			Status:   "already_configured",
			Method:   method,
			Message:  "API key already configured",
		}
	}
	return providerAutodetectResult{
		Provider: name,
		OK:       false,
		Status:   "not_found",
		Method:   method,
		Message:  "API key not found",
	}
}

func autodetectMethodHint(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "prowlarr":
		return "initialize.json"
	case "jackett":
		return "ui session + server config"
	default:
		return "http probe"
	}
}

func parseCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		value := strings.ToLower(strings.TrimSpace(part))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func decodeJSONBody(r *http.Request, dest any) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("invalid json body: %w", err)
	}
	return nil
}

func parsePositiveInt(r *http.Request, key string, fallback int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return 0, errors.New("invalid value")
	}
	return parsed, nil
}

func parseNonNegativeInt(r *http.Request, key string, fallback int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return 0, errors.New("invalid value")
	}
	return parsed, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func parseRankingProfile(r *http.Request) (domain.SearchRankingProfile, error) {
	profile := domain.DefaultSearchRankingProfile()
	var err error

	profile.FreshnessWeight, err = parseOptionalFloat(r, "freshnessWeight", profile.FreshnessWeight)
	if err != nil {
		return profile, fmt.Errorf("invalid freshnessWeight")
	}
	profile.SeedersWeight, err = parseOptionalFloat(r, "seedersWeight", profile.SeedersWeight)
	if err != nil {
		return profile, fmt.Errorf("invalid seedersWeight")
	}
	profile.QualityWeight, err = parseOptionalFloat(r, "qualityWeight", profile.QualityWeight)
	if err != nil {
		return profile, fmt.Errorf("invalid qualityWeight")
	}
	profile.LanguageWeight, err = parseOptionalFloat(r, "languageWeight", profile.LanguageWeight)
	if err != nil {
		return profile, fmt.Errorf("invalid languageWeight")
	}
	profile.SizeWeight, err = parseOptionalFloat(r, "sizeWeight", profile.SizeWeight)
	if err != nil {
		return profile, fmt.Errorf("invalid sizeWeight")
	}

	profile.PreferSeries = parseOptionalBool(r.URL.Query().Get("preferSeries"))
	profile.PreferMovies = parseOptionalBool(r.URL.Query().Get("preferMovies"))
	profile.PreferredAudio = parseCSV(r.URL.Query().Get("preferredAudio"))
	profile.PreferredSubtitles = parseCSV(r.URL.Query().Get("preferredSubtitles"))

	if target := strings.TrimSpace(r.URL.Query().Get("targetSizeBytes")); target != "" {
		value, parseErr := strconv.ParseInt(target, 10, 64)
		if parseErr != nil || value < 0 {
			return profile, fmt.Errorf("invalid targetSizeBytes")
		}
		profile.TargetSizeBytes = value
	}
	return domain.NormalizeRankingProfile(profile), nil
}

func parseOptionalFloat(r *http.Request, key string, fallback float64) (float64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func parseSearchFilters(r *http.Request) domain.SearchFilters {
	q := r.URL.Query()
	var filters domain.SearchFilters

	filters.Quality = parseCSV(q.Get("quality"))
	filters.ContentType = strings.ToLower(strings.TrimSpace(q.Get("contentType")))
	filters.DubbingGroups = parseCSV(q.Get("dubbingGroups"))
	filters.DubbingTypes = parseCSV(q.Get("dubbingTypes"))

	if v := strings.TrimSpace(q.Get("yearFrom")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filters.YearFrom = n
		}
	}
	if v := strings.TrimSpace(q.Get("yearTo")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filters.YearTo = n
		}
	}
	if v := strings.TrimSpace(q.Get("minSeeders")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filters.MinSeeders = n
		}
	}
	return filters
}

func parseOptionalBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err // Client disconnected
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err // Client disconnected
	}
	flusher.Flush()
	return nil
}
