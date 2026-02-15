package domain

import "time"

type SearchSortBy string

const (
	SearchSortByRelevance SearchSortBy = "relevance"
	SearchSortBySeeders   SearchSortBy = "seeders"
	SearchSortBySizeBytes SearchSortBy = "sizeBytes"
	SearchSortByPublished SearchSortBy = "publishedAt"
)

type SearchSortOrder string

const (
	SearchSortOrderAsc  SearchSortOrder = "asc"
	SearchSortOrderDesc SearchSortOrder = "desc"
)

type SearchRequest struct {
	Query     string
	Limit     int
	Offset    int
	SortBy    SearchSortBy
	SortOrder SearchSortOrder
	Profile   SearchRankingProfile
	Filters   SearchFilters
	NoCache   bool
}

type DubbingType string

const (
	DubbingDub        DubbingType = "дубляж"
	DubbingVoiceover  DubbingType = "озвучка"
	DubbingMultiVoice DubbingType = "многоголос"
	DubbingAuthor     DubbingType = "авторский"
	DubbingBackVoice  DubbingType = "закадровый"
	DubbingSubtitles  DubbingType = "субтитры"
	DubbingOriginal   DubbingType = "оригинал"
	DubbingUnknown    DubbingType = ""
)

type DubbingInfo struct {
	Type   DubbingType `json:"type,omitempty"`
	Group  string      `json:"group,omitempty"`
	Groups []string    `json:"groups,omitempty"`
}

type SearchFilters struct {
	Quality       []string `json:"quality,omitempty"`
	ContentType   string   `json:"contentType,omitempty"`
	YearFrom      int      `json:"yearFrom,omitempty"`
	YearTo        int      `json:"yearTo,omitempty"`
	DubbingGroups []string `json:"dubbingGroups,omitempty"`
	DubbingTypes  []string `json:"dubbingTypes,omitempty"`
	MinSeeders    int      `json:"minSeeders,omitempty"`
}

type SearchRankingProfile struct {
	FreshnessWeight    float64  `json:"freshnessWeight"`
	SeedersWeight      float64  `json:"seedersWeight"`
	QualityWeight      float64  `json:"qualityWeight"`
	LanguageWeight     float64  `json:"languageWeight"`
	SizeWeight         float64  `json:"sizeWeight"`
	PreferSeries       bool     `json:"preferSeries"`
	PreferMovies       bool     `json:"preferMovies"`
	PreferredAudio     []string `json:"preferredAudio,omitempty"`
	PreferredSubtitles []string `json:"preferredSubtitles,omitempty"`
	TargetSizeBytes    int64    `json:"targetSizeBytes,omitempty"`
}

type SearchEnrichment struct {
	Description   string      `json:"description,omitempty"`
	NFO           string      `json:"nfo,omitempty"`
	Poster        string      `json:"poster,omitempty"`
	Screenshots   []string    `json:"screenshots,omitempty"`
	Quality       string      `json:"quality,omitempty"`
	Audio         []string    `json:"audio,omitempty"`
	Subtitles     []string    `json:"subtitles,omitempty"`
	IsSeries      bool        `json:"isSeries,omitempty"`
	Season        int         `json:"season,omitempty"`
	Episode       int         `json:"episode,omitempty"`
	Year          int         `json:"year,omitempty"`
	Dubbing       DubbingInfo `json:"dubbing,omitempty"`
	SourceType    string      `json:"sourceType,omitempty"`
	HDR           bool        `json:"hdr,omitempty"`
	DolbyVision   bool        `json:"dolbyVision,omitempty"`
	AudioChannels string      `json:"audioChannels,omitempty"`
	TMDBId        int         `json:"tmdbId,omitempty"`
	TMDBPoster    string      `json:"tmdbPoster,omitempty"`
	TMDBRating    float64     `json:"tmdbRating,omitempty"`
	TMDBOverview  string      `json:"tmdbOverview,omitempty"`
	ContentType   string      `json:"contentType,omitempty"`
}

type SearchResult struct {
	Name        string           `json:"name"`
	InfoHash    string           `json:"infoHash,omitempty"`
	Magnet      string           `json:"magnet,omitempty"`
	PageURL     string           `json:"pageUrl,omitempty"`
	SizeBytes   int64            `json:"sizeBytes,omitempty"`
	Seeders     int              `json:"seeders,omitempty"`
	Leechers    int              `json:"leechers,omitempty"`
	Source      string           `json:"source,omitempty"`
	Tracker     string           `json:"tracker,omitempty"`
	PublishedAt *time.Time       `json:"publishedAt,omitempty"`
	Enrichment  SearchEnrichment `json:"enrichment,omitempty"`
}

