package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

type fakeControlEngine struct {
	startCalled  int
	stopCalled   int
	removeCalled int
	lastID       domain.TorrentID
	startErr     error
	stopErr      error
	removeErr    error
}

func (f *fakeControlEngine) Open(ctx context.Context, src domain.TorrentSource) (ports.Session, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeControlEngine) Close() error { return nil }

func (f *fakeControlEngine) GetSessionState(ctx context.Context, id domain.TorrentID) (domain.SessionState, error) {
	return domain.SessionState{}, nil
}

func (f *fakeControlEngine) GetSession(ctx context.Context, id domain.TorrentID) (ports.Session, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeControlEngine) ListActiveSessions(ctx context.Context) ([]domain.TorrentID, error) {
	return nil, nil
}

func (f *fakeControlEngine) StopSession(ctx context.Context, id domain.TorrentID) error {
	f.stopCalled++
	f.lastID = id
	return f.stopErr
}

func (f *fakeControlEngine) StartSession(ctx context.Context, id domain.TorrentID) error {
	f.startCalled++
	f.lastID = id
	return f.startErr
}

func (f *fakeControlEngine) RemoveSession(ctx context.Context, id domain.TorrentID) error {
	f.removeCalled++
	f.lastID = id
	return f.removeErr
}

func (f *fakeControlEngine) SetPiecePriority(ctx context.Context, id domain.TorrentID, file domain.FileRef, r domain.Range, prio domain.Priority) error {
	return nil
}

func (f *fakeControlEngine) ListSessions(ctx context.Context) ([]domain.TorrentID, error) {
	return nil, nil
}

func (f *fakeControlEngine) FocusSession(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeControlEngine) UnfocusAll(ctx context.Context) error                        { return nil }
func (f *fakeControlEngine) GetSessionMode(ctx context.Context, id domain.TorrentID) (domain.SessionMode, error) {
	return domain.ModeDownloading, nil
}
func (f *fakeControlEngine) SetDownloadRateLimit(ctx context.Context, id domain.TorrentID, bytesPerSec int64) error {
	return nil
}

type fakeControlRepo struct {
	get         domain.TorrentRecord
	getErr      error
	updateErr   error
	deleteErr   error
	updateCalls int
	deleteCalls int
	updated     domain.TorrentRecord
	deletedID   domain.TorrentID
}

func (f *fakeControlRepo) Create(ctx context.Context, t domain.TorrentRecord) error { return nil }

func (f *fakeControlRepo) Update(ctx context.Context, t domain.TorrentRecord) error {
	f.updateCalls++
	f.updated = t
	return f.updateErr
}

func (f *fakeControlRepo) UpdateProgress(ctx context.Context, id domain.TorrentID, update domain.ProgressUpdate) error {
	return nil
}

func (f *fakeControlRepo) Get(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error) {
	if f.getErr != nil {
		return domain.TorrentRecord{}, f.getErr
	}
	return f.get, nil
}

func (f *fakeControlRepo) List(ctx context.Context, filter domain.TorrentFilter) ([]domain.TorrentRecord, error) {
	return nil, nil
}

func (f *fakeControlRepo) GetMany(ctx context.Context, ids []domain.TorrentID) ([]domain.TorrentRecord, error) {
	return nil, nil
}

func (f *fakeControlRepo) Delete(ctx context.Context, id domain.TorrentID) error {
	f.deleteCalls++
	f.deletedID = id
	return f.deleteErr
}

func (f *fakeControlRepo) UpdateTags(ctx context.Context, id domain.TorrentID, tags []string) error {
	return nil
}

func TestStartTorrent(t *testing.T) {
	now := time.Date(2026, 2, 10, 13, 0, 0, 0, time.UTC)
	engine := &fakeControlEngine{}
	repo := &fakeControlRepo{
		get: domain.TorrentRecord{ID: "t1", Name: "Sintel", Status: domain.TorrentStopped, UpdatedAt: now.Add(-time.Hour)},
	}
	uc := StartTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return now }}

	record, err := uc.Execute(context.Background(), "t1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if engine.startCalled != 1 || engine.lastID != "t1" {
		t.Fatalf("engine not called")
	}
	if repo.updateCalls != 1 {
		t.Fatalf("repo update not called")
	}
	if record.Status != domain.TorrentActive || repo.updated.Status != domain.TorrentActive {
		t.Fatalf("status not updated")
	}
	if record.UpdatedAt != now {
		t.Fatalf("updatedAt not set")
	}
}

