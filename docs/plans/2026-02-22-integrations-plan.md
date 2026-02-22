# Integrations Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a new `torrent-notifier` Go microservice that notifies Jellyfin/Emby on torrent completion, implements a qBittorrent API v2 compatibility layer for Sonarr/Radarr, and exposes a widget JSON endpoint for Homepage/Homarr dashboards.

**Architecture:** New standalone Go service (`services/torrent-notifier/`) on the `core`+`edge` Docker networks. It reads completion events via MongoDB change stream on the same DB as torrent-engine. qBittorrent API endpoints proxy HTTP calls to `torrentstream:8080`. Config is stored in MongoDB `settings` collection (same DB, document `_id: "integrations"`), exposed via `/settings/integrations` REST endpoint consumed by the existing React frontend.

**Tech Stack:** Go 1.25, `go.mongodb.org/mongo-driver v1.17.9`, standard `net/http`, no external HTTP frameworks. Frontend: React + TypeScript (existing patterns in `frontend/src/`).

---

## Reference

**Module names:**
- This service: `torrentstream/notifier`
- torrent-engine: `torrentstream` (port 8080)

**Torrent-engine `GET /torrents` response shape:**
```json
{
  "items": [
    {
      "id": "507f1f77bcf86cd799439011",
      "name": "Movie.Name.2023",
      "status": "active",
      "progress": 0.42,
      "doneBytes": 1805123584,
      "totalBytes": 4294967296,
      "createdAt": "2024-01-01T00:00:00Z",
      "updatedAt": "2024-01-01T00:00:00Z",
      "tags": []
    }
  ],
  "count": 1
}
```

**Status values in torrent-engine:** `pending`, `active`, `completed`, `stopped`, `error`

**Existing pattern for MongoDB settings repos:**
See `services/torrent-engine/internal/repository/mongo/encoding_settings.go` — single document upsert with `_id: "encoding"`.

---

## Task 1: Service scaffold

**Files:**
- Create: `services/torrent-notifier/go.mod`
- Create: `services/torrent-notifier/cmd/server/main.go`
- Create: `services/torrent-notifier/internal/app/config.go`

**Step 1: Create directory structure**

```bash
mkdir -p services/torrent-notifier/cmd/server
mkdir -p services/torrent-notifier/internal/app
mkdir -p services/torrent-notifier/internal/domain
mkdir -p services/torrent-notifier/internal/repository/mongo
mkdir -p services/torrent-notifier/internal/notifier
mkdir -p services/torrent-notifier/internal/watcher
mkdir -p services/torrent-notifier/internal/qbt
mkdir -p services/torrent-notifier/internal/api/http
```

**Step 2: Create `services/torrent-notifier/go.mod`**

```
module torrentstream/notifier

go 1.25.0

require (
	go.mongodb.org/mongo-driver v1.17.9
)
```

Run `cd services/torrent-notifier && go mod tidy` — this will populate `go.sum`.

**Step 3: Create `services/torrent-notifier/internal/app/config.go`**

```go
package app

import (
	"os"
	"strings"
)

type Config struct {
	HTTPAddr          string
	MongoURI          string
	MongoDatabase     string
	TorrentEngineURL  string
	LogLevel          string
}

func LoadConfig() Config {
	return Config{
		HTTPAddr:         getEnv("HTTP_ADDR", ":8070"),
		MongoURI:         getEnv("MONGO_URI", "mongodb://localhost:27017"),
		MongoDatabase:    getEnv("MONGO_DB", "torrentstream"),
		TorrentEngineURL: getEnv("TORRENT_ENGINE_URL", "http://localhost:8080"),
		LogLevel:         strings.ToLower(getEnv("LOG_LEVEL", "info")),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

**Step 4: Create `services/torrent-notifier/cmd/server/main.go`**

```go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"torrentstream/notifier/internal/app"
)

func main() {
	cfg := app.LoadConfig()
	log.Printf("torrent-notifier starting on %s", cfg.HTTPAddr)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: mux,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("torrent-notifier stopped")
}
```

**Step 5: Verify build**

```bash
cd services/torrent-notifier && go build ./...
```
Expected: no errors, no output.

**Step 6: Commit**

```bash
git add services/torrent-notifier/
git commit -m "feat(notifier): scaffold torrent-notifier service"
```

---

## Task 2: Domain + MongoDB settings repository

**Files:**
- Create: `services/torrent-notifier/internal/domain/settings.go`
- Create: `services/torrent-notifier/internal/repository/mongo/settings.go`
- Create: `services/torrent-notifier/internal/repository/mongo/settings_test.go`

**Step 1: Create `internal/domain/settings.go`**

```go
package domain

// MediaServerConfig holds connection details for a single media server.
type MediaServerConfig struct {
	Enabled bool   `bson:"enabled" json:"enabled"`
	URL     string `bson:"url"     json:"url"`
	APIKey  string `bson:"apiKey"  json:"apiKey"`
}

// QBTConfig controls the qBittorrent API compatibility layer.
type QBTConfig struct {
	Enabled bool `bson:"enabled" json:"enabled"`
}

// IntegrationSettings is the single settings document stored in MongoDB.
type IntegrationSettings struct {
	Jellyfin  MediaServerConfig `bson:"jellyfin"  json:"jellyfin"`
	Emby      MediaServerConfig `bson:"emby"      json:"emby"`
	QBT       QBTConfig         `bson:"qbt"       json:"qbt"`
	UpdatedAt int64             `bson:"updatedAt" json:"updatedAt"`
}
```

**Step 2: Create `internal/repository/mongo/settings.go`**

```go
package mongorepo

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"torrentstream/notifier/internal/domain"
)

const docID = "integrations"

type SettingsRepository struct {
	col *mongo.Collection
}

func NewSettingsRepository(db *mongo.Database) *SettingsRepository {
	return &SettingsRepository{col: db.Collection("settings")}
}

