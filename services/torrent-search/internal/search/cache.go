package search

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
	"torrentstream/searchservice/internal/domain"
	"torrentstream/searchservice/internal/metrics"
)

const (
	defaultCacheTTL          = 6 * time.Hour
	defaultStaleTTL          = 18 * time.Hour
	defaultWarmInterval      = 5 * time.Minute
	defaultWarmTopQueries    = 12
	defaultCacheMaxEntries   = 400
	defaultPopularMaxEntries = 200
	maxConcurrentWarmRefreshes = 3 // Limit parallel cache warm refreshes to avoid overwhelming providers
)

type searchWarmerConfig struct {
	cacheTTL          time.Duration
	staleTTL          time.Duration
	warmInterval      time.Duration
	warmTopQueries    int
	cacheMaxEntries   int
	popularMaxEntries int
}

type cachedSearchResponse struct {
	response    domain.SearchResponse
	updatedAt   time.Time
	expiresAt   time.Time
	staleUntil  time.Time
	refreshing  bool
	refreshOnce sync.Once // Ensures only one refresh per stale period
}

type popularQuery struct {
	request   domain.SearchRequest
	providers []string
	hits      int
	lastSeen  time.Time
	lastWarm  time.Time
}

type warmSpec struct {
	key       string
	request   domain.SearchRequest
	providers []string
}

func defaultSearchWarmerConfig() searchWarmerConfig {
	return searchWarmerConfig{
		cacheTTL:          defaultCacheTTL,
		staleTTL:          defaultStaleTTL,
		warmInterval:      defaultWarmInterval,
		warmTopQueries:    defaultWarmTopQueries,
		cacheMaxEntries:   defaultCacheMaxEntries,
		popularMaxEntries: defaultPopularMaxEntries,
	}
}

func (s *Service) runWarmer(ctx context.Context) {
	ticker := time.NewTicker(s.warmerCfg.warmInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runWarmCycle(ctx)
		}
	}
}

func (s *Service) runWarmCycle(ctx context.Context) {
	now := time.Now()
	specs := s.collectWarmSpecs(now)
	if len(specs) == 0 {
		return
	}

	// Parallelize warm refreshes with bounded concurrency to avoid overwhelming providers.
	// With 12 queries x 15s each, sequential execution takes 180s on a 5min interval.
	// Parallelizing to 3 concurrent refreshes reduces this to ~60s.
	sem := semaphore.NewWeighted(maxConcurrentWarmRefreshes)
	var wg sync.WaitGroup

	for _, spec := range specs {
		// Check context cancellation between queries
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		default:
		}

		wg.Add(1)
		go func(spec warmSpec) {
			defer wg.Done()

			if err := sem.Acquire(ctx, 1); err != nil {
				s.cacheClearRefreshing(spec.key)
				return
			}
			defer sem.Release(1)

			refreshCtx, cancel := context.WithTimeout(ctx, s.timeout+2*time.Second)
			defer cancel()

			_, err := s.searchNoCache(refreshCtx, spec.request, spec.providers)
			if err != nil {
				s.cacheClearRefreshing(spec.key)
			}
		}(spec)
	}

	wg.Wait()
}

func (s *Service) collectWarmSpecs(now time.Time) []warmSpec {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	if len(s.popular) == 0 {
		return nil
	}

	keys := make([]string, 0, len(s.popular))
	for key := range s.popular {
		keys = append(keys, key)
	}

	sort.Slice(keys, func(i, j int) bool {
		left := s.popular[keys[i]]
		right := s.popular[keys[j]]
		if left.hits != right.hits {
			return left.hits > right.hits
		}
		return left.lastSeen.After(right.lastSeen)
	})

	limit := s.warmerCfg.warmTopQueries
	if limit <= 0 {
		limit = defaultWarmTopQueries
	}
	if len(keys) < limit {
		limit = len(keys)
	}

	specs := make([]warmSpec, 0, limit)
	for _, key := range keys[:limit] {
		pop := s.popular[key]
		if pop == nil {
			continue
		}
		if !pop.lastWarm.IsZero() && now.Sub(pop.lastWarm) < s.warmerCfg.warmInterval/2 {
			continue
		}
		if cacheEntry, ok := s.cache[key]; ok && now.Before(cacheEntry.expiresAt) {
			continue
		}
		pop.lastWarm = now
		if cacheEntry := s.cache[key]; cacheEntry != nil {
			cacheEntry.refreshing = true
		}
		specs = append(specs, warmSpec{
			key:       key,
			request:   pop.request,
			providers: append([]string(nil), pop.providers...),
		})
	}
	return specs
}

