package usecase

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

// --- fakes for sync ---

type fakeSyncEngine struct {
	sessions   []domain.TorrentID
	listErr    error
	states     map[domain.TorrentID]domain.SessionState
	stateErr   error
	stateCalls []domain.TorrentID
}

func (f *fakeSyncEngine) Open(ctx context.Context, src domain.TorrentSource) (ports.Session, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeSyncEngine) Close() error { return nil }
func (f *fakeSyncEngine) GetSessionState(ctx context.Context, id domain.TorrentID) (domain.SessionState, error) {
	f.stateCalls = append(f.stateCalls, id)
	if f.stateErr != nil {
		return domain.SessionState{}, f.stateErr
	}
	state, ok := f.states[id]
	if !ok {
		return domain.SessionState{}, domain.ErrNotFound
	}
	return state, nil
}
func (f *fakeSyncEngine) GetSession(ctx context.Context, id domain.TorrentID) (ports.Session, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeSyncEngine) ListActiveSessions(ctx context.Context) ([]domain.TorrentID, error) {
	return nil, nil
}
func (f *fakeSyncEngine) StopSession(ctx context.Context, id domain.TorrentID) error  { return nil }
func (f *fakeSyncEngine) StartSession(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeSyncEngine) RemoveSession(ctx context.Context, id domain.TorrentID) error {
	return nil
}
func (f *fakeSyncEngine) SetPiecePriority(ctx context.Context, id domain.TorrentID, file domain.FileRef, r domain.Range, prio domain.Priority) error {
	return nil
}
func (f *fakeSyncEngine) ListSessions(ctx context.Context) ([]domain.TorrentID, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.sessions, nil
}
func (f *fakeSyncEngine) FocusSession(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeSyncEngine) UnfocusAll(ctx context.Context) error                        { return nil }
func (f *fakeSyncEngine) GetSessionMode(ctx context.Context, id domain.TorrentID) (domain.SessionMode, error) {
	return domain.ModeDownloading, nil
}
func (f *fakeSyncEngine) SetDownloadRateLimit(ctx context.Context, id domain.TorrentID, bytesPerSec int64) error {
	return nil
}

type fakeSyncRepo struct {
	records         map[domain.TorrentID]domain.TorrentRecord
	getManyErr      error
	updateProgCalls []updateProgCall
	updateProgErr   error
}

type updateProgCall struct {
	ID     domain.TorrentID
	Update domain.ProgressUpdate
}