// Get returns current settings, or defaults if the document does not exist.
func (r *SettingsRepository) Get(ctx context.Context) (domain.IntegrationSettings, error) {
	var doc struct {
		domain.IntegrationSettings `bson:",inline"`
	}
	err := r.col.FindOne(ctx, bson.M{"_id": docID}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return domain.IntegrationSettings{}, nil
	}
	if err != nil {
		return domain.IntegrationSettings{}, err
	}
	return doc.IntegrationSettings, nil
}

// Upsert saves settings, setting UpdatedAt to now.
func (r *SettingsRepository) Upsert(ctx context.Context, s domain.IntegrationSettings) error {
	s.UpdatedAt = time.Now().UnixMilli()
	_, err := r.col.UpdateOne(
		ctx,
		bson.M{"_id": docID},
		bson.M{"$set": s},
		options.Update().SetUpsert(true),
	)
	return err
}
```

**Step 3: Write the test `internal/repository/mongo/settings_test.go`**

```go
package mongorepo_test

import (
	"testing"

	"torrentstream/notifier/internal/domain"
)

func TestIntegrationSettings_Defaults(t *testing.T) {
	s := domain.IntegrationSettings{}
	if s.Jellyfin.Enabled {
		t.Error("Jellyfin should be disabled by default")
	}
	if s.Emby.Enabled {
		t.Error("Emby should be disabled by default")
	}
	if s.QBT.Enabled {
		t.Error("QBT should be disabled by default")
	}
}

func TestIntegrationSettings_Fields(t *testing.T) {
	s := domain.IntegrationSettings{
		Jellyfin: domain.MediaServerConfig{
			Enabled: true,
			URL:     "http://jellyfin:8096",
			APIKey:  "testkey",
		},
	}
	if s.Jellyfin.URL != "http://jellyfin:8096" {
		t.Errorf("unexpected URL: %s", s.Jellyfin.URL)
	}
}
```

**Step 4: Run tests**

```bash
cd services/torrent-notifier && go test ./internal/...
```
Expected: `ok  torrentstream/notifier/internal/repository/mongo`

**Step 5: Commit**

```bash
git add services/torrent-notifier/internal/
git commit -m "feat(notifier): add domain settings + MongoDB repository"
```

---

## Task 3: Jellyfin/Emby notifier

**Files:**
- Create: `services/torrent-notifier/internal/notifier/notifier.go`
- Create: `services/torrent-notifier/internal/notifier/notifier_test.go`

**Step 1: Write the failing test first**

`internal/notifier/notifier_test.go`:

```go
package notifier_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"torrentstream/notifier/internal/domain"
	"torrentstream/notifier/internal/notifier"
)

func TestNotifier_Jellyfin_CallsLibraryRefresh(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/Library/Refresh" {
			t.Errorf("expected /Library/Refresh, got %s", r.URL.Path)
		}
		if r.Header.Get("X-Emby-Token") != "testkey" {
			t.Errorf("expected X-Emby-Token: testkey, got %s", r.Header.Get("X-Emby-Token"))
		}
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	cfg := domain.MediaServerConfig{
		Enabled: true,
		URL:     ts.URL,
		APIKey:  "testkey",
	}
	n := notifier.New()
	err := n.NotifyMediaServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("server was not called")
	}
}

func TestNotifier_Disabled_DoesNotCall(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer ts.Close()

	cfg := domain.MediaServerConfig{Enabled: false, URL: ts.URL, APIKey: "key"}
	n := notifier.New()
	err := n.NotifyMediaServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("server should not have been called when disabled")
	}
}

func TestNotifier_EmptyURL_ReturnsNil(t *testing.T) {
	cfg := domain.MediaServerConfig{Enabled: true, URL: "", APIKey: "key"}
	n := notifier.New()
	err := n.NotifyMediaServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("expected nil for empty URL, got %v", err)
	}
}
```

**Step 2: Run to confirm tests fail**

```bash
cd services/torrent-notifier && go test ./internal/notifier/
```
Expected: compile error "notifier: package not found"

**Step 3: Create `internal/notifier/notifier.go`**

```go
package notifier

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"torrentstream/notifier/internal/domain"
)

// Notifier sends library refresh requests to media servers.
type Notifier struct {
	client *http.Client
}

