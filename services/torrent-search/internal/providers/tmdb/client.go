package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultBaseURL  = "https://api.themoviedb.org/3"
	posterBaseURL   = "https://image.tmdb.org/t/p/w300"
	defaultLanguage = "ru-RU"
	redisCacheKey   = "tsearch:tmdb:"
)

type Client struct {
	apiKey   string
	baseURL  string
	http     *http.Client
	redis    *redis.Client
	cacheTTL time.Duration
}

type Config struct {
	APIKey   string
	BaseURL  string
	Client   *http.Client
	Redis    *redis.Client
	CacheTTL time.Duration
}

type SearchResult struct {
	ID          int     `json:"id"`
	Title       string  `json:"title,omitempty"`
	Name        string  `json:"name,omitempty"`
	Overview    string  `json:"overview,omitempty"`
	PosterPath  string  `json:"poster_path,omitempty"`
	VoteAverage float64 `json:"vote_average,omitempty"`
	ReleaseDate string  `json:"release_date,omitempty"`
	FirstAirDate string `json:"first_air_date,omitempty"`
	MediaType   string  `json:"media_type,omitempty"`
}

func (r SearchResult) DisplayTitle() string {
	if r.Title != "" {
		return r.Title
	}
	return r.Name
}

func (r SearchResult) Year() int {
	date := r.ReleaseDate
	if date == "" {
		date = r.FirstAirDate
	}
	if len(date) >= 4 {
		year := 0
		for _, c := range date[:4] {
			if c >= '0' && c <= '9' {
				year = year*10 + int(c-'0')
			}
		}
		return year
	}
	return 0
}

func (r SearchResult) PosterURL() string {
	if r.PosterPath == "" {
		return ""
	}
	return posterBaseURL + r.PosterPath
}

type multiSearchResponse struct {
	Results []SearchResult `json:"results"`
}

func NewClient(cfg Config) *Client {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	httpClient := cfg.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	cacheTTL := cfg.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = 7 * 24 * time.Hour
	}
	return &Client{
		apiKey:   strings.TrimSpace(cfg.APIKey),
		baseURL:  strings.TrimRight(baseURL, "/"),
		http:     httpClient,
		redis:    cfg.Redis,
		cacheTTL: cacheTTL,
	}
}

func (c *Client) Enabled() bool {
	return c.apiKey != ""
}

func (c *Client) SearchMulti(ctx context.Context, query string, lang string) ([]SearchResult, error) {
	if !c.Enabled() {
		return nil, nil
	}
	if lang == "" {
		lang = defaultLanguage
	}

	cacheKey := fmt.Sprintf("multi:%s:%s", strings.ToLower(strings.TrimSpace(query)), lang)

	// Check Redis cache
	if c.redis != nil {
		data, err := c.redis.Get(ctx, redisCacheKey+cacheKey).Bytes()
		if err == nil {
			var results []SearchResult
			if json.Unmarshal(data, &results) == nil {
				return results, nil
			}
		}
	}

	params := url.Values{
		"api_key":  {c.apiKey},
		"query":    {strings.TrimSpace(query)},
		"language": {lang},
	}

	reqURL := c.baseURL + "/search/multi?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("tmdb HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}

	var response multiSearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}

	// Filter to movies and TV only
	results := make([]SearchResult, 0, len(response.Results))
	for _, r := range response.Results {
		if r.MediaType == "movie" || r.MediaType == "tv" {
			results = append(results, r)
		}
	}

	// Cache in Redis
	if c.redis != nil {
		if data, err := json.Marshal(results); err == nil {
			_ = c.redis.Set(ctx, redisCacheKey+cacheKey, data, c.cacheTTL).Err()
		}
	}

	return results, nil
}
