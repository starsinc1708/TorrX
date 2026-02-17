package search

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"log/slog"

	"golang.org/x/sync/semaphore"
	"torrentstream/searchservice/internal/domain"
)

// maxConcurrentProviders limits the number of provider queries that can run simultaneously.
// This prevents overwhelming the system or remote servers when many providers are configured
// (e.g., Jackett with 20+ indexers).
const maxConcurrentProviders = 10

type preparedSearch struct {
	query         string
	queryMeta     titleMeta
	limit         int
	offset        int
	fetchLimit    int
	sortBy        domain.SearchSortBy
	sortOrder     domain.SearchSortOrder
	profile       domain.SearchRankingProfile
	filters       domain.SearchFilters
	selected      []Provider
	providerNames []string
}

func (p preparedSearch) cacheRequest() domain.SearchRequest {
	return domain.SearchRequest{
		Query:     p.query,
		Limit:     p.limit,
		Offset:    p.offset,
		SortBy:    p.sortBy,
		SortOrder: p.sortOrder,
		Profile:   p.profile,
		Filters:   p.filters,
	}
}

func (s *Service) Search(ctx context.Context, request domain.SearchRequest, providerNames []string) (domain.SearchResponse, error) {
	prepared, err := s.prepareSearch(request, providerNames)
	if err != nil {
		return domain.SearchResponse{}, err
	}

	if s.cacheDisabled || request.NoCache {
		return s.executePreparedSearch(ctx, prepared)
	}

	startedAt := time.Now()
	cacheKey := buildSearchCacheKey(prepared.cacheRequest(), prepared.providerNames)

	if cached, ok, needsRefresh := s.cacheLookup(cacheKey, startedAt); ok {
		// Track popularity even on cache hits, so the warmer can keep hot queries fresh.
		s.markPopular(cacheKey, prepared.cacheRequest(), prepared.providerNames, startedAt)
		if needsRefresh {
			s.refreshCacheAsync(cacheKey, prepared)
		}
		cached.ElapsedMS = time.Since(startedAt).Milliseconds()
		return cached, nil
	}

	response, err := s.executePreparedSearch(ctx, prepared)
	if err != nil {
		return domain.SearchResponse{}, err
	}
	s.cacheStore(cacheKey, response, time.Now())
	s.markPopular(cacheKey, prepared.cacheRequest(), prepared.providerNames, time.Now())
	return response, nil
}

func (s *Service) searchNoCache(ctx context.Context, request domain.SearchRequest, providerNames []string) (domain.SearchResponse, error) {
	prepared, err := s.prepareSearch(request, providerNames)
	if err != nil {
		return domain.SearchResponse{}, err
	}

	response, err := s.executePreparedSearch(ctx, prepared)
	if err != nil {
		return domain.SearchResponse{}, err
	}

	cacheKey := buildSearchCacheKey(prepared.cacheRequest(), prepared.providerNames)
	s.cacheStore(cacheKey, response, time.Now())
	return response, nil
}

func (s *Service) refreshCacheAsync(cacheKey string, prepared preparedSearch) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), s.timeout+2*time.Second)
		defer cancel()
		response, err := s.executePreparedSearch(ctx, prepared)
		if err != nil {
			s.cacheClearRefreshing(cacheKey)
			return
		}
		s.cacheStore(cacheKey, response, time.Now())
	}()
}

func (s *Service) prepareSearch(request domain.SearchRequest, providerNames []string) (preparedSearch, error) {
	normalizedQuery := strings.TrimSpace(request.Query)
	if normalizedQuery == "" {
		return preparedSearch{}, ErrInvalidQuery
	}
	if request.Offset < 0 {
		return preparedSearch{}, ErrInvalidOffset
	}

	limit := request.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := request.Offset
	fetchLimit := limit + offset
	if fetchLimit < 50 {
		fetchLimit = 50
	}
	if fetchLimit > 200 {
		fetchLimit = 200
	}

	sortBy := domain.NormalizeSortBy(string(request.SortBy))
	sortOrder := domain.NormalizeSortOrder(string(request.SortOrder))
	profile := domain.NormalizeRankingProfile(request.Profile)

	selected, err := s.resolveProviders(providerNames)
	if err != nil {
		return preparedSearch{}, err
	}

	providerKeys := make([]string, 0, len(selected))
	for _, provider := range selected {
		name := strings.ToLower(strings.TrimSpace(provider.Name()))
		if name != "" {
			providerKeys = append(providerKeys, name)
		}
	}

	return preparedSearch{
		query:         normalizedQuery,
		queryMeta:     parseTitleMeta(normalizedQuery),
		limit:         limit,
		offset:        offset,
		fetchLimit:    fetchLimit,
		sortBy:        sortBy,
		sortOrder:     sortOrder,
		profile:       profile,
		filters:       request.Filters,
		selected:      selected,
		providerNames: providerKeys,
	}, nil
}