func New() *Notifier {
	return &Notifier{
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// NotifyMediaServer sends POST /Library/Refresh to a configured media server.
// If disabled or URL is empty, it is a no-op.
func (n *Notifier) NotifyMediaServer(ctx context.Context, cfg domain.MediaServerConfig) error {
	if !cfg.Enabled || strings.TrimSpace(cfg.URL) == "" {
		return nil
	}
	url := strings.TrimRight(cfg.URL, "/") + "/Library/Refresh"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-Emby-Token", cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		// Log but do not fail — completion is already persisted.
		log.Printf("notifier: POST %s failed: %v", url, err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("notifier: POST %s returned %d", url, resp.StatusCode)
	}
	return nil
}

// TestConnection checks if the media server is reachable and the API key works.
// Returns an error message string (empty = success).
func (n *Notifier) TestConnection(ctx context.Context, cfg domain.MediaServerConfig) string {
	if strings.TrimSpace(cfg.URL) == "" {
		return "URL is required"
	}
	url := strings.TrimRight(cfg.URL, "/") + "/Library/Refresh"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err.Error()
	}
	req.Header.Set("X-Emby-Token", cfg.APIKey)
	resp, err := n.client.Do(req)
	if err != nil {
		return err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusUnauthorized {
		return fmt.Sprintf("server returned %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return "invalid API key"
	}
	return ""
}
```

**Step 4: Run tests**

```bash
cd services/torrent-notifier && go test ./internal/notifier/
```
Expected: `ok  torrentstream/notifier/internal/notifier`

**Step 5: Commit**

```bash
git add services/torrent-notifier/internal/notifier/
git commit -m "feat(notifier): add Jellyfin/Emby HTTP notifier with tests"
```

---

## Task 4: MongoDB change stream watcher

**Files:**
- Create: `services/torrent-notifier/internal/watcher/watcher.go`
- Create: `services/torrent-notifier/internal/watcher/watcher_test.go`

**Step 1: Write the failing test**

`internal/watcher/watcher_test.go`:

```go
package watcher_test

import (
	"testing"

	"torrentstream/notifier/internal/watcher"
)

func TestIsCompletionEvent_DetectsCompleted(t *testing.T) {
	event := watcher.ChangeEvent{
		OperationType: "update",
		UpdatedFields: map[string]interface{}{
			"status": "completed",
		},
	}
	if !watcher.IsCompletionEvent(event) {
		t.Error("should detect completion event")
	}
}

func TestIsCompletionEvent_IgnoresOtherStatuses(t *testing.T) {
	for _, status := range []string{"active", "stopped", "pending", "error"} {
		event := watcher.ChangeEvent{
			OperationType: "update",
			UpdatedFields: map[string]interface{}{"status": status},
		}
		if watcher.IsCompletionEvent(event) {
			t.Errorf("should not detect %q as completion", status)
		}
	}
}

func TestIsCompletionEvent_IgnoresInsert(t *testing.T) {
	event := watcher.ChangeEvent{
		OperationType: "insert",
		UpdatedFields: map[string]interface{}{"status": "completed"},
	}
	if watcher.IsCompletionEvent(event) {
		t.Error("insert events should not trigger notification")
	}
}
```

**Step 2: Run to confirm failure**

```bash
cd services/torrent-notifier && go test ./internal/watcher/
```
Expected: compile error

**Step 3: Create `internal/watcher/watcher.go`**

```go
package watcher

import (
	"context"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ChangeEvent represents a simplified MongoDB change stream event.
type ChangeEvent struct {
	OperationType string
	UpdatedFields map[string]interface{}
	FullDocument  struct {
		ID   string `bson:"_id"`
		Name string `bson:"name"`
	}
}

// IsCompletionEvent returns true if the event represents a torrent reaching "completed" status.
func IsCompletionEvent(e ChangeEvent) bool {
	if e.OperationType != "update" {
		return false
	}
	status, ok := e.UpdatedFields["status"]
	if !ok {
		return false
	}
	return status == "completed"
}

// NotifyFunc is called with the torrent name when completion is detected.
type NotifyFunc func(ctx context.Context, torrentName string)

// Watcher watches the MongoDB torrents collection for completion events.
type Watcher struct {
	col    *mongo.Collection
	notify NotifyFunc
}

func New(db *mongo.Database, notify NotifyFunc) *Watcher {
	return &Watcher{
		col:    db.Collection("torrents"),
		notify: notify,
	}
}

// Run starts the change stream loop. Blocks until ctx is cancelled.
// Reconnects automatically on transient errors.
func (w *Watcher) Run(ctx context.Context) {
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{
			{Key: "operationType", Value: "update"},
			{Key: "updateDescription.updatedFields.status", Value: "completed"},
		}}},
	}
	opts := options.ChangeStream().SetFullDocument(options.UpdateLookup)

	for {
		if err := w.watch(ctx, pipeline, opts); err != nil {
			if ctx.Err() != nil {
				return // context cancelled — normal shutdown
			}
			log.Printf("watcher: change stream error, retrying in 5s: %v", err)
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (w *Watcher) watch(ctx context.Context, pipeline mongo.Pipeline, opts *options.ChangeStreamOptions) error {
	cs, err := w.col.Watch(ctx, pipeline, opts)
	if err != nil {
		return err
	}
	defer cs.Close(ctx)

	for cs.Next(ctx) {
		var raw struct {
			OperationType string `bson:"operationType"`
			UpdateDesc    struct {
				UpdatedFields bson.M `bson:"updatedFields"`
			} `bson:"updateDescription"`
			FullDocument struct {
				Name string `bson:"name"`
			} `bson:"fullDocument"`
		}
		if err := cs.Decode(&raw); err != nil {
			log.Printf("watcher: decode error: %v", err)
			continue
		}
		event := ChangeEvent{
			OperationType: raw.OperationType,
			UpdatedFields: raw.UpdateDesc.UpdatedFields,
		}
		if IsCompletionEvent(event) {
			w.notify(ctx, raw.FullDocument.Name)
		}
	}
	return cs.Err()
}
```

**Step 4: Run tests**

```bash
cd services/torrent-notifier && go test ./internal/watcher/
```
Expected: `ok  torrentstream/notifier/internal/watcher`

**Step 5: Commit**

```bash
git add services/torrent-notifier/internal/watcher/
git commit -m "feat(notifier): add MongoDB change stream watcher"
```

---

## Task 5: qBittorrent API v2 compatibility layer

**Files:**
- Create: `services/torrent-notifier/internal/qbt/handler.go`
- Create: `services/torrent-notifier/internal/qbt/handler_test.go`

This layer proxies requests from Sonarr/Radarr to `torrentstream:8080`.

**Step 1: Write the failing tests**

`internal/qbt/handler_test.go`:

```go
package qbt_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"torrentstream/notifier/internal/qbt"
)

// fakeEngine returns a mock torrent-engine response.
func fakeEngine(t *testing.T, resp string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(resp))
	}))
}

const engineListResp = `{
  "items": [
    {
      "id": "507f1f77bcf86cd799439011",
      "name": "Test.Torrent",
      "status": "active",
      "progress": 0.5,
      "doneBytes": 512,
      "totalBytes": 1024,
      "createdAt": "2024-01-01T00:00:00Z",
      "updatedAt": "2024-01-01T00:00:00Z"
    }
  ],
  "count": 1
}`

