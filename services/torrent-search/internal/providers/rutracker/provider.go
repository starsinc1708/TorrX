package rutracker

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"torrentstream/searchservice/internal/domain"
	"torrentstream/searchservice/internal/providers/common"
)

const (
	defaultEndpoint  = "https://rutracker.org/forum/tracker.php"
	defaultUserAgent = "torrent-stream-search/1.0"
)

var (
	rutrackerTopicPattern   = regexp.MustCompile(`(?is)<a[^>]+href=(?:"([^"]*viewtopic\.php[^"]*)"|'([^']*viewtopic\.php[^']*)')[^>]*>(.*?)</a>`)
	rutrackerTopicIDPattern = regexp.MustCompile(`(?:\?|&)t=([0-9]+)(?:&|$)`)
	rutrackerMagnetPattern  = regexp.MustCompile(`magnet:\?xt=urn:btih:[a-zA-Z0-9]{32,40}[^\s"'<>]*`)
	rutrackerHashPattern    = regexp.MustCompile(`(?i)urn:btih:([a-z0-9]{40})`)
	rutrackerRowPattern     = regexp.MustCompile(`(?is)<tr[^>]*class="tCenter[^"]*"[^>]*>.*?</tr>`)
	rutrackerSeedPattern    = regexp.MustCompile(`(?i)class="[^"]*seed[^"]*"[^>]*>\s*(?:<b>)?\s*(\d+)`)
	rutrackerLeechPattern   = regexp.MustCompile(`(?i)class="[^"]*leech[^"]*"[^>]*>\s*(?:<b>)?\s*(\d+)`)
	rutrackerSizeBytesAttr  = regexp.MustCompile(`(?i)data-ts_text="(\d+)"`)
	rutrackerDescPattern    = regexp.MustCompile(`(?is)<div[^>]+class="post_body"[^>]*>(.*?)</div>`)
)

var defaultTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
}

type Config struct {
	Endpoint  string
	UserAgent string
	Client    *http.Client
	Trackers  []string
	Cookies   string
}

type Provider struct {
	client    *http.Client
	endpoint  string
	userAgent string
	trackers  []string
	cookies   string
}

type topicEntry struct {
	ID        string
	Name      string
	Seeders   int
	Leechers  int
	SizeBytes int64
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
		cookies:   strings.TrimSpace(cfg.Cookies),
	}
}

func (p *Provider) Name() string {
	return "rutracker"
}

func (p *Provider) Info() domain.ProviderInfo {
	return domain.ProviderInfo{
		Name:    p.Name(),
		Label:   "RuTracker",
		Kind:    "tracker",
		Enabled: p.cookies != "",
	}
}

