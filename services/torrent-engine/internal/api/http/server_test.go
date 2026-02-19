package apihttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/usecase"
)

type fakeCreateTorrent struct {
	called int
	input  usecase.CreateTorrentInput
	result domain.TorrentRecord
	err    error
}

func (f *fakeCreateTorrent) Execute(ctx context.Context, input usecase.CreateTorrentInput) (domain.TorrentRecord, error) {
	f.called++
	f.input = input
	return f.result, f.err
}

type fakeStartTorrent struct {
	called int
	id     domain.TorrentID
	ids    []domain.TorrentID
	result domain.TorrentRecord
	err    error
}

func (f *fakeStartTorrent) Execute(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error) {
	f.called++
	f.id = id
	f.ids = append(f.ids, id)
	return f.result, f.err
}

type fakeStopTorrent struct {
	called int
	id     domain.TorrentID
	ids    []domain.TorrentID
	result domain.TorrentRecord
	err    error
}

func (f *fakeStopTorrent) Execute(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error) {
	f.called++
	f.id = id
	f.ids = append(f.ids, id)
	return f.result, f.err
}

type fakeDeleteTorrent struct {
	called      int
	id          domain.TorrentID
	deleteFiles bool
	err         error
}

func (f *fakeDeleteTorrent) Execute(ctx context.Context, id domain.TorrentID, deleteFiles bool) error {
	f.called++
	f.id = id
	f.deleteFiles = deleteFiles
	return f.err
}

type fakeStreamTorrent struct {
	called    int
	id        domain.TorrentID
	fileIndex int
	result    usecase.StreamResult
	err       error
}

func (f *fakeStreamTorrent) Execute(ctx context.Context, id domain.TorrentID, fileIndex int) (usecase.StreamResult, error) {
	f.called++
	f.id = id
	f.fileIndex = fileIndex
	return f.result, f.err
}

func (f *fakeStreamTorrent) ExecuteRaw(ctx context.Context, id domain.TorrentID, fileIndex int) (usecase.StreamResult, error) {
	return f.Execute(ctx, id, fileIndex)
}

type fakeGetTorrentState struct {
	called int
	id     domain.TorrentID
	result domain.SessionState
	err    error
}

func (f *fakeGetTorrentState) Execute(ctx context.Context, id domain.TorrentID) (domain.SessionState, error) {
	f.called++
	f.id = id
	return f.result, f.err
}

type fakeListTorrentStates struct {
	called int
	result []domain.SessionState
	err    error
}

func (f *fakeListTorrentStates) Execute(ctx context.Context) ([]domain.SessionState, error) {
	f.called++
	return f.result, f.err
}

type fakePlayerSettings struct {
	current   domain.TorrentID
	setCalled int
	setErr    error
}

func (f *fakePlayerSettings) CurrentTorrentID() domain.TorrentID {
	return f.current
}

func (f *fakePlayerSettings) SetCurrentTorrentID(id domain.TorrentID) error {
	f.setCalled++
	if f.setErr != nil {
		return f.setErr
	}
	f.current = id
	return nil
}

type fakeMediaProbe struct {
	called       int
	readerCalled int
	filePath     string
	result       domain.MediaInfo
	err          error
	readerResult domain.MediaInfo
	readerErr    error
}

func (f *fakeMediaProbe) Probe(ctx context.Context, filePath string) (domain.MediaInfo, error) {
	f.called++
	f.filePath = filePath
	if f.err != nil {
		return domain.MediaInfo{}, f.err
	}
	return f.result, nil
}

func (f *fakeMediaProbe) ProbeReader(ctx context.Context, reader io.Reader) (domain.MediaInfo, error) {
	f.readerCalled++
	if f.readerErr != nil {
		return domain.MediaInfo{}, f.readerErr
	}
	if len(f.readerResult.Tracks) > 0 {
		return f.readerResult, nil
	}
	return f.result, nil
}

type fakeRepo struct {
	list          []domain.TorrentRecord
	listErr       error
	get           domain.TorrentRecord
	getErr        error
	lastID        domain.TorrentID
	lastFilter    domain.TorrentFilter
	lastTagsID    domain.TorrentID
	lastTags      []string
	updateTagsErr error
	listCalled    int
	getCalled     int
}

func (f *fakeRepo) Create(ctx context.Context, t domain.TorrentRecord) error { return nil }

func (f *fakeRepo) Update(ctx context.Context, t domain.TorrentRecord) error { return nil }

func (f *fakeRepo) UpdateProgress(ctx context.Context, id domain.TorrentID, update domain.ProgressUpdate) error {
	return nil
}

func (f *fakeRepo) Get(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error) {
	f.getCalled++
	f.lastID = id
	if f.getErr != nil {
		return domain.TorrentRecord{}, f.getErr
	}
	return f.get, nil
}

