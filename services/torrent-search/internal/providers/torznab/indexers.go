package torznab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"torrentstream/searchservice/internal/domain"
)

const (
	indexerCacheTTL    = 5 * time.Minute
	perIndexerTimeout = 15 * time.Second
)

type jackettIndexer struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Configured bool   `json:"configured"`
}

// resolveIndexers returns the cached list of Jackett indexers, refreshing if stale.
// Returns nil on any error (caller should fall back to the aggregated endpoint).
func (p *Provider) resolveIndexers(ctx context.Context) []jackettIndexer {
	p.indexerMu.Lock()
	defer p.indexerMu.Unlock()

	if len(p.indexerList) > 0 && time.Since(p.indexerFetch) < indexerCacheTTL {
		return p.indexerList
	}

	snapshot := p.snapshot()
	baseURL, err := baseProviderURL(snapshot.endpoint)
	if err != nil {
		return nil
	}

	// The API key may be stored separately or embedded in the endpoint URL.
	apiKey := snapshot.apiKey
	if apiKey == "" {
		apiKey = apiKeyFromEndpoint(snapshot.endpoint)
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	indexers, err := fetchConfiguredIndexers(fetchCtx, baseURL, apiKey, snapshot.userAgent, snapshot.client)
	if err != nil || len(indexers) == 0 {
		return nil
	}

	p.indexerList = indexers
	p.indexerFetch = time.Now()
	return indexers
}

// fetchConfiguredIndexers calls Jackett's indexer listing API.
// The admin API requires cookie-based auth, so we bootstrap a session by
// visiting /UI/Login first (same flow as detectJackettAPIKey).
func fetchConfiguredIndexers(ctx context.Context, baseURL, apiKey, userAgent string, client *http.Client) ([]jackettIndexer, error) {
	cookieClient := clientWithCookieJar(client)

	// Bootstrap session cookie.
	loginURL := strings.TrimSuffix(baseURL, "/") + "/UI/Login"
	loginReq, err := http.NewRequestWithContext(ctx, http.MethodGet, loginURL, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(userAgent) != "" {
		loginReq.Header.Set("User-Agent", userAgent)
	}
	loginResp, err := cookieClient.Do(loginReq)
	if err != nil {
		return nil, fmt.Errorf("jackett login bootstrap: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(loginResp.Body, 2048))
	loginResp.Body.Close()

	// Fetch configured indexers.
	u, err := url.Parse(strings.TrimSuffix(baseURL, "/") + "/api/v2.0/indexers")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("configured", "true")
	if strings.TrimSpace(apiKey) != "" {
		q.Set("apikey", apiKey)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(userAgent) != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := cookieClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("jackett indexers API status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var raw []jackettIndexer
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse jackett indexers: %w", err)
	}

	indexers := make([]jackettIndexer, 0, len(raw))
	for _, idx := range raw {
		id := strings.TrimSpace(idx.ID)
		if id == "" || !idx.Configured {
			continue
		}
		indexers = append(indexers, idx)
	}
	return indexers, nil
}

// FanOutActive reports whether per-indexer fan-out is enabled (cached indexer list with 2+ entries).
func (p *Provider) FanOutActive() bool {
	if p.name != "jackett" {
		return false
	}
	p.indexerMu.Lock()
	defer p.indexerMu.Unlock()
	return len(p.indexerList) > 1
}

// ListSubIndexers returns the cached list of Jackett sub-indexers for diagnostics.
func (p *Provider) ListSubIndexers() []domain.SubIndexerInfo {
	p.indexerMu.Lock()
	defer p.indexerMu.Unlock()

	if len(p.indexerList) == 0 {
		return nil
	}
	out := make([]domain.SubIndexerInfo, 0, len(p.indexerList))
	for _, idx := range p.indexerList {
		out = append(out, domain.SubIndexerInfo{
			ID:   idx.ID,
			Name: idx.Name,
		})
	}
	return out
}

// searchFanOut queries each Jackett indexer individually in parallel and merges results.
func (p *Provider) searchFanOut(ctx context.Context, request domain.SearchRequest, indexers []jackettIndexer) ([]domain.SearchResult, error) {
	snapshot := p.snapshot()
	baseURL, err := baseProviderURL(snapshot.endpoint)
	if err != nil {
		return nil, err
	}

	type indexerResult struct {
		items []domain.SearchResult
		err   error
	}

	results := make([]indexerResult, len(indexers))
	var wg sync.WaitGroup

	for i, idx := range indexers {
		wg.Add(1)
		go func(index int, indexer jackettIndexer) {
			defer wg.Done()

			endpoint := fmt.Sprintf("%s/api/v2.0/indexers/%s/results/torznab/api",
				strings.TrimSuffix(baseURL, "/"), url.PathEscape(indexer.ID))

			indexerCtx, cancel := context.WithTimeout(ctx, perIndexerTimeout)
			defer cancel()

			items, searchErr := p.searchSingleEndpoint(indexerCtx, request, snapshot, endpoint)
			results[index] = indexerResult{items: items, err: searchErr}
		}(i, idx)
	}

	wg.Wait()

	// Merge and deduplicate.
	seen := make(map[string]struct{})
	var merged []domain.SearchResult
	var lastErr error

	for _, r := range results {
		if r.err != nil {
			lastErr = r.err
			continue
		}
		for _, item := range r.items {
			key := item.InfoHash
			if key == "" {
				key = item.Magnet
			}
			if key == "" {
				key = strings.ToLower(item.Name)
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, item)
		}
	}

	// If no indexer returned results at all, propagate the last error.
	if len(merged) == 0 && lastErr != nil {
		return nil, lastErr
	}

	if merged == nil {
		merged = []domain.SearchResult{}
	}
	return merged, nil
}

// searchSingleEndpoint performs a Torznab search against a specific endpoint URL.
// This is extracted from the main Search() logic so it can be reused per-indexer.
func (p *Provider) searchSingleEndpoint(ctx context.Context, request domain.SearchRequest, snapshot providerSnapshot, endpoint string) ([]domain.SearchResult, error) {
	uri, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %w", err)
	}
	query := uri.Query()
	query.Set("t", "search")
	query.Set("q", strings.TrimSpace(request.Query))
	if strings.TrimSpace(query.Get("extended")) == "" {
		query.Set("extended", "1")
	}
	apiKey := snapshot.apiKey
	if apiKey == "" {
		apiKey = apiKeyFromEndpoint(snapshot.endpoint)
	}
	if strings.TrimSpace(query.Get("apikey")) == "" && apiKey != "" {
		query.Set("apikey", apiKey)
	}
	if request.Limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", request.Limit))
	}
	if request.Offset > 0 {
		query.Set("offset", fmt.Sprintf("%d", request.Offset))
	}
	uri.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", snapshot.userAgent)
	req.Header.Set("Accept", "application/xml,text/xml,application/rss+xml")

	resp, err := snapshot.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("provider HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, err
	}

	items, err := parseTorznabResponse(payload)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return []domain.SearchResult{}, nil
	}

	limit := request.Limit
	if limit <= 0 {
		limit = 50
	}

	results := make([]domain.SearchResult, 0, limit)
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		result, ok := p.itemToResult(ctx, item, uri.Host)
		if !ok {
			continue
		}
		key := result.InfoHash
		if key == "" {
			key = result.Magnet
		}
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		results = append(results, result)
		if len(results) >= limit {
			break
		}
	}

	return results, nil
}