func (p *Provider) Search(ctx context.Context, request domain.SearchRequest) ([]domain.SearchResult, error) {
	searchURL, err := url.Parse(p.endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %w", err)
	}
	query := searchURL.Query()
	query.Set("nm", strings.TrimSpace(request.Query))
	searchURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	if p.cookies != "" {
		req.Header.Set("Cookie", p.cookies)
	}

	resp, err := p.doRequestWithRetry(ctx, req)
	if err != nil {
		return nil, normalizeTransportError(err)
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
	htmlPayload := decodeHTML(payload)
	if isLoginPage(resp.Request.URL, htmlPayload) {
		return nil, fmt.Errorf("rutracker login required: provide SEARCH_PROVIDER_RUTRACKER_COOKIE")
	}
	entries := parseTopics(htmlPayload)
	if len(entries) == 0 {
		return []domain.SearchResult{}, nil
	}

	limit := request.Limit
	if limit <= 0 {
		limit = 50
	}
	maxScans := limit * 3
	if maxScans < limit {
		maxScans = limit
	}
	if maxScans > len(entries) {
		maxScans = len(entries)
	}
	if maxScans > 30 {
		maxScans = 30
	}

	results := make([]domain.SearchResult, 0, limit)
	for _, entry := range entries[:maxScans] {
		magnet, infoHash, description, detailErr := p.fetchTopicMagnet(ctx, searchURL, entry.ID, entry.Name)
		if detailErr != nil {
			continue
		}
		if magnet == "" && infoHash == "" {
			continue
		}
		if magnet == "" {
			magnet = common.BuildMagnet(infoHash, entry.Name, p.trackers)
		}

		pageURL := ""
		if searchURL != nil && strings.TrimSpace(entry.ID) != "" {
			detail := searchURL.ResolveReference(&url.URL{Path: "/forum/viewtopic.php"})
			q := detail.Query()
			q.Set("t", strings.TrimSpace(entry.ID))
			detail.RawQuery = q.Encode()
			pageURL = detail.String()
		}

		result := domain.SearchResult{
			Name:      entry.Name,
			InfoHash:  infoHash,
			Magnet:    magnet,
			PageURL:   pageURL,
			Source:    "rutracker",
			Tracker:   searchURL.Host,
			Seeders:   entry.Seeders,
			Leechers:  entry.Leechers,
			SizeBytes: entry.SizeBytes,
		}
		if description != "" {
			result.Enrichment.Description = description
		}
		results = append(results, result)
		if len(results) >= limit {
			break
		}
	}

	return results, nil
}

func parseTopics(payload string) []topicEntry {
	// Try row-based parsing first (extracts seeders/leechers/size from table rows)
	rows := rutrackerRowPattern.FindAllString(payload, -1)
	if len(rows) > 0 {
		items := make([]topicEntry, 0, len(rows))
		seen := make(map[string]struct{}, len(rows))
		for _, row := range rows {
			entry := parseTopicRow(row)
			if entry.ID == "" || entry.Name == "" {
				continue
			}
			if _, exists := seen[entry.ID]; exists {
				continue
			}
			seen[entry.ID] = struct{}{}
			items = append(items, entry)
		}
		if len(items) > 0 {
			return items
		}
	}

	// Fallback: link-based parsing (original approach, no seeders/size)
	matches := rutrackerTopicPattern.FindAllStringSubmatch(payload, -1)
	if len(matches) == 0 {
		return nil
	}
	items := make([]topicEntry, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		href := strings.TrimSpace(html.UnescapeString(match[1]))
		if href == "" {
			href = strings.TrimSpace(html.UnescapeString(match[2]))
		}
		id := extractTopicID(href)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		name := common.CleanHTMLText(match[3])
		if name == "" {
			continue
		}
		items = append(items, topicEntry{ID: id, Name: name})
	}
	return items
}

func parseTopicRow(row string) topicEntry {
	entry := topicEntry{}

	// Extract topic link (ID and name)
	linkMatches := rutrackerTopicPattern.FindAllStringSubmatch(row, -1)
	for _, match := range linkMatches {
		if len(match) < 4 {
			continue
		}
		href := strings.TrimSpace(html.UnescapeString(match[1]))
		if href == "" {
			href = strings.TrimSpace(html.UnescapeString(match[2]))
		}
		id := extractTopicID(href)
		if id == "" {
			continue
		}
		name := common.CleanHTMLText(match[3])
		if name == "" {
			continue
		}
		entry.ID = id
		entry.Name = name
		break
	}

	// Extract seeders
	if m := rutrackerSeedPattern.FindStringSubmatch(row); len(m) >= 2 {
		entry.Seeders = parseIntSafe(m[1])
	}

	// Extract leechers
	if m := rutrackerLeechPattern.FindStringSubmatch(row); len(m) >= 2 {
		entry.Leechers = parseIntSafe(m[1])
	}

	// Extract size from data-ts_text attribute (bytes)
	if m := rutrackerSizeBytesAttr.FindStringSubmatch(row); len(m) >= 2 {
		entry.SizeBytes = parseInt64Safe(m[1])
	}

	return entry
}

func parseIntSafe(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

func parseInt64Safe(s string) int64 {
	var n int64
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int64(c-'0')
		}
	}
	return n
}

