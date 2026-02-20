package apihttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"torrentstream/internal/domain"
)

// ---- fake watch history store ----

type fakeWatchHistoryStore struct {
	positions map[string]domain.WatchPosition // keyed by "torrentId:fileIndex"
	listErr   error
	getErr    error
	upsertErr error
}

func newFakeWatchHistoryStore() *fakeWatchHistoryStore {
	return &fakeWatchHistoryStore{
		positions: make(map[string]domain.WatchPosition),
	}
}

func (f *fakeWatchHistoryStore) posKey(id domain.TorrentID, idx int) string {
	return fmt.Sprintf("%s:%d", string(id), idx)
}

func (f *fakeWatchHistoryStore) Upsert(_ context.Context, wp domain.WatchPosition) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	k := f.posKey(wp.TorrentID, wp.FileIndex)
	f.positions[k] = wp
	return nil
}

func (f *fakeWatchHistoryStore) Get(_ context.Context, id domain.TorrentID, idx int) (domain.WatchPosition, error) {
	if f.getErr != nil {
		return domain.WatchPosition{}, f.getErr
	}
	k := f.posKey(id, idx)
	pos, ok := f.positions[k]
	if !ok {
		return domain.WatchPosition{}, domain.ErrNotFound
	}
	return pos, nil
}

func (f *fakeWatchHistoryStore) ListRecent(_ context.Context, limit int) ([]domain.WatchPosition, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	result := make([]domain.WatchPosition, 0, len(f.positions))
	for _, p := range f.positions {
		result = append(result, p)
	}
	if limit > 0 && limit < len(result) {
		result = result[:limit]
	}
	return result, nil
}

// ---- helpers ----

func makeHistoryServer(store WatchHistoryStore) *Server {
	var opts []ServerOption
	if store != nil {
		opts = append(opts, WithWatchHistory(store))
	}
	return NewServer(nil, opts...)
}