func (s *Service) executePreparedSearch(ctx context.Context, prepared preparedSearch) (domain.SearchResponse, error) {
	runCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && s.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	startedAt := time.Now()
	statuses := make([]domain.ProviderStatus, len(prepared.selected))
	resultsByKey := make(map[string]domain.SearchResult)

	var mu sync.Mutex
	runPass := func(indices []int, queryFor func(Provider) string) {
		// Limit concurrent provider queries to prevent overwhelming the system
		sem := semaphore.NewWeighted(maxConcurrentProviders)
		var wg sync.WaitGroup
		for _, i := range indices {
			provider := prepared.selected[i]
			wg.Add(1)
			go func(index int, current Provider) {
				defer wg.Done()

				// Acquire semaphore before querying provider
				if err := sem.Acquire(runCtx, 1); err != nil {
					// Context cancelled or deadline exceeded
					mu.Lock()
					statuses[index] = domain.ProviderStatus{
						Name:  strings.ToLower(strings.TrimSpace(current.Info().Name)),
						OK:    false,
						Error: "context cancelled",
						Count: 0,
					}
					mu.Unlock()
					return
				}
				defer sem.Release(1)

				providerKey := strings.ToLower(strings.TrimSpace(current.Name()))
				statusName := strings.ToLower(strings.TrimSpace(current.Info().Name))
				if statusName == "" {
					statusName = providerKey
				}

				now := time.Now()
				if blocked, until, lastErr := s.isProviderBlocked(providerKey, now); blocked {
					mu.Lock()
					statuses[index] = domain.ProviderStatus{
						Name:  statusName,
						OK:    false,
						Error: fmt.Sprintf("provider temporarily unhealthy until %s: %s", until.UTC().Format(time.RFC3339), lastErr),
						Count: 0,
					}
					mu.Unlock()
					return
				}

				if err := s.waitProviderRateLimit(runCtx, providerKey); err != nil {
					mu.Lock()
					statuses[index] = domain.ProviderStatus{
						Name:  statusName,
						OK:    false,
						Error: "rate limit wait cancelled",
					}
					mu.Unlock()
					return
				}

				providerQuery := queryFor(current)
				requestLimit := prepared.fetchLimit
				// Torznab providers may require per-item torrent downloads to derive infohash/magnet.
				// Avoid the generic "min 50" fetch expansion to keep latency reasonable.
				if providerKey == "jackett" || providerKey == "prowlarr" {
					requestLimit = prepared.limit + prepared.offset
					if requestLimit <= 0 {
						requestLimit = prepared.limit
					}
					if requestLimit < 10 {
						requestLimit = 10
					}
					if requestLimit > 80 {
						requestLimit = 80
					}
				}
				providerStartedAt := time.Now()
				var items []domain.SearchResult
				searchErr := RetryWithBackoff(runCtx, DefaultRetryConfig(), func() error {
					var err error
					items, err = current.Search(runCtx, domain.SearchRequest{
						Query:     providerQuery,
						Limit:     requestLimit,
						SortBy:    prepared.sortBy,
						SortOrder: prepared.sortOrder,
						Profile:   prepared.profile,
					})
					return err
				})
				s.recordProviderResult(providerKey, providerQuery, searchErr, time.Since(providerStartedAt), time.Now())

				status := domain.ProviderStatus{
					Name: statusName,
					OK:   searchErr == nil,
				}
				if searchErr != nil {
					status.Error = searchErr.Error()
				}
				status.Count = len(items)

				mu.Lock()
				statuses[index] = status
				for _, item := range items {
					item = enrichSearchResult(item)
					key := dedupeKey(item)
					existing, exists := resultsByKey[key]
					if !exists || shouldReplace(existing, item, prepared.queryMeta, prepared.profile) {
						resultsByKey[key] = item
					}
				}
				mu.Unlock()
			}(i, provider)
		}
		wg.Wait()
	}

	all := make([]int, 0, len(prepared.selected))
	for i := range prepared.selected {
		all = append(all, i)
	}

	// First pass: search with the raw query.
	runPass(all, func(_ Provider) string { return prepared.query })

	// Fallback: if we got too few items, retry providers that returned 0 with an expanded query.
	if len(resultsByKey) < 5 {
		retry := make([]int, 0, len(statuses))
		for i, status := range statuses {
			if !status.OK || status.Count != 0 {
				continue
			}
			expanded := expandedQueryForProvider(prepared.query, prepared.selected[i].Name(), prepared.profile)
			if strings.TrimSpace(expanded) == "" || strings.EqualFold(expanded, prepared.query) {
				continue
			}
			retry = append(retry, i)
		}
		if len(retry) > 0 {
			runPass(retry, func(p Provider) string { return expandedQueryForProvider(prepared.query, p.Name(), prepared.profile) })
		}
	}

	items := make([]domain.SearchResult, 0, len(resultsByKey))
	for _, item := range resultsByKey {
		items = append(items, item)
	}

	sortResults(items, prepared.sortBy, prepared.sortOrder, prepared.queryMeta, prepared.profile)

	// Enrich with TMDB metadata (poster, rating, overview)
	items = s.enrichWithTMDB(runCtx, prepared.query, items)

	// Apply server-side filters
	items = applyFilters(items, prepared.filters)

	total := len(items)
	start := prepared.offset
	if start > total {
		start = total
	}
	end := start + prepared.limit
	if end > total {
		end = total
	}
	page := make([]domain.SearchResult, 0, end-start)
	page = append(page, items[start:end]...)

	return domain.SearchResponse{
		Query:      prepared.query,
		Items:      page,
		Providers:  statuses,
		ElapsedMS:  time.Since(startedAt).Milliseconds(),
		TotalItems: total,
		Limit:      prepared.limit,
		Offset:     prepared.offset,
		HasMore:    end < total,
		SortBy:     prepared.sortBy,
		SortOrder:  prepared.sortOrder,
	}, nil
}

