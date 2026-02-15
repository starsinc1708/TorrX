package torznab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"torrentstream/searchservice/internal/domain"
)

var (
	ErrUnknownProvider = errors.New("unknown provider")

	apiKeyJSONPattern  = regexp.MustCompile(`(?i)"api(?:_|)key"\s*:\s*"([a-z0-9_-]{16,128})"`)
	apiKeyXMLPattern   = regexp.MustCompile(`(?i)<api(?:_|)key>\s*([a-z0-9_-]{16,128})\s*</api(?:_|)key>`)
	apiKeyValuePattern = regexp.MustCompile(`(?i)\bapi(?:_|)key\b[^a-z0-9]+([a-z0-9_-]{16,128})`)
	apiKeyQueryPattern = regexp.MustCompile(`(?i)[?&]apikey=([a-z0-9_-]{16,128})`)
)

type RuntimeConfigService struct {
	providers              map[string]*Provider
	defaultFlareSolverrURL string
	store                  RuntimeConfigStore
}

func NewRuntimeConfigService(defaultFlareSolverrURL string, store RuntimeConfigStore, providers ...*Provider) *RuntimeConfigService {
	registry := make(map[string]*Provider, len(providers))
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(provider.Name()))
		if name == "" {
			continue
		}
		registry[name] = provider
	}
	service := &RuntimeConfigService{
		providers:              registry,
		defaultFlareSolverrURL: normalizeFlareSolverrURL(defaultFlareSolverrURL),
		store:                  store,
	}
	service.restorePersistedRuntimeSettings()
	return service
}

func (s *RuntimeConfigService) restorePersistedRuntimeSettings() {
	if s.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	entries, err := s.store.Load(ctx)
	if err != nil || len(entries) == 0 {
		return
	}
	for name, state := range entries {
		provider := s.providers[strings.ToLower(strings.TrimSpace(name))]
		if provider == nil {
			continue
		}
		endpoint := state.Endpoint
		apiKey := state.APIKey
		proxyURL := state.ProxyURL
		_, _ = provider.UpdateRuntimeSettings(&endpoint, &apiKey, &proxyURL)
	}
}

func (s *RuntimeConfigService) persistProviderRuntimeSettings(providerName string) {
	if s.store == nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(providerName))
	provider := s.providers[name]
	if provider == nil {
		return
	}
	state := provider.RuntimeSettingsState()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = s.store.Save(ctx, name, state)
}

func (p *Provider) RuntimeSettingsState() RuntimeProviderState {
	snapshot := p.snapshot()
	return RuntimeProviderState{
		Endpoint: strings.TrimSpace(snapshot.endpoint),
		APIKey:   strings.TrimSpace(snapshot.apiKey),
		ProxyURL: strings.TrimSpace(snapshot.proxyURL),
	}
}

func (s *RuntimeConfigService) ListProviderConfigs() []domain.ProviderRuntimeConfig {
	if len(s.providers) == 0 {
		return nil
	}
	items := make([]domain.ProviderRuntimeConfig, 0, len(s.providers))
	for _, provider := range s.providers {
		items = append(items, provider.RuntimeConfig())
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items
}

func (s *RuntimeConfigService) UpdateProviderConfig(ctx context.Context, patch domain.ProviderRuntimePatch) (domain.ProviderRuntimeConfig, error) {
	_ = ctx
	name := strings.ToLower(strings.TrimSpace(patch.Name))
	provider := s.providers[name]
	if provider == nil {
		return domain.ProviderRuntimeConfig{}, fmt.Errorf("%w: %s", ErrUnknownProvider, name)
	}
	updated, err := provider.UpdateRuntimeSettings(patch.Endpoint, patch.APIKey, patch.ProxyURL)
	if err != nil {
		return updated, err
	}
	s.persistProviderRuntimeSettings(name)
	return updated, nil
}

func (s *RuntimeConfigService) AutoDetectProviderConfig(ctx context.Context, name string) (domain.ProviderRuntimeConfig, error) {
	providerName := strings.ToLower(strings.TrimSpace(name))
	provider := s.providers[providerName]
	if provider == nil {
		return domain.ProviderRuntimeConfig{}, fmt.Errorf("%w: %s", ErrUnknownProvider, providerName)
	}
	item, err := provider.AutoDetect(ctx)
	if err == nil {
		s.persistProviderRuntimeSettings(providerName)
	}
	return item, err
}

func (s *RuntimeConfigService) AutoDetectAllProviderConfigs(ctx context.Context) ([]domain.ProviderRuntimeConfig, map[string]string) {
	if len(s.providers) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(s.providers))
	for name := range s.providers {
		names = append(names, name)
	}
	sort.Strings(names)

	items := make([]domain.ProviderRuntimeConfig, 0, len(names))
	errorsByProvider := make(map[string]string)
	for _, name := range names {
		provider := s.providers[name]
		item, err := provider.AutoDetect(ctx)
		if err != nil {
			errorsByProvider[name] = err.Error()
			item = provider.RuntimeConfig()
		} else {
			s.persistProviderRuntimeSettings(name)
		}
		items = append(items, item)
	}
	if len(errorsByProvider) == 0 {
		return items, nil
	}
	return items, errorsByProvider
}