func (f *fakeSyncRepo) Create(ctx context.Context, t domain.TorrentRecord) error  { return nil }
func (f *fakeSyncRepo) Update(ctx context.Context, t domain.TorrentRecord) error  { return nil }
func (f *fakeSyncRepo) Get(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error) {
	return domain.TorrentRecord{}, nil
}
func (f *fakeSyncRepo) List(ctx context.Context, filter domain.TorrentFilter) ([]domain.TorrentRecord, error) {
	return nil, nil
}
func (f *fakeSyncRepo) GetMany(ctx context.Context, ids []domain.TorrentID) ([]domain.TorrentRecord, error) {
	if f.getManyErr != nil {
		return nil, f.getManyErr
	}
	var result []domain.TorrentRecord
	for _, id := range ids {
		if rec, ok := f.records[id]; ok {
			result = append(result, rec)
		}
	}
	return result, nil
}
func (f *fakeSyncRepo) Delete(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeSyncRepo) UpdateTags(ctx context.Context, id domain.TorrentID, tags []string) error {
	return nil
}
func (f *fakeSyncRepo) UpdateProgress(ctx context.Context, id domain.TorrentID, update domain.ProgressUpdate) error {
	f.updateProgCalls = append(f.updateProgCalls, updateProgCall{ID: id, Update: update})
	return f.updateProgErr
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nopWriter{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// --- sumBytesCompleted ---

func TestSumBytesCompleted(t *testing.T) {
	tests := []struct {
		name  string
		files []domain.FileRef
		want  int64
	}{
		{"nil", nil, 0},
		{"empty", []domain.FileRef{}, 0},
		{"single", []domain.FileRef{{BytesCompleted: 100}}, 100},
		{"multiple", []domain.FileRef{{BytesCompleted: 100}, {BytesCompleted: 200}, {BytesCompleted: 50}}, 350},
		{"zero_bytes", []domain.FileRef{{BytesCompleted: 0}, {BytesCompleted: 0}}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sumBytesCompleted(tt.files)
			if got != tt.want {
				t.Fatalf("sumBytesCompleted = %d, want %d", got, tt.want)
			}
		})
	}
}

// --- SyncState.Run ---

func TestSyncStateRunStopsOnCancel(t *testing.T) {
	engine := &fakeSyncEngine{sessions: nil}
	repo := &fakeSyncRepo{records: map[domain.TorrentID]domain.TorrentRecord{}}
	s := SyncState{
		Engine:   engine,
		Repo:     repo,
		Logger:   discardLogger(),
		Interval: 10 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	// Let at least one tick happen.
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

func TestSyncStateDefaultInterval(t *testing.T) {
	// Verify that zero/negative interval defaults to 10s by checking the code path.
	// We can't easily test the exact ticker value, but we verify Run starts and stops.
	engine := &fakeSyncEngine{sessions: nil}
	repo := &fakeSyncRepo{records: map[domain.TorrentID]domain.TorrentRecord{}}
	s := SyncState{
		Engine:   engine,
		Repo:     repo,
		Logger:   discardLogger(),
		Interval: 0, // should default to 10s
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	// Cancel immediately — we just verify it doesn't panic and exits.
	cancel()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

// --- SyncState.sync ---

func TestSyncStateSyncNoSessions(t *testing.T) {
	repo := &fakeSyncRepo{records: map[domain.TorrentID]domain.TorrentRecord{}}
	s := SyncState{
		Engine:   &fakeSyncEngine{sessions: nil},
		Repo:     repo,
		Logger:   discardLogger(),
		Interval: time.Second,
	}
	s.sync(context.Background())

	if len(repo.updateProgCalls) != 0 {
		t.Fatalf("expected no update calls, got %d", len(repo.updateProgCalls))
	}
}

func TestSyncStateSyncListSessionsError(t *testing.T) {
	repo := &fakeSyncRepo{records: map[domain.TorrentID]domain.TorrentRecord{}}
	s := SyncState{
		Engine:   &fakeSyncEngine{listErr: errors.New("network error")},
		Repo:     repo,
		Logger:   discardLogger(),
		Interval: time.Second,
	}
	s.sync(context.Background())

	if len(repo.updateProgCalls) != 0 {
		t.Fatalf("expected no update calls, got %d", len(repo.updateProgCalls))
	}
}

func TestSyncStateSyncGetManyError(t *testing.T) {
	engine := &fakeSyncEngine{sessions: []domain.TorrentID{"t1"}}
	repo := &fakeSyncRepo{
		records:    map[domain.TorrentID]domain.TorrentRecord{},
		getManyErr: errors.New("db error"),
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}
	s.sync(context.Background())

	if len(repo.updateProgCalls) != 0 {
		t.Fatalf("expected no update calls, got %d", len(repo.updateProgCalls))
	}
}

func TestSyncStateSyncProgressUpdate(t *testing.T) {
	engine := &fakeSyncEngine{
		sessions: []domain.TorrentID{"t1"},
		states: map[domain.TorrentID]domain.SessionState{
			"t1": {
				ID:     "t1",
				Status: domain.TorrentActive,
				Files:  []domain.FileRef{{Index: 0, Path: "a.mp4", Length: 1000, BytesCompleted: 500}},
			},
		},
	}
	repo := &fakeSyncRepo{
		records: map[domain.TorrentID]domain.TorrentRecord{
			"t1": {
				ID:        "t1",
				Name:      "test",
				Status:    domain.TorrentActive,
				DoneBytes: 200,
				Files:     []domain.FileRef{{Index: 0, Path: "a.mp4", Length: 1000, BytesCompleted: 200}},
			},
		},
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}
	s.sync(context.Background())

	if len(repo.updateProgCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(repo.updateProgCalls))
	}
	call := repo.updateProgCalls[0]
	if call.ID != "t1" {
		t.Fatalf("update ID = %q, want t1", call.ID)
	}
	if call.Update.DoneBytes != 500 {
		t.Fatalf("DoneBytes = %d, want 500", call.Update.DoneBytes)
	}
}

func TestSyncStateSyncProgressNoRegression(t *testing.T) {
	// Engine reports lower progress than DB — only files-changed check matters.
	engine := &fakeSyncEngine{
		sessions: []domain.TorrentID{"t1"},
		states: map[domain.TorrentID]domain.SessionState{
			"t1": {
				ID:     "t1",
				Status: domain.TorrentActive,
				Files:  []domain.FileRef{{Index: 0, Path: "a.mp4", Length: 1000, BytesCompleted: 100}},
			},
		},
	}
	repo := &fakeSyncRepo{
		records: map[domain.TorrentID]domain.TorrentRecord{
			"t1": {
				ID:        "t1",
				Name:      "test",
				Status:    domain.TorrentActive,
				DoneBytes: 500,
				Files:     []domain.FileRef{{Index: 0, Path: "a.mp4", Length: 1000, BytesCompleted: 500}},
			},
		},
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}
	s.sync(context.Background())

	// No changes: engine progress is lower, status same, same file count, per-file not higher.
	if len(repo.updateProgCalls) != 0 {
		t.Fatalf("expected no update calls (no regression), got %d", len(repo.updateProgCalls))
	}
}

func TestSyncStateSyncStatusChange(t *testing.T) {
	engine := &fakeSyncEngine{
		sessions: []domain.TorrentID{"t1"},
		states: map[domain.TorrentID]domain.SessionState{
			"t1": {
				ID:     "t1",
				Status: domain.TorrentCompleted,
				Files:  []domain.FileRef{{Index: 0, Path: "a.mp4", Length: 1000, BytesCompleted: 1000}},
			},
		},
	}
	repo := &fakeSyncRepo{
		records: map[domain.TorrentID]domain.TorrentRecord{
			"t1": {
				ID:         "t1",
				Name:       "test",
				Status:     domain.TorrentActive,
				DoneBytes:  1000,
				TotalBytes: 1000,
				Files:      []domain.FileRef{{Index: 0, Path: "a.mp4", Length: 1000, BytesCompleted: 1000}},
			},
		},
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}
	s.sync(context.Background())

	if len(repo.updateProgCalls) != 1 {
		t.Fatalf("expected 1 update call for status change, got %d", len(repo.updateProgCalls))
	}
	if repo.updateProgCalls[0].Update.Status != domain.TorrentCompleted {
		t.Fatalf("status = %q, want completed", repo.updateProgCalls[0].Update.Status)
	}
}

func TestSyncStateSyncFilesCountChanged(t *testing.T) {
	// Engine has 2 files, DB has 1 → files changed, update called.
	engine := &fakeSyncEngine{
		sessions: []domain.TorrentID{"t1"},
		states: map[domain.TorrentID]domain.SessionState{
			"t1": {
				ID:     "t1",
				Status: domain.TorrentActive,
				Files: []domain.FileRef{
					{Index: 0, Path: "a.mp4", Length: 500, BytesCompleted: 100},
					{Index: 1, Path: "b.mp4", Length: 500, BytesCompleted: 0},
				},
			},
		},
	}
	repo := &fakeSyncRepo{
		records: map[domain.TorrentID]domain.TorrentRecord{
			"t1": {
				ID:        "t1",
				Name:      "test",
				Status:    domain.TorrentActive,
				DoneBytes: 100,
				Files:     []domain.FileRef{{Index: 0, Path: "a.mp4", Length: 500, BytesCompleted: 100}},
			},
		},
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}
	s.sync(context.Background())

	if len(repo.updateProgCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(repo.updateProgCalls))
	}
	if len(repo.updateProgCalls[0].Update.Files) != 2 {
		t.Fatalf("files count = %d, want 2", len(repo.updateProgCalls[0].Update.Files))
	}
	if repo.updateProgCalls[0].Update.TotalBytes != 1000 {
		t.Fatalf("TotalBytes = %d, want 1000", repo.updateProgCalls[0].Update.TotalBytes)
	}
}

func TestSyncStateSyncFileProgressMerge(t *testing.T) {
	// Same file count, but engine has higher per-file progress on file 0.
	engine := &fakeSyncEngine{
		sessions: []domain.TorrentID{"t1"},
		states: map[domain.TorrentID]domain.SessionState{
			"t1": {
				ID:     "t1",
				Status: domain.TorrentActive,
				Files: []domain.FileRef{
					{Index: 0, Path: "a.mp4", Length: 1000, BytesCompleted: 800},
					{Index: 1, Path: "b.mp4", Length: 500, BytesCompleted: 100},
				},
			},
		},
	}
	repo := &fakeSyncRepo{
		records: map[domain.TorrentID]domain.TorrentRecord{
			"t1": {
				ID:        "t1",
				Name:      "test",
				Status:    domain.TorrentActive,
				DoneBytes: 600,
				Files: []domain.FileRef{
					{Index: 0, Path: "a.mp4", Length: 1000, BytesCompleted: 500},
					{Index: 1, Path: "b.mp4", Length: 500, BytesCompleted: 200},
				},
			},
		},
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}
	s.sync(context.Background())

	if len(repo.updateProgCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(repo.updateProgCalls))
	}
	call := repo.updateProgCalls[0]
	// File 0: engine 800 > record 500 → merged as 800
	if call.Update.Files[0].BytesCompleted != 800 {
		t.Fatalf("file[0] BytesCompleted = %d, want 800", call.Update.Files[0].BytesCompleted)
	}
	// File 1: engine 100 < record 200 → merged as 200 (DB value preserved)
	if call.Update.Files[1].BytesCompleted != 200 {
		t.Fatalf("file[1] BytesCompleted = %d, want 200 (preserved from DB)", call.Update.Files[1].BytesCompleted)
	}
}

func TestSyncStateSyncNoChanges(t *testing.T) {
	engine := &fakeSyncEngine{
		sessions: []domain.TorrentID{"t1"},
		states: map[domain.TorrentID]domain.SessionState{
			"t1": {
				ID:     "t1",
				Status: domain.TorrentActive,
				Files:  []domain.FileRef{{Index: 0, Path: "a.mp4", Length: 1000, BytesCompleted: 500}},
			},
		},
	}
	repo := &fakeSyncRepo{
		records: map[domain.TorrentID]domain.TorrentRecord{
			"t1": {
				ID:        "t1",
				Name:      "test",
				Status:    domain.TorrentActive,
				DoneBytes: 500,
				Files:     []domain.FileRef{{Index: 0, Path: "a.mp4", Length: 1000, BytesCompleted: 500}},
			},
		},
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}
	s.sync(context.Background())

	if len(repo.updateProgCalls) != 0 {
		t.Fatalf("expected no update calls, got %d", len(repo.updateProgCalls))
	}
}

func TestSyncStateSyncNameDerived(t *testing.T) {
	// Record has empty name, engine has files → name should be derived.
	engine := &fakeSyncEngine{
		sessions: []domain.TorrentID{"t1"},
		states: map[domain.TorrentID]domain.SessionState{
			"t1": {
				ID:     "t1",
				Status: domain.TorrentActive,
				Files: []domain.FileRef{
					{Index: 0, Path: "Movies/Sintel (2010)/Sintel.mp4", Length: 1000, BytesCompleted: 100},
				},
			},
		},
	}
	repo := &fakeSyncRepo{
		records: map[domain.TorrentID]domain.TorrentRecord{
			"t1": {
				ID:        "t1",
				Name:      "", // empty name
				Status:    domain.TorrentActive,
				DoneBytes: 0,
				Files:     []domain.FileRef{{Index: 0, Path: "Movies/Sintel (2010)/Sintel.mp4", Length: 1000, BytesCompleted: 0}},
			},
		},
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}
	s.sync(context.Background())

	if len(repo.updateProgCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(repo.updateProgCalls))
	}
	if repo.updateProgCalls[0].Update.Name == "" {
		t.Fatal("expected non-empty derived name")
	}
}

func TestSyncStateSyncGetSessionStateNotFound(t *testing.T) {
	// Engine lists session, but GetSessionState returns ErrNotFound → skip silently.
	engine := &fakeSyncEngine{
		sessions: []domain.TorrentID{"t1"},
		states:   map[domain.TorrentID]domain.SessionState{}, // t1 not in states → ErrNotFound
	}
	repo := &fakeSyncRepo{
		records: map[domain.TorrentID]domain.TorrentRecord{
			"t1": {ID: "t1", Name: "test", Status: domain.TorrentActive},
		},
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}
	s.sync(context.Background())

	if len(repo.updateProgCalls) != 0 {
		t.Fatalf("expected no update calls, got %d", len(repo.updateProgCalls))
	}
}

func TestSyncStateSyncGetSessionStateError(t *testing.T) {
	engine := &fakeSyncEngine{
		sessions: []domain.TorrentID{"t1"},
		stateErr: errors.New("engine error"),
	}
	repo := &fakeSyncRepo{
		records: map[domain.TorrentID]domain.TorrentRecord{
			"t1": {ID: "t1", Name: "test", Status: domain.TorrentActive},
		},
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}
	s.sync(context.Background())

	if len(repo.updateProgCalls) != 0 {
		t.Fatalf("expected no update calls, got %d", len(repo.updateProgCalls))
	}
}

func TestSyncStateSyncRecordNotInRepo(t *testing.T) {
	// Engine has session t1, but repo doesn't have a record for it.
	engine := &fakeSyncEngine{
		sessions: []domain.TorrentID{"t1"},
		states: map[domain.TorrentID]domain.SessionState{
			"t1": {ID: "t1", Status: domain.TorrentActive},
		},
	}
	repo := &fakeSyncRepo{
		records: map[domain.TorrentID]domain.TorrentRecord{}, // no records
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}
	s.sync(context.Background())

	if len(repo.updateProgCalls) != 0 {
		t.Fatalf("expected no update calls, got %d", len(repo.updateProgCalls))
	}
}

func TestSyncStateSyncUpdateProgressError(t *testing.T) {
	// UpdateProgress fails — should log warning but not crash.
	engine := &fakeSyncEngine{
		sessions: []domain.TorrentID{"t1"},
		states: map[domain.TorrentID]domain.SessionState{
			"t1": {
				ID:     "t1",
				Status: domain.TorrentCompleted,
				Files:  []domain.FileRef{{Index: 0, Path: "a.mp4", Length: 1000, BytesCompleted: 1000}},
			},
		},
	}
	repo := &fakeSyncRepo{
		records: map[domain.TorrentID]domain.TorrentRecord{
			"t1": {ID: "t1", Name: "test", Status: domain.TorrentActive, DoneBytes: 500,
				Files: []domain.FileRef{{Index: 0, Path: "a.mp4", Length: 1000, BytesCompleted: 500}}},
		},
		updateProgErr: errors.New("write failed"),
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}

	// Should not panic.
	s.sync(context.Background())

	// The call was attempted even though it failed.
	if len(repo.updateProgCalls) != 1 {
		t.Fatalf("expected 1 update call attempt, got %d", len(repo.updateProgCalls))
	}
}

func TestSyncStateSyncTotalBytesPreserved(t *testing.T) {
	// When update.TotalBytes would be 0, but record has a value, it should be preserved.
	engine := &fakeSyncEngine{
		sessions: []domain.TorrentID{"t1"},
		states: map[domain.TorrentID]domain.SessionState{
			"t1": {
				ID:     "t1",
				Status: domain.TorrentCompleted, // status change triggers update
				Files:  nil,                      // no files → sumFileLengths=0
			},
		},
	}
	repo := &fakeSyncRepo{
		records: map[domain.TorrentID]domain.TorrentRecord{
			"t1": {
				ID:         "t1",
				Name:       "test",
				Status:     domain.TorrentActive,
				TotalBytes: 5000,
				DoneBytes:  0,
			},
		},
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}
	s.sync(context.Background())

	if len(repo.updateProgCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(repo.updateProgCalls))
	}
	if repo.updateProgCalls[0].Update.TotalBytes != 5000 {
		t.Fatalf("TotalBytes = %d, want 5000 (preserved from record)", repo.updateProgCalls[0].Update.TotalBytes)
	}
}

