package torznab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"torrentstream/searchservice/internal/domain"
)

// adminClientTimeout is a longer timeout for administrative API calls
// (e.g. applying FlareSolverr settings) where the target service may
// perform its own outbound validation before responding.
const adminClientTimeout = 60 * time.Second

const (
	jackettProviderName  = "jackett"
	prowlarrProviderName = "prowlarr"
)

type jackettServerConfig struct {
	Port                      int    `json:"port"`
	External                  bool   `json:"external"`
	LocalBindAddress          string `json:"local_bind_address"`
	CORS                      bool   `json:"cors"`
	UpdateDisabled            bool   `json:"updatedisabled"`
	Prerelease                bool   `json:"prerelease"`
	BlackholeDir              string `json:"blackholedir"`
	Logging                   bool   `json:"logging"`
	BasePathOverride          string `json:"basepathoverride"`
	BaseURLOverride           string `json:"baseurloverride"`
	CacheEnabled              bool   `json:"cache_enabled"`
	CacheTTL                  int    `json:"cache_ttl"`
	CacheMaxResultsPerIndexer int    `json:"cache_max_results_per_indexer"`
	FlareSolverrURL           string `json:"flaresolverrurl"`
	FlareSolverrMaxTimeout    int    `json:"flaresolverr_maxtimeout"`
	OMDBKey                   string `json:"omdbkey"`
	OMDBURL                   string `json:"omdburl"`
	ProxyType                 int    `json:"proxy_type"`
	ProxyURL                  string `json:"proxy_url"`
	ProxyPort                 *int   `json:"proxy_port"`
	ProxyUsername             string `json:"proxy_username"`
	ProxyPassword             string `json:"proxy_password"`
}

func (s *RuntimeConfigService) GetFlareSolverrSettings(ctx context.Context) (domain.FlareSolverrSettings, error) {
	defaultURL := normalizeFlareSolverrURL(s.defaultFlareSolverrURL)
	if defaultURL == "" {
		defaultURL = "http://flaresolverr:8191/"
	}

	items := make([]domain.FlareSolverrProviderStatus, 0, 2)
	var selectedURL string
	for _, providerName := range []string{jackettProviderName, prowlarrProviderName} {
		provider := s.providers[providerName]
		if provider == nil {
			continue
		}
		status := domain.FlareSolverrProviderStatus{
			Provider: providerName,
		}
		switch providerName {
		case jackettProviderName:
			cfg, err := fetchJackettConfig(ctx, provider)
			if err != nil {
				status.Message = err.Error()
				items = append(items, status)
				continue
			}
			status.URL = normalizeFlareSolverrURL(cfg.FlareSolverrURL)
			status.Configured = status.URL != ""
		case prowlarrProviderName:
			host, configured, err := fetchProwlarrFlareSolverrURL(ctx, provider)
			if err != nil {
				status.Message = err.Error()
				items = append(items, status)
				continue
			}
			status.URL = normalizeFlareSolverrURL(host)
			status.Configured = configured
		}
		if selectedURL == "" && status.URL != "" {
			selectedURL = status.URL
		}
		items = append(items, status)
	}

	if selectedURL == "" {
		selectedURL = defaultURL
	}
	return domain.FlareSolverrSettings{
		DefaultURL: defaultURL,
		URL:        selectedURL,
		Providers:  items,
	}, nil
}

func (s *RuntimeConfigService) ApplyFlareSolverr(
	ctx context.Context,
	rawURL string,
	providerFilter string,
) (domain.FlareSolverrApplyResponse, error) {
	targetURL := normalizeFlareSolverrURL(rawURL)
	if targetURL == "" {
		targetURL = normalizeFlareSolverrURL(s.defaultFlareSolverrURL)
	}
	if targetURL == "" {
		return domain.FlareSolverrApplyResponse{}, errors.New("flaresolverr url is required")
	}
	if _, err := url.ParseRequestURI(targetURL); err != nil {
		return domain.FlareSolverrApplyResponse{}, fmt.Errorf("invalid flaresolverr url: %w", err)
	}

	targets := make([]string, 0, 2)
	filter := strings.ToLower(strings.TrimSpace(providerFilter))
	if filter != "" {
		if s.providers[filter] == nil {
			return domain.FlareSolverrApplyResponse{}, fmt.Errorf("%w: %s", ErrUnknownProvider, filter)
		}
		targets = append(targets, filter)
	} else {
		for _, providerName := range []string{jackettProviderName, prowlarrProviderName} {
			if s.providers[providerName] != nil {
				targets = append(targets, providerName)
			}
		}
	}

	results := make([]domain.FlareSolverrApplyResult, 0, len(targets))
	for _, providerName := range targets {
		provider := s.providers[providerName]
		if provider == nil {
			results = append(results, domain.FlareSolverrApplyResult{
				Provider: providerName,
				OK:       false,
				Status:   "error",
				Message:  "provider is not configured",
			})
			continue
		}
		var err error
		switch providerName {
		case jackettProviderName:
			err = applyJackettFlareSolverrURL(ctx, provider, targetURL)
		case prowlarrProviderName:
			err = applyProwlarrFlareSolverrURL(ctx, provider, targetURL)
		default:
			err = errors.New("provider is not supported")
		}
		if err != nil {
			results = append(results, domain.FlareSolverrApplyResult{
				Provider: providerName,
				OK:       false,
				Status:   "error",
				Message:  err.Error(),
			})
			continue
		}
		results = append(results, domain.FlareSolverrApplyResult{
			Provider: providerName,
			OK:       true,
			Status:   "applied",
			Message:  "FlareSolverr linked successfully",
		})
	}

	return domain.FlareSolverrApplyResponse{
		URL:     targetURL,
		Results: results,
	}, nil
}