func (s *Service) cacheLookup(key string, now time.Time) (domain.SearchResponse, bool, bool) {
	// Try Redis first
	if s.redisCache != nil {
		resp, found, err := s.redisCache.Get(context.Background(), key)
		if err == nil && found {
			metrics.CacheHitsTotal.Inc()
			// Keep a local in-memory copy so the warmer can reason about freshness without re-querying Redis.
			s.cacheStoreMemoryOnly(key, resp, now)
			return resp, true, false
		}
	}

	// Fallback to in-memory
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	entry, ok := s.cache[key]
	if !ok {
		metrics.CacheMissesTotal.Inc()
		return domain.SearchResponse{}, false, false
	}

	if now.Before(entry.expiresAt) {
		metrics.CacheHitsTotal.Inc()
		return cloneSearchResponse(entry.response), true, false
	}

	if now.Before(entry.staleUntil) {
		metrics.CacheHitsTotal.Inc()
		// Use sync.Once to ensure only one refresh happens per stale period.
		// This prevents duplicate refreshes even if multiple requests arrive
		// during the stale window or if a previous refresh failed quickly.
		needsRefresh := false
		entry.refreshOnce.Do(func() {
			needsRefresh = true
			entry.refreshing = true
		})
		return cloneSearchResponse(entry.response), true, needsRefresh
	}

	metrics.CacheMissesTotal.Inc()
	delete(s.cache, key)
	delete(s.popular, key)
	return domain.SearchResponse{}, false, false
}

func (s *Service) cacheStore(key string, response domain.SearchResponse, now time.Time) {
	cacheTTL := s.warmerCfg.cacheTTL
	if cacheTTL <= 0 {
		cacheTTL = defaultCacheTTL
	}
	staleTTL := s.warmerCfg.staleTTL
	if staleTTL <= cacheTTL {
		staleTTL = cacheTTL * 3
	}

	// Store in Redis if available
	if s.redisCache != nil {
		_ = s.redisCache.Set(context.Background(), key, response, cacheTTL)
	}

	// Also store in memory
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	s.cache[key] = &cachedSearchResponse{
		response:   cloneSearchResponse(response),
		updatedAt:  now,
		expiresAt:  now.Add(cacheTTL),
		staleUntil: now.Add(staleTTL),
		refreshing: false,
	}

	s.trimCacheLocked(now)
}

func (s *Service) cacheClearRefreshing(key string) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if entry := s.cache[key]; entry != nil {
		entry.refreshing = false
	}
}

func (s *Service) markPopular(key string, request domain.SearchRequest, providers []string, now time.Time) {
	// Warm cache for first-page requests; deeper pages are cheap once first page is warm.
	if request.Offset > 0 {
		return
	}

	filters := domain.SearchFilters{
		Quality:       append([]string(nil), request.Filters.Quality...),
		ContentType:   request.Filters.ContentType,
		YearFrom:      request.Filters.YearFrom,
		YearTo:        request.Filters.YearTo,
		DubbingGroups: append([]string(nil), request.Filters.DubbingGroups...),
		DubbingTypes:  append([]string(nil), request.Filters.DubbingTypes...),
		MinSeeders:    request.Filters.MinSeeders,
	}

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	pop, ok := s.popular[key]
	if !ok {
		s.popular[key] = &popularQuery{
			request: domain.SearchRequest{
				Query:     request.Query,
				Limit:     request.Limit,
				Offset:    request.Offset,
				SortBy:    request.SortBy,
				SortOrder: request.SortOrder,
				Profile:   request.Profile,
				Filters:   filters,
			},
			providers: append([]string(nil), providers...),
			hits:      1,
			lastSeen:  now,
		}
	} else {
		pop.hits++
		pop.lastSeen = now
		pop.request = domain.SearchRequest{
			Query:     request.Query,
			Limit:     request.Limit,
			Offset:    request.Offset,
			SortBy:    request.SortBy,
			SortOrder: request.SortOrder,
			Profile:   request.Profile,
			Filters:   filters,
		}
		pop.providers = append(pop.providers[:0], providers...)
	}

	limit := s.warmerCfg.popularMaxEntries
	if limit <= 0 {
		limit = defaultPopularMaxEntries
	}
	if len(s.popular) <= limit {
		return
	}

	// Drop least popular + oldest query.
	type pair struct {
		key   string
		value *popularQuery
	}
	items := make([]pair, 0, len(s.popular))
	for popKey, value := range s.popular {
		items = append(items, pair{key: popKey, value: value})
	}
	sort.Slice(items, func(i, j int) bool {
		left := items[i].value
		right := items[j].value
		if left.hits != right.hits {
			return left.hits < right.hits
		}
		return left.lastSeen.Before(right.lastSeen)
	})
	for i := 0; i < len(items)-limit; i++ {
		delete(s.popular, items[i].key)
	}
}

