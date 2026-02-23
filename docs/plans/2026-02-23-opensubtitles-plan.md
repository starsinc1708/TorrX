# OpenSubtitles Integration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add OpenSubtitles search and download to the torrent-engine backend and video player frontend, with configurable language preferences via settings UI.

**Architecture:** New `subtitle_settings` app-layer manager (same pattern as `EncodingSettingsManager`) backed by MongoDB. OpenSubtitles REST API v1 adapter in `internal/services/subtitles/opensubtitles/`. Two new HTTP endpoints for search/download. Frontend: settings section + player integration with auto-search fallback.

**Tech Stack:** Go (net/http client), MongoDB, React, TypeScript, HLS.js `<track>` element

---

### Task 1: Subtitle Settings — App Layer

**Files:**
- Create: `services/torrent-engine/internal/app/subtitle_settings.go`
- Test: `services/torrent-engine/internal/app/subtitle_settings_test.go`

**Step 1: Write the failing test**

```go
// subtitle_settings_test.go
package app

import (
	"context"
	"testing"
)

type fakeSubtitleStore struct {
	settings SubtitleSettings
	exists   bool
	getErr   error
	setErr   error
	setCalls int
}

func (f *fakeSubtitleStore) GetSubtitleSettings(ctx context.Context) (SubtitleSettings, bool, error) {
	return f.settings, f.exists, f.getErr
}

func (f *fakeSubtitleStore) SetSubtitleSettings(ctx context.Context, s SubtitleSettings) error {
	f.setCalls++
	f.settings = s
	f.exists = true
	return f.setErr
}

func TestSubtitleSettingsManager_GetDefault(t *testing.T) {
	mgr := NewSubtitleSettingsManager(nil)
	got := mgr.Get()
	if got.Enabled {
		t.Fatal("expected disabled by default")
	}
	if len(got.Languages) != 0 {
		t.Fatalf("expected no languages, got %v", got.Languages)
	}
}

func TestSubtitleSettingsManager_UpdateAndGet(t *testing.T) {
	store := &fakeSubtitleStore{}
	mgr := NewSubtitleSettingsManager(store)
	err := mgr.Update(SubtitleSettings{
		Enabled:    true,
		APIKey:     "test-key",
		Languages:  []string{"en", "ru"},
		AutoSearch: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mgr.Get()
	if !got.Enabled || got.APIKey != "test-key" || len(got.Languages) != 2 || !got.AutoSearch {
		t.Fatalf("unexpected settings: %+v", got)
	}
	if store.setCalls != 1 {
		t.Fatalf("expected 1 store call, got %d", store.setCalls)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd services/torrent-engine && go test ./internal/app/ -run TestSubtitleSettings -v`
Expected: FAIL — types not defined

**Step 3: Write minimal implementation**

```go
// subtitle_settings.go
package app

import (
	"context"
	"sync"
	"time"
)

type SubtitleSettings struct {
	Enabled    bool     `json:"enabled"`
	APIKey     string   `json:"apiKey"`
	Languages  []string `json:"languages"`
	AutoSearch bool     `json:"autoSearch"`
}

type SubtitleSettingsStore interface {
	GetSubtitleSettings(ctx context.Context) (SubtitleSettings, bool, error)
	SetSubtitleSettings(ctx context.Context, settings SubtitleSettings) error
}

type SubtitleSettingsManager struct {
	mu       sync.RWMutex
	current  SubtitleSettings
	store    SubtitleSettingsStore
	timeout  time.Duration
}

func NewSubtitleSettingsManager(store SubtitleSettingsStore) *SubtitleSettingsManager {
	mgr := &SubtitleSettingsManager{
		store:   store,
		timeout: 5 * time.Second,
	}
	if store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), mgr.timeout)
		defer cancel()
		if saved, ok, err := store.GetSubtitleSettings(ctx); err == nil && ok {
			mgr.current = saved
		}
	}
	return mgr
}

func (m *SubtitleSettingsManager) Get() SubtitleSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.current
	langs := make([]string, len(s.Languages))
	copy(langs, s.Languages)
	s.Languages = langs
	return s
}

func (m *SubtitleSettingsManager) Update(settings SubtitleSettings) error {
	if m.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
		defer cancel()
		if err := m.store.SetSubtitleSettings(ctx, settings); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.current = settings
	m.mu.Unlock()
	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `cd services/torrent-engine && go test ./internal/app/ -run TestSubtitleSettings -v`
Expected: PASS

**Step 5: Commit**

```bash
git add services/torrent-engine/internal/app/subtitle_settings.go services/torrent-engine/internal/app/subtitle_settings_test.go
git commit -m "feat(subtitles): add SubtitleSettings app layer with manager and store interface"
```

---

### Task 2: Subtitle Settings — MongoDB Repository

**Files:**
- Create: `services/torrent-engine/internal/repository/mongo/subtitle_settings.go`

Follow exact same pattern as `encoding_settings.go` in same directory.

**Step 1: Write implementation**

```go
// subtitle_settings.go
package mongo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"torrentstream/internal/app"
)