func (s *Service) resolveProviders(providerNames []string) ([]Provider, error) {
	if len(s.providers) == 0 {
		return nil, ErrNoProviders
	}

	if len(providerNames) == 0 {
		all := make([]Provider, 0, len(s.providers))
		seen := make(map[string]struct{}, len(s.providers))
		for _, provider := range s.providers {
			key := strings.ToLower(strings.TrimSpace(provider.Name()))
			if key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			all = append(all, provider)
		}
		sort.Slice(all, func(i, j int) bool {
			return strings.ToLower(all[i].Name()) < strings.ToLower(all[j].Name())
		})
		return all, nil
	}

	selected := make([]Provider, 0, len(providerNames))
	seen := make(map[string]struct{}, len(providerNames))
	for _, rawName := range providerNames {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if name == "" {
			continue
		}
		provider, ok := s.providers[name]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, name)
		}
		key := strings.ToLower(strings.TrimSpace(provider.Name()))
		if key == "" {
			key = name
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		selected = append(selected, provider)
	}

	if len(selected) == 0 {
		return nil, ErrNoProviders
	}
	return selected, nil
}

func dedupeKey(item domain.SearchResult) string {
	if hash := normalizeInfoHash(item.InfoHash); hash != "" {
		return "hash:" + hash
	}
	if hash := extractInfoHashFromMagnet(item.Magnet); hash != "" {
		return "hash:" + hash
	}
	if titleKey := buildTitleDedupeKey(item); titleKey != "" {
		return titleKey
	}
	magnet := strings.ToLower(strings.TrimSpace(item.Magnet))
	if magnet != "" {
		return "magnet:" + magnet
	}
	return strings.ToLower(strings.TrimSpace(item.Name)) + ":" + strconvI64(item.SizeBytes)
}

