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

type e2eDeleteTorrent struct {
	repo        *fakeRepo
	called      int
	id          domain.TorrentID
	deleteFiles bool
}

func (d *e2eDeleteTorrent) Execute(_ context.Context, id domain.TorrentID, deleteFiles bool) error {
	d.called++
	d.id = id
	d.deleteFiles = deleteFiles

	next := make([]domain.TorrentRecord, 0, len(d.repo.list))
	for _, record := range d.repo.list {
		if record.ID == id {
			continue
		}
		next = append(next, record)
	}
	d.repo.list = next
	return nil
}

type slowSeekStreamTorrent struct {
	delay     time.Duration
	result    usecase.StreamResult
	err       error
	called    int
	id        domain.TorrentID
	fileIndex int
}

func (s *slowSeekStreamTorrent) Execute(ctx context.Context, id domain.TorrentID, fileIndex int) (usecase.StreamResult, error) {
	s.called++
	s.id = id
	s.fileIndex = fileIndex

	timer := time.NewTimer(s.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return usecase.StreamResult{}, ctx.Err()
	case <-timer.C:
	}

	return s.result, s.err
}

func (s *slowSeekStreamTorrent) ExecuteRaw(ctx context.Context, id domain.TorrentID, fileIndex int) (usecase.StreamResult, error) {
	return s.Execute(ctx, id, fileIndex)
}

func TestE2EPlayerDeleteWithFilesFlow(t *testing.T) {
	repo := &fakeRepo{
		list: []domain.TorrentRecord{
			{
				ID:     "t-delete",
				Name:   "movie-to-delete.mkv",
				Status: domain.TorrentActive,
				Files: []domain.FileRef{
					{Index: 0, Path: "movie-to-delete.mkv", Length: 1024},
				},
			},
		},
	}
	deleteUC := &e2eDeleteTorrent{repo: repo}

	server := NewServer(
		&fakeCreateTorrent{},
		WithRepository(repo),
		WithDeleteTorrent(deleteUC),
	)

	beforeReq := httptest.NewRequest(http.MethodGet, "/torrents?view=full", nil)
	beforeW := httptest.NewRecorder()
	server.ServeHTTP(beforeW, beforeReq)
	if beforeW.Code != http.StatusOK {
		t.Fatalf("list before delete: status = %d", beforeW.Code)
	}
	var before struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(beforeW.Body).Decode(&before); err != nil {
		t.Fatalf("decode before list: %v", err)
	}
	if before.Count != 1 {
		t.Fatalf("before count = %d, want 1", before.Count)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/torrents/t-delete?deleteFiles=true", nil)
	deleteW := httptest.NewRecorder()
	server.ServeHTTP(deleteW, deleteReq)
	if deleteW.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d", deleteW.Code)
	}
	if deleteUC.called != 1 {
		t.Fatalf("delete usecase called = %d, want 1", deleteUC.called)
	}
	if !deleteUC.deleteFiles {
		t.Fatalf("deleteFiles should be true")
	}
	if deleteUC.id != "t-delete" {
		t.Fatalf("delete id = %q, want t-delete", deleteUC.id)
	}

	afterReq := httptest.NewRequest(http.MethodGet, "/torrents?view=full", nil)
	afterW := httptest.NewRecorder()
	server.ServeHTTP(afterW, afterReq)
	if afterW.Code != http.StatusOK {
		t.Fatalf("list after delete: status = %d", afterW.Code)
	}
	var after struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(afterW.Body).Decode(&after); err != nil {
		t.Fatalf("decode after list: %v", err)
	}
	if after.Count != 0 {
		t.Fatalf("after count = %d, want 0", after.Count)
	}
}

func TestE2EPlayerEpisodeSelectAndResumeFlow(t *testing.T) {
	watchStore := newFakeWatchHistoryStore()
	episodeData := []byte("episode-two-data")
	stream := &fakeStreamTorrent{
		result: usecase.StreamResult{
			Reader: &testStreamReader{Reader: bytes.NewReader(episodeData)},
			File: domain.FileRef{
				Index:  1,
				Path:   "Show.S01E02.mkv",
				Length: int64(len(episodeData)),
			},
		},
	}
	server := NewServer(
		&fakeCreateTorrent{},
		WithWatchHistory(watchStore),
		WithStreamTorrent(stream),
	)

	selectReq := httptest.NewRequest(http.MethodGet, "/torrents/show1/stream?fileIndex=1", nil)
	selectW := httptest.NewRecorder()
	server.ServeHTTP(selectW, selectReq)
	if selectW.Code != http.StatusOK {
		t.Fatalf("episode select stream: status = %d", selectW.Code)
	}
	if stream.called != 1 || stream.fileIndex != 1 {
		t.Fatalf("stream calls: called=%d fileIndex=%d, want called=1 fileIndex=1", stream.called, stream.fileIndex)
	}

	saveBody, _ := json.Marshal(map[string]any{
		"position":    915.0,
		"duration":    3600.0,
		"torrentName": "Show Season 1",
		"filePath":    "Show.S01E02.mkv",
	})
	saveW := doHistoryRequest(server, http.MethodPut, "/watch-history/show1/1", saveBody)
	if saveW.Code != http.StatusNoContent {
		t.Fatalf("save resume point: status = %d", saveW.Code)
	}

	resumeW := doHistoryRequest(server, http.MethodGet, "/watch-history/show1/1", nil)
	if resumeW.Code != http.StatusOK {
		t.Fatalf("get resume point: status = %d", resumeW.Code)
	}
	var resume domain.WatchPosition
	if err := json.NewDecoder(resumeW.Body).Decode(&resume); err != nil {
		t.Fatalf("decode resume point: %v", err)
	}
	if resume.FileIndex != 1 {
		t.Fatalf("resume fileIndex = %d, want 1", resume.FileIndex)
	}
	if resume.Position != 915.0 {
		t.Fatalf("resume position = %f, want 915.0", resume.Position)
	}

	resumePlayReq := httptest.NewRequest(http.MethodGet, "/torrents/show1/stream?fileIndex=1", nil)
	resumePlayW := httptest.NewRecorder()
	server.ServeHTTP(resumePlayW, resumePlayReq)
	if resumePlayW.Code != http.StatusOK {
		t.Fatalf("resume stream: status = %d", resumePlayW.Code)
	}
	if stream.called != 2 || stream.fileIndex != 1 {
		t.Fatalf("resume stream calls: called=%d fileIndex=%d, want called=2 fileIndex=1", stream.called, stream.fileIndex)
	}
}

