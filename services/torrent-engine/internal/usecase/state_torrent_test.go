package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

type fakeStateEngine struct {
	states     map[domain.TorrentID]domain.SessionState
	list       []domain.TorrentID
	stateErr   error
	listErr    error
	stateCalls []domain.TorrentID
}

func (f *fakeStateEngine) Open(ctx context.Context, src domain.TorrentSource) (ports.Session, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeStateEngine) Close() error { return nil }

func (f *fakeStateEngine) GetSessionState(ctx context.Context, id domain.TorrentID) (domain.SessionState, error) {
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

func (f *fakeStateEngine) GetSession(ctx context.Context, id domain.TorrentID) (ports.Session, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeStateEngine) ListActiveSessions(ctx context.Context) ([]domain.TorrentID, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.list, nil
}

func (f *fakeStateEngine) StopSession(ctx context.Context, id domain.TorrentID) error   { return nil }
func (f *fakeStateEngine) StartSession(ctx context.Context, id domain.TorrentID) error  { return nil }
func (f *fakeStateEngine) RemoveSession(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeStateEngine) SetPiecePriority(ctx context.Context, id domain.TorrentID, file domain.FileRef, r domain.Range, prio domain.Priority) error {
	return nil
}

func (f *fakeStateEngine) ListSessions(ctx context.Context) ([]domain.TorrentID, error) {
	return nil, nil
}

func (f *fakeStateEngine) FocusSession(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeStateEngine) UnfocusAll(ctx context.Context) error                        { return nil }
func (f *fakeStateEngine) GetSessionMode(ctx context.Context, id domain.TorrentID) (domain.SessionMode, error) {
	return domain.ModeDownloading, nil
}
func (f *fakeStateEngine) SetDownloadRateLimit(ctx context.Context, id domain.TorrentID, bytesPerSec int64) error {
	return nil
}

func TestGetTorrentState(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	engine := &fakeStateEngine{
		states: map[domain.TorrentID]domain.SessionState{
			"t1": {ID: "t1", Status: domain.TorrentActive, Progress: 0.5, UpdatedAt: now},
		},
	}
	uc := GetTorrentState{Engine: engine}

	state, err := uc.Execute(context.Background(), "t1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if state.ID != "t1" || state.Status != domain.TorrentActive {
		t.Fatalf("state mismatch: %+v", state)
	}
}

func TestGetTorrentStateNotFound(t *testing.T) {
	engine := &fakeStateEngine{states: map[domain.TorrentID]domain.SessionState{}}
	uc := GetTorrentState{Engine: engine}

	_, err := uc.Execute(context.Background(), "t404")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestListActiveTorrentStates(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	engine := &fakeStateEngine{
		list: []domain.TorrentID{"t1", "t2"},
		states: map[domain.TorrentID]domain.SessionState{
			"t1": {ID: "t1", Status: domain.TorrentActive, Progress: 0.2, UpdatedAt: now},
			"t2": {ID: "t2", Status: domain.TorrentActive, Progress: 0.8, UpdatedAt: now},
		},
	}
	uc := ListActiveTorrentStates{Engine: engine}

	states, err := uc.Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("expected 2 states, got %d", len(states))
	}
	if engine.stateCalls[0] != "t1" || engine.stateCalls[1] != "t2" {
		t.Fatalf("state calls mismatch: %+v", engine.stateCalls)
	}
}

func TestListActiveTorrentStatesEngineError(t *testing.T) {
	engine := &fakeStateEngine{listErr: errors.New("list failed")}
	uc := ListActiveTorrentStates{Engine: engine}

	_, err := uc.Execute(context.Background())
	if err == nil {
		t.Fatalf("expected error")
	}
}
