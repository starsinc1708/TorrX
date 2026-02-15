package bittorrentindex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"torrentstream/searchservice/internal/domain"
	"torrentstream/searchservice/internal/providers/common"
)

const (
	defaultEndpoint  = "https://apibay.org/q.php"
	defaultUserAgent = "torrent-stream-search/1.0"
)

var defaultTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
}

type Config struct {
	Endpoint  string
	UserAgent string
	Trackers  []string
	Client    *http.Client
}

type Provider struct {
	client    *http.Client
	endpoint  string
	userAgent string
	trackers  []string
}

type apiItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	InfoHash string `json:"info_hash"`
	Size     string `json:"size"`
	Seeders  string `json:"seeders"`
	Leechers string `json:"leechers"`
	Added    string `json:"added"`
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
	trackers := cfg.Trackers
	if len(trackers) == 0 {
		trackers = append([]string(nil), defaultTrackers...)
	}

	return &Provider{
		client:    client,
		endpoint:  endpoint,
		userAgent: userAgent,
		trackers:  trackers,
	}
}

func (p *Provider) Name() string {
	return "piratebay"
}

func (p *Provider) Info() domain.ProviderInfo {
	return domain.ProviderInfo{
		Name:    p.Name(),
		Label:   "The Pirate Bay",
		Kind:    "index",
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
	uri.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("provider HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}

	items, err := parseAPIItems(payload)
	if err != nil {
		return nil, err
	}

	limit := request.Limit
	if limit <= 0 {
		limit = 50
	}
	results := make([]domain.SearchResult, 0, len(items))
	for _, item := range items {
		result, ok := p.toResult(item)
		if !ok {
			continue
		}
		results = append(results, result)
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func parseAPIItems(payload []byte) ([]apiItem, error) {
	var items []apiItem
	if err := json.Unmarshal(payload, &items); err == nil {
		return items, nil
	}

	var single map[string]string
	if err := json.Unmarshal(payload, &single); err == nil {
		return []apiItem{}, nil
	}

	return nil, fmt.Errorf("unexpected provider payload")
}

func (p *Provider) toResult(item apiItem) (domain.SearchResult, bool) {
	name := strings.TrimSpace(item.Name)
	infoHash := common.NormalizeInfoHash(item.InfoHash)
	if infoHash == "" || name == "" {
		return domain.SearchResult{}, false
	}
	if strings.Contains(strings.ToLower(name), "no results returned") {
		return domain.SearchResult{}, false
	}
	seeders := atoi(item.Seeders)
	leechers := atoi(item.Leechers)
	sizeBytes := atoi64(item.Size)
	publishedAt := parseUnixTS(item.Added)

	pageURL := ""
	if id := strings.TrimSpace(item.ID); id != "" {
		if base, err := url.Parse(p.endpoint); err == nil && base.Scheme != "" && base.Host != "" {
			detail := &url.URL{Scheme: base.Scheme, Host: base.Host, Path: "/t.php"}
			q := detail.Query()
			q.Set("id", id)
			detail.RawQuery = q.Encode()
			pageURL = detail.String()
		}
	}

	return domain.SearchResult{
		Name:        name,
		InfoHash:    infoHash,
		Magnet:      common.BuildMagnet(infoHash, name, p.trackers),
		PageURL:     pageURL,
		SizeBytes:   sizeBytes,
		Seeders:     seeders,
		Leechers:    leechers,
		Source:      "piratebay",
		Tracker:     "thepiratebay.org",
		PublishedAt: publishedAt,
	}, true
}

func atoi(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return value
}

func atoi64(raw string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func parseUnixTS(raw string) *time.Time {
	ts, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || ts <= 0 {
		return nil
	}
	value := time.Unix(ts, 0).UTC()
	return &value
}