func applyJackettFlareSolverrURL(ctx context.Context, provider *Provider, targetURL string) error {
	cfg, err := fetchJackettConfig(ctx, provider)
	if err != nil {
		return err
	}
	cfg.FlareSolverrURL = normalizeFlareSolverrURL(targetURL)
	if cfg.FlareSolverrMaxTimeout <= 0 {
		cfg.FlareSolverrMaxTimeout = 60000
	}

	client, baseURL, userAgent, err := jackettSessionClient(provider)
	if err != nil {
		return err
	}
	if err := jackettBootstrapSession(ctx, client, baseURL, userAgent); err != nil {
		return err
	}

	payload, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(baseURL, "/")+"/api/v2.0/server/config", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(userAgent) != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("jackett update failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func fetchJackettConfig(ctx context.Context, provider *Provider) (jackettServerConfig, error) {
	client, baseURL, userAgent, err := jackettSessionClient(provider)
	if err != nil {
		return jackettServerConfig{}, err
	}
	if err := jackettBootstrapSession(ctx, client, baseURL, userAgent); err != nil {
		return jackettServerConfig{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(baseURL, "/")+"/api/v2.0/server/config", nil)
	if err != nil {
		return jackettServerConfig{}, err
	}
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(userAgent) != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return jackettServerConfig{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return jackettServerConfig{}, fmt.Errorf("jackett config request failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var cfg jackettServerConfig
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&cfg); err != nil {
		return jackettServerConfig{}, fmt.Errorf("decode jackett config: %w", err)
	}
	return cfg, nil
}

func jackettSessionClient(provider *Provider) (*http.Client, string, string, error) {
	snapshot := provider.snapshot()
	baseURL, err := baseProviderURL(snapshot.endpoint)
	if err != nil {
		return nil, "", "", err
	}
	client := clientWithCookieJar(snapshot.client)
	return client, baseURL, snapshot.userAgent, nil
}

func jackettBootstrapSession(ctx context.Context, client *http.Client, baseURL, userAgent string) error {
	loginURL := strings.TrimSuffix(baseURL, "/") + "/UI/Login"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loginURL, nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(userAgent) != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("jackett login bootstrap failed: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
	resp.Body.Close()
	return nil
}

func applyProwlarrFlareSolverrURL(ctx context.Context, provider *Provider, targetURL string) error {
	apiKey, baseURL, client, userAgent, err := resolveProwlarrAdminClient(provider)
	if err != nil {
		return err
	}

	proxies, err := prowlarrGetIndexerProxies(ctx, client, baseURL, apiKey, userAgent)
	if err != nil && strings.Contains(err.Error(), "status 401") {
		empty := ""
		provider.UpdateRuntimeSettings(nil, &empty, nil)
		if detected, detectErr := provider.AutoDetect(ctx); detectErr == nil && detected.HasAPIKey {
			apiKey, baseURL, client, userAgent, err = resolveProwlarrAdminClient(provider)
			if err != nil {
				return err
			}
			proxies, err = prowlarrGetIndexerProxies(ctx, client, baseURL, apiKey, userAgent)
		}
	}
	if err != nil {
		return err
	}
	for _, proxy := range proxies {
		implementation := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", proxy["implementation"])))
		if implementation != "flaresolverr" {
			continue
		}
		setProwlarrFieldValue(proxy, "host", targetURL)
		id, _ := asInt(proxy["id"])
		if id <= 0 {
			continue
		}
		return prowlarrUpdateIndexerProxy(ctx, client, baseURL, apiKey, userAgent, id, proxy)
	}

	schemaItems, err := prowlarrGetIndexerProxySchema(ctx, client, baseURL, apiKey, userAgent)
	if err != nil {
		return err
	}
	for _, schema := range schemaItems {
		implementation := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", schema["implementation"])))
		if implementation != "flaresolverr" {
			continue
		}
		payload := map[string]any{
			"name":               "FlareSolverr",
			"implementation":     schema["implementation"],
			"implementationName": schema["implementationName"],
			"configContract":     schema["configContract"],
			"tags":               []any{},
			"fields":             schema["fields"],
		}
		setProwlarrFieldValue(payload, "host", targetURL)
		return prowlarrCreateIndexerProxy(ctx, client, baseURL, apiKey, userAgent, payload)
	}
	return errors.New("prowlarr flaresolverr schema not found")
}

func fetchProwlarrFlareSolverrURL(ctx context.Context, provider *Provider) (string, bool, error) {
	apiKey, baseURL, client, userAgent, err := resolveProwlarrAdminClient(provider)
	if err != nil {
		return "", false, err
	}
	items, err := prowlarrGetIndexerProxies(ctx, client, baseURL, apiKey, userAgent)
	if err != nil && strings.Contains(err.Error(), "status 401") {
		// Stale API key — clear it and re-detect.
		empty := ""
		provider.UpdateRuntimeSettings(nil, &empty, nil)
		if detected, detectErr := provider.AutoDetect(ctx); detectErr == nil && detected.HasAPIKey {
			apiKey, baseURL, client, userAgent, err = resolveProwlarrAdminClient(provider)
			if err != nil {
				return "", false, err
			}
			items, err = prowlarrGetIndexerProxies(ctx, client, baseURL, apiKey, userAgent)
		}
	}
	if err != nil {
		return "", false, err
	}
	for _, item := range items {
		implementation := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", item["implementation"])))
		if implementation != "flaresolverr" {
			continue
		}
		if host := getProwlarrFieldValue(item, "host"); strings.TrimSpace(host) != "" {
			return host, true, nil
		}
		return "", false, nil
	}
	return "", false, nil
}

func resolveProwlarrClient(provider *Provider) (string, string, *http.Client, string, error) {
	return resolveProwlarrClientWithTimeout(provider, 0)
}

func resolveProwlarrAdminClient(provider *Provider) (string, string, *http.Client, string, error) {
	return resolveProwlarrClientWithTimeout(provider, adminClientTimeout)
}

func resolveProwlarrClientWithTimeout(provider *Provider, timeout time.Duration) (string, string, *http.Client, string, error) {
	snapshot := provider.snapshot()
	baseURL, err := baseProviderURL(snapshot.endpoint)
	if err != nil {
		return "", "", nil, "", err
	}
	apiKey := strings.TrimSpace(snapshot.apiKey)
	if apiKey == "" {
		if detected, detectErr := provider.AutoDetect(context.Background()); detectErr == nil && detected.HasAPIKey {
			refreshed := provider.snapshot()
			apiKey = strings.TrimSpace(refreshed.apiKey)
			snapshot = refreshed
		}
	}
	if apiKey == "" {
		return "", "", nil, "", errors.New("prowlarr api key is not configured")
	}
	client := snapshot.client
	if client == nil {
		client = &http.Client{}
	}
	if timeout > 0 && (client.Timeout == 0 || client.Timeout < timeout) {
		clone := *client
		clone.Timeout = timeout
		client = &clone
	}
	return apiKey, baseURL, client, snapshot.userAgent, nil
}

func prowlarrGetIndexerProxies(ctx context.Context, client *http.Client, baseURL, apiKey, userAgent string) ([]map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(baseURL, "/")+"/api/v1/indexerProxy", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(userAgent) != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("prowlarr indexerProxy request failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var items []map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func prowlarrGetIndexerProxySchema(ctx context.Context, client *http.Client, baseURL, apiKey, userAgent string) ([]map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(baseURL, "/")+"/api/v1/indexerProxy/schema", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(userAgent) != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("prowlarr schema request failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var items []map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func prowlarrUpdateIndexerProxy(
	ctx context.Context,
	client *http.Client,
	baseURL,
	apiKey,
	userAgent string,
	id int,
	payload map[string]any,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, fmt.Sprintf("%s/api/v1/indexerProxy/%d", strings.TrimSuffix(baseURL, "/"), id), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(userAgent) != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("prowlarr update failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func prowlarrCreateIndexerProxy(
	ctx context.Context,
	client *http.Client,
	baseURL,
	apiKey,
	userAgent string,
	payload map[string]any,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(baseURL, "/")+"/api/v1/indexerProxy", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(userAgent) != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("prowlarr create failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func setProwlarrFieldValue(container map[string]any, fieldName string, value any) {
	fieldsRaw, ok := container["fields"]
	if !ok {
		return
	}
	fields, ok := fieldsRaw.([]any)
	if !ok {
		return
	}
	for _, raw := range fields {
		field, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", field["name"])))
		if name != strings.ToLower(strings.TrimSpace(fieldName)) {
			continue
		}
		field["value"] = value
	}
}

func getProwlarrFieldValue(container map[string]any, fieldName string) string {
	fieldsRaw, ok := container["fields"]
	if !ok {
		return ""
	}
	fields, ok := fieldsRaw.([]any)
	if !ok {
		return ""
	}
	for _, raw := range fields {
		field, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", field["name"])))
		if name != strings.ToLower(strings.TrimSpace(fieldName)) {
			continue
		}
		return strings.TrimSpace(fmt.Sprintf("%v", field["value"]))
	}
	return ""
}

func asInt(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	case json.Number:
		v, err := value.Int64()
		if err != nil {
			return 0, false
		}
		return int(v), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func normalizeFlareSolverrURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		value = "http://" + value
	}
	if !strings.HasSuffix(value, "/") {
		value += "/"
	}
	return value
}