func TestE2EPlayerRestartVerifyStateFlow(t *testing.T) {
	states := &fakeGetStateSequence{
		states: []domain.SessionState{
			{
				ID:                   "t-verify",
				Status:               domain.TorrentActive,
				Mode:                 domain.ModeDownloading,
				TransferPhase:        domain.TransferPhaseVerifying,
				Progress:             0.82,
				VerificationProgress: 0.37,
			},
			{
				ID:                   "t-verify",
				Status:               domain.TorrentActive,
				Mode:                 domain.ModeDownloading,
				TransferPhase:        domain.TransferPhaseDownloading,
				Progress:             0.82,
				VerificationProgress: 0,
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithGetTorrentState(states))

	firstReq := httptest.NewRequest(http.MethodGet, "/torrents/t-verify/state", nil)
	firstW := httptest.NewRecorder()
	server.ServeHTTP(firstW, firstReq)
	if firstW.Code != http.StatusOK {
		t.Fatalf("first state poll: status = %d", firstW.Code)
	}
	var first domain.SessionState
	if err := json.NewDecoder(firstW.Body).Decode(&first); err != nil {
		t.Fatalf("decode first state: %v", err)
	}
	if first.TransferPhase != domain.TransferPhaseVerifying {
		t.Fatalf("first transferPhase = %q, want verifying", first.TransferPhase)
	}
	if first.VerificationProgress <= 0 || first.VerificationProgress >= 1 {
		t.Fatalf("first verificationProgress = %f, want (0,1)", first.VerificationProgress)
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/torrents/t-verify/state", nil)
	secondW := httptest.NewRecorder()
	server.ServeHTTP(secondW, secondReq)
	if secondW.Code != http.StatusOK {
		t.Fatalf("second state poll: status = %d", secondW.Code)
	}
	var second domain.SessionState
	if err := json.NewDecoder(secondW.Body).Decode(&second); err != nil {
		t.Fatalf("decode second state: %v", err)
	}
	if second.TransferPhase != domain.TransferPhaseDownloading {
		t.Fatalf("second transferPhase = %q, want downloading", second.TransferPhase)
	}
	if second.Progress < first.Progress {
		t.Fatalf("progress regressed from %f to %f", first.Progress, second.Progress)
	}
}

func TestE2EPlayerSeekAcceptedDuringJobStartup(t *testing.T) {
	stream := &slowSeekStreamTorrent{
		delay: 6 * time.Second,
		result: usecase.StreamResult{
			Reader: &testStreamReader{Reader: bytes.NewReader([]byte("seek-pending"))},
			File: domain.FileRef{
				Index:  0,
				Path:   "movie.mkv",
				Length: 11,
			},
		},
	}
	server := NewServer(&fakeCreateTorrent{}, WithStreamTorrent(stream))

	started := time.Now()
	req := httptest.NewRequest(http.MethodPost, "/torrents/t-seek/hls/0/seek?time=120", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	elapsed := time.Since(started)

	if w.Code != http.StatusOK {
		t.Fatalf("seek status = %d, body = %s", w.Code, w.Body.String())
	}
	if elapsed < 4*time.Second {
		t.Fatalf("seek returned too quickly (%s), expected startup wait path", elapsed)
	}

	var resp struct {
		SeekTime float64 `json:"seekTime"`
		SeekMode string  `json:"seekMode"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode seek response: %v", err)
	}
	if resp.SeekTime < 119.9 || resp.SeekTime > 120.1 {
		t.Fatalf("seekTime = %f, want 120", resp.SeekTime)
	}
	if resp.SeekMode != SeekModeHard.String() {
		t.Fatalf("seekMode = %q, want %q", resp.SeekMode, SeekModeHard.String())
	}
}
