package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	apihttp "torrentstream/internal/api/http"
	"torrentstream/internal/app"
	"torrentstream/internal/domain"
	"torrentstream/internal/metrics"
	mongorepo "torrentstream/internal/repository/mongo"
	"torrentstream/internal/services/session/player"
	sessionmongo "torrentstream/internal/services/session/repository/mongo"
	"torrentstream/internal/services/torrent/engine/anacrolix"
	"torrentstream/internal/services/torrent/engine/ffprobe"
	"torrentstream/internal/telemetry"
	"torrentstream/internal/usecase"

	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.opentelemetry.io/contrib/instrumentation/go.mongodb.org/mongo-driver/mongo/otelmongo"
)

func main() {
	cfg := app.LoadConfig()
	logger := newLogger(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)
	metrics.Register(prometheus.DefaultRegisterer)

	shutdownTracer, err := telemetry.Init(context.Background(), "torrent-engine")
	if err != nil {
		logger.Warn("otel init failed", slog.String("error", err.Error()))
	}
	defer func() {
		if shutdownTracer != nil {
			_ = shutdownTracer(context.Background())
		}
	}()

	logger.Info("configuration loaded",
		slog.String("service", "torrent-engine"),
		slog.String("httpAddr", cfg.HTTPAddr),
		slog.String("logLevel", cfg.LogLevel),
		slog.String("logFormat", cfg.LogFormat),
		slog.String("storageMode", cfg.StorageMode),
		slog.Int64("memoryLimitBytes", cfg.MemoryLimitBytes),
		slog.String("hlsDir", cfg.HLSDir),
		slog.String("dataDir", cfg.TorrentDataDir),
	)

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctx, cancel := context.WithTimeout(rootCtx, 10*time.Second)
	defer cancel()

	mongoOpts := otelmongo.NewMonitor()
	mongoClient, err := mongorepo.Connect(ctx, cfg.MongoURI, options.Client().SetMonitor(mongoOpts))
	if err != nil {
		logger.Error("mongo connect failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if err := mongoClient.Ping(ctx, readpref.Primary()); err != nil {
		logger.Error("mongo ping failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	repo := mongorepo.NewRepository(mongoClient, cfg.MongoDatabase, cfg.MongoCollection)
	settingsRepo := mongorepo.NewStorageSettingsRepository(mongoClient, cfg.MongoDatabase)
	watchHistoryRepo := sessionmongo.NewWatchHistoryRepository(mongoClient, cfg.MongoDatabase)
	encodingSettingsRepo := mongorepo.NewEncodingSettingsRepository(mongoClient, cfg.MongoDatabase)
	hlsSettingsRepo := mongorepo.NewHLSSettingsRepository(mongoClient, cfg.MongoDatabase)
	playerSettingsRepo := sessionmongo.NewPlayerSettingsRepository(mongoClient, cfg.MongoDatabase)

	if err := repo.EnsureIndexes(ctx); err != nil {
		logger.Warn("mongo ensure indexes failed", slog.String("error", err.Error()))
	}

	if limit, ok, err := settingsRepo.GetMemoryLimitBytes(ctx); err != nil {
		logger.Warn("storage settings load failed", slog.String("error", err.Error()))
	} else if ok {
		cfg.MemoryLimitBytes = limit
	}

	if enc, ok, err := encodingSettingsRepo.GetEncodingSettings(ctx); err != nil {
		logger.Warn("encoding settings load failed", slog.String("error", err.Error()))
	} else if ok {
		cfg.HLSPreset = enc.Preset
		cfg.HLSCRF = enc.CRF
		cfg.HLSAudioBitrate = enc.AudioBitrate
	}

	if hls, ok, err := hlsSettingsRepo.GetHLSSettings(ctx); err != nil {
		logger.Warn("hls settings load failed", slog.String("error", err.Error()))
	} else if ok {
		if hls.MemBufSizeMB > 0 {
			cfg.HLSMemBufSizeBytes = int64(hls.MemBufSizeMB) * 1024 * 1024
		}
		if hls.CacheSizeMB > 0 {
			cfg.HLSCacheSizeBytes = int64(hls.CacheSizeMB) * 1024 * 1024
		}
		if hls.CacheMaxAgeHours > 0 {
			cfg.HLSCacheMaxAgeH = int64(hls.CacheMaxAgeHours)
		}
	}

	currentTorrentID := domain.TorrentID("")
	if id, ok, err := playerSettingsRepo.GetCurrentTorrentID(ctx); err != nil {
		logger.Warn("player settings load failed", slog.String("error", err.Error()))
	} else if ok {
		currentTorrentID = id
	}

	engine, err := anacrolix.New(anacrolix.Config{
		DataDir:          cfg.TorrentDataDir,
		StorageMode:      cfg.StorageMode,
		MemoryLimitBytes: cfg.MemoryLimitBytes,
		MemorySpillDir:   cfg.MemorySpillDir,
		MaxSessions:      cfg.MaxSessions,
	})
	if err != nil {
		logger.Error("torrent engine init failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Restore previously active torrents from DB (in background so HTTP server starts immediately).
	go func() {
		restoreTorrents(rootCtx, engine, repo, logger)
		if currentTorrentID != "" {
			if err := engine.FocusSession(rootCtx, currentTorrentID); err != nil {
				logger.Warn("restore focused torrent failed",
					slog.String("torrentId", string(currentTorrentID)),
					slog.String("error", err.Error()),
				)
			}
		}
	}()

	// Start background state sync.
	syncUC := usecase.SyncState{Engine: engine, Repo: repo, Logger: logger}
	go syncUC.Run(rootCtx)

	// Start disk pressure monitor.
	if cfg.MinDiskSpaceBytes > 0 {
		diskUC := usecase.DiskPressure{
			Engine:        engine,
			Logger:        logger,
			DataDir:       cfg.TorrentDataDir,
			MinFreeBytes:  cfg.MinDiskSpaceBytes,
			ResumeBytes:   cfg.MinDiskSpaceBytes * 2,
		}
		go diskUC.Run(rootCtx)
	}

	createUC := usecase.CreateTorrent{Engine: engine, Repo: repo, Now: time.Now}
	startUC := usecase.StartTorrent{Engine: engine, Repo: repo, Now: time.Now}
	stopUC := usecase.StopTorrent{Engine: engine, Repo: repo, Now: time.Now}
	deleteUC := usecase.DeleteTorrent{Engine: engine, Repo: repo, DataDir: cfg.TorrentDataDir}
	streamUC := usecase.StreamTorrent{Engine: engine, Repo: repo, ReadaheadBytes: 2 << 20}
	stateUC := usecase.GetTorrentState{Engine: engine}
	listStateUC := usecase.ListActiveTorrentStates{Engine: engine}
	mediaProbe := ffprobe.New(cfg.FFProbePath)
	storageSettings := app.NewStorageSettingsManager(engine, settingsRepo)
	playerSettings := player.NewPlayerSettingsManager(engine, playerSettingsRepo, currentTorrentID)

	hlsCfg := apihttp.HLSConfig{
		FFMPEGPath:        cfg.FFMPEGPath,
		FFProbePath:       cfg.FFProbePath,
		BaseDir:           cfg.HLSDir,
		DataDir:           cfg.TorrentDataDir,
		ListenAddr:        cfg.HTTPAddr,
		Preset:            cfg.HLSPreset,
		CRF:               cfg.HLSCRF,
		AudioBitrate:      cfg.HLSAudioBitrate,
		MaxCacheSizeBytes: cfg.HLSCacheSizeBytes,
		MaxCacheAge:       time.Duration(cfg.HLSCacheMaxAgeH) * time.Hour,
		MemBufSizeBytes:   cfg.HLSMemBufSizeBytes,
	}

	options := []apihttp.ServerOption{
		apihttp.WithRepository(repo),
		apihttp.WithLogger(logger),
		apihttp.WithStartTorrent(startUC),
		apihttp.WithStopTorrent(stopUC),
		apihttp.WithDeleteTorrent(deleteUC),
		apihttp.WithStreamTorrent(streamUC),
		apihttp.WithGetTorrentState(stateUC),
		apihttp.WithListTorrentStates(listStateUC),
		apihttp.WithStorageSettings(storageSettings),
		apihttp.WithHLS(hlsCfg),
		apihttp.WithMediaProbe(mediaProbe, cfg.TorrentDataDir),
		apihttp.WithWatchHistory(watchHistoryRepo),
		apihttp.WithEngine(engine),
		apihttp.WithPlayerSettings(playerSettings),
		apihttp.WithAllowedOrigins(cfg.CORSAllowedOrigins),
	}
	if cfg.OpenAPIPath != "" {
		options = append(options, apihttp.WithOpenAPIPath(cfg.OpenAPIPath))
	}

	handler := apihttp.NewServer(createUC, options...)

	// Wire encoding settings manager after server creation (needs HLS engine).
	if hlsEngine := handler.EncodingSettingsEngine(); hlsEngine != nil {
		encodingMgr := app.NewEncodingSettingsManager(hlsEngine, encodingSettingsRepo)
		handler.SetEncodingSettings(encodingMgr)
	}

	// Wire HLS settings manager after server creation (needs HLS engine).
	if hlsEngine := handler.HLSSettingsEngine(); hlsEngine != nil {
		hlsMgr := app.NewHLSSettingsManager(hlsEngine, hlsSettingsRepo)
		handler.SetHLSSettings(hlsMgr)
	}

	// Periodically update Prometheus gauges from engine state.
	go updateEngineMetrics(rootCtx, engine, handler.HLSCacheTotalSize, handler)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	logger.Info("server started", slog.String("addr", cfg.HTTPAddr))

	select {
	case <-rootCtx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	handler.Close()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown error", slog.String("error", err.Error()))
	}
	if err := engine.Close(); err != nil {
		logger.Warn("engine close error", slog.String("error", err.Error()))
	}
	if err := mongoClient.Disconnect(context.Background()); err != nil {
		logger.Warn("mongo disconnect error", slog.String("error", err.Error()))
	}

	logger.Info("server stopped")
}

func updateEngineMetrics(ctx context.Context, engine *anacrolix.Engine, cacheSize func() int64, handler *apihttp.Server) {
	stateTicker := time.NewTicker(5 * time.Second)
	torrentTicker := time.NewTicker(15 * time.Second)
	healthTicker := time.NewTicker(30 * time.Second)
	defer stateTicker.Stop()
	defer torrentTicker.Stop()
	defer healthTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-stateTicker.C:
			ids, err := engine.ListActiveSessions(ctx)
			if err != nil {
				continue
			}
			metrics.ActiveSessions.Set(float64(len(ids)))
			var dlTotal, ulTotal int64
			var peersTotal int64
			var states []domain.SessionState
			for _, id := range ids {
				state, err := engine.GetSessionState(ctx, id)
				if err != nil {
					continue
				}
				dlTotal += state.DownloadSpeed
				ulTotal += state.UploadSpeed
				peersTotal += int64(state.Peers)
				states = append(states, state)
			}
			metrics.DownloadSpeedBytes.Set(float64(dlTotal))
			metrics.UploadSpeedBytes.Set(float64(ulTotal))
			metrics.PeersConnected.Set(float64(peersTotal))
			if cacheSize != nil {
				metrics.HLSCacheSizeBytes.Set(float64(cacheSize()))
			}
			handler.BroadcastStates(states)
		case <-torrentTicker.C:
			handler.BroadcastTorrents()
		case <-healthTicker.C:
			handler.BroadcastHealth(ctx)
		}
	}
}

func restoreTorrents(ctx context.Context, engine *anacrolix.Engine, repo *mongorepo.Repository, logger *slog.Logger) {
	active := domain.TorrentActive
	pending := domain.TorrentPending

	var records []domain.TorrentRecord
	for _, status := range []*domain.TorrentStatus{&active, &pending} {
		recs, err := repo.List(ctx, domain.TorrentFilter{Status: status})
		if err != nil {
			logger.Warn("restore: list failed", slog.String("status", string(*status)), slog.String("error", err.Error()))
			continue
		}
		records = append(records, recs...)
	}

	if len(records) == 0 {
		return
	}

	logger.Info("restoring torrents", slog.Int("count", len(records)))

	for _, rec := range records {
		src := rec.Source
		if strings.TrimSpace(src.Magnet) == "" && strings.TrimSpace(src.Torrent) == "" {
			logger.Warn("restore: no source", slog.String("id", string(rec.ID)))
			continue
		}

		session, err := engine.Open(ctx, src)
		if err != nil {
			logger.Warn("restore: open failed", slog.String("id", string(rec.ID)), slog.String("error", err.Error()))
			continue
		}

		if rec.Status == domain.TorrentActive {
			if err := session.Start(); err != nil {
				logger.Warn("restore: start failed", slog.String("id", string(rec.ID)), slog.String("error", err.Error()))
			}
		}

		logger.Info("restored torrent", slog.String("id", string(rec.ID)), slog.String("name", rec.Name))
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