func doHistoryRequest(s *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

// ---- Tests: GET /watch-history (list) ----

func TestListWatchHistory_ReturnsPositions(t *testing.T) {
	store := newFakeWatchHistoryStore()
	store.positions["abc:0"] = domain.WatchPosition{
		TorrentID: "abc", FileIndex: 0, Position: 120.5, Duration: 3600, TorrentName: "Movie", FilePath: "movie.mp4",
	}
	store.positions["def:1"] = domain.WatchPosition{
		TorrentID: "def", FileIndex: 1, Position: 60.0, Duration: 1800, TorrentName: "Show", FilePath: "show.mkv",
	}
	s := makeHistoryServer(store)

	rec := doHistoryRequest(s, http.MethodGet, "/watch-history", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var positions []domain.WatchPosition
	if err := json.NewDecoder(rec.Body).Decode(&positions); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(positions) != 2 {
		t.Fatalf("expected 2 positions, got %d", len(positions))
	}
}

func TestListWatchHistory_EmptyReturnsEmptyArray(t *testing.T) {
	store := newFakeWatchHistoryStore()
	s := makeHistoryServer(store)

	rec := doHistoryRequest(s, http.MethodGet, "/watch-history", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var positions []domain.WatchPosition
	if err := json.NewDecoder(rec.Body).Decode(&positions); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(positions) != 0 {
		t.Fatalf("expected 0 positions, got %d", len(positions))
	}
}

func TestListWatchHistory_RespectsLimit(t *testing.T) {
	store := newFakeWatchHistoryStore()
	for i := 0; i < 10; i++ {
		k := fmt.Sprintf("id%d:0", i)
		store.positions[k] = domain.WatchPosition{
			TorrentID: domain.TorrentID(fmt.Sprintf("id%d", i)),
			FileIndex: 0, Position: float64(i * 10),
		}
	}
	s := makeHistoryServer(store)

	rec := doHistoryRequest(s, http.MethodGet, "/watch-history?limit=3", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var positions []domain.WatchPosition
	if err := json.NewDecoder(rec.Body).Decode(&positions); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(positions) != 3 {
		t.Fatalf("expected 3 positions, got %d", len(positions))
	}
}

func TestListWatchHistory_DefaultLimitIs20(t *testing.T) {
	store := newFakeWatchHistoryStore()
	for i := 0; i < 25; i++ {
		k := fmt.Sprintf("id%d:0", i)
		store.positions[k] = domain.WatchPosition{
			TorrentID: domain.TorrentID(fmt.Sprintf("id%d", i)),
			FileIndex: 0, Position: float64(i),
		}
	}
	s := makeHistoryServer(store)

	rec := doHistoryRequest(s, http.MethodGet, "/watch-history", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var positions []domain.WatchPosition
	if err := json.NewDecoder(rec.Body).Decode(&positions); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(positions) != 20 {
		t.Fatalf("expected 20 (default limit), got %d", len(positions))
	}
}

func TestListWatchHistory_InvalidLimit(t *testing.T) {
	store := newFakeWatchHistoryStore()
	s := makeHistoryServer(store)

	rec := doHistoryRequest(s, http.MethodGet, "/watch-history?limit=abc", nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestListWatchHistory_NotConfigured(t *testing.T) {
	s := makeHistoryServer(nil)

	rec := doHistoryRequest(s, http.MethodGet, "/watch-history", nil)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rec.Code)
	}
}

func TestListWatchHistory_MethodNotAllowed(t *testing.T) {
	store := newFakeWatchHistoryStore()
	s := makeHistoryServer(store)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rec := doHistoryRequest(s, method, "/watch-history", nil)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: expected 405, got %d", method, rec.Code)
		}
	}
}

func TestListWatchHistory_StoreError(t *testing.T) {
	store := newFakeWatchHistoryStore()
	store.listErr = errors.New("db down")
	s := makeHistoryServer(store)

	rec := doHistoryRequest(s, http.MethodGet, "/watch-history", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// ---- Tests: GET /watch-history/{torrentId}/{fileIndex} ----

func TestGetWatchPosition_Found(t *testing.T) {
	store := newFakeWatchHistoryStore()
	store.positions["abc:0"] = domain.WatchPosition{
		TorrentID: "abc", FileIndex: 0, Position: 120.5, Duration: 3600, TorrentName: "Movie", FilePath: "movie.mp4",
	}
	s := makeHistoryServer(store)

	rec := doHistoryRequest(s, http.MethodGet, "/watch-history/abc/0", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var pos domain.WatchPosition
	if err := json.NewDecoder(rec.Body).Decode(&pos); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pos.TorrentID != "abc" {
		t.Errorf("expected torrentId 'abc', got %q", pos.TorrentID)
	}
	if pos.Position != 120.5 {
		t.Errorf("expected position 120.5, got %f", pos.Position)
	}
	if pos.Duration != 3600 {
		t.Errorf("expected duration 3600, got %f", pos.Duration)
	}
	if pos.TorrentName != "Movie" {
		t.Errorf("expected torrentName 'Movie', got %q", pos.TorrentName)
	}
}

func TestGetWatchPosition_NotFound(t *testing.T) {
	store := newFakeWatchHistoryStore()
	s := makeHistoryServer(store)

	rec := doHistoryRequest(s, http.MethodGet, "/watch-history/abc/0", nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestGetWatchPosition_InvalidFileIndex(t *testing.T) {
	store := newFakeWatchHistoryStore()
	s := makeHistoryServer(store)

	rec := doHistoryRequest(s, http.MethodGet, "/watch-history/abc/notanumber", nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestGetWatchPosition_NegativeFileIndex(t *testing.T) {
	store := newFakeWatchHistoryStore()
	s := makeHistoryServer(store)

	rec := doHistoryRequest(s, http.MethodGet, "/watch-history/abc/-1", nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestGetWatchPosition_MissingParts(t *testing.T) {
	store := newFakeWatchHistoryStore()
	s := makeHistoryServer(store)

	tests := []struct {
		name string
		path string
	}{
		{"no fileIndex", "/watch-history/abc"},
		{"empty torrentId", "/watch-history//0"},
		{"empty fileIndex", "/watch-history/abc/"},
		{"just slash", "/watch-history/"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doHistoryRequest(s, http.MethodGet, tc.path, nil)
			if rec.Code == http.StatusOK {
				t.Errorf("path %q: expected non-200, got 200", tc.path)
			}
		})
	}
}

func TestGetWatchPosition_NotConfigured(t *testing.T) {
	s := makeHistoryServer(nil)

	rec := doHistoryRequest(s, http.MethodGet, "/watch-history/abc/0", nil)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rec.Code)
	}
}

func TestGetWatchPosition_StoreError(t *testing.T) {
	store := newFakeWatchHistoryStore()
	store.getErr = errors.New("db down")
	s := makeHistoryServer(store)

	rec := doHistoryRequest(s, http.MethodGet, "/watch-history/abc/0", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// ---- Tests: PUT /watch-history/{torrentId}/{fileIndex} ----

func TestPutWatchPosition_Success(t *testing.T) {
	store := newFakeWatchHistoryStore()
	s := makeHistoryServer(store)

	body, _ := json.Marshal(map[string]interface{}{
		"position":    120.5,
		"duration":    3600.0,
		"torrentName": "Movie",
		"filePath":    "movie.mp4",
	})

	rec := doHistoryRequest(s, http.MethodPut, "/watch-history/abc/0", body)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	pos, ok := store.positions["abc:0"]
	if !ok {
		t.Fatal("position not stored")
	}
	if pos.TorrentID != "abc" {
		t.Errorf("expected torrentId 'abc', got %q", pos.TorrentID)
	}
	if pos.FileIndex != 0 {
		t.Errorf("expected fileIndex 0, got %d", pos.FileIndex)
	}
	if pos.Position != 120.5 {
		t.Errorf("expected position 120.5, got %f", pos.Position)
	}
	if pos.Duration != 3600.0 {
		t.Errorf("expected duration 3600.0, got %f", pos.Duration)
	}
	if pos.TorrentName != "Movie" {
		t.Errorf("expected torrentName 'Movie', got %q", pos.TorrentName)
	}
	if pos.FilePath != "movie.mp4" {
		t.Errorf("expected filePath 'movie.mp4', got %q", pos.FilePath)
	}
}

func TestPutWatchPosition_Upsert(t *testing.T) {
	store := newFakeWatchHistoryStore()
	s := makeHistoryServer(store)

	body1, _ := json.Marshal(map[string]interface{}{
		"position": 60.0, "duration": 3600.0, "torrentName": "Movie", "filePath": "movie.mp4",
	})
	rec := doHistoryRequest(s, http.MethodPut, "/watch-history/abc/0", body1)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first put: expected 204, got %d", rec.Code)
	}

	body2, _ := json.Marshal(map[string]interface{}{
		"position": 300.0, "duration": 3600.0, "torrentName": "Movie", "filePath": "movie.mp4",
	})
	rec = doHistoryRequest(s, http.MethodPut, "/watch-history/abc/0", body2)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("second put: expected 204, got %d", rec.Code)
	}

	if len(store.positions) != 1 {
		t.Fatalf("expected 1 entry after upsert, got %d", len(store.positions))
	}
	pos := store.positions["abc:0"]
	if pos.Position != 300.0 {
		t.Errorf("expected position 300.0 after upsert, got %f", pos.Position)
	}
}

func TestPutWatchPosition_InvalidJSON(t *testing.T) {
	store := newFakeWatchHistoryStore()
	s := makeHistoryServer(store)

	rec := doHistoryRequest(s, http.MethodPut, "/watch-history/abc/0", []byte("not json"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestPutWatchPosition_StoreError(t *testing.T) {
	store := newFakeWatchHistoryStore()
	store.upsertErr = errors.New("db error")
	s := makeHistoryServer(store)

	body, _ := json.Marshal(map[string]interface{}{
		"position": 60.0, "duration": 3600.0, "torrentName": "Movie", "filePath": "movie.mp4",
	})

	rec := doHistoryRequest(s, http.MethodPut, "/watch-history/abc/0", body)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestPutWatchPosition_NotConfigured(t *testing.T) {
	s := makeHistoryServer(nil)

	body, _ := json.Marshal(map[string]interface{}{"position": 60.0})
	rec := doHistoryRequest(s, http.MethodPut, "/watch-history/abc/0", body)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rec.Code)
	}
}

func TestWatchHistoryByID_MethodNotAllowed(t *testing.T) {
	store := newFakeWatchHistoryStore()
	s := makeHistoryServer(store)

	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPatch} {
		rec := doHistoryRequest(s, method, "/watch-history/abc/0", nil)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: expected 405, got %d", method, rec.Code)
		}
	}
}

// ---- Tests: PUT then GET roundtrip ----

func TestWatchPosition_PutThenGet_Roundtrip(t *testing.T) {
	store := newFakeWatchHistoryStore()
	s := makeHistoryServer(store)

	body, _ := json.Marshal(map[string]interface{}{
		"position":    542.3,
		"duration":    7200.0,
		"torrentName": "Big Movie",
		"filePath":    "big-movie.mkv",
	})
	rec := doHistoryRequest(s, http.MethodPut, "/watch-history/xyz/2", body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("put: expected 204, got %d", rec.Code)
	}

	rec = doHistoryRequest(s, http.MethodGet, "/watch-history/xyz/2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", rec.Code)
	}

	var pos domain.WatchPosition
	if err := json.NewDecoder(rec.Body).Decode(&pos); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pos.TorrentID != "xyz" {
		t.Errorf("torrentId: expected 'xyz', got %q", pos.TorrentID)
	}
	if pos.FileIndex != 2 {
		t.Errorf("fileIndex: expected 2, got %d", pos.FileIndex)
	}
	if pos.Position != 542.3 {
		t.Errorf("position: expected 542.3, got %f", pos.Position)
	}
	if pos.Duration != 7200.0 {
		t.Errorf("duration: expected 7200.0, got %f", pos.Duration)
	}
	if pos.TorrentName != "Big Movie" {
		t.Errorf("torrentName: expected 'Big Movie', got %q", pos.TorrentName)
	}
	if pos.FilePath != "big-movie.mkv" {
		t.Errorf("filePath: expected 'big-movie.mkv', got %q", pos.FilePath)
	}
}

// ---- Tests: PUT then List ----

func TestWatchPosition_PutMultiple_ThenList(t *testing.T) {
	store := newFakeWatchHistoryStore()
	s := makeHistoryServer(store)

	entries := []struct {
		torrentID string
		fileIndex string
		position  float64
	}{
		{"aaa", "0", 10.0},
		{"bbb", "1", 20.0},
		{"ccc", "0", 30.0},
	}

	for _, e := range entries {
		body, _ := json.Marshal(map[string]interface{}{
			"position":    e.position,
			"duration":    100.0,
			"torrentName": "Test",
			"filePath":    "test.mp4",
		})
		rec := doHistoryRequest(s, http.MethodPut, "/watch-history/"+e.torrentID+"/"+e.fileIndex, body)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("put %s/%s: expected 204, got %d", e.torrentID, e.fileIndex, rec.Code)
		}
	}

	rec := doHistoryRequest(s, http.MethodGet, "/watch-history", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", rec.Code)
	}

	var positions []domain.WatchPosition
	if err := json.NewDecoder(rec.Body).Decode(&positions); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(positions) != 3 {
		t.Fatalf("expected 3 positions, got %d", len(positions))
	}
}
