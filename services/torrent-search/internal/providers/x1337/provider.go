package x1337

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"torrentstream/searchservice/internal/domain"
	"torrentstream/searchservice/internal/providers/common"
)

const (
	defaultEndpoint  = "https://x1337x.ws"
	defaultUserAgent = "torrent-stream-search/1.0"
)

var (
	x1337SearchEntryPattern = regexp.MustCompile(`(?is)<a[^>]+href="(/torrent/[^"]+)"[^>]*>(.*?)</a>`)
	x1337MagnetPattern      = regexp.MustCompile(`magnet:\?xt=urn:btih:[a-zA-Z0-9]{32,40}[^\s"'<>]*`)
	x1337SeedersPattern     = regexp.MustCompile(`(?is)(?:Seeders|Seeds?)\s*</[^>]*>\s*<[^>]*>\s*([0-9]+)`)
	x1337LeechersPattern    = regexp.MustCompile(`(?is)(?:Leechers|Peers?)\s*</[^>]*>\s*<[^>]*>\s*([0-9]+)`)
	x1337SizePattern        = regexp.MustCompile(`(?is)(?:Total size|Size)\s*</[^>]*>\s*<[^>]*>\s*([^<]+)`)
	x1337DescriptionPattern = regexp.MustCompile(`(?is)<div[^>]+class="[^"]*(?:description|torrent-detail|torrent-desc)[^"]*"[^>]*>(.*?)</div>`)
	x1337NfoPattern         = regexp.MustCompile(`(?is)<pre[^>]*>(.*?)</pre>`)
	x1337ImagePattern       = regexp.MustCompile(`(?is)<img[^>]+(?:src|data-src|data-original)=["']([^"']+)["'][^>]*>`)
)

type Config struct {
	Endpoint  string
	UserAgent string
	Client    *http.Client
}

type Provider struct {
	client    *http.Client
	endpoints []string
	userAgent string
}

type searchEntry struct {
	Name string
	Path string
}

func NewProvider(cfg Config) *Provider {
	client := cfg.Client
	if client == nil {
		client = &http.Client{}
	}
	endpoints := parseEndpoints(cfg.Endpoint)
	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	return &Provider{
		client:    client,
		endpoints: endpoints,
		userAgent: userAgent,
	}
}

func (p *Provider) Name() string {
	return "1337x"
}

func (p *Provider) Info() domain.ProviderInfo {
	return domain.ProviderInfo{
		Name:    p.Name(),
		Label:   "1337x",
		Kind:    "index",
		Enabled: true,
	}
}

func (p *Provider) Search(ctx context.Context, request domain.SearchRequest) ([]domain.SearchResult, error) {
	var (
		entries []searchEntry
		baseURL *url.URL
		err     error
	)
	for _, endpoint := range p.endpoints {
		entries, baseURL, err = p.fetchSearchEntries(ctx, endpoint, request.Query)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return []domain.SearchResult{}, nil
	}

	limit := request.Limit
	if limit <= 0 {
		limit = 50
	}
	maxScans := limit * 4
	if maxScans < limit {
		maxScans = limit
	}
	if maxScans > len(entries) {
		maxScans = len(entries)
	}
	if maxScans > 40 {
		maxScans = 40
	}

	results := make([]domain.SearchResult, 0, limit)
	for _, entry := range entries[:maxScans] {
		detailHTML, fetchErr := p.fetchDetailHTML(ctx, baseURL, entry.Path)
		if fetchErr != nil {
			continue
		}
		magnet, seeders, leechers, sizeBytes, enrichment := parseDetailHTML(detailHTML, baseURL)
		if magnet == "" {
			continue
		}

		magnetURL, parseErr := url.Parse(magnet)
		if parseErr != nil {
			continue
		}
		infoHash := common.NormalizeInfoHash(magnetURL.Query().Get("xt"))
		if infoHash == "" {
			continue
		}
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			name = strings.TrimSpace(magnetURL.Query().Get("dn"))
		}
		if name == "" {
			name = "1337x " + infoHash
		}

		pageURL := ""
		if baseURL != nil && strings.TrimSpace(entry.Path) != "" {
			pageURL = baseURL.ResolveReference(&url.URL{Path: strings.TrimSpace(entry.Path)}).String()
		}

		results = append(results, domain.SearchResult{
			Name:       name,
			InfoHash:   infoHash,
			Magnet:     magnet,
			PageURL:    pageURL,
			SizeBytes:  sizeBytes,
			Seeders:    seeders,
			Leechers:   leechers,
			Source:     "1337x",
			Tracker:    baseURL.Host,
			Enrichment: enrichment,
		})
		if len(results) >= limit {
			break
		}
	}

	return results, nil
}

