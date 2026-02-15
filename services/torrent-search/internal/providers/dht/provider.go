package dht

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
	defaultEndpoint  = "https://btdig.com/search"
	defaultUserAgent = "torrent-stream-search/1.0"
)

var magnetPattern = regexp.MustCompile(`magnet:\?xt=urn:btih:[a-zA-Z0-9]{32,40}[^\s"'<>]*`)

type Config struct {
	Endpoint  string
	UserAgent string
	Client    *http.Client
}

type Provider struct {
	client    *http.Client
	endpoint  string
	userAgent string
}

func NewProvider(cfg Config) *Provider {
	client := cfg.Client
	if client == nil {
		client = &http.Client{}
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}

	return &Provider{
		client:    client,
		endpoint:  endpoint,
		userAgent: userAgent,
	}
}

func (p *Provider) Name() string {
	return "dht"
}

func (p *Provider) Info() domain.ProviderInfo {
	return domain.ProviderInfo{
		Name:    p.Name(),
		Label:   "DHT Index",
		Kind:    "dht",
		Enabled: true,
	}
}

func (p *Provider) Search(ctx context.Context, request domain.SearchRequest) ([]domain.SearchResult, error) {
	uri, err := url.Parse(p.endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %w", err)
	}
	query := uri.Query()
	query.Set("q", strings.TrimSpace(request.Query))
	query.Set("order", "0")
	uri.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("provider HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 6*1024*1024))
	if err != nil {
		return nil, err
	}

	magnets := extractMagnets(string(payload))
	if len(magnets) == 0 {
		return []domain.SearchResult{}, nil
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 50
	}

	results := make([]domain.SearchResult, 0, len(magnets))
	seen := make(map[string]struct{}, len(magnets))
	for _, magnet := range magnets {
		result, ok := magnetToResult(magnet)
		if !ok {
			continue
		}
		key := result.InfoHash
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

func extractMagnets(htmlPayload string) []string {
	matches := magnetPattern.FindAllString(htmlPayload, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		decoded := html.UnescapeString(match)
		out = append(out, strings.TrimSpace(decoded))
	}
	return out
}

func magnetToResult(magnet string) (domain.SearchResult, bool) {
	uri, err := url.Parse(strings.TrimSpace(magnet))
	if err != nil {
		return domain.SearchResult{}, false
	}
	if !strings.EqualFold(uri.Scheme, "magnet") {
		return domain.SearchResult{}, false
	}
	query := uri.Query()
	infoHash := common.NormalizeInfoHash(query.Get("xt"))
	if infoHash == "" {
		return domain.SearchResult{}, false
	}
	name := strings.TrimSpace(query.Get("dn"))
	if name == "" {
		name = "DHT result " + infoHash
	}

	sizeBytes := int64(0)
	if raw := strings.TrimSpace(query.Get("xl")); raw != "" {
		if value, parseErr := strconv.ParseInt(raw, 10, 64); parseErr == nil {
			sizeBytes = value
		}
	}

	return domain.SearchResult{
		Name:      name,
		InfoHash:  infoHash,
		Magnet:    magnet,
		SizeBytes: sizeBytes,
		Source:    "dht",
		Tracker:   "btdig.com",
		Seeders:   0,
		Leechers:  0,
	}, true
}