func TestStartTorrentNotFound(t *testing.T) {
	uc := StartTorrent{Engine: &fakeControlEngine{}, Repo: &fakeControlRepo{getErr: domain.ErrNotFound}}
	if _, err := uc.Execute(context.Background(), "t1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestStartTorrentEngineError(t *testing.T) {
	engine := &fakeControlEngine{startErr: errors.New("engine broke")}
	repo := &fakeControlRepo{
		get: domain.TorrentRecord{ID: "t1", Status: domain.TorrentStopped},
	}
	uc := StartTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return time.Unix(0, 0) }}

	_, err := uc.Execute(context.Background(), "t1")
	if !errors.Is(err, ErrEngine) {
		t.Fatalf("expected engine error, got %v", err)
	}
}

func TestStartTorrentRepoUpdateError(t *testing.T) {
	engine := &fakeControlEngine{}
	repo := &fakeControlRepo{
		get:       domain.TorrentRecord{ID: "t1", Status: domain.TorrentStopped},
		updateErr: errors.New("update failed"),
	}
	uc := StartTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return time.Unix(0, 0) }}

	_, err := uc.Execute(context.Background(), "t1")
	if !errors.Is(err, ErrRepository) {
		t.Fatalf("expected repo error, got %v", err)
	}
}

func TestStartTorrentRepoGetError(t *testing.T) {
	uc := StartTorrent{Engine: &fakeControlEngine{}, Repo: &fakeControlRepo{getErr: errors.New("db down")}}
	_, err := uc.Execute(context.Background(), "t1")
	if !errors.Is(err, ErrRepository) {
		t.Fatalf("expected repo error, got %v", err)
	}
}

func TestStopTorrent(t *testing.T) {
	now := time.Date(2026, 2, 10, 13, 0, 0, 0, time.UTC)
	engine := &fakeControlEngine{}
	repo := &fakeControlRepo{
		get: domain.TorrentRecord{ID: "t1", Name: "Sintel", Status: domain.TorrentActive, UpdatedAt: now.Add(-time.Hour)},
	}
	uc := StopTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return now }}

	record, err := uc.Execute(context.Background(), "t1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if engine.stopCalled != 1 || engine.lastID != "t1" {
		t.Fatalf("engine not called")
	}
	if repo.updateCalls != 1 {
		t.Fatalf("repo update not called")
	}
	if record.Status != domain.TorrentStopped || repo.updated.Status != domain.TorrentStopped {
		t.Fatalf("status not updated")
	}
	if record.UpdatedAt != now {
		t.Fatalf("updatedAt not set")
	}
}

func TestStopTorrentNotFound(t *testing.T) {
	uc := StopTorrent{Engine: &fakeControlEngine{}, Repo: &fakeControlRepo{getErr: domain.ErrNotFound}}
	_, err := uc.Execute(context.Background(), "t1")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestStopTorrentRepoGetError(t *testing.T) {
	uc := StopTorrent{Engine: &fakeControlEngine{}, Repo: &fakeControlRepo{getErr: errors.New("db down")}}
	_, err := uc.Execute(context.Background(), "t1")
	if !errors.Is(err, ErrRepository) {
		t.Fatalf("expected repo error, got %v", err)
	}
}

func TestStopTorrentEngineError(t *testing.T) {
	engine := &fakeControlEngine{stopErr: errors.New("engine broke")}
	repo := &fakeControlRepo{
		get: domain.TorrentRecord{ID: "t1", Status: domain.TorrentActive},
	}
	uc := StopTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return time.Unix(0, 0) }}

	_, err := uc.Execute(context.Background(), "t1")
	if !errors.Is(err, ErrEngine) {
		t.Fatalf("expected engine error, got %v", err)
	}
}

func TestStopTorrentRepoUpdateError(t *testing.T) {
	engine := &fakeControlEngine{}
	repo := &fakeControlRepo{
		get:       domain.TorrentRecord{ID: "t1", Status: domain.TorrentActive},
		updateErr: errors.New("update failed"),
	}
	uc := StopTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return time.Unix(0, 0) }}

	_, err := uc.Execute(context.Background(), "t1")
	if !errors.Is(err, ErrRepository) {
		t.Fatalf("expected repo error, got %v", err)
	}
}