func TestSyncStateSyncMultipleSessions(t *testing.T) {
	engine := &fakeSyncEngine{
		sessions: []domain.TorrentID{"t1", "t2", "t3"},
		states: map[domain.TorrentID]domain.SessionState{
			"t1": {ID: "t1", Status: domain.TorrentActive,
				Files: []domain.FileRef{{Index: 0, Path: "a.mp4", Length: 1000, BytesCompleted: 800}}},
			"t2": {ID: "t2", Status: domain.TorrentActive,
				Files: []domain.FileRef{{Index: 0, Path: "b.mp4", Length: 2000, BytesCompleted: 2000}}},
			"t3": {ID: "t3", Status: domain.TorrentActive,
				Files: []domain.FileRef{{Index: 0, Path: "c.mp4", Length: 500, BytesCompleted: 250}}},
		},
	}
	repo := &fakeSyncRepo{
		records: map[domain.TorrentID]domain.TorrentRecord{
			"t1": {ID: "t1", Name: "A", Status: domain.TorrentActive, DoneBytes: 500,
				Files: []domain.FileRef{{Index: 0, Path: "a.mp4", Length: 1000, BytesCompleted: 500}}},
			"t2": {ID: "t2", Name: "B", Status: domain.TorrentActive, DoneBytes: 2000,
				Files: []domain.FileRef{{Index: 0, Path: "b.mp4", Length: 2000, BytesCompleted: 2000}}},
			"t3": {ID: "t3", Name: "C", Status: domain.TorrentActive, DoneBytes: 100,
				Files: []domain.FileRef{{Index: 0, Path: "c.mp4", Length: 500, BytesCompleted: 100}}},
		},
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}
	s.sync(context.Background())

	// t1: 800 > 500 → updated
	// t2: 2000 == 2000 → no change
	// t3: 250 > 100 → updated
	if len(repo.updateProgCalls) != 2 {
		t.Fatalf("expected 2 update calls, got %d", len(repo.updateProgCalls))
	}

	ids := make(map[domain.TorrentID]bool)
	for _, c := range repo.updateProgCalls {
		ids[c.ID] = true
	}
	if !ids["t1"] || !ids["t3"] {
		t.Fatalf("expected updates for t1 and t3, got %v", ids)
	}
}

func TestSyncStateSyncEmptyEngineFiles(t *testing.T) {
	// Engine has no files, record has name → no files update, no name derivation.
	engine := &fakeSyncEngine{
		sessions: []domain.TorrentID{"t1"},
		states: map[domain.TorrentID]domain.SessionState{
			"t1": {ID: "t1", Status: domain.TorrentActive, Files: nil},
		},
	}
	repo := &fakeSyncRepo{
		records: map[domain.TorrentID]domain.TorrentRecord{
			"t1": {ID: "t1", Name: "test", Status: domain.TorrentActive, DoneBytes: 0},
		},
	}
	s := SyncState{Engine: engine, Repo: repo, Logger: discardLogger(), Interval: time.Second}
	s.sync(context.Background())

	if len(repo.updateProgCalls) != 0 {
		t.Fatalf("expected no update calls, got %d", len(repo.updateProgCalls))
	}
}