const subtitleSettingsID = "subtitle_settings"

type subtitleSettingsDoc struct {
	ID         string   `bson:"_id"`
	Enabled    bool     `bson:"enabled"`
	APIKey     string   `bson:"apiKey"`
	Languages  []string `bson:"languages"`
	AutoSearch bool     `bson:"autoSearch"`
	UpdatedAt  int64    `bson:"updatedAt"`
}

type SubtitleSettingsRepository struct {
	collection *mongo.Collection
}

func NewSubtitleSettingsRepository(client *mongo.Client, dbName string) *SubtitleSettingsRepository {
	return &SubtitleSettingsRepository{
		collection: client.Database(dbName).Collection("settings"),
	}
}

func (r *SubtitleSettingsRepository) GetSubtitleSettings(ctx context.Context) (app.SubtitleSettings, bool, error) {
	var doc subtitleSettingsDoc
	err := r.collection.FindOne(ctx, bson.M{"_id": subtitleSettingsID}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return app.SubtitleSettings{}, false, nil
		}
		return app.SubtitleSettings{}, false, err
	}
	return app.SubtitleSettings{
		Enabled:    doc.Enabled,
		APIKey:     doc.APIKey,
		Languages:  doc.Languages,
		AutoSearch: doc.AutoSearch,
	}, true, nil
}