func TestStopTorrentEngineNotFoundIgnored(t *testing.T) {
	now := time.Date(2026, 2, 10, 13, 0, 0, 0, time.UTC)
	engine := &fakeControlEngine{stopErr: domain.ErrNotFound}
	repo := &fakeControlRepo{
		get: domain.TorrentRecord{ID: "t1", Status: domain.TorrentActive},
	}
	uc := StopTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return now }}

	record, err := uc.Execute(context.Background(), "t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if record.Status != domain.TorrentStopped {
		t.Fatalf("status = %q", record.Status)
	}
}

func TestDeleteTorrentRemovesFiles(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Join("folder", "video.mp4")
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	engine := &fakeControlEngine{}
	repo := &fakeControlRepo{
		get: domain.TorrentRecord{
			ID:    "t1",
			Files: []domain.FileRef{{Index: 0, Path: filepath.ToSlash(rel), Length: 4}},
		},
	}
	uc := DeleteTorrent{Engine: engine, Repo: repo, DataDir: dir}

	if err := uc.Execute(context.Background(), "t1", true); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if engine.removeCalled != 1 {
		t.Fatalf("engine remove not called")
	}
	if repo.deleteCalls != 1 || repo.deletedID != "t1" {
		t.Fatalf("repo delete not called")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file not removed")
	}
}

func TestDeleteTorrentRemovesEmptyParentDirectories(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Join("series", "season1", "episode1.mkv")
	filePath := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	repo := &fakeControlRepo{
		get: domain.TorrentRecord{
			ID:    "t1",
			Files: []domain.FileRef{{Index: 0, Path: filepath.ToSlash(rel), Length: 4}},
		},
	}
	uc := DeleteTorrent{Engine: &fakeControlEngine{}, Repo: repo, DataDir: dir}

	if err := uc.Execute(context.Background(), "t1", true); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	seasonDir := filepath.Join(dir, "series", "season1")
	if _, err := os.Stat(seasonDir); !os.IsNotExist(err) {
		t.Fatalf("season directory should be removed when empty")
	}
	seriesDir := filepath.Join(dir, "series")
	if _, err := os.Stat(seriesDir); !os.IsNotExist(err) {
		t.Fatalf("series directory should be removed when empty")
	}
}

func TestDeleteTorrentKeepsNonEmptyParentDirectories(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Join("series", "season1", "episode1.mkv")
	filePath := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write episode: %v", err)
	}
	keepPath := filepath.Join(dir, "series", "season1", "keep.txt")
	if err := os.WriteFile(keepPath, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write keep file: %v", err)
	}

	repo := &fakeControlRepo{
		get: domain.TorrentRecord{
			ID:    "t1",
			Files: []domain.FileRef{{Index: 0, Path: filepath.ToSlash(rel), Length: 4}},
		},
	}
	uc := DeleteTorrent{Engine: &fakeControlEngine{}, Repo: repo, DataDir: dir}

	if err := uc.Execute(context.Background(), "t1", true); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Stat(keepPath); err != nil {
		t.Fatalf("keep file must stay: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "series", "season1")); err != nil {
		t.Fatalf("non-empty parent directory must stay: %v", err)
	}
}

func TestDeleteTorrentKeepFiles(t *testing.T) {
	dir := t.TempDir()
	rel := "video.mp4"
	path := filepath.Join(dir, rel)
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	engine := &fakeControlEngine{}
	repo := &fakeControlRepo{
		get: domain.TorrentRecord{
			ID:    "t1",
			Files: []domain.FileRef{{Index: 0, Path: rel, Length: 4}},
		},
	}
	uc := DeleteTorrent{Engine: engine, Repo: repo, DataDir: dir}

	if err := uc.Execute(context.Background(), "t1", false); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if repo.deleteCalls != 1 {
		t.Fatalf("repo delete not called")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should not be removed: %v", err)
	}
}