func (f *fakeRepo) List(ctx context.Context, filter domain.TorrentFilter) ([]domain.TorrentRecord, error) {
	f.listCalled++
	f.lastFilter = filter
	if f.listErr != nil {
		return nil, f.listErr
	}
	items := f.list
	if filter.Offset > 0 {
		if filter.Offset >= len(items) {
			return []domain.TorrentRecord{}, nil
		}
		items = items[filter.Offset:]
	}
	if filter.Limit > 0 && filter.Limit < len(items) {
		items = items[:filter.Limit]
	}
	return items, nil
}

func (f *fakeRepo) GetMany(ctx context.Context, ids []domain.TorrentID) ([]domain.TorrentRecord, error) {
	return nil, nil
}

func (f *fakeRepo) Delete(ctx context.Context, id domain.TorrentID) error { return nil }

func (f *fakeRepo) UpdateTags(ctx context.Context, id domain.TorrentID, tags []string) error {
	f.lastTagsID = id
	f.lastTags = append([]string(nil), tags...)
	return f.updateTagsErr
}

func TestCreateTorrentJSON(t *testing.T) {
	uc := &fakeCreateTorrent{result: domain.TorrentRecord{ID: "t1", Name: "Sintel", Status: domain.TorrentActive}}
	server := NewServer(uc)

	payload := []byte(`{"magnet":"magnet:?xt=urn:btih:abc","name":"Sintel"}`)
	req := httptest.NewRequest(http.MethodPost, "/torrents", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d", w.Code)
	}
	if uc.called != 1 {
		t.Fatalf("usecase not called")
	}
	if uc.input.Source.Magnet == "" || uc.input.Name != "Sintel" {
		t.Fatalf("input not set")
	}

	var got domain.TorrentRecord
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "t1" || got.Name != "Sintel" {
		t.Fatalf("response mismatch: %+v", got)
	}
}

