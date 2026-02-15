package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	apihttp "torrentstream/searchservice/internal/api/http"
	"torrentstream/searchservice/internal/app"
	"torrentstream/searchservice/internal/metrics"
	"torrentstream/searchservice/internal/providers/bittorrentindex"
	"torrentstream/searchservice/internal/providers/rutracker"
	"torrentstream/searchservice/internal/providers/tmdb"
	"torrentstream/searchservice/internal/providers/torznab"
	"torrentstream/searchservice/internal/providers/x1337"
	"torrentstream/searchservice/internal/search"
	"torrentstream/searchservice/internal/telemetry"
)

func main() {
	cfg := app.LoadConfig()
	logger := newLogger(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)
	metrics.Register(prometheus.DefaultRegisterer)

	shutdownTracer, err := telemetry.Init(context.Background(), "torrent-search")
	if err != nil {
		logger.Warn("otel init failed", slog.String("error", err.Error()))
	}
	defer func() {
		if shutdownTracer != nil {
			_ = shutdownTracer(context.Background())
		}
	}()

	logger.Info("configuration loaded",
		slog.String("service", "torrent-search"),
		slog.String("httpAddr", cfg.HTTPAddr),
		slog.String("logLevel", cfg.LogLevel),
		slog.String("logFormat", cfg.LogFormat),
		slog.Duration("requestTimeout", cfg.RequestTimeout),
		slog.String("piratebayEndpoint", cfg.PirateBayEndpoint),
		slog.String("x1337Endpoint", cfg.X1337Endpoint),
		slog.String("rutrackerEndpoint", cfg.RutrackerEndpoint),
		slog.Bool("hasRutrackerProxy", strings.TrimSpace(cfg.RutrackerProxyURL) != ""),
		slog.Bool("hasRutrackerCookies", strings.TrimSpace(cfg.RutrackerCookies) != ""),
		slog.String("flareSolverrURL", strings.TrimSpace(cfg.FlareSolverrURL)),
		slog.Bool("hasRedis", strings.TrimSpace(cfg.RedisURL) != ""),
		slog.Bool("hasTMDBKey", strings.TrimSpace(cfg.TMDBAPIKey) != ""),
		slog.Duration("cacheTTL", cfg.CacheTTL),
	)

	pirateBayClient := &http.Client{Timeout: cfg.RequestTimeout, Transport: otelhttp.NewTransport(http.DefaultTransport)}
	x1337Client := &http.Client{Timeout: cfg.RequestTimeout, Transport: otelhttp.NewTransport(http.DefaultTransport)}
	jackettClient := &http.Client{Timeout: cfg.RequestTimeout, Transport: otelhttp.NewTransport(http.DefaultTransport)}
	prowlarrClient := &http.Client{Timeout: cfg.RequestTimeout, Transport: otelhttp.NewTransport(http.DefaultTransport)}
	rutrackerClient := newRutrackerHTTPClient(cfg.RequestTimeout, cfg.RutrackerProxyURL)

	jackettProvider := torznab.NewProvider(torznab.Config{
		Name:      "jackett",
		Label:     "Jackett (Torznab)",
		Kind:      "indexer",
		UserAgent: cfg.UserAgent,
		Client:    jackettClient,
	})
	prowlarrProvider := torznab.NewProvider(torznab.Config{
		Name:      "prowlarr",
		Label:     "Prowlarr (Torznab)",
		Kind:      "indexer",
		UserAgent: cfg.UserAgent,
		Client:    prowlarrClient,
	})

	searchService := search.NewService([]search.Provider{
		bittorrentindex.NewProvider(bittorrentindex.Config{
			Endpoint:  cfg.PirateBayEndpoint,
			UserAgent: cfg.UserAgent,
			Client:    pirateBayClient,
		}),
		jackettProvider,
		prowlarrProvider,
		x1337.NewProvider(x1337.Config{
			Endpoint:  cfg.X1337Endpoint,
			UserAgent: cfg.UserAgent,
			Client:    x1337Client,
		}),
		rutracker.NewProvider(rutracker.Config{
			Endpoint:  cfg.RutrackerEndpoint,
			UserAgent: cfg.UserAgent,
			Client:    rutrackerClient,
			Cookies:   cfg.RutrackerCookies,
		}),
	}, cfg.RequestTimeout, buildServiceOptions(cfg, logger)...)

	runtimeConfigStore := buildRuntimeConfigStore(cfg, logger)
	providerSettings := torznab.NewRuntimeConfigService(
		cfg.FlareSolverrURL,
		runtimeConfigStore,
		jackettProvider,
		prowlarrProvider,
	)
	serverOpts := []apihttp.ServerOption{
		apihttp.WithLogger(logger),
		apihttp.WithProviderSettings(providerSettings),
	}
	tmdbClient := buildTMDBClient(cfg, logger)
	if tmdbClient != nil && tmdbClient.Enabled() {
		serverOpts = append(serverOpts, apihttp.WithTMDB(tmdbClient))
	}

	handler := apihttp.NewServer(searchService, serverOpts...).Handler()
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// SSE streaming (/search/stream) can legitimately exceed short write timeouts.
		// Keep it disabled at the server level; rely on per-provider timeouts and upstream limits.
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	searchService.StartBackground(rootCtx)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	logger.Info("torrent search service started",
		slog.String("addr", cfg.HTTPAddr),
		slog.Duration("timeout", cfg.RequestTimeout),
	)

	select {
	case <-rootCtx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("shutdown error", slog.String("error", err.Error()))
	}
	logger.Info("torrent search service stopped")
}