func (s *Service) trimCacheLocked(now time.Time) {
	maxEntries := s.warmerCfg.cacheMaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultCacheMaxEntries
	}

	for key, entry := range s.cache {
		if now.After(entry.staleUntil) {
			delete(s.cache, key)
		}
	}

	if len(s.cache) <= maxEntries {
		return
	}

	type pair struct {
		key   string
		entry *cachedSearchResponse
	}
	items := make([]pair, 0, len(s.cache))
	for key, entry := range s.cache {
		items = append(items, pair{key: key, entry: entry})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].entry.updatedAt.Before(items[j].entry.updatedAt)
	})
	for i := 0; i < len(items)-maxEntries; i++ {
		delete(s.cache, items[i].key)
	}
}

func cloneSearchResponse(response domain.SearchResponse) domain.SearchResponse {
	cloned := response
	if response.Items != nil {
		cloned.Items = make([]domain.SearchResult, len(response.Items))
		for i, item := range response.Items {
			copied := item
			if item.PublishedAt != nil {
				value := *item.PublishedAt
				copied.PublishedAt = &value
			}
			copied.Enrichment.Audio = append([]string(nil), item.Enrichment.Audio...)
			copied.Enrichment.Subtitles = append([]string(nil), item.Enrichment.Subtitles...)
			copied.Enrichment.Screenshots = append([]string(nil), item.Enrichment.Screenshots...)
			cloned.Items[i] = copied
		}
	}
	if response.Providers != nil {
		cloned.Providers = append([]domain.ProviderStatus(nil), response.Providers...)
	}
	return cloned
}

func buildSearchCacheKey(request domain.SearchRequest, providers []string) string {
	names := normalizeProviderNames(providers)
	return strings.Join([]string{
		"q=" + strings.ToLower(strings.TrimSpace(request.Query)),
		"l=" + strconvI64(int64(request.Limit)),
		"o=" + strconvI64(int64(request.Offset)),
		"sb=" + string(request.SortBy),
		"so=" + string(request.SortOrder),
		"profile=" + rankingProfileKey(request.Profile),
		"f=" + filtersKey(request.Filters),
		"p=" + strings.Join(names, ","),
	}, "|")
}

func rankingProfileKey(profile domain.SearchRankingProfile) string {
	audio := strings.Join(normalizeProviderNames(profile.PreferredAudio), ",")
	subs := strings.Join(normalizeProviderNames(profile.PreferredSubtitles), ",")
	flags := []string{}
	if profile.PreferSeries {
		flags = append(flags, "series")
	}
	if profile.PreferMovies {
		flags = append(flags, "movies")
	}
	return strings.Join([]string{
		"fw=" + strconvI64(int64(profile.FreshnessWeight*100)),
		"sw=" + strconvI64(int64(profile.SeedersWeight*100)),
		"qw=" + strconvI64(int64(profile.QualityWeight*100)),
		"lw=" + strconvI64(int64(profile.LanguageWeight*100)),
		"zw=" + strconvI64(int64(profile.SizeWeight*100)),
		"sz=" + strconvI64(profile.TargetSizeBytes),
		"pa=" + audio,
		"ps=" + subs,
		"f=" + strings.Join(flags, "+"),
	}, ";")
}

func filtersKey(filters domain.SearchFilters) string {
	qualities := strings.Join(normalizeProviderNames(filters.Quality), ",")
	groups := strings.Join(normalizeProviderNames(filters.DubbingGroups), ",")
	types := strings.Join(normalizeProviderNames(filters.DubbingTypes), ",")
	contentType := strings.ToLower(strings.TrimSpace(filters.ContentType))

	return strings.Join([]string{
		"q=" + qualities,
		"ct=" + contentType,
		"yf=" + strconvI64(int64(filters.YearFrom)),
		"yt=" + strconvI64(int64(filters.YearTo)),
		"dg=" + groups,
		"dt=" + types,
		"ms=" + strconvI64(int64(filters.MinSeeders)),
	}, ";")
}

func normalizeProviderNames(providerNames []string) []string {
	if len(providerNames) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(providerNames))
	names := make([]string, 0, len(providerNames))
	for _, raw := range providerNames {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		names = append(names, value)
	}
	sort.Strings(names)
	return names
}

func (s *Service) cacheStoreMemoryOnly(key string, response domain.SearchResponse, now time.Time) {
	cacheTTL := s.warmerCfg.cacheTTL
	if cacheTTL <= 0 {
		cacheTTL = defaultCacheTTL
	}
	staleTTL := s.warmerCfg.staleTTL
	if staleTTL <= cacheTTL {
		staleTTL = cacheTTL * 3
	}

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	s.cache[key] = &cachedSearchResponse{
		response:   cloneSearchResponse(response),
		updatedAt:  now,
		expiresAt:  now.Add(cacheTTL),
		staleUntil: now.Add(staleTTL),
		refreshing: false,
	}
	s.trimCacheLocked(now)
}