func shouldReplace(existing, candidate domain.SearchResult, queryMeta titleMeta, profile domain.SearchRankingProfile) bool {
	candidateScore := relevanceScoreForResult(queryMeta, profile, candidate)
	existingScore := relevanceScoreForResult(queryMeta, profile, existing)
	if cmp := compareFloat64(candidateScore, existingScore); cmp != 0 {
		return cmp > 0
	}
	if candidate.Seeders != existing.Seeders {
		return candidate.Seeders > existing.Seeders
	}
	if candidate.Leechers != existing.Leechers {
		return candidate.Leechers > existing.Leechers
	}
	if candidate.PublishedAt != nil && existing.PublishedAt != nil && !candidate.PublishedAt.Equal(*existing.PublishedAt) {
		return candidate.PublishedAt.After(*existing.PublishedAt)
	}
	if strings.TrimSpace(existing.InfoHash) == "" && strings.TrimSpace(candidate.InfoHash) != "" {
		return true
	}
	if strings.TrimSpace(existing.Magnet) == "" && strings.TrimSpace(candidate.Magnet) != "" {
		return true
	}
	if existing.Enrichment.Poster == "" && candidate.Enrichment.Poster != "" {
		return true
	}
	if len(existing.Enrichment.Screenshots) == 0 && len(candidate.Enrichment.Screenshots) > 0 {
		return true
	}
	return false
}

func strconvI64(value int64) string {
	return strconv.FormatInt(value, 10)
}

func sortResults(items []domain.SearchResult, sortBy domain.SearchSortBy, sortOrder domain.SearchSortOrder, queryMeta titleMeta, profile domain.SearchRankingProfile) {
	relevanceCache := make(map[string]float64, len(items))
	relevance := func(item domain.SearchResult) float64 {
		cacheKey := dedupeKey(item) + "|" + strings.ToLower(strings.TrimSpace(item.Name))
		if score, ok := relevanceCache[cacheKey]; ok {
			return score
		}
		score := relevanceScoreForResult(queryMeta, profile, item)
		relevanceCache[cacheKey] = score
		return score
	}

	sort.Slice(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		cmp := compareResults(left, right, sortBy, relevance)
		if sortOrder == domain.SearchSortOrderAsc {
			return cmp < 0
		}
		return cmp > 0
	})
}

func compareResults(
	left,
	right domain.SearchResult,
	sortBy domain.SearchSortBy,
	relevance func(domain.SearchResult) float64,
) int {
	switch sortBy {
	case domain.SearchSortBySeeders:
		if cmp := compareInt(left.Seeders, right.Seeders); cmp != 0 {
			return cmp
		}
	case domain.SearchSortBySizeBytes:
		if cmp := compareInt64(left.SizeBytes, right.SizeBytes); cmp != 0 {
			return cmp
		}
	case domain.SearchSortByPublished:
		if cmp := compareTime(left.PublishedAt, right.PublishedAt); cmp != 0 {
			return cmp
		}
	default:
		if cmp := compareFloat64(relevance(left), relevance(right)); cmp != 0 {
			return cmp
		}
		if cmp := compareInt(left.Seeders, right.Seeders); cmp != 0 {
			return cmp
		}
		if cmp := compareInt(left.Leechers, right.Leechers); cmp != 0 {
			return cmp
		}
	}
	if cmp := strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name)); cmp != 0 {
		return -cmp
	}
	return compareInt64(left.SizeBytes, right.SizeBytes)
}

func compareInt(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func compareInt64(left, right int64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func compareTime(left, right *time.Time) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return -1
	}
	if right == nil {
		return 1
	}
	switch {
	case left.Before(*right):
		return -1
	case left.After(*right):
		return 1
	default:
		return 0
	}
}