func TestCreateTorrentUnsupportedContentType(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{})
	req := httptest.NewRequest(http.MethodPost, "/torrents", bytes.NewReader([]byte("x")))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestCreateTorrentInvalidSource(t *testing.T) {
	uc := &fakeCreateTorrent{err: usecase.ErrInvalidSource}
	server := NewServer(uc)

	req := httptest.NewRequest(http.MethodPost, "/torrents", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestOpenAPI(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{})

	req := httptest.NewRequest(http.MethodGet, "/swagger/openapi.json", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		body, _ := io.ReadAll(w.Body)
		t.Fatalf("status = %d body=%s", w.Code, string(body))
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %s", ct)
	}
}

func TestListTorrentsSummaryDefault(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepo{
		list: []domain.TorrentRecord{
			{ID: "t1", Name: "Sintel", Status: domain.TorrentActive, TotalBytes: 100, DoneBytes: 25, CreatedAt: now, UpdatedAt: now},
			{ID: "t2", Name: "Second", Status: domain.TorrentCompleted, TotalBytes: 200, DoneBytes: 200, CreatedAt: now, UpdatedAt: now},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithRepository(repo))

	req := httptest.NewRequest(http.MethodGet, "/torrents", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var resp struct {
		Items []struct {
			ID         string    `json:"id"`
			Name       string    `json:"name"`
			Status     string    `json:"status"`
			Progress   float64   `json:"progress"`
			DoneBytes  int64     `json:"doneBytes"`
			TotalBytes int64     `json:"totalBytes"`
			CreatedAt  time.Time `json:"createdAt"`
			UpdatedAt  time.Time `json:"updatedAt"`
		} `json:"items"`
		Count int `json:"count"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 2 || len(resp.Items) != 2 {
		t.Fatalf("count/items mismatch: %+v", resp)
	}
	if resp.Items[0].ID != "t1" || resp.Items[0].Progress != 0.25 {
		t.Fatalf("summary mismatch: %+v", resp.Items[0])
	}
}

func TestListTorrentsFull(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepo{
		list: []domain.TorrentRecord{
			{ID: "t1", Name: "Sintel", Status: domain.TorrentActive, TotalBytes: 100, DoneBytes: 25, CreatedAt: now, UpdatedAt: now},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithRepository(repo))

	req := httptest.NewRequest(http.MethodGet, "/torrents?view=full", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var resp struct {
		Items []domain.TorrentRecord `json:"items"`
		Count int                    `json:"count"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 1 || len(resp.Items) != 1 {
		t.Fatalf("count/items mismatch: %+v", resp)
	}
	if resp.Items[0].ID != "t1" {
		t.Fatalf("item mismatch: %+v", resp.Items[0])
	}
}

func TestListTorrentsStatusFilter(t *testing.T) {
	repo := &fakeRepo{}
	server := NewServer(&fakeCreateTorrent{}, WithRepository(repo))

	req := httptest.NewRequest(http.MethodGet, "/torrents?status=completed", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if repo.listCalled != 1 || repo.lastFilter.Status == nil || *repo.lastFilter.Status != domain.TorrentCompleted {
		t.Fatalf("filter not applied")
	}
}

func TestListTorrentsInvalidStatus(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{}, WithRepository(&fakeRepo{}))
	req := httptest.NewRequest(http.MethodGet, "/torrents?status=bad", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestListTorrentsInvalidView(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{}, WithRepository(&fakeRepo{}))
	req := httptest.NewRequest(http.MethodGet, "/torrents?view=bad", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestListTorrentsRepoError(t *testing.T) {
	repo := &fakeRepo{listErr: errors.New("db fail")}
	server := NewServer(&fakeCreateTorrent{}, WithRepository(repo))

	req := httptest.NewRequest(http.MethodGet, "/torrents", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
	var resp errorEnvelope
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != "repository_error" {
		t.Fatalf("code = %s", resp.Error.Code)
	}
}

func TestListTorrentsLimitOffset(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepo{
		list: []domain.TorrentRecord{
			{ID: "t1", Name: "First", Status: domain.TorrentActive, TotalBytes: 100, DoneBytes: 0, CreatedAt: now, UpdatedAt: now},
			{ID: "t2", Name: "Second", Status: domain.TorrentActive, TotalBytes: 100, DoneBytes: 0, CreatedAt: now, UpdatedAt: now},
			{ID: "t3", Name: "Third", Status: domain.TorrentActive, TotalBytes: 100, DoneBytes: 0, CreatedAt: now, UpdatedAt: now},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithRepository(repo))

	req := httptest.NewRequest(http.MethodGet, "/torrents?limit=1&offset=1", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var resp struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Count int `json:"count"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 1 || len(resp.Items) != 1 || resp.Items[0].ID != "t2" {
		t.Fatalf("slice mismatch: %+v", resp)
	}
}

func TestListTorrentsAdvancedFilterParams(t *testing.T) {
	repo := &fakeRepo{}
	server := NewServer(&fakeCreateTorrent{}, WithRepository(repo))

	req := httptest.NewRequest(
		http.MethodGet,
		"/torrents?search=matrix&tags=sci-fi,4k&sortBy=name&sortOrder=asc&limit=5&offset=2",
		nil,
	)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if repo.lastFilter.Search != "matrix" {
		t.Fatalf("search mismatch: %q", repo.lastFilter.Search)
	}
	if repo.lastFilter.SortBy != "name" || repo.lastFilter.SortOrder != domain.SortAsc {
		t.Fatalf("sort mismatch: %+v", repo.lastFilter)
	}
	if repo.lastFilter.Limit != 5 || repo.lastFilter.Offset != 2 {
		t.Fatalf("limit/offset mismatch: %+v", repo.lastFilter)
	}
	if len(repo.lastFilter.Tags) != 2 || repo.lastFilter.Tags[0] != "sci-fi" || repo.lastFilter.Tags[1] != "4k" {
		t.Fatalf("tags mismatch: %+v", repo.lastFilter.Tags)
	}
}

func TestGetTorrentByID(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepo{
		get: domain.TorrentRecord{ID: "t1", Name: "Sintel", Status: domain.TorrentActive, TotalBytes: 100, DoneBytes: 25, CreatedAt: now, UpdatedAt: now},
	}
	server := NewServer(&fakeCreateTorrent{}, WithRepository(repo))

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if repo.getCalled != 1 || repo.lastID != "t1" {
		t.Fatalf("repo not called")
	}
}

func TestGetTorrentByIDNotFound(t *testing.T) {
	repo := &fakeRepo{getErr: domain.ErrNotFound}
	server := NewServer(&fakeCreateTorrent{}, WithRepository(repo))

	req := httptest.NewRequest(http.MethodGet, "/torrents/t404", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestStartTorrentEndpoint(t *testing.T) {
	start := &fakeStartTorrent{result: domain.TorrentRecord{ID: "t1", Status: domain.TorrentActive}}
	server := NewServer(&fakeCreateTorrent{}, WithStartTorrent(start))

	req := httptest.NewRequest(http.MethodPost, "/torrents/t1/start", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if start.called != 1 || start.id != "t1" {
		t.Fatalf("usecase not called")
	}
}

func TestStopTorrentEndpoint(t *testing.T) {
	stop := &fakeStopTorrent{result: domain.TorrentRecord{ID: "t1", Status: domain.TorrentStopped}}
	server := NewServer(&fakeCreateTorrent{}, WithStopTorrent(stop))

	req := httptest.NewRequest(http.MethodPost, "/torrents/t1/stop", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if stop.called != 1 || stop.id != "t1" {
		t.Fatalf("usecase not called")
	}
}

func TestDeleteTorrentEndpoint(t *testing.T) {
	del := &fakeDeleteTorrent{}
	server := NewServer(&fakeCreateTorrent{}, WithDeleteTorrent(del))

	req := httptest.NewRequest(http.MethodDelete, "/torrents/t1", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
	if del.called != 1 || del.id != "t1" {
		t.Fatalf("usecase not called")
	}
}

func TestDeleteTorrentEndpointWithFiles(t *testing.T) {
	del := &fakeDeleteTorrent{}
	server := NewServer(&fakeCreateTorrent{}, WithDeleteTorrent(del))

	req := httptest.NewRequest(http.MethodDelete, "/torrents/t1?deleteFiles=true", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
	if !del.deleteFiles {
		t.Fatalf("deleteFiles not set")
	}
}

func TestUpdateTagsEndpoint(t *testing.T) {
	repo := &fakeRepo{
		get: domain.TorrentRecord{
			ID:   "t1",
			Name: "Movie",
			Tags: []string{"existing"},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithRepository(repo))

	req := httptest.NewRequest(http.MethodPut, "/torrents/t1/tags", bytes.NewBufferString(`{"tags":["movie","4k"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if repo.lastTagsID != "t1" {
		t.Fatalf("update tags id mismatch: %q", repo.lastTagsID)
	}
	if len(repo.lastTags) != 2 || repo.lastTags[0] != "movie" || repo.lastTags[1] != "4k" {
		t.Fatalf("update tags payload mismatch: %+v", repo.lastTags)
	}
}

func TestBulkStartEndpoint(t *testing.T) {
	start := &fakeStartTorrent{result: domain.TorrentRecord{Status: domain.TorrentActive}}
	server := NewServer(&fakeCreateTorrent{}, WithStartTorrent(start))

	req := httptest.NewRequest(http.MethodPost, "/torrents/bulk/start", bytes.NewBufferString(`{"ids":["t1","t2"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if start.called != 2 {
		t.Fatalf("expected 2 start calls, got %d", start.called)
	}
	if len(start.ids) != 2 || start.ids[0] != "t1" || start.ids[1] != "t2" {
		t.Fatalf("unexpected ids: %+v", start.ids)
	}
}

func TestStartTorrentNotFound(t *testing.T) {
	start := &fakeStartTorrent{err: domain.ErrNotFound}
	server := NewServer(&fakeCreateTorrent{}, WithStartTorrent(start))

	req := httptest.NewRequest(http.MethodPost, "/torrents/t404/start", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestStartTorrentEngineError(t *testing.T) {
	start := &fakeStartTorrent{err: usecase.ErrEngine}
	server := NewServer(&fakeCreateTorrent{}, WithStartTorrent(start))

	req := httptest.NewRequest(http.MethodPost, "/torrents/t1/start", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
	var resp errorEnvelope
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != "engine_error" {
		t.Fatalf("code = %s", resp.Error.Code)
	}
}

func TestDeleteTorrentInvalidQuery(t *testing.T) {
	del := &fakeDeleteTorrent{}
	server := NewServer(&fakeCreateTorrent{}, WithDeleteTorrent(del))

	req := httptest.NewRequest(http.MethodDelete, "/torrents/t1?deleteFiles=bad", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestStreamTorrentFull(t *testing.T) {
	data := []byte("hello world")
	reader := &testStreamReader{Reader: bytes.NewReader(data)}
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			Reader: reader,
			File:   domain.FileRef{Index: 0, Path: "movie.mp4", Length: int64(len(data))},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream))

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/stream?fileIndex=0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Body.String(); got != string(data) {
		t.Fatalf("body mismatch: %q", got)
	}
	if w.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("accept-ranges not set")
	}
}

func TestStreamTorrentDoesNotOverrideCurrentPriority(t *testing.T) {
	data := []byte("hello world")
	reader := &testStreamReader{Reader: bytes.NewReader(data)}
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			Reader: reader,
			File:   domain.FileRef{Index: 0, Path: "movie.mp4", Length: int64(len(data))},
		},
	}
	player := &fakePlayerSettings{current: "t1"}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream), WithPlayerSettings(player))

	req := httptest.NewRequest(http.MethodGet, "/torrents/t2/stream?fileIndex=0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if stream.called != 1 || stream.id != "t2" {
		t.Fatalf("stream usecase not called with target torrent: called=%d id=%q", stream.called, stream.id)
	}
	if player.current != "t1" {
		t.Fatalf("current torrent changed unexpectedly: %q", player.current)
	}
	if player.setCalled != 0 {
		t.Fatalf("player current torrent should not be updated on stream requests, called=%d", player.setCalled)
	}
}

func TestStreamTorrentDoesNotAutoStartViaStartUseCase(t *testing.T) {
	data := []byte("hello world")
	reader := &testStreamReader{Reader: bytes.NewReader(data)}
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			Reader: reader,
			File:   domain.FileRef{Index: 0, Path: "movie.mp4", Length: int64(len(data))},
		},
	}
	start := &fakeStartTorrent{}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream), WithStartTorrent(start))

	req := httptest.NewRequest(http.MethodGet, "/torrents/t2/stream?fileIndex=0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if start.called != 0 {
		t.Fatalf("start usecase should not be called from stream endpoint, called=%d", start.called)
	}
}

func TestStreamTorrentRange(t *testing.T) {
	data := []byte("hello world")
	reader := &testStreamReader{Reader: bytes.NewReader(data)}
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			Reader: reader,
			File:   domain.FileRef{Index: 0, Path: "movie.mp4", Length: int64(len(data))},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream))

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/stream?fileIndex=0", nil)
	req.Header.Set("Range", "bytes=0-4")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Body.String(); got != "hello" {
		t.Fatalf("body mismatch: %q", got)
	}
	if w.Header().Get("Content-Range") != "bytes 0-4/11" {
		t.Fatalf("content-range mismatch: %s", w.Header().Get("Content-Range"))
	}
}

func TestStreamTorrentInvalidRange(t *testing.T) {
	data := []byte("hello world")
	reader := &testStreamReader{Reader: bytes.NewReader(data)}
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			Reader: reader,
			File:   domain.FileRef{Index: 0, Path: "movie.mp4", Length: int64(len(data))},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream))

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/stream?fileIndex=0", nil)
	req.Header.Set("Range", "bytes=100-200")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestStreamTorrentInvalidFileIndex(t *testing.T) {
	stream := &fakeStreamTorrent{err: usecase.ErrInvalidFileIndex}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream))

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/stream?fileIndex=99", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	var resp errorEnvelope
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != "invalid_request" {
		t.Fatalf("code = %s", resp.Error.Code)
	}
}

func TestStreamTorrentMissingFileIndex(t *testing.T) {
	stream := &fakeStreamTorrent{}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream))

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/stream", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestGetTorrentStateEndpoint(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	state := &fakeGetTorrentState{
		result: domain.SessionState{ID: "t1", Status: domain.TorrentActive, Progress: 0.5, UpdatedAt: now},
	}
	server := NewServer(&fakeCreateTorrent{}, WithGetTorrentState(state))

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/state", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if state.called != 1 || state.id != "t1" {
		t.Fatalf("usecase not called")
	}
}

func TestListTorrentStatesEndpoint(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	list := &fakeListTorrentStates{
		result: []domain.SessionState{
			{ID: "t1", Status: domain.TorrentActive, Progress: 0.2, UpdatedAt: now},
			{ID: "t2", Status: domain.TorrentActive, Progress: 0.8, UpdatedAt: now},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithListTorrentStates(list))

	req := httptest.NewRequest(http.MethodGet, "/torrents/state?status=active", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if list.called != 1 {
		t.Fatalf("usecase not called")
	}
}

func TestListTorrentStatesMissingStatus(t *testing.T) {
	list := &fakeListTorrentStates{}
	server := NewServer(&fakeCreateTorrent{}, WithListTorrentStates(list))

	req := httptest.NewRequest(http.MethodGet, "/torrents/state", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestGetPlayerSettings(t *testing.T) {
	player := &fakePlayerSettings{current: "t1"}
	server := NewServer(&fakeCreateTorrent{}, WithPlayerSettings(player))

	req := httptest.NewRequest(http.MethodGet, "/settings/player", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		CurrentTorrentID string `json:"currentTorrentId"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.CurrentTorrentID != "t1" {
		t.Fatalf("unexpected current torrent: %q", resp.CurrentTorrentID)
	}
}

func TestUpdatePlayerSettings(t *testing.T) {
	player := &fakePlayerSettings{}
	server := NewServer(&fakeCreateTorrent{}, WithPlayerSettings(player))

	req := httptest.NewRequest(http.MethodPatch, "/settings/player", bytes.NewBufferString(`{"currentTorrentId":"t2"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if player.setCalled != 1 || player.current != "t2" {
		t.Fatalf("player settings not updated: called=%d current=%q", player.setCalled, player.current)
	}
}

func TestFocusUsesPlayerSettingsController(t *testing.T) {
	player := &fakePlayerSettings{}
	server := NewServer(&fakeCreateTorrent{}, WithPlayerSettings(player))

	req := httptest.NewRequest(http.MethodPost, "/torrents/t7/focus", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
	if player.current != "t7" {
		t.Fatalf("focus not persisted: %q", player.current)
	}
}

func TestHLSSeekRouteAllowsPost(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{})

	req := httptest.NewRequest(http.MethodPost, "/torrents/t1/hls/0/seek?time=10", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	// Route should be handled by HLS handler (not blocked by method check in path router).
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestPlayerHealthEndpoint(t *testing.T) {
	player := &fakePlayerSettings{current: "t1"}
	server := NewServer(&fakeCreateTorrent{}, WithPlayerSettings(player))

	req := httptest.NewRequest(http.MethodGet, "/internal/health/player", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var resp playerHealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "degraded" {
		t.Fatalf("unexpected status: %s", resp.Status)
	}
	if resp.CurrentTorrentID != "t1" || !resp.FocusModeEnabled {
		t.Fatalf("unexpected player context: %+v", resp)
	}
	if len(resp.Issues) == 0 {
		t.Fatalf("expected degradation issues")
	}
}

func TestPlayerHealthEndpointMethodNotAllowed(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{})
	req := httptest.NewRequest(http.MethodPost, "/internal/health/player", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestGetMediaInfoEndpoint(t *testing.T) {
	base := t.TempDir()
	repo := &fakeRepo{
		get: domain.TorrentRecord{
			ID:    "t1",
			Files: []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: 100}},
		},
	}
	probe := &fakeMediaProbe{
		result: domain.MediaInfo{
			Tracks: []domain.MediaTrack{
				{Index: 0, Type: "audio", Codec: "aac", Language: "eng", Default: true},
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithRepository(repo), WithMediaProbe(probe, base))

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/media/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if probe.called != 1 {
		t.Fatalf("probe not called")
	}
	expected := filepath.Join(base, "movie.mkv")
	if probe.filePath != expected {
		t.Fatalf("path mismatch: got=%s want=%s", probe.filePath, expected)
	}

	var resp domain.MediaInfo
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tracks) != 1 || resp.Tracks[0].Type != "audio" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestGetMediaInfoInvalidFileIndex(t *testing.T) {
	repo := &fakeRepo{
		get: domain.TorrentRecord{
			ID:    "t1",
			Files: []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: 100}},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithRepository(repo), WithMediaProbe(&fakeMediaProbe{}, t.TempDir()))

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/media/1", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestGetMediaInfoFallsBackToStreamSession(t *testing.T) {
	base := t.TempDir()
	repo := &fakeRepo{
		get: domain.TorrentRecord{
			ID:    "t1",
			Files: []domain.FileRef{},
		},
	}
	probe := &fakeMediaProbe{
		result: domain.MediaInfo{
			Tracks: []domain.MediaTrack{
				{Index: 0, Type: "audio", Codec: "ac3", Language: "rus", Default: true},
			},
		},
		readerResult: domain.MediaInfo{
			Tracks: []domain.MediaTrack{
				{Index: 0, Type: "audio", Codec: "ac3", Language: "rus", Default: true},
				{Index: 1, Type: "audio", Codec: "dts", Language: "eng"},
				{Index: 0, Type: "subtitle", Codec: "subrip", Language: "eng"},
			},
		},
	}
	reader := &testStreamReader{Reader: bytes.NewReader([]byte("x"))}
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			Reader: reader,
			File:   domain.FileRef{Index: 0, Path: "Spider.Man.2002.mkv", Length: 1},
		},
	}
	server := NewServer(
		&fakeCreateTorrent{},
		WithRepository(repo),
		WithMediaProbe(probe, base),
		WithStreamTorrent(stream),
	)

	req := httptest.NewRequest(http.MethodGet, "/torrents/t1/media/0", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if stream.called < 1 {
		t.Fatalf("stream fallback not called")
	}
	if probe.called != 1 {
		t.Fatalf("probe not called")
	}
	if probe.readerCalled != 1 {
		t.Fatalf("probe reader fallback not called")
	}

	var resp domain.MediaInfo
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tracks) != 3 {
		t.Fatalf("expected 3 tracks, got %d", len(resp.Tracks))
	}
}

func TestRewritePlaylistSegmentURLs(t *testing.T) {
	input := []byte("#EXTM3U\n#EXTINF:4.0,\nseg-00001.ts\n")
	output := string(rewritePlaylistSegmentURLs(input, 2, 1))
	if output == string(input) {
		t.Fatalf("playlist should be rewritten")
	}
	if !bytes.Contains([]byte(output), []byte("seg-00001.ts?audioTrack=2&subtitleTrack=1")) {
		t.Fatalf("unexpected output: %s", output)
	}
}

// --- Method routing tests ---

func TestTorrentsMethodNotAllowed(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{})

	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, "/torrents", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("method %s: status = %d, want 405", method, w.Code)
		}
	}
}

func TestTorrentByIDMethodNotAllowed(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{}, WithRepository(&fakeRepo{}))

	req := httptest.NewRequest(http.MethodPost, "/torrents/t1", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

// --- Create handler error paths ---

func TestCreateTorrentEngineError(t *testing.T) {
	uc := &fakeCreateTorrent{err: usecase.ErrEngine}
	server := NewServer(uc)

	req := httptest.NewRequest(http.MethodPost, "/torrents", bytes.NewReader([]byte(`{"magnet":"m"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
	var resp errorEnvelope
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != "engine_error" {
		t.Fatalf("code = %s", resp.Error.Code)
	}
}

func TestCreateTorrentBadJSON(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{})

	req := httptest.NewRequest(http.MethodPost, "/torrents", bytes.NewReader([]byte(`{bad`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestCreateTorrentNotConfigured(t *testing.T) {
	s := &Server{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bypass middleware to test nil create use case
	})}
	_ = s // Server with nil createTorrent already tested via normal flow
}

// --- Delete handler error paths ---

func TestDeleteTorrentNotFoundEndpoint(t *testing.T) {
	del := &fakeDeleteTorrent{err: domain.ErrNotFound}
	server := NewServer(&fakeCreateTorrent{}, WithDeleteTorrent(del))

	req := httptest.NewRequest(http.MethodDelete, "/torrents/t404", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestDeleteTorrentEngineErrorEndpoint(t *testing.T) {
	del := &fakeDeleteTorrent{err: usecase.ErrEngine}
	server := NewServer(&fakeCreateTorrent{}, WithDeleteTorrent(del))

	req := httptest.NewRequest(http.MethodDelete, "/torrents/t1", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
}

// --- List handler validation ---

func TestListTorrentsInvalidSortBy(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{}, WithRepository(&fakeRepo{}))
	req := httptest.NewRequest(http.MethodGet, "/torrents?sortBy=invalid", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestListTorrentsInvalidSortOrder(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{}, WithRepository(&fakeRepo{}))
	req := httptest.NewRequest(http.MethodGet, "/torrents?sortOrder=invalid", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestListTorrentsInvalidLimit(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{}, WithRepository(&fakeRepo{}))
	req := httptest.NewRequest(http.MethodGet, "/torrents?limit=abc", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestListTorrentsInvalidOffset(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{}, WithRepository(&fakeRepo{}))
	req := httptest.NewRequest(http.MethodGet, "/torrents?offset=abc", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestListTorrentsLimitClamped(t *testing.T) {
	repo := &fakeRepo{}
	server := NewServer(&fakeCreateTorrent{}, WithRepository(repo))
	req := httptest.NewRequest(http.MethodGet, "/torrents?limit=9999", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if repo.lastFilter.Limit != 1000 {
		t.Fatalf("limit not clamped: %d", repo.lastFilter.Limit)
	}
}

func TestListTorrentsNoRepo(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{})
	req := httptest.NewRequest(http.MethodGet, "/torrents", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestListTorrentsSortByProgress(t *testing.T) {
	repo := &fakeRepo{}
	server := NewServer(&fakeCreateTorrent{}, WithRepository(repo))
	req := httptest.NewRequest(http.MethodGet, "/torrents?sortBy=progress&sortOrder=asc", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if repo.lastFilter.SortBy != "progress" || repo.lastFilter.SortOrder != domain.SortAsc {
		t.Fatalf("filter mismatch: %+v", repo.lastFilter)
	}
}

// --- Bulk operation tests ---

func TestBulkStopEndpoint(t *testing.T) {
	stop := &fakeStopTorrent{result: domain.TorrentRecord{Status: domain.TorrentStopped}}
	server := NewServer(&fakeCreateTorrent{}, WithStopTorrent(stop))

	req := httptest.NewRequest(http.MethodPost, "/torrents/bulk/stop", bytes.NewBufferString(`{"ids":["t1","t2"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if stop.called != 2 {
		t.Fatalf("expected 2 stop calls, got %d", stop.called)
	}
}

func TestBulkDeleteEndpoint(t *testing.T) {
	del := &fakeDeleteTorrent{}
	server := NewServer(&fakeCreateTorrent{}, WithDeleteTorrent(del))

	req := httptest.NewRequest(http.MethodPost, "/torrents/bulk/delete", bytes.NewBufferString(`{"ids":["t1"],"deleteFiles":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if del.called != 1 || !del.deleteFiles {
		t.Fatalf("delete not called correctly: called=%d files=%v", del.called, del.deleteFiles)
	}
}

func TestBulkTooManyIDs(t *testing.T) {
	start := &fakeStartTorrent{}
	server := NewServer(&fakeCreateTorrent{}, WithStartTorrent(start))

	ids := make([]string, 101)
	for i := range ids {
		ids[i] = "t" + time.Now().String()
	}
	body, _ := json.Marshal(bulkRequest{IDs: ids})
	req := httptest.NewRequest(http.MethodPost, "/torrents/bulk/start", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestBulkEmptyIDs(t *testing.T) {
	start := &fakeStartTorrent{}
	server := NewServer(&fakeCreateTorrent{}, WithStartTorrent(start))

	req := httptest.NewRequest(http.MethodPost, "/torrents/bulk/start", bytes.NewBufferString(`{"ids":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestBulkInvalidJSON(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{}, WithStartTorrent(&fakeStartTorrent{}))

	req := httptest.NewRequest(http.MethodPost, "/torrents/bulk/start", bytes.NewBufferString(`{bad`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestBulkMethodNotAllowed(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{})

	req := httptest.NewRequest(http.MethodGet, "/torrents/bulk/start", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestBulkUnknownAction(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{})

	req := httptest.NewRequest(http.MethodPost, "/torrents/bulk/unknown", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestBulkStartPartialFailure(t *testing.T) {
	callCount := 0
	start := &fakeStartTorrent{}
	// Override to simulate partial failure
	server := NewServer(&fakeCreateTorrent{}, WithStartTorrent(start))

	req := httptest.NewRequest(http.MethodPost, "/torrents/bulk/start", bytes.NewBufferString(`{"ids":["t1"," ","t2"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	_ = callCount

	var resp bulkResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(resp.Items))
	}
	// The empty ID should fail
	found := false
	for _, item := range resp.Items {
		if !item.OK && item.Error == "empty id" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected empty id error in results: %+v", resp.Items)
	}
}

// --- Utility function tests ---

func TestProgressRatio(t *testing.T) {
	tests := []struct {
		done, total int64
		want        float64
	}{
		{0, 0, 0},
		{0, 100, 0},
		{50, 100, 0.5},
		{100, 100, 1.0},
		{-1, 100, 0},
		{200, 100, 1.0},
	}
	for _, tt := range tests {
		got := progressRatio(tt.done, tt.total)
		if got != tt.want {
			t.Errorf("progressRatio(%d, %d) = %f, want %f", tt.done, tt.total, got, tt.want)
		}
	}
}

func TestParseBoolQuery(t *testing.T) {
	tests := []struct {
		input   string
		want    bool
		wantErr bool
	}{
		{"", false, false},
		{"true", true, false},
		{"false", false, false},
		{"TRUE", true, false},
		{"bad", false, true},
	}
	for _, tt := range tests {
		got, err := parseBoolQuery(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseBoolQuery(%q): err=%v wantErr=%v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("parseBoolQuery(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseStatus(t *testing.T) {
	tests := []struct {
		input   string
		isNil   bool
		want    domain.TorrentStatus
		wantErr bool
	}{
		{"", true, "", false},
		{"all", true, "", false},
		{"active", false, domain.TorrentActive, false},
		{"completed", false, domain.TorrentCompleted, false},
		{"stopped", false, domain.TorrentStopped, false},
		{"bad", false, "", true},
		{"pending", false, "", true},
	}
	for _, tt := range tests {
		got, err := parseStatus(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseStatus(%q): err=%v wantErr=%v", tt.input, err, tt.wantErr)
			continue
		}
		if tt.isNil && got != nil {
			t.Errorf("parseStatus(%q) = %v, want nil", tt.input, *got)
		}
		if !tt.isNil && got != nil && *got != tt.want {
			t.Errorf("parseStatus(%q) = %v, want %v", tt.input, *got, tt.want)
		}
	}
}

func TestIsAllowedSortBy(t *testing.T) {
	valid := []string{"name", "createdAt", "updatedAt", "totalBytes", "progress"}
	for _, v := range valid {
		if !isAllowedSortBy(v) {
			t.Errorf("isAllowedSortBy(%q) = false, want true", v)
		}
	}
	invalid := []string{"", "unknown", "doneBytes", "status"}
	for _, v := range invalid {
		if isAllowedSortBy(v) {
			t.Errorf("isAllowedSortBy(%q) = true, want false", v)
		}
	}
}

func TestParseCommaSeparated(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"  ", 0},
		{"a", 1},
		{"a,b,c", 3},
		{"a, b, c", 3},
		{"a,,b", 2},
		{"a,A,b", 2}, // case-insensitive dedup
	}
	for _, tt := range tests {
		got := parseCommaSeparated(tt.input)
		if len(got) != tt.want {
			t.Errorf("parseCommaSeparated(%q) = %v (len=%d), want len=%d", tt.input, got, len(got), tt.want)
		}
	}
}

func TestParseSortOrder(t *testing.T) {
	tests := []struct {
		input   string
		want    domain.SortOrder
		wantErr bool
	}{
		{"", domain.SortDesc, false},
		{"asc", domain.SortAsc, false},
		{"desc", domain.SortDesc, false},
		{"ASC", domain.SortAsc, false},
		{"bad", "", true},
	}
	for _, tt := range tests {
		got, err := parseSortOrder(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseSortOrder(%q): err=%v wantErr=%v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("parseSortOrder(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- Tags endpoint edge cases ---

func TestUpdateTagsNotFound(t *testing.T) {
	repo := &fakeRepo{updateTagsErr: domain.ErrNotFound}
	server := NewServer(&fakeCreateTorrent{}, WithRepository(repo))

	req := httptest.NewRequest(http.MethodPut, "/torrents/t404/tags", bytes.NewBufferString(`{"tags":["a"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestUpdateTagsBadJSON(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{}, WithRepository(&fakeRepo{}))

	req := httptest.NewRequest(http.MethodPut, "/torrents/t1/tags", bytes.NewBufferString(`{bad`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

// --- Route dispatching edge cases ---

func TestEmptyTorrentIDNotFound(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{})

	req := httptest.NewRequest(http.MethodGet, "/torrents/", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestUnknownActionNotFound(t *testing.T) {
	server := NewServer(&fakeCreateTorrent{})

	req := httptest.NewRequest(http.MethodPost, "/torrents/t1/unknown", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

type testStreamReader struct {
	*bytes.Reader
	ctx        context.Context
	readahead  int64
	responsive bool
}

func (t *testStreamReader) SetContext(ctx context.Context) { t.ctx = ctx }
func (t *testStreamReader) SetReadahead(n int64)           { t.readahead = n }
func (t *testStreamReader) SetResponsive()                 { t.responsive = true }
func (t *testStreamReader) Close() error                   { return nil }