func TestDeleteTorrentNotFound(t *testing.T) {
	uc := DeleteTorrent{
		Engine: &fakeControlEngine{},
		Repo:   &fakeControlRepo{getErr: domain.ErrNotFound},
	}
	err := uc.Execute(context.Background(), "t1", false)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestDeleteTorrentRepoGetError(t *testing.T) {
	uc := DeleteTorrent{
		Engine: &fakeControlEngine{},
		Repo:   &fakeControlRepo{getErr: errors.New("db down")},
	}
	err := uc.Execute(context.Background(), "t1", false)
	if !errors.Is(err, ErrRepository) {
		t.Fatalf("expected repo error, got %v", err)
	}
}

func TestDeleteTorrentEngineNotFoundIgnored(t *testing.T) {
	engine := &fakeControlEngine{removeErr: domain.ErrNotFound}
	repo := &fakeControlRepo{
		get: domain.TorrentRecord{ID: "t1"},
	}
	uc := DeleteTorrent{Engine: engine, Repo: repo}

	if err := uc.Execute(context.Background(), "t1", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if engine.removeCalled != 1 {
		t.Fatalf("engine remove not called")
	}
	if repo.deleteCalls != 1 {
		t.Fatalf("repo delete not called")
	}
}

func TestDeleteTorrentEngineError(t *testing.T) {
	engine := &fakeControlEngine{removeErr: errors.New("engine broke")}
	repo := &fakeControlRepo{
		get: domain.TorrentRecord{ID: "t1"},
	}
	uc := DeleteTorrent{Engine: engine, Repo: repo}

	err := uc.Execute(context.Background(), "t1", false)
	if !errors.Is(err, ErrEngine) {
		t.Fatalf("expected engine error, got %v", err)
	}
}

func TestDeleteTorrentRepoDeleteError(t *testing.T) {
	engine := &fakeControlEngine{}
	repo := &fakeControlRepo{
		get:       domain.TorrentRecord{ID: "t1"},
		deleteErr: errors.New("delete failed"),
	}
	uc := DeleteTorrent{Engine: engine, Repo: repo}

	err := uc.Execute(context.Background(), "t1", false)
	if !errors.Is(err, ErrRepository) {
		t.Fatalf("expected repo error, got %v", err)
	}
}

func TestDeleteTorrentNilEngine(t *testing.T) {
	repo := &fakeControlRepo{
		get: domain.TorrentRecord{ID: "t1"},
	}
	uc := DeleteTorrent{Engine: nil, Repo: repo}

	if err := uc.Execute(context.Background(), "t1", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.deleteCalls != 1 {
		t.Fatalf("repo delete not called")
	}
}

func TestDeleteTorrentPathTraversal(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name string
		path string
	}{
		{"parent_escape", "../../../etc/passwd"},
		{"empty_path", "  "},
	}

	// Platform-specific absolute path test
	if filepath.IsAbs("C:\\Windows\\System32") {
		tests = append(tests, struct {
			name string
			path string
		}{"absolute_path_win", "C:\\Windows\\System32\\file"})
	} else {
		tests = append(tests, struct {
			name string
			path string
		}{"absolute_path_unix", "/etc/passwd"})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeControlRepo{
				get: domain.TorrentRecord{
					ID:    "t1",
					Files: []domain.FileRef{{Index: 0, Path: tt.path, Length: 1}},
				},
			}
			uc := DeleteTorrent{Engine: &fakeControlEngine{}, Repo: repo, DataDir: dir}

			err := uc.Execute(context.Background(), "t1", true)
			if err == nil {
				t.Fatalf("expected error for path %q, got nil", tt.path)
			}
		})
	}
}

func TestDeleteTorrentEmptyDataDir(t *testing.T) {
	repo := &fakeControlRepo{
		get: domain.TorrentRecord{
			ID:    "t1",
			Files: []domain.FileRef{{Index: 0, Path: "video.mp4", Length: 1}},
		},
	}
	uc := DeleteTorrent{Engine: &fakeControlEngine{}, Repo: repo, DataDir: ""}

	err := uc.Execute(context.Background(), "t1", true)
	if err == nil {
		t.Fatalf("expected error for empty DataDir")
	}
}

func TestDeleteTorrentMissingFileIgnored(t *testing.T) {
	dir := t.TempDir()
	repo := &fakeControlRepo{
		get: domain.TorrentRecord{
			ID:    "t1",
			Files: []domain.FileRef{{Index: 0, Path: "nonexistent.mp4", Length: 1}},
		},
	}
	uc := DeleteTorrent{Engine: &fakeControlEngine{}, Repo: repo, DataDir: dir}

	if err := uc.Execute(context.Background(), "t1", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteTorrentMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	goodFile := "good.mp4"
	goodPath := filepath.Join(dir, goodFile)
	if err := os.WriteFile(goodPath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	repo := &fakeControlRepo{
		get: domain.TorrentRecord{
			ID: "t1",
			Files: []domain.FileRef{
				{Index: 0, Path: goodFile, Length: 4},
				{Index: 1, Path: "nonexistent.mp4", Length: 1},
			},
		},
	}
	uc := DeleteTorrent{Engine: &fakeControlEngine{}, Repo: repo, DataDir: dir}

	if err := uc.Execute(context.Background(), "t1", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(goodPath); !os.IsNotExist(err) {
		t.Fatalf("file should be removed")
	}
}