func newRutrackerHTTPClient(timeout time.Duration, proxyRaw string) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true

	proxyValue := strings.TrimSpace(proxyRaw)
	if proxyValue != "" {
		parsed, err := url.Parse(proxyValue)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			if err == nil {
				err = errors.New("missing scheme or host")
			}
			slog.Default().Warn("invalid rutracker proxy url; proxy disabled", slog.String("error", err.Error()))
			transport.Proxy = nil
		} else {
			transport.Proxy = http.ProxyURL(parsed)
		}
	} else {
		// Avoid picking up unrelated container/host proxy environment variables unless explicitly configured.
		transport.Proxy = nil
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: otelhttp.NewTransport(transport),
	}
}

func newLogger(levelRaw, formatRaw string) *slog.Logger {
	level := parseLogLevel(levelRaw)
	options := &slog.HandlerOptions{Level: level}
	format := strings.ToLower(strings.TrimSpace(formatRaw))
	if format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, options))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, options))
}

func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func buildServiceOptions(cfg app.Config, logger *slog.Logger) []search.ServiceOption {
	var opts []search.ServiceOption

	if cfg.CacheDisabled {
		opts = append(opts, search.WithCacheDisabled(true))
		return opts
	}

	if cfg.CacheTTL > 0 {
		opts = append(opts, search.WithCacheTTL(cfg.CacheTTL))
	}

	redisURL := strings.TrimSpace(cfg.RedisURL)
	if redisURL != "" {
		redisOpts, err := redis.ParseURL(redisURL)
		if err != nil {
			logger.Warn("invalid redis url, using in-memory cache only", slog.String("error", err.Error()))
			return opts
		}
		redisClient := redis.NewClient(redisOpts)
		if err := redisClient.Ping(context.Background()).Err(); err != nil {
			logger.Warn("redis not reachable, using in-memory cache only", slog.String("error", err.Error()))
			return opts
		}
		logger.Info("redis connected", slog.String("addr", redisOpts.Addr))
		opts = append(opts, search.WithRedisCache(search.NewRedisCacheBackend(redisClient)))
	}

	return opts
}

func buildTMDBClient(cfg app.Config, logger *slog.Logger) *tmdb.Client {
	apiKey := strings.TrimSpace(cfg.TMDBAPIKey)
	if apiKey == "" {
		logger.Info("tmdb api key not configured, suggest endpoint disabled")
		return nil
	}

	// Try to reuse Redis for TMDB caching.
	var redisClient *redis.Client
	redisURL := strings.TrimSpace(cfg.RedisURL)
	if redisURL != "" {
		if opts, err := redis.ParseURL(redisURL); err == nil {
			redisClient = redis.NewClient(opts)
		}
	}

	client := tmdb.NewClient(tmdb.Config{
		APIKey:   apiKey,
		BaseURL:  cfg.TMDBBaseURL,
		Client:   &http.Client{Timeout: 10 * time.Second},
		Redis:    redisClient,
		CacheTTL: cfg.TMDBCacheTTL,
	})
	logger.Info("tmdb client initialized", slog.Bool("enabled", client.Enabled()))
	return client
}

func buildRuntimeConfigStore(cfg app.Config, logger *slog.Logger) torznab.RuntimeConfigStore {
	redisURL := strings.TrimSpace(cfg.RedisURL)
	if redisURL == "" {
		return nil
	}
	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		logger.Warn("runtime config store disabled: invalid redis url", slog.String("error", err.Error()))
		return nil
	}
	client := redis.NewClient(redisOpts)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		logger.Warn("runtime config store disabled: redis unavailable", slog.String("error", err.Error()))
		return nil
	}
	return torznab.NewRedisRuntimeConfigStore(client, "")
}