func TestQBT_TorrentsInfo_MapsStatus(t *testing.T) {
	engine := fakeEngine(t, engineListResp)
	defer engine.Close()

	h := qbt.NewHandler(engine.URL)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v2/torrents/info", nil)
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var items []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0]["state"] != "downloading" {
		t.Errorf("expected state=downloading, got %v", items[0]["state"])
	}
	if items[0]["hash"] != "507f1f77bcf86cd799439011" {
		t.Errorf("unexpected hash: %v", items[0]["hash"])
	}
}

func TestQBT_Login_AlwaysOk(t *testing.T) {
	h := qbt.NewHandler("http://unused")
	w := httptest.NewRecorder()
	form := url.Values{"username": {"admin"}, "password": {"pass"}}
	r := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "Ok." {
		t.Errorf("expected 'Ok.', got %q", w.Body.String())
	}
}

func TestQBT_AppVersion(t *testing.T) {
	h := qbt.NewHandler("http://unused")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v2/app/version", nil)
	h.ServeHTTP(w, r)
	if w.Body.String() != "4.6.0" {
		t.Errorf("expected 4.6.0, got %q", w.Body.String())
	}
}
```

**Step 2: Run to confirm failure**

```bash
cd services/torrent-notifier && go test ./internal/qbt/
```
Expected: compile error

**Step 3: Create `internal/qbt/handler.go`**

```go
package qbt

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// statusMap converts torrent-engine status to qBittorrent state string.
var statusMap = map[string]string{
	"active":    "downloading",
	"completed": "uploading",
	"stopped":   "pausedDL",
	"pending":   "checkingDL",
	"error":     "error",
}

type engineTorrent struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Progress   float64   `json:"progress"`
	DoneBytes  int64     `json:"doneBytes"`
	TotalBytes int64     `json:"totalBytes"`
	CreatedAt  time.Time `json:"createdAt"`
}

type engineListResp struct {
	Items []engineTorrent `json:"items"`
	Count int             `json:"count"`
}

// qbtTorrent is the qBittorrent API v2 torrent info shape.
type qbtTorrent struct {
	Hash           string  `json:"hash"`
	Name           string  `json:"name"`
	State          string  `json:"state"`
	Progress       float64 `json:"progress"`
	Size           int64   `json:"size"`
	Downloaded     int64   `json:"downloaded"`
	DlSpeed        int64   `json:"dlspeed"`
	SavePath       string  `json:"save_path"`
	Category       string  `json:"category"`
	AddedOn        int64   `json:"added_on"`
	CompletionOn   int64   `json:"completion_on"`
}

// Handler implements the qBittorrent WebAPI v2 subset needed by Sonarr/Radarr.
type Handler struct {
	engineURL  string
	client     *http.Client
	mux        *http.ServeMux
}

func NewHandler(engineURL string) *Handler {
	h := &Handler{
		engineURL: strings.TrimRight(engineURL, "/"),
		client:    &http.Client{Timeout: 10 * time.Second},
	}
	h.mux = http.NewServeMux()
	h.mux.HandleFunc("/api/v2/auth/login", h.handleLogin)
	h.mux.HandleFunc("/api/v2/app/version", h.handleAppVersion)
	h.mux.HandleFunc("/api/v2/app/webapiVersion", h.handleWebapiVersion)
	h.mux.HandleFunc("/api/v2/torrents/info", h.handleTorrentsInfo)
	h.mux.HandleFunc("/api/v2/torrents/add", h.handleTorrentsAdd)
	h.mux.HandleFunc("/api/v2/torrents/delete", h.handleTorrentsDelete)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	// Always succeed — localhost-only, no real auth needed.
	http.SetCookie(w, &http.Cookie{Name: "SID", Value: "localtoken", Path: "/"})
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "Ok.")
}

func (h *Handler) handleAppVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "4.6.0")
}

func (h *Handler) handleWebapiVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "2.8.3")
}

func (h *Handler) handleTorrentsInfo(w http.ResponseWriter, r *http.Request) {
	resp, err := h.client.Get(h.engineURL + "/torrents")
	if err != nil {
		log.Printf("qbt: GET /torrents error: %v", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var list engineListResp
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		http.Error(w, "decode error", http.StatusInternalServerError)
		return
	}

	out := make([]qbtTorrent, 0, len(list.Items))
	for _, t := range list.Items {
		state, ok := statusMap[t.Status]
		if !ok {
			state = "unknown"
		}
		qt := qbtTorrent{
			Hash:       t.ID,
			Name:       t.Name,
			State:      state,
			Progress:   t.Progress,
			Size:       t.TotalBytes,
			Downloaded: t.DoneBytes,
			DlSpeed:    0,
			SavePath:   "/data/",
			Category:   "",
			AddedOn:    t.CreatedAt.Unix(),
		}
		if t.Status == "completed" {
			qt.CompletionOn = t.CreatedAt.Unix()
		}
		out = append(out, qt)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *Handler) handleTorrentsAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		r.ParseForm()
	}
	magnet := r.FormValue("urls")
	name := r.FormValue("rename")

	body := fmt.Sprintf(`{"magnetURI":%q,"name":%q}`, magnet, name)
	resp, err := h.client.Post(h.engineURL+"/torrents", "application/json",
		strings.NewReader(body))
	if err != nil {
		log.Printf("qbt: POST /torrents error: %v", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "Ok.")
}

func (h *Handler) handleTorrentsDelete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	hash := r.FormValue("hashes")
	deleteFiles := r.FormValue("deleteFiles") == "true"

	url := fmt.Sprintf("%s/torrents/%s?deleteFiles=%v", h.engineURL, hash, deleteFiles)
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodDelete, url, nil)
	resp, err := h.client.Do(req)
	if err != nil {
		log.Printf("qbt: DELETE /torrents/%s error: %v", hash, err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	w.WriteHeader(http.StatusOK)
}
```

**Step 4: Run tests**

```bash
cd services/torrent-notifier && go test ./internal/qbt/
```
Expected: `ok  torrentstream/notifier/internal/qbt`

**Step 5: Commit**

```bash
git add services/torrent-notifier/internal/qbt/
git commit -m "feat(notifier): add qBittorrent API v2 compatibility layer"
```

---

## Task 6: Widget + health HTTP handlers

**Files:**
- Create: `services/torrent-notifier/internal/api/http/handlers_widget.go`
- Create: `services/torrent-notifier/internal/api/http/handlers_widget_test.go`

**Step 1: Write failing test**

`internal/api/http/handlers_widget_test.go`:

```go
package apihttp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apihttp "torrentstream/notifier/internal/api/http"
)

