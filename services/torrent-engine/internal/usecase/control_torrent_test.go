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