func applyFilters(items []domain.SearchResult, filters domain.SearchFilters) []domain.SearchResult {
	if !hasActiveFilters(filters) {
		return items
	}

	qualitySet := make(map[string]struct{}, len(filters.Quality))
	for _, q := range filters.Quality {
		qualitySet[strings.ToLower(strings.TrimSpace(q))] = struct{}{}
	}

	dubbingGroupSet := make(map[string]struct{}, len(filters.DubbingGroups))
	for _, g := range filters.DubbingGroups {
		dubbingGroupSet[strings.ToLower(strings.TrimSpace(g))] = struct{}{}
	}

	dubbingTypeSet := make(map[string]struct{}, len(filters.DubbingTypes))
	for _, t := range filters.DubbingTypes {
		dubbingTypeSet[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
	}

	contentType := strings.ToLower(strings.TrimSpace(filters.ContentType))

	filtered := make([]domain.SearchResult, 0, len(items))
	for _, item := range items {
		if len(qualitySet) > 0 {
			q := strings.ToLower(strings.TrimSpace(item.Enrichment.Quality))
			if _, ok := qualitySet[q]; !ok && q != "" {
				continue
			}
			if q == "" {
				continue
			}
		}

		if contentType != "" {
			ct := strings.ToLower(strings.TrimSpace(item.Enrichment.ContentType))
			if contentType == "movie" && item.Enrichment.IsSeries {
				continue
			}
			if contentType == "series" && !item.Enrichment.IsSeries && ct != "series" && ct != "anime" {
				continue
			}
			if contentType == "anime" && ct != "anime" {
				continue
			}
		}

		if filters.YearFrom > 0 && item.Enrichment.Year > 0 && item.Enrichment.Year < filters.YearFrom {
			continue
		}
		if filters.YearTo > 0 && item.Enrichment.Year > 0 && item.Enrichment.Year > filters.YearTo {
			continue
		}

		if len(dubbingGroupSet) > 0 {
			if !matchesDubbingGroups(item.Enrichment.Dubbing, dubbingGroupSet) {
				continue
			}
		}

		if len(dubbingTypeSet) > 0 {
			dt := strings.ToLower(string(item.Enrichment.Dubbing.Type))
			if _, ok := dubbingTypeSet[dt]; !ok {
				continue
			}
		}

		if filters.MinSeeders > 0 && item.Seeders < filters.MinSeeders {
			continue
		}

		filtered = append(filtered, item)
	}
	return filtered
}

func hasActiveFilters(f domain.SearchFilters) bool {
	return len(f.Quality) > 0 ||
		f.ContentType != "" ||
		f.YearFrom > 0 ||
		f.YearTo > 0 ||
		len(f.DubbingGroups) > 0 ||
		len(f.DubbingTypes) > 0 ||
		f.MinSeeders > 0
}

func matchesDubbingGroups(info domain.DubbingInfo, wanted map[string]struct{}) bool {
	if info.Group != "" {
		if _, ok := wanted[strings.ToLower(info.Group)]; ok {
			return true
		}
	}
	for _, g := range info.Groups {
		if _, ok := wanted[strings.ToLower(g)]; ok {
			return true
		}
	}
	return false
}

func appendUniqueSource(result *domain.SearchResult, ref domain.SourceRef) {
	if ref.Name == "" {
		return
	}
	for _, existing := range result.Sources {
		if strings.EqualFold(existing.Name, ref.Name) && strings.EqualFold(existing.Tracker, ref.Tracker) {
			return
		}
	}
	result.Sources = append(result.Sources, ref)
}

func sourceRefFromResult(item domain.SearchResult) domain.SourceRef {
	return domain.SourceRef{
		Name:    item.Source,
		Tracker: item.Tracker,
	}
}

func (s *Service) SearchStream(ctx context.Context, request domain.SearchRequest, providerNames []string) <-chan domain.SearchResponse {
	ch := make(chan domain.SearchResponse, 8)

	prepared, err := s.prepareSearch(request, providerNames)
	if err != nil {
		close(ch)
		return ch
	}

	// Check cache first (non-streaming path)
	if !s.cacheDisabled && !request.NoCache {
		startedAt := time.Now()
		cacheKey := buildSearchCacheKey(prepared.cacheRequest(), prepared.providerNames)
		if cached, ok, needsRefresh := s.cacheLookup(cacheKey, startedAt); ok {
			s.markPopular(cacheKey, prepared.cacheRequest(), prepared.providerNames, startedAt)
			if needsRefresh {
				s.refreshCacheAsync(cacheKey, prepared)
			}
			cached.ElapsedMS = time.Since(startedAt).Milliseconds()
			cached.Final = true
			ch <- cached
			close(ch)
			return ch
		}
	}

	go s.executeStreamSearch(ctx, prepared, ch)
	return ch
}

func (s *Service) executeStreamSearch(ctx context.Context, prepared preparedSearch, ch chan<- domain.SearchResponse) {
	defer close(ch)

	runCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && s.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	startedAt := time.Now()
	statuses := make([]domain.ProviderStatus, len(prepared.selected))
	resultsByKey := make(map[string]domain.SearchResult)

	providerNames := make([]string, len(prepared.selected))
	for i, p := range prepared.selected {
		providerNames[i] = strings.ToLower(strings.TrimSpace(p.Name()))
	}
	slog.Info("stream search started",
		slog.String("query", prepared.query),
		slog.Any("providers", providerNames),
		slog.Int("limit", prepared.limit),
	)

	var mu sync.Mutex
	sem := semaphore.NewWeighted(maxConcurrentProviders)
	var wg sync.WaitGroup

	for i, provider := range prepared.selected {
		wg.Add(1)
		go func(index int, current Provider) {
			defer wg.Done()

			if err := sem.Acquire(runCtx, 1); err != nil {
				mu.Lock()
				statuses[index] = domain.ProviderStatus{
					Name:  strings.ToLower(strings.TrimSpace(current.Info().Name)),
					OK:    false,
					Error: "context cancelled",
				}
				mu.Unlock()
				return
			}
			defer sem.Release(1)

			providerKey := strings.ToLower(strings.TrimSpace(current.Name()))
			statusName := strings.ToLower(strings.TrimSpace(current.Info().Name))
			if statusName == "" {
				statusName = providerKey
			}

			now := time.Now()
			if blocked, until, lastErr := s.isProviderBlocked(providerKey, now); blocked {
				slog.Warn("stream search: provider blocked",
					slog.String("provider", providerKey),
					slog.String("until", until.UTC().Format(time.RFC3339)),
					slog.String("lastError", lastErr),
				)
				mu.Lock()
				statuses[index] = domain.ProviderStatus{
					Name:  statusName,
					OK:    false,
					Error: fmt.Sprintf("provider temporarily unhealthy until %s: %s", until.UTC().Format(time.RFC3339), lastErr),
				}
				mu.Unlock()
				return
			}

			if err := s.waitProviderRateLimit(runCtx, providerKey); err != nil {
				mu.Lock()
				statuses[index] = domain.ProviderStatus{
					Name:  statusName,
					OK:    false,
					Error: "rate limit wait cancelled",
				}
				mu.Unlock()
				return
			}

			requestLimit := prepared.fetchLimit
			if providerKey == "jackett" || providerKey == "prowlarr" {
				requestLimit = prepared.limit + prepared.offset
				if requestLimit <= 0 {
					requestLimit = prepared.limit
				}
				if requestLimit < 10 {
					requestLimit = 10
				}
				if requestLimit > 80 {
					requestLimit = 80
				}
			}

			providerStartedAt := time.Now()
			var items []domain.SearchResult
			searchErr := RetryWithBackoff(runCtx, DefaultRetryConfig(), func() error {
				var err error
				items, err = current.Search(runCtx, domain.SearchRequest{
					Query:     prepared.query,
					Limit:     requestLimit,
					SortBy:    prepared.sortBy,
					SortOrder: prepared.sortOrder,
					Profile:   prepared.profile,
				})
				return err
			})
			elapsed := time.Since(providerStartedAt)
			s.recordProviderResult(providerKey, prepared.query, searchErr, elapsed, time.Now())

			if searchErr != nil {
				slog.Warn("stream search: provider failed",
					slog.String("provider", providerKey),
					slog.String("query", prepared.query),
					slog.Int64("elapsedMs", elapsed.Milliseconds()),
					slog.String("error", searchErr.Error()),
				)
			} else {
				slog.Info("stream search: provider completed",
					slog.String("provider", providerKey),
					slog.String("query", prepared.query),
					slog.Int("results", len(items)),
					slog.Int64("elapsedMs", elapsed.Milliseconds()),
				)
			}

			status := domain.ProviderStatus{
				Name: statusName,
				OK:   searchErr == nil,
			}
			if searchErr != nil {
				status.Error = searchErr.Error()
			}
			status.Count = len(items)

			mu.Lock()
			statuses[index] = status

			for _, item := range items {
				item = enrichSearchResult(item)
				ref := sourceRefFromResult(item)
				key := dedupeKey(item)
				existing, exists := resultsByKey[key]
				if !exists {
					appendUniqueSource(&item, ref)
					resultsByKey[key] = item
				} else if shouldReplace(existing, item, prepared.queryMeta, prepared.profile) {
					// Keep accumulated sources from the existing entry
					item.Sources = existing.Sources
					appendUniqueSource(&item, ref)
					resultsByKey[key] = item
				} else {
					// Just add the new source to the existing result
					appendUniqueSource(&existing, ref)
					resultsByKey[key] = existing
				}
			}

			// Build snapshot
			snapshot := s.buildStreamSnapshot(prepared, resultsByKey, statuses, startedAt)
			snapshot.Provider = statusName
			mu.Unlock()

			// Send snapshot (non-blocking if context cancelled)
			select {
			case ch <- snapshot:
			case <-ctx.Done():
			}
		}(i, provider)
	}

	wg.Wait()

	// Query expansion fallback
	if len(resultsByKey) < 5 {
		retry := make([]int, 0, len(statuses))
		for i, status := range statuses {
			if !status.OK || status.Count != 0 {
				continue
			}
			expanded := expandedQueryForProvider(prepared.query, prepared.selected[i].Name(), prepared.profile)
			if strings.TrimSpace(expanded) == "" || strings.EqualFold(expanded, prepared.query) {
				continue
			}
			retry = append(retry, i)
		}
		if len(retry) > 0 {
			var retryWg sync.WaitGroup
			for _, idx := range retry {
				retryWg.Add(1)
				go func(index int) {
					defer retryWg.Done()
					current := prepared.selected[index]
					providerKey := strings.ToLower(strings.TrimSpace(current.Name()))
					statusName := strings.ToLower(strings.TrimSpace(current.Info().Name))
					if statusName == "" {
						statusName = providerKey
					}
					expanded := expandedQueryForProvider(prepared.query, current.Name(), prepared.profile)

					if err := sem.Acquire(runCtx, 1); err != nil {
						return
					}
					defer sem.Release(1)

					requestLimit := prepared.fetchLimit
					providerStartedAt := time.Now()
					items, searchErr := current.Search(runCtx, domain.SearchRequest{
						Query:     expanded,
						Limit:     requestLimit,
						SortBy:    prepared.sortBy,
						SortOrder: prepared.sortOrder,
						Profile:   prepared.profile,
					})
					s.recordProviderResult(providerKey, expanded, searchErr, time.Since(providerStartedAt), time.Now())

					if searchErr != nil {
						return
					}

					mu.Lock()
					statuses[index].Count += len(items)
					for _, item := range items {
						item = enrichSearchResult(item)
						ref := sourceRefFromResult(item)
						key := dedupeKey(item)
						existing, exists := resultsByKey[key]
						if !exists {
							appendUniqueSource(&item, ref)
							resultsByKey[key] = item
						} else if shouldReplace(existing, item, prepared.queryMeta, prepared.profile) {
							item.Sources = existing.Sources
							appendUniqueSource(&item, ref)
							resultsByKey[key] = item
						} else {
							appendUniqueSource(&existing, ref)
							resultsByKey[key] = existing
						}
					}
					mu.Unlock()
				}(idx)
			}
			retryWg.Wait()
		}
	}

	// TMDB enrichment (single pass after all providers done)
	allItems := make([]domain.SearchResult, 0, len(resultsByKey))
	for _, item := range resultsByKey {
		allItems = append(allItems, item)
	}
	allItems = s.enrichWithTMDB(runCtx, prepared.query, allItems)

	// Rebuild map after TMDB enrichment
	for _, item := range allItems {
		key := dedupeKey(item)
		resultsByKey[key] = item
	}

	// Ensure Source/Tracker backward compat: populate from Sources[0] if empty
	for key, item := range resultsByKey {
		if item.Source == "" && len(item.Sources) > 0 {
			item.Source = item.Sources[0].Name
			item.Tracker = item.Sources[0].Tracker
			resultsByKey[key] = item
		}
	}

	// Build final response
	final := s.buildStreamSnapshot(prepared, resultsByKey, statuses, startedAt)
	final.Final = true

	// Cache the final response
	cacheKey := buildSearchCacheKey(prepared.cacheRequest(), prepared.providerNames)
	s.cacheStore(cacheKey, final, time.Now())
	s.markPopular(cacheKey, prepared.cacheRequest(), prepared.providerNames, time.Now())

	failed := 0
	for _, st := range statuses {
		if !st.OK {
			failed++
		}
	}
	slog.Info("stream search completed",
		slog.String("query", prepared.query),
		slog.Int("totalResults", final.TotalItems),
		slog.Int("providers", len(statuses)),
		slog.Int("failed", failed),
		slog.Int64("elapsedMs", time.Since(startedAt).Milliseconds()),
	)

	select {
	case ch <- final:
	case <-ctx.Done():
	}
}

func (s *Service) buildStreamSnapshot(
	prepared preparedSearch,
	resultsByKey map[string]domain.SearchResult,
	statuses []domain.ProviderStatus,
	startedAt time.Time,
) domain.SearchResponse {
	items := make([]domain.SearchResult, 0, len(resultsByKey))
	for _, item := range resultsByKey {
		items = append(items, item)
	}

	sortResults(items, prepared.sortBy, prepared.sortOrder, prepared.queryMeta, prepared.profile)
	items = applyFilters(items, prepared.filters)

	total := len(items)
	start := prepared.offset
	if start > total {
		start = total
	}
	end := start + prepared.limit
	if end > total {
		end = total
	}
	page := make([]domain.SearchResult, 0, end-start)
	page = append(page, items[start:end]...)

	statusesCopy := make([]domain.ProviderStatus, len(statuses))
	copy(statusesCopy, statuses)

	return domain.SearchResponse{
		Query:      prepared.query,
		Items:      page,
		Providers:  statusesCopy,
		ElapsedMS:  time.Since(startedAt).Milliseconds(),
		TotalItems: total,
		Limit:      prepared.limit,
		Offset:     prepared.offset,
		HasMore:    end < total,
		SortBy:     prepared.sortBy,
		SortOrder:  prepared.sortOrder,
	}
}

func (s *Service) enrichWithTMDB(ctx context.Context, query string, items []domain.SearchResult) []domain.SearchResult {
	if s.tmdb == nil || !s.tmdb.Enabled() || len(items) == 0 {
		return items
	}

	results, err := s.tmdb.SearchMulti(ctx, query, "ru-RU")
	if err != nil || len(results) == 0 {
		return items
	}

	// Use the best TMDB match
	best := results[0]
	posterURL := ""
	if best.PosterPath != "" {
		posterURL = "https://image.tmdb.org/t/p/w300" + best.PosterPath
	}

	overview := best.Overview
	if len(overview) > 500 {
		overview = overview[:500]
	}

	for i := range items {
		if items[i].Enrichment.TMDBId == 0 {
			items[i].Enrichment.TMDBId = best.ID
		}
		if items[i].Enrichment.TMDBPoster == "" && posterURL != "" {
			items[i].Enrichment.TMDBPoster = posterURL
		}
		if items[i].Enrichment.TMDBRating == 0 && best.VoteAverage > 0 {
			items[i].Enrichment.TMDBRating = best.VoteAverage
		}
		if items[i].Enrichment.TMDBOverview == "" && overview != "" {
			items[i].Enrichment.TMDBOverview = overview
		}
	}
	return items
}