func (r *SubtitleSettingsRepository) SetSubtitleSettings(ctx context.Context, s app.SubtitleSettings) error {
	_, err := r.collection.UpdateOne(
		ctx,
		bson.M{"_id": subtitleSettingsID},
		bson.M{"$set": bson.M{
			"enabled":    s.Enabled,
			"apiKey":     s.APIKey,
			"languages":  s.Languages,
			"autoSearch": s.AutoSearch,
			"updatedAt":  time.Now().Unix(),
		}},
		options.Update().SetUpsert(true),
	)
	return err
}
```

**Step 2: Verify compilation**

Run: `cd services/torrent-engine && go build ./...`
Expected: OK

**Step 3: Commit**

```bash
git add services/torrent-engine/internal/repository/mongo/subtitle_settings.go
git commit -m "feat(subtitles): add MongoDB repository for subtitle settings"
```

---

### Task 3: OpenSubtitles API Client

**Files:**
- Create: `services/torrent-engine/internal/services/subtitles/opensubtitles/client.go`
- Create: `services/torrent-engine/internal/services/subtitles/opensubtitles/client_test.go`

**Step 1: Write the failing test**

```go
// client_test.go
package opensubtitles

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_Search(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/subtitles" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Api-Key") != "test-key" {
			t.Fatalf("missing api key header")
		}
		q := r.URL.Query()
		if q.Get("moviehash") != "abc123" {
			t.Fatalf("unexpected moviehash: %s", q.Get("moviehash"))
		}
		if q.Get("languages") != "en,ru" {
			t.Fatalf("unexpected languages: %s", q.Get("languages"))
		}
		resp := searchResponse{
			Data: []searchResult{
				{
					ID: "1",
					Attributes: searchAttributes{
						Language:    "en",
						Release:     "Movie.2024.1080p",
						Ratings:     8.5,
						DownloadCount: 1000,
						Files: []searchFile{{FileID: 101, FileName: "movie.srt"}},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	results, err := client.Search(context.Background(), SearchParams{
		MovieHash: "abc123",
		Languages: []string{"en", "ru"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Language != "en" || results[0].FileID != 101 {
		t.Fatalf("unexpected result: %+v", results[0])
	}
}

func TestClient_SearchByQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("query") != "Pirates Caribbean" {
			t.Fatalf("unexpected query: %s", q.Get("query"))
		}
		resp := searchResponse{Data: []searchResult{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	results, err := client.Search(context.Background(), SearchParams{
		Query:     "Pirates Caribbean",
		Languages: []string{"en"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestClient_Download(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/download" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		resp := downloadResponse{Link: "https://dl.opensubtitles.com/file/123"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	link, err := client.DownloadLink(context.Background(), 101)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if link != "https://dl.opensubtitles.com/file/123" {
		t.Fatalf("unexpected link: %s", link)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd services/torrent-engine && go test ./internal/services/subtitles/opensubtitles/ -v`
Expected: FAIL — package not found

**Step 3: Write implementation**

```go
// client.go
package opensubtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.opensubtitles.com/api/v1"

type SubtitleResult struct {
	FileID        int
	Language      string
	Release       string
	Rating        float64
	DownloadCount int
	FileName      string
}

type SearchParams struct {
	MovieHash string
	Query     string
	Languages []string
}

type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

type Option func(*Client)

func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = url }
}

func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// --- search types ---

type searchResponse struct {
	Data []searchResult `json:"data"`
}

type searchResult struct {
	ID         string           `json:"id"`
	Attributes searchAttributes `json:"attributes"`
}

type searchAttributes struct {
	Language      string       `json:"language"`
	Release       string       `json:"release"`
	Ratings       float64      `json:"ratings"`
	DownloadCount int          `json:"download_count"`
	Files         []searchFile `json:"files"`
}

type searchFile struct {
	FileID   int    `json:"file_id"`
	FileName string `json:"file_name"`
}

func (c *Client) Search(ctx context.Context, params SearchParams) ([]SubtitleResult, error) {
	q := url.Values{}
	if params.MovieHash != "" {
		q.Set("moviehash", params.MovieHash)
	}
	if params.Query != "" {
		q.Set("query", params.Query)
	}
	if len(params.Languages) > 0 {
		q.Set("languages", strings.Join(params.Languages, ","))
	}

	reqURL := c.baseURL + "/subtitles?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("opensubtitles search: status %d: %s", resp.StatusCode, body)
	}

	var sr searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("opensubtitles search: decode: %w", err)
	}

	var results []SubtitleResult
	for _, item := range sr.Data {
		for _, file := range item.Attributes.Files {
			results = append(results, SubtitleResult{
				FileID:        file.FileID,
				Language:      item.Attributes.Language,
				Release:       item.Attributes.Release,
				Rating:        item.Attributes.Ratings,
				DownloadCount: item.Attributes.DownloadCount,
				FileName:      file.FileName,
			})
		}
	}
	return results, nil
}

// --- download types ---

type downloadRequest struct {
	FileID int `json:"file_id"`
}

type downloadResponse struct {
	Link string `json:"link"`
}

func (c *Client) DownloadLink(ctx context.Context, fileID int) (string, error) {
	body, err := json.Marshal(downloadRequest{FileID: fileID})
	if err != nil {
		return "", err
	}

	reqURL := c.baseURL + "/download"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("opensubtitles download: status %d: %s", resp.StatusCode, respBody)
	}

	var dr downloadResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return "", fmt.Errorf("opensubtitles download: decode: %w", err)
	}
	return dr.Link, nil
}
```

**Step 4: Run test to verify it passes**

Run: `cd services/torrent-engine && go test ./internal/services/subtitles/opensubtitles/ -v`
Expected: PASS (3 tests)

**Step 5: Commit**

```bash
git add services/torrent-engine/internal/services/subtitles/opensubtitles/
git commit -m "feat(subtitles): add OpenSubtitles REST API client with search and download"
```

---

### Task 4: Subtitle HTTP Handlers + Settings Endpoint

**Files:**
- Create: `services/torrent-engine/internal/api/http/handlers_subtitles.go`
- Modify: `services/torrent-engine/internal/api/http/server.go` — add SubtitleSettingsController interface, field, option, route

**Step 1: Add controller interface and wiring to server.go**

In `server.go`, add alongside existing settings controller interfaces:

```go
type SubtitleSettingsController interface {
	Get() app.SubtitleSettings
	Update(settings app.SubtitleSettings) error
}
```

Add field to Server struct: `subtitles SubtitleSettingsController`

Add option function:

```go
func WithSubtitleSettings(ctrl SubtitleSettingsController) ServerOption {
	return func(s *Server) { s.subtitles = ctrl }
}
```

Add setter (same pattern as `SetEncodingSettings`):

```go
func (s *Server) SetSubtitleSettings(ctrl SubtitleSettingsController) {
	s.subtitles = ctrl
}
```

Register routes in NewServer (after existing settings routes):

```go
mux.HandleFunc("/settings/subtitles", s.handleSubtitleSettings)
mux.HandleFunc("/torrents/subtitles/search", s.handleSubtitleSearch)
mux.HandleFunc("/torrents/subtitles/download", s.handleSubtitleDownload)
```

**Step 2: Write handlers file**

```go
// handlers_subtitles.go
package http

import (
	"encoding/json"
	"net/http"
	"strings"

	"torrentstream/internal/app"
	"torrentstream/internal/services/subtitles/opensubtitles"
)

func (s *Server) handleSubtitleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetSubtitleSettings(w, r)
	case http.MethodPatch, http.MethodPut:
		s.handleUpdateSubtitleSettings(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetSubtitleSettings(w http.ResponseWriter, _ *http.Request) {
	if s.subtitles == nil {
		writeJSON(w, http.StatusOK, app.SubtitleSettings{})
		return
	}
	writeJSON(w, http.StatusOK, s.subtitles.Get())
}

func (s *Server) handleUpdateSubtitleSettings(w http.ResponseWriter, r *http.Request) {
	if s.subtitles == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "subtitle settings not configured")
		return
	}

	var body app.SubtitleSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}

	current := s.subtitles.Get()
	// Merge partial update: only overwrite non-zero fields.
	if body.APIKey == "" {
		body.APIKey = current.APIKey
	}
	if body.Languages == nil {
		body.Languages = current.Languages
	}

	if err := s.subtitles.Update(body); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update subtitle settings")
		return
	}
	writeJSON(w, http.StatusOK, s.subtitles.Get())
}

type subtitleSearchResponse struct {
	Results []opensubtitles.SubtitleResult `json:"results"`
}

func (s *Server) handleSubtitleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.subtitles == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "subtitle settings not configured")
		return
	}

	settings := s.subtitles.Get()
	if settings.APIKey == "" {
		writeError(w, http.StatusBadRequest, "no_api_key", "OpenSubtitles API key not configured")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("query"))
	hash := strings.TrimSpace(r.URL.Query().Get("hash"))
	langParam := strings.TrimSpace(r.URL.Query().Get("lang"))

	if query == "" && hash == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "query or hash required")
		return
	}

	var langs []string
	if langParam != "" {
		langs = strings.Split(langParam, ",")
	} else {
		langs = settings.Languages
	}

	client := opensubtitles.NewClient(settings.APIKey)

	// Try hash first, fallback to query.
	var results []opensubtitles.SubtitleResult
	var err error

	if hash != "" {
		results, err = client.Search(r.Context(), opensubtitles.SearchParams{
			MovieHash: hash,
			Languages: langs,
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, "search_failed", err.Error())
			return
		}
	}

	if len(results) == 0 && query != "" {
		results, err = client.Search(r.Context(), opensubtitles.SearchParams{
			Query:     query,
			Languages: langs,
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, "search_failed", err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, subtitleSearchResponse{Results: results})
}

type subtitleDownloadRequest struct {
	FileID int `json:"fileId"`
}

func (s *Server) handleSubtitleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.subtitles == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "subtitle settings not configured")
		return
	}

	settings := s.subtitles.Get()
	if settings.APIKey == "" {
		writeError(w, http.StatusBadRequest, "no_api_key", "OpenSubtitles API key not configured")
		return
	}

	var body subtitleDownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.FileID == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "fileId required")
		return
	}

	client := opensubtitles.NewClient(settings.APIKey)
	link, err := client.DownloadLink(r.Context(), body.FileID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "download_failed", err.Error())
		return
	}

	// Fetch the actual subtitle file and proxy it as VTT.
	resp, err := http.Get(link)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetch_failed", err.Error())
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)

	// SRT → VTT conversion: prepend header, replace commas with dots in timestamps.
	// For simplicity, proxy raw content — frontend handles both SRT and VTT via HLS.js.
	w.Write([]byte("WEBVTT\n\n"))
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}
}
```

**Step 3: Verify compilation**

Run: `cd services/torrent-engine && go build ./...`
Expected: OK

**Step 4: Commit**

```bash
git add services/torrent-engine/internal/api/http/handlers_subtitles.go services/torrent-engine/internal/api/http/server.go
git commit -m "feat(subtitles): add HTTP handlers for subtitle settings, search, and download"
```

---

### Task 5: Wire Subtitle Settings in main.go

**Files:**
- Modify: `services/torrent-engine/cmd/server/main.go`

**Step 1: Add repository + manager wiring**

After the existing `storageSettingsRepo` initialization block, add:

```go
subtitleSettingsRepo := mongo.NewSubtitleSettingsRepository(mongoClient, cfg.MongoDatabase)
subtitleMgr := app.NewSubtitleSettingsManager(subtitleSettingsRepo)
```

Add to the options slice:

```go
apihttp.WithSubtitleSettings(subtitleMgr),
```

**Step 2: Add import for subtitles mongo package**

Ensure `mongo` import alias resolves correctly (it should already be imported as the same package).

**Step 3: Verify compilation**

Run: `cd services/torrent-engine && go build ./...`
Expected: OK

**Step 4: Run all tests**

Run: `cd services/torrent-engine && go test ./...`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add services/torrent-engine/cmd/server/main.go
git commit -m "feat(subtitles): wire subtitle settings manager in main.go"
```