const engineResp = `{"items":[
  {"id":"1","name":"A","status":"active","progress":0.5,"doneBytes":512,"totalBytes":1024,"createdAt":"2024-01-01T00:00:00Z","updatedAt":"2024-01-01T00:00:00Z"},
  {"id":"2","name":"B","status":"completed","progress":1.0,"doneBytes":1024,"totalBytes":1024,"createdAt":"2024-01-01T00:00:00Z","updatedAt":"2024-01-01T00:00:00Z"},
  {"id":"3","name":"C","status":"stopped","progress":0.1,"doneBytes":100,"totalBytes":1024,"createdAt":"2024-01-01T00:00:00Z","updatedAt":"2024-01-01T00:00:00Z"}
],"count":3}`

func TestWidget_CountsByStatus(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(engineResp))
	}))
	defer engine.Close()

	srv := apihttp.NewServer(engine.URL, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/widget", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["active"].(float64) != 1 {
		t.Errorf("expected active=1, got %v", resp["active"])
	}
	if resp["completed"].(float64) != 1 {
		t.Errorf("expected completed=1, got %v", resp["completed"])
	}
	if resp["total"].(float64) != 3 {
		t.Errorf("expected total=3, got %v", resp["total"])
	}
}
```

**Step 2: Create `internal/api/http/server.go`**

```go
package apihttp

import (
	"net/http"

	"torrentstream/notifier/internal/repository/mongo"
)

// Server holds all HTTP handlers for the notifier service.
type Server struct {
	engineURL string
	repo      *mongorepo.SettingsRepository
	mux       *http.ServeMux
}