func (p *Provider) RuntimeConfig() domain.ProviderRuntimeConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.runtimeConfigLocked()
}

func (p *Provider) runtimeConfigLocked() domain.ProviderRuntimeConfig {
	endpoint := strings.TrimSpace(p.endpoint)
	apiKey := strings.TrimSpace(p.apiKey)
	if apiKey == "" {
		apiKey = apiKeyFromEndpoint(endpoint)
	}
	return domain.ProviderRuntimeConfig{
		Name:          p.name,
		Label:         p.label,
		Endpoint:      endpoint,
		ProxyURL:      strings.TrimSpace(p.proxyURL),
		HasAPIKey:     apiKey != "",
		APIKeyPreview: previewAPIKey(apiKey),
		Configured:    endpoint != "" && (apiKey != "" || endpointHasAPIKey(endpoint)),
	}
}

func (p *Provider) UpdateRuntimeSettings(endpoint, apiKey, proxyURL *string) (domain.ProviderRuntimeConfig, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if endpoint != nil {
		p.endpoint = strings.TrimSpace(*endpoint)
	}
	if apiKey != nil {
		p.apiKey = strings.TrimSpace(*apiKey)
	}
	if proxyURL != nil {
		normalizedProxy := strings.TrimSpace(*proxyURL)
		client, err := buildProviderHTTPClient(p.client, normalizedProxy)
		if err != nil {
			return p.runtimeConfigLocked(), err
		}
		p.client = client
		p.proxyURL = normalizedProxy
	}

	return p.runtimeConfigLocked(), nil
}

func (p *Provider) AutoDetect(ctx context.Context) (domain.ProviderRuntimeConfig, error) {
	p.mu.RLock()
	name := p.name
	endpoint := strings.TrimSpace(p.endpoint)
	existingAPIKey := strings.TrimSpace(p.apiKey)
	client := p.client
	userAgent := p.userAgent
	p.mu.RUnlock()

	if endpoint == "" {
		endpoint = defaultEndpointForProvider(name)
	}
	if endpoint == "" {
		return p.RuntimeConfig(), errors.New("endpoint is not configured")
	}
	if existingAPIKey == "" {
		existingAPIKey = apiKeyFromEndpoint(endpoint)
	}
	if existingAPIKey != "" {
		return p.UpdateRuntimeSettings(&endpoint, &existingAPIKey, nil)
	}

	detectedAPIKey, err := detectAPIKey(ctx, name, endpoint, userAgent, client)
	if err != nil {
		return p.RuntimeConfig(), err
	}
	if detectedAPIKey == "" {
		return p.RuntimeConfig(), errors.New("api key not found")
	}

	return p.UpdateRuntimeSettings(&endpoint, &detectedAPIKey, nil)
}

func defaultEndpointForProvider(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "jackett":
		return "http://jackett:9117/api/v2.0/indexers/all/results/torznab/api"
	case "prowlarr":
		return "http://prowlarr:9696/1/api"
	default:
		return ""
	}
}

func buildProviderHTTPClient(base *http.Client, proxyRaw string) (*http.Client, error) {
	if base == nil {
		base = &http.Client{}
	}
	transport := cloneTransport(base.Transport)
	proxyValue := strings.TrimSpace(proxyRaw)
	if proxyValue == "" {
		transport.Proxy = nil
	} else {
		parsed, err := url.Parse(proxyValue)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			if err == nil {
				err = errors.New("proxy url must include scheme and host")
			}
			return nil, fmt.Errorf("invalid proxy url: %w", err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}

	return &http.Client{
		Timeout:       base.Timeout,
		CheckRedirect: base.CheckRedirect,
		Jar:           base.Jar,
		Transport:     transport,
	}, nil
}

func cloneTransport(base http.RoundTripper) *http.Transport {
	if transport, ok := base.(*http.Transport); ok && transport != nil {
		return transport.Clone()
	}
	return http.DefaultTransport.(*http.Transport).Clone()
}

func detectAPIKey(ctx context.Context, providerName, endpoint, userAgent string, client *http.Client) (string, error) {
	if key := apiKeyFromEndpoint(endpoint); key != "" {
		return key, nil
	}

	baseURL, err := baseProviderURL(endpoint)
	if err != nil {
		return "", err
	}

	provider := strings.ToLower(strings.TrimSpace(providerName))
	switch provider {
	case "prowlarr":
		if key, detectErr := detectProwlarrAPIKey(ctx, baseURL, userAgent, client); detectErr == nil && key != "" {
			return key, nil
		}
	case "jackett":
		if key, detectErr := detectJackettAPIKey(ctx, baseURL, userAgent, client); detectErr == nil && key != "" {
			return key, nil
		}
	}

	probeClient := clientWithCookieJar(client)
	if probeClient == nil {
		probeClient = &http.Client{Timeout: 8 * time.Second}
	}
	paths := apiKeyDetectionPaths(providerName)
	if len(paths) == 0 {
		paths = []string{"/"}
	}

	probeCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		probeCtx, cancel = context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
	}

	lastStatus := 0
	var lastErr error
	for _, path := range paths {
		reqURL := strings.TrimSuffix(baseURL, "/") + path
		req, reqErr := http.NewRequestWithContext(probeCtx, http.MethodGet, reqURL, nil)
		if reqErr != nil {
			lastErr = reqErr
			continue
		}
		if strings.TrimSpace(userAgent) != "" {
			req.Header.Set("User-Agent", strings.TrimSpace(userAgent))
		}
		req.Header.Set("Accept", "application/json,text/html,application/xml,text/xml,*/*")

		resp, doErr := probeClient.Do(req)
		if doErr != nil {
			lastErr = doErr
			continue
		}
		lastStatus = resp.StatusCode
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		resp.Body.Close()
		if key := extractAPIKeyFromBody(string(body)); key != "" {
			return key, nil
		}
	}

	if lastStatus > 0 {
		return "", fmt.Errorf("unable to detect api key (last status: %d)", lastStatus)
	}
	if lastErr != nil {
		return "", fmt.Errorf("unable to detect api key: %w", lastErr)
	}
	return "", errors.New("unable to detect api key")
}

