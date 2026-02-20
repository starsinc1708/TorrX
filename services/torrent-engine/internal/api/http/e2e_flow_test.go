package apihttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/usecase"
)

// TestE2ECreateStartFocusStreamFlow validates the complete user journey:
// POST /torrents (add) → GET /torrents (verify listed) → POST /torrents/:id/start
// → POST /torrents/:id/focus → GET /torrents/:id/state → GET /torrents/:id/stream
//
// This mirrors the frontend flow: SearchPage adds a torrent via magnet,
// CatalogPage lists and starts it, PlayerPage focuses and streams it.
func TestE2ECreateStartFocusStreamFlow(t *testing.T) {
	now := time.Date(2026, 2, 20, 12, 0, 0, 0, time.UTC)
	record := domain.TorrentRecord{
		ID:         "abc123",
		Name:       "Sintel.2010.1080p",
		Status:     domain.TorrentActive,
		TotalBytes: 1_500_000_000,
		DoneBytes:  0,
		Files: []domain.FileRef{
			{Index: 0, Path: "Sintel.2010.1080p.mkv", Length: 1_500_000_000},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	create := &fakeCreateTorrent{result: record}
	start := &fakeStartTorrent{result: domain.TorrentRecord{
		ID:         "abc123",
		Name:       "Sintel.2010.1080p",
		Status:     domain.TorrentActive,
		TotalBytes: 1_500_000_000,
		DoneBytes:  50_000_000,
		CreatedAt:  now,
		UpdatedAt:  now,
	}}
	videoData := []byte("fake-video-segment-data")
	reader := &testStreamReader{Reader: bytes.NewReader(videoData)}
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			Reader: reader,
			File:   domain.FileRef{Index: 0, Path: "Sintel.2010.1080p.mkv", Length: int64(len(videoData))},
		},
	}
	state := &fakeGetTorrentState{
		result: domain.SessionState{
			ID:            "abc123",
			Status:        domain.TorrentActive,
			Progress:      0.03,
			DownloadSpeed: 2_500_000,
			UploadSpeed:   500_000,
			Peers:         12,
			UpdatedAt:     now,
		},
	}
	repo := &fakeRepo{
		list: []domain.TorrentRecord{record},
		get:  record,
	}
	player := &fakePlayerSettings{}

	server := NewServer(
		create,
		WithRepository(repo),
		WithStartTorrent(start),
		WithStreamTorrent(stream),
		WithGetTorrentState(state),
		WithPlayerSettings(player),
	)

	// Step 1: Add torrent via magnet (simulates SearchPage "Add to catalog" click)
	t.Run("step1_create_from_magnet", func(t *testing.T) {
		payload := []byte(`{"magnet":"magnet:?xt=urn:btih:abc123def456&dn=Sintel.2010.1080p","name":"Sintel.2010.1080p"}`)
		req := httptest.NewRequest(http.MethodPost, "/torrents", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("create: status = %d, body = %s", w.Code, w.Body.String())
		}
		if create.called != 1 {
			t.Fatalf("create usecase not called")
		}
		if create.input.Source.Magnet == "" {
			t.Fatalf("magnet not passed to usecase")
		}
		if create.input.Name != "Sintel.2010.1080p" {
			t.Fatalf("name = %q, want Sintel.2010.1080p", create.input.Name)
		}

		var got domain.TorrentRecord
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
		if got.ID != "abc123" {
			t.Fatalf("created torrent ID = %q, want abc123", got.ID)
		}
	})

	// Step 2: List torrents (simulates CatalogPage loading)
	t.Run("step2_list_catalog", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/torrents", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("list: status = %d", w.Code)
		}

		var resp struct {
			Items []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"items"`
			Count int `json:"count"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode list response: %v", err)
		}
		if resp.Count != 1 {
			t.Fatalf("catalog count = %d, want 1", resp.Count)
		}
		if resp.Items[0].ID != "abc123" {
			t.Fatalf("listed ID = %q, want abc123", resp.Items[0].ID)
		}
	})

	// Step 3: Start the torrent (simulates CatalogPage "Start" button)
	t.Run("step3_start_torrent", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/torrents/abc123/start", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("start: status = %d", w.Code)
		}
		if start.called != 1 || start.id != "abc123" {
			t.Fatalf("start usecase: called=%d id=%q", start.called, start.id)
		}

		var got domain.TorrentRecord
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decode start response: %v", err)
		}
		if got.Status != domain.TorrentActive {
			t.Fatalf("started status = %q, want active", got.Status)
		}
	})

	// Step 4: Focus torrent for streaming (simulates PlayerPage auto-focus)
	t.Run("step4_focus_for_playback", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/torrents/abc123/focus", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("focus: status = %d", w.Code)
		}
		if player.current != "abc123" {
			t.Fatalf("focus not persisted: current = %q", player.current)
		}
	})

	// Step 5: Get torrent state (simulates PlayerPage polling download progress)
	t.Run("step5_check_download_state", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/torrents/abc123/state", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("state: status = %d", w.Code)
		}

		var got domain.SessionState
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decode state response: %v", err)
		}
		if got.Progress == 0 {
			t.Fatalf("progress should be non-zero during active download")
		}
		if got.DownloadSpeed == 0 {
			t.Fatalf("download rate should be non-zero during active download")
		}
	})

	// Step 6: Stream video file (simulates PlayerPage starting HLS/direct playback)
	t.Run("step6_stream_video", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/torrents/abc123/stream?fileIndex=0", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("stream: status = %d, body = %s", w.Code, w.Body.String())
		}
		if stream.called != 1 || stream.id != "abc123" {
			t.Fatalf("stream usecase: called=%d id=%q", stream.called, stream.id)
		}
		if w.Body.String() != string(videoData) {
			t.Fatalf("streamed data mismatch: got %d bytes, want %d", w.Body.Len(), len(videoData))
		}
		if w.Header().Get("Accept-Ranges") != "bytes" {
			t.Fatalf("Accept-Ranges header missing")
		}
	})
}