// NewServer creates the HTTP server. repo may be nil in tests.
func NewServer(engineURL string, repo *mongorepo.SettingsRepository) *Server {
	s := &Server{
		engineURL: engineURL,
		repo:      repo,
		mux:       http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/widget", s.handleWidget)
	s.mux.HandleFunc("/settings/integrations", s.handleIntegrationSettings)
	s.mux.HandleFunc("/settings/integrations/test-jellyfin", s.handleTestJellyfin)
	s.mux.HandleFunc("/settings/integrations/test-emby", s.handleTestEmby)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
```

**Step 3: Create `internal/api/http/handlers_widget.go`**

```go
package apihttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type engineItem struct {
	Status string `json:"status"`
}
type engineList struct {
	Items []engineItem `json:"items"`
	Count int          `json:"count"`
}

type widgetResponse struct {
	Active    int    `json:"active"`
	Completed int    `json:"completed"`
	Stopped   int    `json:"stopped"`
	Total     int    `json:"total"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"ok"}`)
}

func (s *Server) handleWidget(w http.ResponseWriter, r *http.Request) {
	client := &http.Client{Timeout: 5 * time.Second}
	url := strings.TrimRight(s.engineURL, "/") + "/torrents"
	resp, err := client.Get(url)
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var list engineList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		http.Error(w, "decode error", http.StatusInternalServerError)
		return
	}

	out := widgetResponse{Total: list.Count}
	for _, item := range list.Items {
		switch item.Status {
		case "active":
			out.Active++
		case "completed":
			out.Completed++
		case "stopped":
			out.Stopped++
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
```

**Step 4: Run tests**

```bash
cd services/torrent-notifier && go test ./internal/api/http/
```
Expected: `ok  torrentstream/notifier/internal/api/http`

**Step 5: Commit**

```bash
git add services/torrent-notifier/internal/api/
git commit -m "feat(notifier): add widget and health HTTP handlers"
```

---

## Task 7: Settings HTTP handlers

**Files:**
- Create: `services/torrent-notifier/internal/api/http/handlers_settings.go`
- Create: `services/torrent-notifier/internal/api/http/handlers_settings_test.go`

**Step 1: Create `internal/api/http/handlers_settings.go`**

```go
package apihttp

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"torrentstream/notifier/internal/domain"
	"torrentstream/notifier/internal/notifier"
)

func (s *Server) handleIntegrationSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetSettings(w, r)
	case http.MethodPatch, http.MethodPut:
		s.handleUpdateSettings(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		json.NewEncoder(w).Encode(domain.IntegrationSettings{})
		return
	}
	settings, err := s.repo.Get(r.Context())
	if err != nil {
		http.Error(w, "repository error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var body domain.IntegrationSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.repo.Upsert(r.Context(), body); err != nil {
		http.Error(w, "repository error", http.StatusInternalServerError)
		return
	}
	saved, _ := s.repo.Get(r.Context())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(saved)
}

type testResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (s *Server) handleTestJellyfin(w http.ResponseWriter, r *http.Request) {
	s.handleTestMediaServer(w, r, func(settings domain.IntegrationSettings) domain.MediaServerConfig {
		return settings.Jellyfin
	})
}

func (s *Server) handleTestEmby(w http.ResponseWriter, r *http.Request) {
	s.handleTestMediaServer(w, r, func(settings domain.IntegrationSettings) domain.MediaServerConfig {
		return settings.Emby
	})
}

func (s *Server) handleTestMediaServer(w http.ResponseWriter, r *http.Request,
	getCfg func(domain.IntegrationSettings) domain.MediaServerConfig) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var settings domain.IntegrationSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	cfg := getCfg(settings)
	n := notifier.New()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	errMsg := n.TestConnection(ctx, cfg)
	result := testResult{OK: errMsg == "", Error: errMsg}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
```

**Step 2: Write test `internal/api/http/handlers_settings_test.go`**

```go
package apihttp_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apihttp "torrentstream/notifier/internal/api/http"
)

func TestSettings_GetReturnsDefaults(t *testing.T) {
	srv := apihttp.NewServer("http://unused", nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/settings/integrations", nil)
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if _, ok := body["jellyfin"]; !ok {
		t.Error("response missing jellyfin field")
	}
}

func TestSettings_MethodNotAllowed(t *testing.T) {
	srv := apihttp.NewServer("http://unused", nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/settings/integrations", nil)
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestTestJellyfin_InvalidURL_ReturnsError(t *testing.T) {
	srv := apihttp.NewServer("http://unused", nil)
	body := `{"jellyfin":{"enabled":true,"url":"http://127.0.0.1:1","apiKey":"key"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/settings/integrations/test-jellyfin",
		bytes.NewBufferString(body))
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	if result["ok"] == true {
		t.Error("connection to port 1 should fail")
	}
}
```

**Step 3: Run tests**

```bash
cd services/torrent-notifier && go test ./internal/api/http/
```
Expected: `ok  torrentstream/notifier/internal/api/http`

**Step 4: Commit**

```bash
git add services/torrent-notifier/internal/api/http/handlers_settings.go \
        services/torrent-notifier/internal/api/http/handlers_settings_test.go
git commit -m "feat(notifier): add integration settings GET/PATCH/test handlers"
```

---

## Task 8: Wire main.go + complete HTTP server

**Files:**
- Modify: `services/torrent-notifier/cmd/server/main.go`
- Modify: `services/torrent-notifier/internal/api/http/server.go` (add qbt routes)

**Step 1: Update `internal/api/http/server.go` to mount qBittorrent routes**

Add after `s.routes()` in `NewServer`:
```go
import "torrentstream/notifier/internal/qbt"
```

Add a new method:
```go
// MountQBT mounts the qBittorrent API compatibility routes.
func (s *Server) MountQBT(handler *qbt.Handler) {
	s.mux.Handle("/api/v2/", handler)
}
```

**Step 2: Replace `cmd/server/main.go` with the full wired version**

```go
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

	// MongoDB
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		log.Fatalf("mongo connect: %v", err)
	}
	db := client.Database(cfg.MongoDatabase)
	repo := mongorepo.NewSettingsRepository(db)

	// Notifier
	n := notifier.New()
	notify := func(ctx context.Context, torrentName string) {
		settings, err := repo.Get(ctx)
		if err != nil {
			log.Printf("notify: get settings: %v", err)
			return
		}
		notifyAll(ctx, n, settings, torrentName)
	}

	// Change stream watcher
	w := watcher.New(db, notify)

	// HTTP server
	srv := apihttp.NewServer(cfg.TorrentEngineURL, repo)
	srv.MountQBT(qbt.NewHandler(cfg.TorrentEngineURL))

	httpSrv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: srv,
	}

	// Start watcher
	watchCtx, watchCancel := context.WithCancel(context.Background())
	go w.Run(watchCtx)

	// Start HTTP
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	watchCancel()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	_ = httpSrv.Shutdown(shutCtx)
	_ = client.Disconnect(shutCtx)
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
```

**Step 3: Run `go mod tidy` then build**

```bash
cd services/torrent-notifier && go mod tidy && go build ./...
```
Expected: no errors.

**Step 4: Run all tests**

```bash
cd services/torrent-notifier && go test ./...
```
Expected: all packages `ok`.

**Step 5: Commit**

```bash
git add services/torrent-notifier/
git commit -m "feat(notifier): wire main.go with MongoDB, watcher, qbt, HTTP server"
```

---

## Task 9: Dockerfile + Docker Compose + Traefik

**Files:**
- Create: `build/torrent-notifier.Dockerfile`
- Modify: `deploy/docker-compose.yml`
- Modify: `deploy/traefik/dynamic.yml`

**Step 1: Create `build/torrent-notifier.Dockerfile`**

```dockerfile
FROM golang:1.25 AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/torrent-notifier ./cmd/server

FROM alpine:3.20

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=build /out/torrent-notifier /app/torrent-notifier

ENV HTTP_ADDR=:8070

EXPOSE 8070

ENTRYPOINT ["/app/torrent-notifier"]
```

**Step 2: Add `torrent-notifier` to `deploy/docker-compose.yml`**

Add after the `torrent-search:` service block, before `redis:`:

```yaml
  torrent-notifier:
    build:
      context: ../services/torrent-notifier
      dockerfile: ../../build/torrent-notifier.Dockerfile
    logging: *default-logging
    restart: unless-stopped
    ports:
      - "127.0.0.1:8070:8070"
    environment:
      HTTP_ADDR: ":8070"
      LOG_LEVEL: "info"
      LOG_FORMAT: "text"
      MONGO_URI: "mongodb://mongo:27017"
      MONGO_DB: "torrentstream"
      TORRENT_ENGINE_URL: "http://torrentstream:8080"
    depends_on:
      mongo:
        condition: service_healthy
      torrentstream:
        condition: service_healthy
    networks:
      - edge
      - core
    deploy:
      resources:
        limits:
          memory: 128m
          cpus: "0.25"
    healthcheck:
      test: ["CMD-SHELL", "wget --spider --quiet http://localhost:8070/health || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 3
```

**Step 3: Add routes to `deploy/traefik/dynamic.yml`**

Add under `routers:` (between `search` and `jackett`):

```yaml
    integrations:
      entryPoints:
        - web
      rule: "PathPrefix(`/settings/integrations`) || PathPrefix(`/api/v2`) || PathPrefix(`/widget`)"
      service: torrent-notifier-api
      middlewares:
        - security-headers
      priority: 195

    integrations-secure:
      entryPoints:
        - websecure
      rule: "PathPrefix(`/settings/integrations`) || PathPrefix(`/api/v2`) || PathPrefix(`/widget`)"
      service: torrent-notifier-api
      middlewares:
        - security-headers
      priority: 195
      tls: {}
```

Add under `services:` (after `torrent-search-api`):

```yaml
    torrent-notifier-api:
      loadBalancer:
        serversTransport: backend-long-timeout
        servers:
          - url: "http://torrent-notifier:8070"
```

**Step 4: Validate compose syntax**

```bash
docker compose -f deploy/docker-compose.yml config --quiet
```
Expected: no output (no errors).

**Step 5: Commit**

```bash
git add build/torrent-notifier.Dockerfile deploy/docker-compose.yml deploy/traefik/dynamic.yml
git commit -m "feat(notifier): add Dockerfile, compose service, Traefik routes"
```

---

## Task 10: Frontend API client

**Files:**
- Modify: `frontend/src/api.ts`
- Modify: `frontend/vite.config.ts`

**Step 1: Add proxy to `frontend/vite.config.ts`**

The existing Vite config has individual `/settings/*` entries. Add these entries **before** (or alongside) existing entries — Vite uses first-match so more specific paths should come first:

```typescript
'/settings/integrations': {
  target: notifierProxyTarget,  // http://localhost:8070
  changeOrigin: true,
},
'/api/v2': {
  target: notifierProxyTarget,
  changeOrigin: true,
},
'/widget': {
  target: notifierProxyTarget,
  changeOrigin: true,
},
```

Also add `notifierProxyTarget` variable:

```typescript
const notifierProxyTarget = env.VITE_NOTIFIER_PROXY_TARGET || 'http://localhost:8070';
```

**Step 2: Add types and API calls to `frontend/src/api.ts`**

Add these types near the other settings types (search for `StorageSettings` to find the right place):

```typescript
export interface MediaServerConfig {
  enabled: boolean;
  url: string;
  apiKey: string;
}

export interface QBTConfig {
  enabled: boolean;
}

export interface IntegrationSettings {
  jellyfin: MediaServerConfig;
  emby: MediaServerConfig;
  qbt: QBTConfig;
  updatedAt?: number;
}

export interface TestConnectionResult {
  ok: boolean;
  error?: string;
}
```

Add these functions near the other `getSettings`/`updateSettings` functions:

```typescript
export async function getIntegrationSettings(): Promise<IntegrationSettings> {
  const resp = await fetchWithTimeout('/settings/integrations');
  if (!resp.ok) throw new Error(`Failed to fetch integration settings: ${resp.status}`);
  return resp.json();
}

export async function updateIntegrationSettings(
  settings: IntegrationSettings
): Promise<IntegrationSettings> {
  const resp = await fetchWithTimeout('/settings/integrations', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
  });
  if (!resp.ok) throw new Error(`Failed to update integration settings: ${resp.status}`);
  return resp.json();
}

export async function testJellyfinConnection(
  settings: IntegrationSettings
): Promise<TestConnectionResult> {
  const resp = await fetchWithTimeout('/settings/integrations/test-jellyfin', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
  });
  if (!resp.ok) throw new Error(`Test request failed: ${resp.status}`);
  return resp.json();
}

export async function testEmbyConnection(
  settings: IntegrationSettings
): Promise<TestConnectionResult> {
  const resp = await fetchWithTimeout('/settings/integrations/test-emby', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
  });
  if (!resp.ok) throw new Error(`Test request failed: ${resp.status}`);
  return resp.json();
}
```

**Step 3: Type-check**

```bash
cd frontend && npx tsc --noEmit
```
Expected: no errors.

**Step 4: Commit**

```bash
git add frontend/vite.config.ts frontend/src/api.ts
git commit -m "feat(notifier): add frontend API client for integration settings"
```

---

## Task 11: Frontend Integrations Settings UI

**Files:**
- Modify: `frontend/src/pages/SettingsPage.tsx`

Read the current end of `SettingsPage.tsx` first to find the last section and where to insert.
The pattern: each section is a `<div>` block with a heading, form fields using `<input className="ts-dropdown-trigger ...">` and `<button>` elements.

**Step 1: Add imports at top of `SettingsPage.tsx`**

Find the existing API imports line (search for `getStorageSettings` or similar) and add:

```typescript
import {
  getIntegrationSettings,
  updateIntegrationSettings,
  testJellyfinConnection,
  testEmbyConnection,
  type IntegrationSettings,
  type MediaServerConfig,
} from '../api';
```

**Step 2: Add state near other settings state variables**

```typescript
const [integrations, setIntegrations] = useState<IntegrationSettings>({
  jellyfin: { enabled: false, url: '', apiKey: '' },
  emby: { enabled: false, url: '', apiKey: '' },
  qbt: { enabled: true },
});
const [integrationsSaving, setIntegrationsSaving] = useState(false);
const [jellyfinTestResult, setJellyfinTestResult] = useState<string | null>(null);
const [embyTestResult, setEmbyTestResult] = useState<string | null>(null);
```

**Step 3: Add useEffect to load integrations**

```typescript
useEffect(() => {
  getIntegrationSettings()
    .then(setIntegrations)
    .catch(() => {}); // default values if service not running
}, []);
```

**Step 4: Add save handler**

```typescript
const handleSaveIntegrations = async () => {
  setIntegrationsSaving(true);
  try {
    const saved = await updateIntegrationSettings(integrations);
    setIntegrations(saved);
  } finally {
    setIntegrationsSaving(false);
  }
};
```

**Step 5: Add test handlers**

```typescript
const handleTestJellyfin = async () => {
  setJellyfinTestResult(null);
  const result = await testJellyfinConnection(integrations);
  setJellyfinTestResult(result.ok ? '✓ Connected' : `✗ ${result.error}`);
};

const handleTestEmby = async () => {
  setEmbyTestResult(null);
  const result = await testEmbyConnection(integrations);
  setEmbyTestResult(result.ok ? '✓ Connected' : `✗ ${result.error}`);
};
```

**Step 6: Add the Integrations section JSX**

Add after the last existing settings section (before the closing `</div>` of the page):

```tsx
{/* ── Integrations ── */}
<div className="settings-section">
  <h2 className="settings-section-title">Integrations</h2>

  {/* Jellyfin */}
  <div className="settings-card">
    <div className="settings-card-header">
      <h3>Jellyfin</h3>
      <label className="settings-toggle">
        <input
          type="checkbox"
          checked={integrations.jellyfin.enabled}
          onChange={e =>
            setIntegrations(s => ({
              ...s,
              jellyfin: { ...s.jellyfin, enabled: e.target.checked },
            }))
          }
        />
        Enabled
      </label>
    </div>
    {integrations.jellyfin.enabled && (
      <div className="settings-card-body">
        <label className="settings-label">URL</label>
        <input
          className="ts-dropdown-trigger settings-input"
          type="url"
          placeholder="http://jellyfin:8096"
          value={integrations.jellyfin.url}
          onChange={e =>
            setIntegrations(s => ({
              ...s,
              jellyfin: { ...s.jellyfin, url: e.target.value },
            }))
          }
        />
        <label className="settings-label">API Key</label>
        <input
          className="ts-dropdown-trigger settings-input"
          type="password"
          placeholder="Paste Jellyfin API key"
          value={integrations.jellyfin.apiKey}
          onChange={e =>
            setIntegrations(s => ({
              ...s,
              jellyfin: { ...s.jellyfin, apiKey: e.target.value },
            }))
          }
        />
        <div className="settings-row">
          <button className="btn-secondary" onClick={handleTestJellyfin}>
            Test
          </button>
          {jellyfinTestResult && (
            <span className={jellyfinTestResult.startsWith('✓') ? 'text-success' : 'text-error'}>
              {jellyfinTestResult}
            </span>
          )}
        </div>
      </div>
    )}
  </div>

  {/* Emby */}
  <div className="settings-card">
    <div className="settings-card-header">
      <h3>Emby</h3>
      <label className="settings-toggle">
        <input
          type="checkbox"
          checked={integrations.emby.enabled}
          onChange={e =>
            setIntegrations(s => ({
              ...s,
              emby: { ...s.emby, enabled: e.target.checked },
            }))
          }
        />
        Enabled
      </label>
    </div>
    {integrations.emby.enabled && (
      <div className="settings-card-body">
        <label className="settings-label">URL</label>
        <input
          className="ts-dropdown-trigger settings-input"
          type="url"
          placeholder="http://emby:8096"
          value={integrations.emby.url}
          onChange={e =>
            setIntegrations(s => ({
              ...s,
              emby: { ...s.emby, url: e.target.value },
            }))
          }
        />
        <label className="settings-label">API Key</label>
        <input
          className="ts-dropdown-trigger settings-input"
          type="password"
          placeholder="Paste Emby API key"
          value={integrations.emby.apiKey}
          onChange={e =>
            setIntegrations(s => ({
              ...s,
              emby: { ...s.emby, apiKey: e.target.value },
            }))
          }
        />
        <div className="settings-row">
          <button className="btn-secondary" onClick={handleTestEmby}>
            Test
          </button>
          {embyTestResult && (
            <span className={embyTestResult.startsWith('✓') ? 'text-success' : 'text-error'}>
              {embyTestResult}
            </span>
          )}
        </div>
      </div>
    )}
  </div>

  {/* qBittorrent API */}
  <div className="settings-card">
    <div className="settings-card-header">
      <h3>Download Client (Sonarr / Radarr)</h3>
      <label className="settings-toggle">
        <input
          type="checkbox"
          checked={integrations.qbt.enabled}
          onChange={e =>
            setIntegrations(s => ({
              ...s,
              qbt: { enabled: e.target.checked },
            }))
          }
        />
        Enabled
      </label>
    </div>
    {integrations.qbt.enabled && (
      <div className="settings-card-body settings-info">
        <p>Connect Sonarr or Radarr using the <strong>qBittorrent</strong> download client type:</p>
        <ul>
          <li>Host: <code>{window.location.hostname}</code></li>
          <li>Port: <code>8070</code></li>
          <li>Password: <em>(leave empty)</em></li>
        </ul>
      </div>
    )}
  </div>

  <div className="settings-actions">
    <button
      className="btn-primary"
      onClick={handleSaveIntegrations}
      disabled={integrationsSaving}
    >
      {integrationsSaving ? 'Saving…' : 'Save Integrations'}
    </button>
  </div>
</div>
```

**Step 7: Type-check**

```bash
cd frontend && npx tsc --noEmit
```
Expected: no errors.

**Step 8: Run all Go tests**

```bash
cd services/torrent-notifier && go test ./...
```
Expected: all `ok`.

**Step 9: Commit**

```bash
git add frontend/src/pages/SettingsPage.tsx
git commit -m "feat(notifier): add Integrations settings UI section (Jellyfin, Emby, qBt)"
```

---

## Final Verification

```bash
# Validate compose file
docker compose -f deploy/docker-compose.yml config --quiet

# All Go tests
cd services/torrent-notifier && go test ./...

# Frontend type-check
cd frontend && npx tsc --noEmit
```

Expected: no errors across all three checks.