type ProviderInfo struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`
}

type ProviderRuntimeConfig struct {
	Name          string `json:"name"`
	Label         string `json:"label"`
	Endpoint      string `json:"endpoint,omitempty"`
	ProxyURL      string `json:"proxyUrl,omitempty"`
	HasAPIKey     bool   `json:"hasApiKey"`
	APIKeyPreview string `json:"apiKeyPreview,omitempty"`
	Configured    bool   `json:"configured"`
}

type ProviderRuntimePatch struct {
	Name     string
	Endpoint *string
	APIKey   *string
	ProxyURL *string
}

type FlareSolverrProviderStatus struct {
	Provider   string `json:"provider"`
	Configured bool   `json:"configured"`
	URL        string `json:"url,omitempty"`
	Message    string `json:"message,omitempty"`
}

type FlareSolverrSettings struct {
	DefaultURL string                       `json:"defaultUrl"`
	URL        string                       `json:"url,omitempty"`
	Providers  []FlareSolverrProviderStatus `json:"providers"`
}

type FlareSolverrApplyResult struct {
	Provider string `json:"provider"`
	OK       bool   `json:"ok"`
	Status   string `json:"status"`
	Message  string `json:"message"`
}

type FlareSolverrApplyResponse struct {
	URL     string                    `json:"url"`
	Results []FlareSolverrApplyResult `json:"results"`
}

type ProviderStatus struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Count int    `json:"count"`
	Error string `json:"error,omitempty"`
}

type SubIndexerInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ProviderDiagnostics struct {
	Name                string           `json:"name"`
	Label               string           `json:"label"`
	Kind                string           `json:"kind"`
	Enabled             bool             `json:"enabled"`
	ConsecutiveFailures int              `json:"consecutiveFailures"`
	BlockedUntil        *time.Time       `json:"blockedUntil,omitempty"`
	LastError           string           `json:"lastError,omitempty"`
	LastSuccessAt       *time.Time       `json:"lastSuccessAt,omitempty"`
	LastFailureAt       *time.Time       `json:"lastFailureAt,omitempty"`
	LastLatencyMS       int64            `json:"lastLatencyMs,omitempty"`
	LastTimeout         bool             `json:"lastTimeout,omitempty"`
	LastQuery           string           `json:"lastQuery,omitempty"`
	TotalRequests       int64            `json:"totalRequests,omitempty"`
	TotalFailures       int64            `json:"totalFailures,omitempty"`
	TimeoutCount        int64            `json:"timeoutCount,omitempty"`
	FanOut              bool             `json:"fanOut,omitempty"`
	SubIndexers         []SubIndexerInfo `json:"subIndexers,omitempty"`
}

type SearchResponse struct {
	Query      string           `json:"query"`
	Items      []SearchResult   `json:"items"`
	Providers  []ProviderStatus `json:"providers"`
	ElapsedMS  int64            `json:"elapsedMs"`
	TotalItems int              `json:"totalItems"`
	Limit      int              `json:"limit"`
	Offset     int              `json:"offset"`
	HasMore    bool             `json:"hasMore"`
	SortBy     SearchSortBy     `json:"sortBy"`
	SortOrder  SearchSortOrder  `json:"sortOrder"`
	Phase      string           `json:"phase,omitempty"`
	Final      bool             `json:"final"`
}

func NormalizeSortBy(raw string) SearchSortBy {
	switch SearchSortBy(raw) {
	case SearchSortBySeeders:
		return SearchSortBySeeders
	case SearchSortBySizeBytes:
		return SearchSortBySizeBytes
	case SearchSortByPublished:
		return SearchSortByPublished
	default:
		return SearchSortByRelevance
	}
}

func NormalizeSortOrder(raw string) SearchSortOrder {
	switch SearchSortOrder(raw) {
	case SearchSortOrderAsc:
		return SearchSortOrderAsc
	default:
		return SearchSortOrderDesc
	}
}

func DefaultSearchRankingProfile() SearchRankingProfile {
	return SearchRankingProfile{
		FreshnessWeight: 1,
		SeedersWeight:   1,
		QualityWeight:   1,
		LanguageWeight:  5,
		SizeWeight:      0.4,
		PreferSeries:    true,
		PreferMovies:    true,
		PreferredAudio: []string{
			"ru",
		},
		PreferredSubtitles: []string{
			"ru",
		},
	}
}

func NormalizeRankingProfile(profile SearchRankingProfile) SearchRankingProfile {
	clamp := func(value float64) float64 {
		if value < 0 {
			return 0
		}
		if value > 5 {
			return 5
		}
		return value
	}

	profile.FreshnessWeight = clamp(profile.FreshnessWeight)
	profile.SeedersWeight = clamp(profile.SeedersWeight)
	profile.QualityWeight = clamp(profile.QualityWeight)
	profile.LanguageWeight = clamp(profile.LanguageWeight)
	profile.SizeWeight = clamp(profile.SizeWeight)
	if profile.TargetSizeBytes < 0 {
		profile.TargetSizeBytes = 0
	}
	return profile
}
