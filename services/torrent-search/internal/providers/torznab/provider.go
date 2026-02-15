package torznab

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"torrentstream/searchservice/internal/domain"
	"torrentstream/searchservice/internal/providers/common"
)

const defaultUserAgent = "torrent-stream-search/1.0"

var defaultTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
}

type Config struct {
	Name      string
	Label     string
	Kind      string
	Endpoint  string
	APIKey    string
	ProxyURL  string
	UserAgent string
	Client    *http.Client
	Trackers  []string
}

type Provider struct {
	mu        sync.RWMutex
	name      string
	label     string
	kind      string
	endpoint  string
	apiKey    string
	proxyURL  string
	userAgent string
	client    *http.Client
	trackers  []string

	// Jackett per-indexer fan-out cache (only used when name == "jackett").
	indexerMu    sync.Mutex
	indexerList  []jackettIndexer
	indexerFetch time.Time
}

func NewProvider(cfg Config) *Provider {
	baseClient := cfg.Client
	if baseClient == nil {
		baseClient = &http.Client{}
	}
	name := strings.ToLower(strings.TrimSpace(cfg.Name))
	if name == "" {
		name = "torznab"
	}
	label := strings.TrimSpace(cfg.Label)
	if label == "" {
		label = "Torznab"
	}
	kind := strings.TrimSpace(cfg.Kind)
	if kind == "" {
		kind = "indexer"
	}
	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	trackers := cfg.Trackers
	if len(trackers) == 0 {
		trackers = append([]string(nil), defaultTrackers...)
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultEndpointForProvider(name)
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	proxyURL := strings.TrimSpace(cfg.ProxyURL)

	client, err := buildProviderHTTPClient(baseClient, proxyURL)
	if err != nil {
		client, _ = buildProviderHTTPClient(baseClient, "")
		proxyURL = ""
	}

	return &Provider{
		name:      name,
		label:     label,
		kind:      kind,
		endpoint:  endpoint,
		apiKey:    apiKey,
		proxyURL:  proxyURL,
		userAgent: userAgent,
		client:    client,
		trackers:  trackers,
	}
}

func (p *Provider) Name() string {
	return p.name
}

func (p *Provider) Info() domain.ProviderInfo {
	return domain.ProviderInfo{
		Name:    p.name,
		Label:   p.label,
		Kind:    p.kind,
		Enabled: p.isConfigured(),
	}
}

func (p *Provider) Search(ctx context.Context, request domain.SearchRequest) ([]domain.SearchResult, error) {
	snapshot := p.snapshot()
	if strings.TrimSpace(request.Query) == "" {
		return nil, errors.New("query is required")
	}
	if !snapshot.isConfigured() {
		return nil, errors.New("provider is not configured")
	}

	// Jackett per-indexer fan-out: query each indexer individually instead of
	// the slow aggregated /indexers/all endpoint.
	if p.name == "jackett" {
		if indexers := p.resolveIndexers(ctx); len(indexers) > 1 {
			return p.searchFanOut(ctx, request, indexers)
		}
	}

	uri, err := url.Parse(snapshot.endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %w", err)
	}
	query := uri.Query()
	query.Set("t", "search")
	query.Set("q", strings.TrimSpace(request.Query))
	// Jackett (and some other Torznab providers) only include important attrs like
	// infohash/seeders/size when extended output is requested.
	if strings.TrimSpace(query.Get("extended")) == "" {
		query.Set("extended", "1")
	}
	if strings.TrimSpace(query.Get("apikey")) == "" && snapshot.apiKey != "" {
		query.Set("apikey", snapshot.apiKey)
	}
	if request.Limit > 0 {
		query.Set("limit", strconv.Itoa(request.Limit))
	}
	if request.Offset > 0 {
		query.Set("offset", strconv.Itoa(request.Offset))
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

func (p *Provider) itemToResult(ctx context.Context, item torznabItem, endpointHost string) (domain.SearchResult, bool) {
	name := strings.TrimSpace(item.Title)
	if name == "" {
		return domain.SearchResult{}, false
	}

	attrs := make(map[string]string, len(item.Attrs))
	for _, attr := range item.Attrs {
		key := strings.ToLower(strings.TrimSpace(attr.Name))
		if key == "" {
			continue
		}
		if _, exists := attrs[key]; exists {
			continue
		}
		attrs[key] = strings.TrimSpace(attr.Value)
	}

	magnet := firstMagnet(item.Guid, item.Link, item.Enclosure.URL)
	infoHash := ""
	if raw, ok := attrs["infohash"]; ok {
		infoHash = common.NormalizeInfoHash(raw)
	}
	if infoHash == "" && magnet != "" {
		infoHash = common.NormalizeInfoHash(extractInfoHashFromMagnet(magnet))
	}

	// Many Torznab sources (notably RuTracker via Jackett/Prowlarr) return only a torrent
	// download URL. In that case, download the torrent and compute the infohash.
	if infoHash == "" && magnet == "" {
		downloadURL := strings.TrimSpace(item.Enclosure.URL)
		if downloadURL == "" {
			downloadURL = strings.TrimSpace(item.Link)
		}
		if downloadURL != "" {
			downloadCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
			hash, err := p.fetchInfoHashFromTorrentURL(downloadCtx, downloadURL)
			cancel()
			if err == nil && hash != "" {
				infoHash = common.NormalizeInfoHash(hash)
			}
		}
	}
	if magnet == "" && infoHash != "" {
		magnet = common.BuildMagnet(infoHash, name, p.trackers)
	}
	if magnet == "" && infoHash == "" {
		return domain.SearchResult{}, false
	}

	sizeBytes := parseI64(attrs["size"])
	if sizeBytes <= 0 && item.Enclosure.Length > 0 {
		sizeBytes = item.Enclosure.Length
	}

	seeders := parseInt(attrs["seeders"])
	leechers := parseInt(attrs["leechers"])
	if leechers == 0 {
		peers := parseInt(attrs["peers"])
		if peers > seeders {
			leechers = peers - seeders
		}
	}

	var publishedAt *time.Time
	if published := parsePubDate(item.PubDate); published != nil {
		publishedAt = published
	}

	source := strings.ToLower(strings.TrimSpace(attrs["indexer"]))
	if source == "" {
		source = strings.ToLower(strings.TrimSpace(attrs["tracker"]))
	}
	if source == "" {
		source = p.name
	}

	tracker := strings.TrimSpace(attrs["tracker"])
	if tracker == "" {
		tracker = endpointHost
	}

	pageURL := selectOriginalPageURL(item, attrs, endpointHost)

	return domain.SearchResult{
		Name:        name,
		InfoHash:    infoHash,
		Magnet:      magnet,
		PageURL:     pageURL,
		SizeBytes:   sizeBytes,
		Seeders:     seeders,
		Leechers:    leechers,
		Source:      source,
		Tracker:     tracker,
		PublishedAt: publishedAt,
	}, true
}

func selectOriginalPageURL(item torznabItem, attrs map[string]string, endpointHost string) string {
	return firstOriginalHTTPURL(
		endpointHost,
		item.Comments,
		attrs["comments"],
		attrs["details"],
		attrs["info"],
		attrs["infourl"],
		attrs["source"],
		item.Link,
		attrs["guid"],
		item.Guid,
	)
}

func firstOriginalHTTPURL(endpointHost string, candidates ...string) string {
	endpointHostNorm := normalizeHost(endpointHost)
	for _, candidate := range candidates {
		value := strings.TrimSpace(candidate)
		if value == "" {
			continue
		}
		lower := strings.ToLower(value)
		if strings.HasPrefix(lower, "magnet:?") {
			continue
		}
		if !(strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")) {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		hostNorm := normalizeHost(parsed.Host)
		if hostNorm == "" {
			continue
		}
		if endpointHostNorm != "" && hostNorm == endpointHostNorm {
			continue
		}
		if strings.Contains(hostNorm, "jackett") || strings.Contains(hostNorm, "prowlarr") {
			continue
		}
		return parsed.String()
	}
	return ""
}

func normalizeHost(raw string) string {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	host := strings.TrimSpace(strings.ToLower(parsed.Hostname()))
	if host == "" {
		return ""
	}
	return strings.TrimPrefix(host, "www.")
}

func (p *Provider) fetchInfoHashFromTorrentURL(ctx context.Context, rawURL string) (string, error) {
	snapshot := p.snapshot()
	if strings.TrimSpace(rawURL) == "" {
		return "", errors.New("torrent url is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", snapshot.userAgent)
	req.Header.Set("Accept", "application/x-bittorrent,application/octet-stream,*/*")

	resp, err := snapshot.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("torrent download HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", err
	}
	return ExtractInfoHashFromTorrent(payload)
}

func (p *Provider) isConfigured() bool {
	snapshot := p.snapshot()
	return snapshot.isConfigured()
}

type providerSnapshot struct {
	endpoint  string
	apiKey    string
	proxyURL  string
	userAgent string
	client    *http.Client
}

func (p *Provider) snapshot() providerSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()

	client := p.client
	if client == nil {
		client = &http.Client{}
	}

	return providerSnapshot{
		endpoint:  strings.TrimSpace(p.endpoint),
		apiKey:    strings.TrimSpace(p.apiKey),
		proxyURL:  strings.TrimSpace(p.proxyURL),
		userAgent: strings.TrimSpace(p.userAgent),
		client:    client,
	}
}

func (p providerSnapshot) isConfigured() bool {
	if strings.TrimSpace(p.endpoint) == "" {
		return false
	}
	if strings.TrimSpace(p.apiKey) != "" {
		return true
	}
	return endpointHasAPIKey(p.endpoint)
}

type torznabResponse struct {
	Channel torznabChannel `xml:"channel"`
}

type torznabChannel struct {
	Items []torznabItem `xml:"item"`
}

type torznabItem struct {
	Title     string           `xml:"title"`
	Guid      string           `xml:"guid"`
	Link      string           `xml:"link"`
	Comments  string           `xml:"comments"`
	PubDate   string           `xml:"pubDate"`
	Enclosure torznabEnclosure `xml:"enclosure"`
	Attrs     []torznabAttr    `xml:"attr"`
}

type torznabEnclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
}

type torznabAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

func parseTorznabResponse(payload []byte) ([]torznabItem, error) {
	var rss torznabResponse
	if err := xml.Unmarshal(payload, &rss); err != nil {
		return nil, fmt.Errorf("invalid torznab XML: %w", err)
	}
	return rss.Channel.Items, nil
}

func firstMagnet(candidates ...string) string {
	for _, candidate := range candidates {
		value := strings.TrimSpace(candidate)
		if strings.HasPrefix(strings.ToLower(value), "magnet:?") {
			return value
		}
	}
	return ""
}

func extractInfoHashFromMagnet(rawMagnet string) string {
	value := strings.TrimSpace(rawMagnet)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return parsed.Query().Get("xt")
}

func parseInt(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return value
}

func parseI64(raw string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func parsePubDate(raw string) *time.Time {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}

	// Torznab providers often follow RSS formats. Accept common variants.
	formats := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		time.RFC3339,
	}
	for _, format := range formats {
		parsed, err := time.Parse(format, value)
		if err == nil {
			utc := parsed.UTC()
			return &utc
		}
	}
	return nil
}

func endpointHasAPIKey(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return strings.TrimSpace(parsed.Query().Get("apikey")) != ""
}