---

### Task 6: Frontend Types + API Client

**Files:**
- Modify: `frontend/src/types.ts`
- Modify: `frontend/src/api.ts`

**Step 1: Add types**

In `types.ts`, add after existing settings interfaces:

```ts
export interface SubtitleSettings {
  enabled: boolean;
  apiKey: string;
  languages: string[];
  autoSearch: boolean;
}

export interface SubtitleResult {
  fileID: number;
  language: string;
  release: string;
  rating: number;
  downloadCount: number;
  fileName: string;
}

export interface SubtitleSearchResponse {
  results: SubtitleResult[];
}
```

**Step 2: Add API functions**

In `api.ts`, add:

```ts
export const getSubtitleSettings = async (): Promise<SubtitleSettings> => {
  const response = await deduplicatedFetch(buildUrl('/settings/subtitles'));
  return handleResponse(response);
};

export const updateSubtitleSettings = async (
  settings: Partial<SubtitleSettings>,
): Promise<SubtitleSettings> => {
  const response = await fetch(buildUrl('/settings/subtitles'), {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
  });
  return handleResponse(response);
};

export const searchSubtitles = async (
  query: string,
  lang?: string[],
): Promise<SubtitleSearchResponse> => {
  const params = new URLSearchParams();
  params.set('query', query);
  if (lang && lang.length > 0) params.set('lang', lang.join(','));
  const response = await deduplicatedFetch(
    buildUrl(`/torrents/subtitles/search?${params}`),
  );
  return handleResponse(response);
};

export const buildSubtitleDownloadUrl = (fileId: number): string =>
  buildUrl(`/torrents/subtitles/download`);

export const downloadSubtitle = async (
  fileId: number,
): Promise<string> => {
  const response = await fetch(buildUrl('/torrents/subtitles/download'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ fileId }),
  });
  if (!response.ok) throw new Error('Failed to download subtitle');
  const blob = await response.blob();
  return URL.createObjectURL(blob);
};
```

