package app

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr          string
	RequestTimeout    time.Duration
	LogLevel          string
	LogFormat         string
	UserAgent         string
	PirateBayEndpoint string
	X1337Endpoint     string
	RutrackerEndpoint string
	RutrackerCookies  string
	RutrackerProxyURL string
	FlareSolverrURL   string
	RedisURL          string
	TMDBAPIKey        string
	TMDBBaseURL       string
	CacheTTL          time.Duration
	CacheDisabled     bool
	TMDBCacheTTL      time.Duration
}

func LoadConfig() Config {
	return Config{
		HTTPAddr:          getEnv("HTTP_ADDR", ":8090"),
		RequestTimeout:    time.Duration(getEnvInt("SEARCH_TIMEOUT_SECONDS", 15)) * time.Second,
		LogLevel:          strings.ToLower(getEnv("LOG_LEVEL", "info")),
		LogFormat:         strings.ToLower(getEnv("LOG_FORMAT", "text")),
		UserAgent:         getEnv("SEARCH_USER_AGENT", "torrent-stream-search/1.0"),
		PirateBayEndpoint: getEnv("SEARCH_PROVIDER_PIRATEBAY_ENDPOINT", getEnv("SEARCH_PROVIDER_BITTORRENT_ENDPOINT", "https://apibay.org/q.php")),
		X1337Endpoint:     getEnv("SEARCH_PROVIDER_1337X_ENDPOINT", "https://x1337x.ws,https://1337x.to,https://1377x.to"),
		RutrackerEndpoint: getEnv("SEARCH_PROVIDER_RUTRACKER_ENDPOINT", "https://rutracker.org/forum/tracker.php"),
		RutrackerCookies:  buildRutrackerCookies(),
		RutrackerProxyURL: getEnv("SEARCH_PROVIDER_RUTRACKER_PROXY", ""),
		FlareSolverrURL:   normalizeFlareSolverrURL(getEnv("FLARESOLVERR_URL", "http://flaresolverr:8191/")),
		RedisURL:          getEnv("REDIS_URL", ""),
		TMDBAPIKey:        strings.TrimSpace(os.Getenv("TMDB_API_KEY")),
		TMDBBaseURL:       getEnv("TMDB_BASE_URL", "https://api.themoviedb.org/3"),
		CacheTTL:          time.Duration(getEnvInt("SEARCH_CACHE_TTL_HOURS", 6)) * time.Hour,
		CacheDisabled:     getEnvBool("SEARCH_CACHE_DISABLED", false),
		TMDBCacheTTL:      time.Duration(getEnvInt("TMDB_CACHE_TTL_DAYS", 7)) * 24 * time.Hour,
	}
}

func getEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getEnvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func getEnvBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func buildRutrackerCookies() string {
	raw := strings.TrimSpace(os.Getenv("SEARCH_PROVIDER_RUTRACKER_COOKIE"))
	if raw != "" {
		return raw
	}
	parts := make([]string, 0, 4)
	for _, item := range []struct {
		Env  string
		Name string
	}{
		{Env: "SEARCH_PROVIDER_RUTRACKER_BB_SESSION", Name: "bb_session"},
		{Env: "SEARCH_PROVIDER_RUTRACKER_BB_GUID", Name: "bb_guid"},
		{Env: "SEARCH_PROVIDER_RUTRACKER_BB_SSL", Name: "bb_ssl"},
		{Env: "SEARCH_PROVIDER_RUTRACKER_CF_CLEARANCE", Name: "cf_clearance"},
	} {
		value := strings.TrimSpace(os.Getenv(item.Env))
		if value == "" {
			continue
		}
		parts = append(parts, item.Name+"="+value)
	}
	return strings.Join(parts, "; ")
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