// TestE2EStreamBeforeDownloadComplete validates that HLS streaming begins even
// with an incomplete download (acceptance criterion: "HLS streaming begins even
// with incomplete download").
func TestE2EStreamBeforeDownloadComplete(t *testing.T) {
	partialData := []byte("partial-video-data")
	reader := &testStreamReader{Reader: bytes.NewReader(partialData)}
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			Reader: reader,
			// File reports full size but only partial data is available via reader
			File: domain.FileRef{Index: 0, Path: "movie.mkv", Length: 4_000_000_000},
		},
	}

	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream))

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/stream?fileIndex=0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	// Streaming should begin even though the file isn't fully downloaded.
	// The handler serves whatever data the reader provides.
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (should stream partial data)", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Fatalf("body should contain partial data, got empty")
	}
	if stream.called != 1 {
		t.Fatalf("stream usecase should be called once, got %d", stream.called)
	}
}

// TestE2EFocusModeActivatesDuringPlayback validates that focus mode is
// activated during playback and deactivated after unfocusing.
func TestE2EFocusModeActivatesDuringPlayback(t *testing.T) {
	player := &fakePlayerSettings{}
	server := NewServer(&fakeCreateTorrent{}, WithPlayerSettings(player))

	// Focus torrent for playback
	req := httptest.NewRequest(http.MethodPost, "/torrents/t1/focus", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("focus: status = %d", w.Code)
	}
	if player.current != "t1" {
		t.Fatalf("focus not set: %q", player.current)
	}

	// Verify focus persists via player health endpoint
	healthReq := httptest.NewRequest(http.MethodGet, "/internal/health/player", nil)
	healthW := httptest.NewRecorder()
	server.ServeHTTP(healthW, healthReq)

	if healthW.Code != http.StatusOK {
		t.Fatalf("health: status = %d", healthW.Code)
	}
	var health playerHealthResponse
	if err := json.NewDecoder(healthW.Body).Decode(&health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if !health.FocusModeEnabled {
		t.Fatalf("focus mode should be enabled during playback")
	}
	if health.CurrentTorrentID != "t1" {
		t.Fatalf("current torrent = %q, want t1", health.CurrentTorrentID)
	}

	// Unfocus after stopping playback
	unfocusReq := httptest.NewRequest(http.MethodPost, "/torrents/unfocus", nil)
	unfocusW := httptest.NewRecorder()
	server.ServeHTTP(unfocusW, unfocusReq)

	if unfocusW.Code != http.StatusNoContent {
		t.Fatalf("unfocus: status = %d", unfocusW.Code)
	}
	if player.current != "" {
		t.Fatalf("unfocus did not clear: %q", player.current)
	}
}

// TestE2ESearchResultContractMatchesCreateInput validates that the JSON
// contract between search results (torrent-search) and torrent creation
// (torrent-engine) is compatible. A search result's magnet/name fields
// map directly to the create torrent input.
func TestE2ESearchResultContractMatchesCreateInput(t *testing.T) {
	// Simulate a search result from torrent-search with typical fields
	searchResult := map[string]interface{}{
		"name":      "Sintel.2010.1080p.BluRay.x264",
		"infoHash":  "abc123def456789abc123def456789abc123def4",
		"magnet":    "magnet:?xt=urn:btih:abc123def456789abc123def456789abc123def4&dn=Sintel.2010.1080p.BluRay.x264",
		"sizeBytes": 1500000000,
		"seeders":   42,
		"leechers":  3,
		"source":    "bittorrent",
	}

	// Extract magnet and name (as the frontend does)
	magnet, ok := searchResult["magnet"].(string)
	if !ok || magnet == "" {
		t.Fatalf("search result should have a magnet link")
	}
	name, ok := searchResult["name"].(string)
	if !ok || name == "" {
		t.Fatalf("search result should have a name")
	}

	// Build the create payload (as api.ts createTorrentFromMagnet does)
	createPayload := map[string]string{
		"magnet": magnet,
		"name":   name,
	}
	body, _ := json.Marshal(createPayload)

	// Verify the engine accepts it
	create := &fakeCreateTorrent{result: domain.TorrentRecord{
		ID:     "new-torrent",
		Name:   name,
		Status: domain.TorrentActive,
	}}
	server := NewServer(create)

	req := httptest.NewRequest(http.MethodPost, "/torrents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if create.input.Source.Magnet != magnet {
		t.Fatalf("magnet mismatch: got %q", create.input.Source.Magnet)
	}
	if create.input.Name != name {
		t.Fatalf("name mismatch: got %q", create.input.Name)
	}
}

// fakeCreateForE2E wraps fakeCreateTorrent but tracks sequential calls
// to verify the full flow uses a single torrent ID consistently.
type fakeGetStateSequence struct {
	states []domain.SessionState
	index  int
}

func (f *fakeGetStateSequence) Execute(ctx context.Context, id domain.TorrentID) (domain.SessionState, error) {
	if f.index >= len(f.states) {
		return f.states[len(f.states)-1], nil
	}
	s := f.states[f.index]
	f.index++
	return s, nil
}

// TestE2EProgressUpdatesWhileStreaming validates that the torrent state
// shows increasing progress, simulating the download progressing while
// the user watches.
func TestE2EProgressUpdatesWhileStreaming(t *testing.T) {
	now := time.Date(2026, 2, 20, 12, 0, 0, 0, time.UTC)
	states := &fakeGetStateSequence{
		states: []domain.SessionState{
			{ID: "t1", Status: domain.TorrentActive, Progress: 0.02, DownloadSpeed: 5_000_000, Peers: 10, UpdatedAt: now},
			{ID: "t1", Status: domain.TorrentActive, Progress: 0.15, DownloadSpeed: 8_000_000, Peers: 15, UpdatedAt: now.Add(10 * time.Second)},
			{ID: "t1", Status: domain.TorrentActive, Progress: 0.40, DownloadSpeed: 12_000_000, Peers: 20, UpdatedAt: now.Add(30 * time.Second)},
		},
	}

	server := NewServer(&fakeCreateTorrent{}, WithGetTorrentState(states))

	var prevProgress float64
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/torrents/t1/state", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("poll %d: status = %d", i, w.Code)
		}

		var got domain.SessionState
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("poll %d: decode: %v", i, err)
		}
		if got.Progress < prevProgress {
			t.Fatalf("poll %d: progress decreased from %.2f to %.2f", i, prevProgress, got.Progress)
		}
		prevProgress = got.Progress
	}

	if prevProgress < 0.30 {
		t.Fatalf("final progress = %.2f, expected at least 0.30", prevProgress)
	}
}