func extractTopicID(href string) string {
	trimmed := strings.TrimSpace(href)
	if trimmed == "" {
		return ""
	}

	if parsed, err := url.Parse(trimmed); err == nil {
		if value := strings.TrimSpace(parsed.Query().Get("t")); value != "" {
			return value
		}
	}

	match := rutrackerTopicIDPattern.FindStringSubmatch(trimmed)
	if len(match) >= 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func isLoginPage(finalURL *url.URL, payload string) bool {
	if finalURL != nil && strings.Contains(strings.ToLower(finalURL.Path), "login.php") {
		return true
	}
	content := strings.ToLower(payload)
	if strings.Contains(content, "form action=\"login.php\"") {
		return true
	}
	if strings.Contains(content, "name=\"login_username\"") || strings.Contains(content, "name='login_username'") {
		return true
	}
	return false
}

func (p *Provider) fetchTopicMagnet(ctx context.Context, baseURL *url.URL, topicID, fallbackName string) (magnet string, infoHash string, description string, err error) {
	detailURL := &url.URL{
		Scheme: baseURL.Scheme,
		Host:   baseURL.Host,
		Path:   "/forum/viewtopic.php",
	}
	params := detailURL.Query()
	params.Set("t", topicID)
	detailURL.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, detailURL.String(), nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	if p.cookies != "" {
		req.Header.Set("Cookie", p.cookies)
	}

	resp, err := p.doRequestWithRetry(ctx, req)
	if err != nil {
		return "", "", "", normalizeTransportError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("topic HTTP %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return "", "", "", err
	}

	htmlPayload := decodeHTML(payload)
	magnet = strings.TrimSpace(html.UnescapeString(rutrackerMagnetPattern.FindString(htmlPayload)))
	if magnet != "" {
		magnetURL, parseErr := url.Parse(magnet)
		if parseErr == nil {
			infoHash = common.NormalizeInfoHash(magnetURL.Query().Get("xt"))
			if magnetURL.Query().Get("dn") == "" && infoHash != "" {
				magnet = common.BuildMagnet(infoHash, fallbackName, p.trackers)
			}
		}
	}
	if infoHash == "" {
		hashMatch := rutrackerHashPattern.FindStringSubmatch(htmlPayload)
		if len(hashMatch) >= 2 {
			infoHash = common.NormalizeInfoHash(hashMatch[1])
		}
	}

	// Extract description from post body for dubbing detection
	if m := rutrackerDescPattern.FindStringSubmatch(htmlPayload); len(m) >= 2 {
		description = common.CleanHTMLText(m[1])
		if len(description) > 2000 {
			description = description[:2000]
		}
	}

	return magnet, infoHash, description, nil
}

func decodeHTML(payload []byte) string {
	if utf8.Valid(payload) {
		return string(payload)
	}
	decoded, err := charmap.Windows1251.NewDecoder().Bytes(payload)
	if err != nil {
		return string(payload)
	}
	return string(decoded)
}

func (p *Provider) doRequestWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	const maxAttempts = 3
	backoffs := []time.Duration{0, 250 * time.Millisecond, 700 * time.Millisecond}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		retryReq := req.Clone(ctx)
		resp, err := p.client.Do(retryReq)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isTransientNetworkError(err) || attempt == maxAttempts-1 {
			break
		}
		delay := backoffs[attempt+1]
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "eof") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "tls: bad record mac") ||
		strings.Contains(lower, "handshake") ||
		strings.Contains(lower, "timeout")
}

func normalizeTransportError(err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "tls") ||
		strings.Contains(lower, "handshake") ||
		strings.Contains(lower, "eof") {
		return fmt.Errorf("rutracker unreachable from this network (tls/connection reset). check VPN/proxy or refresh cf_clearance cookie: %w", err)
	}
	return err
}
