package search

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"torrentstream/searchservice/internal/domain"
)

var (
	ErrInvalidQuery    = errors.New("query is required")
	ErrNoProviders     = errors.New("no search providers configured")
	ErrUnknownProvider = errors.New("unknown provider")
	ErrInvalidOffset   = errors.New("offset must be >= 0")
)

type Provider interface {
	Name() string
	Info() domain.ProviderInfo
	Search(ctx context.Context, request domain.SearchRequest) ([]domain.SearchResult, error)
}

// IndexerLister is an optional interface that providers with per-indexer fan-out
// can implement to expose their sub-indexer list for diagnostics.
type IndexerLister interface {
	FanOutActive() bool
	ListSubIndexers() []domain.SubIndexerInfo
}

// TMDBClient is an optional interface for TMDB search enrichment.
type TMDBClient interface {
	Enabled() bool
	SearchMulti(ctx context.Context, query string, lang string) ([]tmdbResult, error)
}

type tmdbResult = struct {
	ID           int     `json:"id"`
	Title        string  `json:"title,omitempty"`
	Name         string  `json:"name,omitempty"`
	Overview     string  `json:"overview,omitempty"`
	PosterPath   string  `json:"poster_path,omitempty"`
	VoteAverage  float64 `json:"vote_average,omitempty"`
	ReleaseDate  string  `json:"release_date,omitempty"`
	FirstAirDate string  `json:"first_air_date,omitempty"`
	MediaType    string  `json:"media_type,omitempty"`
}

type Service struct {
	providers     map[string]Provider
	timeout       time.Duration
	cacheDisabled bool
	cacheMu       sync.RWMutex
	cache         map[string]*cachedSearchResponse
	popular       map[string]*popularQuery
	warmerCfg     searchWarmerConfig
	warmerRun     atomic.Bool
	redisCache    *RedisCacheBackend
	tmdb          TMDBClient
	healthMu      sync.Mutex
	health        map[string]*providerHealth
}

type ServiceOption func(*Service)

func WithRedisCache(backend *RedisCacheBackend) ServiceOption {
	return func(s *Service) {
		s.redisCache = backend
	}
}

func WithCacheTTL(ttl time.Duration) ServiceOption {
	return func(s *Service) {
		if ttl > 0 {
			s.warmerCfg.cacheTTL = ttl
			s.warmerCfg.staleTTL = ttl * 3
		}
	}
}

func WithCacheDisabled(disabled bool) ServiceOption {
	return func(s *Service) {
		s.cacheDisabled = disabled
	}
}

func WithTMDB(client TMDBClient) ServiceOption {
	return func(s *Service) {
		s.tmdb = client
	}
}

func NewService(providers []Provider, timeout time.Duration, opts ...ServiceOption) *Service {
	registry := make(map[string]Provider, len(providers))
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(provider.Name()))
		if name == "" {
			continue
		}
		registry[name] = provider
		for _, alias := range providerAliases(name) {
			if _, exists := registry[alias]; !exists {
				registry[alias] = provider
			}
		}
	}

	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	svc := &Service{
		providers: registry,
		timeout:   timeout,
		cache:     make(map[string]*cachedSearchResponse),
		popular:   make(map[string]*popularQuery),
		warmerCfg: defaultSearchWarmerConfig(),
		health:    make(map[string]*providerHealth),
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

func (s *Service) StartBackground(ctx context.Context) {
	if s.warmerRun.CompareAndSwap(false, true) {
		go s.runWarmer(ctx)
	}
}

func providerAliases(name string) []string {
	switch name {
	case "piratebay":
		return []string{"bittorrent", "tpb"}
	case "1337x":
		return []string{"x1337"}
	case "rutracker":
		return []string{"rt"}
	default:
		return nil
	}
}

func (s *Service) Providers() []domain.ProviderInfo {
	if len(s.providers) == 0 {
		return nil
	}
	items := make([]domain.ProviderInfo, 0, len(s.providers))
	seen := make(map[string]struct{}, len(s.providers))
	for _, provider := range s.providers {
		info := provider.Info()
		if info.Name == "" {
			info.Name = strings.ToLower(strings.TrimSpace(provider.Name()))
		}
		info.Name = strings.ToLower(strings.TrimSpace(info.Name))
		if info.Name == "" {
			continue
		}
		if _, exists := seen[info.Name]; exists {
			continue
		}
		seen[info.Name] = struct{}{}
		if info.Label == "" {
			info.Label = info.Name
		}
		items = append(items, info)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items
}