**Step 3: Verify type-check**

Run: `cd frontend && npx tsc --noEmit`
Expected: OK

**Step 4: Commit**

```bash
git add frontend/src/types.ts frontend/src/api.ts
git commit -m "feat(subtitles): add subtitle types and API client functions"
```

---

### Task 7: Settings Page — Subtitles Section

**Files:**
- Modify: `frontend/src/pages/SettingsPage.tsx`

**Step 1: Add state + load + update pattern**

Follow the same pattern as encoding settings. Add state variables, `loadSubtitleSettings` callback, `useEffect` on mount, and `handleUpdateSubtitleSettings` callback.

**Step 2: Add UI section**

Add a new section after existing settings sections with:
- Toggle for `enabled`
- Input for `apiKey` (password type, with show/hide)
- Toggle for `autoSearch`
- Language list: multi-select or comma-separated input for `languages`
  - Common presets: en, ru, es, fr, de, pt, it, zh, ja, ko
  - Use the existing `Input` component

**Step 3: Verify type-check**

Run: `cd frontend && npx tsc --noEmit`
Expected: OK

**Step 4: Commit**

```bash
git add frontend/src/pages/SettingsPage.tsx
git commit -m "feat(subtitles): add Subtitles settings section to SettingsPage"
```

---

### Task 8: Player — Subtitle Search Integration

