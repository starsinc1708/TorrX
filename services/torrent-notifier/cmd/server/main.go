package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"torrentstream/notifier/internal/app"
	apihttp "torrentstream/notifier/internal/api/http"
	"torrentstream/notifier/internal/domain"
	"torrentstream/notifier/internal/notifier"
	mongorepo "torrentstream/notifier/internal/repository/mongo"
	"torrentstream/notifier/internal/qbt"
	"torrentstream/notifier/internal/watcher"
)

func main() {
	cfg := app.LoadConfig()
	log.Printf("torrent-notifier starting on %s", cfg.HTTPAddr)

	// Connect to MongoDB
	connectCtx, connectCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer connectCancel()
	client, err := mongo.Connect(connectCtx, options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		log.Fatalf("mongo connect: %v", err)
	}
	db := client.Database(cfg.MongoDatabase)
	repo := mongorepo.NewSettingsRepository(db)

	// Build notifier for media server notifications
	n := notifier.New()

	// Notify function — called by watcher when a torrent completes
	notifyFn := func(ctx context.Context, torrentName string) {
		settings, err := repo.Get(ctx)
		if err != nil {
			log.Printf("notify: get settings: %v", err)
			return
		}
		notifyAll(ctx, n, settings, torrentName)
	}

	// Change stream watcher
	w := watcher.New(db, notifyFn)

	// HTTP server with all routes
	srv := apihttp.NewServer(cfg.TorrentEngineURL, repo)
	srv.MountQBT(qbt.NewHandler(cfg.TorrentEngineURL))

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Start watcher goroutine
	watchCtx, watchCancel := context.WithCancel(context.Background())
	go w.Run(watchCtx)

	// Start HTTP server
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	log.Printf("torrent-notifier ready")

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("torrent-notifier shutting down...")
	watchCancel()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	if err := client.Disconnect(shutCtx); err != nil {
		log.Printf("mongo disconnect: %v", err)
	}
	log.Println("torrent-notifier stopped")
}

func notifyAll(ctx context.Context, n *notifier.Notifier, settings domain.IntegrationSettings, torrentName string) {
	log.Printf("torrent completed: %q — notifying media servers", torrentName)
	if err := n.NotifyMediaServer(ctx, settings.Jellyfin); err != nil {
		log.Printf("jellyfin notify error: %v", err)
	}
	if err := n.NotifyMediaServer(ctx, settings.Emby); err != nil {
		log.Printf("emby notify error: %v", err)
	}
}