func (p *Provider) fetchSearchEntries(ctx context.Context, endpoint, query string) ([]searchEntry, *url.URL, error) {
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid endpoint: %w", err)
	}
	path := "/search/" + url.PathEscape(strings.TrimSpace(query)) + "/1/"
	searchURL := baseURL.ResolveReference(&url.URL{Path: path})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, nil, fmt.Errorf("provider HTTP %d: %s", resp.StatusCode, compactSnippet(string(body), 220))
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, nil, err
	}

	entries := parseSearchEntries(string(payload))
	return entries, searchURL, nil
}

func (p *Provider) fetchDetailHTML(ctx context.Context, baseURL *url.URL, rawPath string) (string, error) {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return "", fmt.Errorf("empty detail path")
	}
	detailURL, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	resolved := baseURL.ResolveReference(detailURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resolved.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("detail HTTP %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func parseSearchEntries(payload string) []searchEntry {
	matches := x1337SearchEntryPattern.FindAllStringSubmatch(payload, -1)
	if len(matches) == 0 {
		return nil
	}
	items := make([]searchEntry, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		path := strings.TrimSpace(match[1])
		if path == "" {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		name := common.CleanHTMLText(match[2])
		if name == "" {
			continue
		}
		items = append(items, searchEntry{Name: name, Path: path})
	}
	return items
}

func parseEndpoints(raw string) []string {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = defaultEndpoint + ",https://1337x.to,https://1377x.to"
	}
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		endpoint := strings.TrimSpace(part)
		if endpoint == "" {
			continue
		}
		if _, exists := seen[endpoint]; exists {
			continue
		}
		seen[endpoint] = struct{}{}
		items = append(items, endpoint)
	}
	if len(items) == 0 {
		return []string{defaultEndpoint}
	}
	return items
}

func compactSnippet(raw string, maxLen int) string {
	value := common.CleanHTMLText(raw)
	if value == "" {
		return "empty response body"
	}
	if len(value) <= maxLen {
		return value
	}
	if maxLen < 4 {
		return value[:maxLen]
	}
	return value[:maxLen-3] + "..."
}

func parseDetailHTML(payload string, baseURL *url.URL) (magnet string, seeders int, leechers int, sizeBytes int64, enrichment domain.SearchEnrichment) {
	magnet = strings.TrimSpace(html.UnescapeString(x1337MagnetPattern.FindString(payload)))
	seeders = findFirstInt(payload, x1337SeedersPattern)
	leechers = findFirstInt(payload, x1337LeechersPattern)
	sizeBytes = common.ParseHumanSize(findFirstText(payload, x1337SizePattern))
	enrichment.Description = findFirstText(payload, x1337DescriptionPattern)
	enrichment.NFO = compactSnippet(findFirstText(payload, x1337NfoPattern), 600)
	images := findImageURLs(payload, baseURL, 4)
	if len(images) > 0 {
		enrichment.Poster = images[0]
		if len(images) > 1 {
			enrichment.Screenshots = append([]string(nil), images[1:]...)
		}
	}
	return magnet, seeders, leechers, sizeBytes, enrichment
}

func findFirstInt(payload string, pattern *regexp.Regexp) int {
	match := pattern.FindStringSubmatch(payload)
	if len(match) < 2 {
		return 0
	}
	value, err := strconv.Atoi(strings.TrimSpace(match[1]))
	if err != nil {
		return 0
	}
	return value
}

func findFirstText(payload string, pattern *regexp.Regexp) string {
	match := pattern.FindStringSubmatch(payload)
	if len(match) < 2 {
		return ""
	}
	return common.CleanHTMLText(match[1])
}

func findImageURLs(payload string, baseURL *url.URL, limit int) []string {
	if limit <= 0 {
		return nil
	}
	matches := x1337ImagePattern.FindAllStringSubmatch(payload, -1)
	if len(matches) == 0 {
		return nil
	}
	items := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		raw := strings.TrimSpace(html.UnescapeString(match[1]))
		if raw == "" {
			continue
		}
		lower := strings.ToLower(raw)
		if strings.Contains(lower, "sprite") || strings.Contains(lower, "icon") || strings.Contains(lower, "logo") {
			continue
		}
		if !strings.Contains(lower, ".jpg") && !strings.Contains(lower, ".jpeg") && !strings.Contains(lower, ".png") && !strings.Contains(lower, ".webp") {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			continue
		}
		resolved := raw
		if baseURL != nil {
			resolved = baseURL.ResolveReference(parsed).String()
		}
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		items = append(items, resolved)
		if len(items) >= limit {
			break
		}
	}
	return items
}