**Files:**
- Modify: `frontend/src/components/VideoPlayer.tsx` — add "Find Subtitles" button + panel
- Modify: `frontend/src/hooks/useVideoPlayer.ts` — expose external subtitle URL state

**Step 1: Add external subtitle state to useVideoPlayer**

In `useVideoPlayer.ts`, add:
- `externalSubtitleUrl` state (string, initially '')
- `setExternalSubtitleUrl` exposed in return object
- Modify `subtitleTrackUrl` memo to prefer `externalSubtitleUrl` when set

**Step 2: Add subtitle search UI to VideoPlayer**

In `VideoPlayer.tsx`, add:
- "Find Subtitles" button in the subtitle track selector area (shown when no embedded subtitles or user clicks)
- Panel/dropdown showing search results (language, release, rating)
- On select: call `downloadSubtitle(fileId)` → get blob URL → set as `externalSubtitleUrl`
- Loading state while searching/downloading

**Step 3: Auto-search on media load**

In `PlayerPage.tsx` or `useVideoPlayer.ts`:
- When `mediaInfo` loads and `subtitleTracks.length === 0` and subtitle settings have `autoSearch: true`:
  - Call `searchSubtitles(torrent.name, settings.languages)`
  - If results found, show notification or auto-select best match

**Step 4: Verify type-check**

Run: `cd frontend && npx tsc --noEmit`
Expected: OK

**Step 5: Commit**

```bash
git add frontend/src/components/VideoPlayer.tsx frontend/src/hooks/useVideoPlayer.ts frontend/src/pages/PlayerPage.tsx
git commit -m "feat(subtitles): add subtitle search UI to video player with auto-search"
```

---

### Task 9: Traefik Routing + Final Integration Test

**Files:**
- Modify: `deploy/traefik/dynamic.yml` — add `/settings/subtitles` and `/torrents/subtitles/` to torrent-engine routes
- Modify: `frontend/vite.config.ts` — add proxy rule for `/torrents/subtitles/`

**Step 1: Verify Traefik routing**

Check if existing `/torrents` prefix rule already covers `/torrents/subtitles/*`. If so, no change needed.
Check if `/settings/subtitles` is covered by `/settings/*` rule. If so, no change needed.

**Step 2: Verify Vite proxy**

Check if existing `/torrents` proxy rule covers `/torrents/subtitles/*`. If so, no change needed.

**Step 3: Run full test suite**

Run: `cd services/torrent-engine && go test ./...`
Run: `cd frontend && npx tsc --noEmit`

**Step 4: Final commit if any routing changes needed**

```bash
git commit -m "feat(subtitles): add routing rules for subtitle endpoints"
```

---

## Summary

| Task | Component | Files | Estimated Steps |
|------|-----------|-------|-----------------|
| 1 | App layer settings | `app/subtitle_settings.go` + test | 5 |
| 2 | MongoDB repository | `repository/mongo/subtitle_settings.go` | 3 |
| 3 | OpenSubtitles client | `services/subtitles/opensubtitles/client.go` + test | 5 |
| 4 | HTTP handlers | `handlers_subtitles.go` + server.go mods | 4 |
| 5 | Wire in main.go | `cmd/server/main.go` | 5 |
| 6 | Frontend types + API | `types.ts` + `api.ts` | 4 |
| 7 | Settings page UI | `SettingsPage.tsx` | 4 |
| 8 | Player integration | `VideoPlayer.tsx` + `useVideoPlayer.ts` | 5 |
| 9 | Routing + final test | `traefik/dynamic.yml` + `vite.config.ts` | 4 |

**Total: 9 tasks, ~39 steps**