func detectProwlarrAPIKey(ctx context.Context, baseURL, userAgent string, client *http.Client) (string, error) {
	probeClient := clientWithCookieJar(client)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(baseURL, "/")+"/initialize.json", nil)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(userAgent) != "" {
		req.Header.Set("User-Agent", strings.TrimSpace(userAgent))
	}
	req.Header.Set("Accept", "application/json")
	resp, err := probeClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("initialize.json status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		APIKey string `json:"apiKey"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", fmt.Errorf("parse initialize.json: %w", err)
	}
	return strings.TrimSpace(payload.APIKey), nil
}

func detectJackettAPIKey(ctx context.Context, baseURL, userAgent string, client *http.Client) (string, error) {
	probeClient := clientWithCookieJar(client)

	loginReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(baseURL, "/")+"/UI/Login", nil)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(userAgent) != "" {
		loginReq.Header.Set("User-Agent", strings.TrimSpace(userAgent))
	}
	loginResp, err := probeClient.Do(loginReq)
	if err != nil {
		return "", fmt.Errorf("login bootstrap failed: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(loginResp.Body, 2048))
	loginResp.Body.Close()

	cfgReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(baseURL, "/")+"/api/v2.0/server/config", nil)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(userAgent) != "" {
		cfgReq.Header.Set("User-Agent", strings.TrimSpace(userAgent))
	}
	cfgReq.Header.Set("Accept", "application/json")
	resp, err := probeClient.Do(cfgReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("server config status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", fmt.Errorf("parse server config: %w", err)
	}
	return strings.TrimSpace(payload.APIKey), nil
}

func clientWithCookieJar(base *http.Client) *http.Client {
	if base == nil {
		base = &http.Client{Timeout: 8 * time.Second}
	}
	jar := base.Jar
	if jar == nil {
		created, err := cookiejar.New(nil)
		if err == nil {
			jar = created
		}
	}
	clone := *base
	clone.Jar = jar
	return &clone
}

func baseProviderURL(endpoint string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", fmt.Errorf("invalid endpoint: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("endpoint must include scheme and host")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func apiKeyDetectionPaths(providerName string) []string {
	switch strings.ToLower(strings.TrimSpace(providerName)) {
	case "jackett":
		return []string{
			"/api/v2.0/server/config",
			"/UI/Dashboard",
			"/",
		}
	case "prowlarr":
		return []string{
			"/api/v1/config/host",
			"/api/v1/system/status",
			"/",
		}
	default:
		return []string{"/"}
	}
}

func extractAPIKeyFromBody(body string) string {
	text := strings.TrimSpace(body)
	if text == "" {
		return ""
	}
	for _, pattern := range []*regexp.Regexp{
		apiKeyJSONPattern,
		apiKeyXMLPattern,
		apiKeyValuePattern,
		apiKeyQueryPattern,
	} {
		matches := pattern.FindStringSubmatch(text)
		if len(matches) < 2 {
			continue
		}
		key := strings.TrimSpace(matches[1])
		if key != "" {
			return key
		}
	}
	return ""
}

func apiKeyFromEndpoint(endpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Query().Get("apikey"))
}

func previewAPIKey(apiKey string) string {
	value := strings.TrimSpace(apiKey)
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return strings.Repeat("*", len(value))
	}
	return value[:4] + "..." + value[len(value)-4:]
}
