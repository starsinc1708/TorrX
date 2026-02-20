package usecase

import (
	"context"
	"errors"
	"testing"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

type fakeRestoreEngine struct {
	openSrc    domain.TorrentSource
	openCalled int
	openErr    error
	session    ports.Session
}

func (f *fakeRestoreEngine) Open(ctx context.Context, src domain.TorrentSource) (ports.Session, error) {
	f.openCalled++
	f.openSrc = src
	if f.openErr != nil {
		return nil, f.openErr
	}
	return f.session, nil
}
func (f *fakeRestoreEngine) Close() error { return nil }
func (f *fakeRestoreEngine) GetSessionState(ctx context.Context, id domain.TorrentID) (domain.SessionState, error) {
	return domain.SessionState{}, nil
}
func (f *fakeRestoreEngine) GetSession(ctx context.Context, id domain.TorrentID) (ports.Session, error) {
	return nil, nil
}
func (f *fakeRestoreEngine) ListActiveSessions(ctx context.Context) ([]domain.TorrentID, error) {
	return nil, nil
}
func (f *fakeRestoreEngine) StopSession(ctx context.Context, id domain.TorrentID) error  { return nil }
func (f *fakeRestoreEngine) StartSession(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeRestoreEngine) RemoveSession(ctx context.Context, id domain.TorrentID) error {
	return nil
}
func (f *fakeRestoreEngine) SetPiecePriority(ctx context.Context, id domain.TorrentID, file domain.FileRef, r domain.Range, prio domain.Priority) error {
	return nil
}
func (f *fakeRestoreEngine) ListSessions(ctx context.Context) ([]domain.TorrentID, error) {
	return nil, nil
}
func (f *fakeRestoreEngine) FocusSession(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeRestoreEngine) UnfocusAll(ctx context.Context) error                        { return nil }
func (f *fakeRestoreEngine) GetSessionMode(ctx context.Context, id domain.TorrentID) (domain.SessionMode, error) {
	return domain.ModeIdle, nil
}
func (f *fakeRestoreEngine) SetDownloadRateLimit(ctx context.Context, id domain.TorrentID, bytesPerSec int64) error {
	return nil
}

// fakeSession is defined in create_torrent_test.go (same package).
// We reuse it here for openSessionFromRecord tests.

// --- hasSource ---

func TestHasSource(t *testing.T) {
	tests := []struct {
		name   string
		src    domain.TorrentSource
		expect bool
	}{
		{"magnet_only", domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:abc123"}, true},
		{"torrent_only", domain.TorrentSource{Torrent: "/path/to/file.torrent"}, true},
		{"both", domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:abc", Torrent: "/file.torrent"}, true},
		{"neither", domain.TorrentSource{}, false},
		{"whitespace_magnet", domain.TorrentSource{Magnet: "   "}, false},
		{"whitespace_torrent", domain.TorrentSource{Torrent: "  \t  "}, false},
		{"whitespace_both", domain.TorrentSource{Magnet: "  ", Torrent: " "}, false},
		{"empty_magnet_valid_torrent", domain.TorrentSource{Magnet: "", Torrent: "file.torrent"}, true},
		{"valid_magnet_empty_torrent", domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:abc", Torrent: ""}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasSource(tt.src)
			if got != tt.expect {
				t.Fatalf("hasSource(%+v) = %v, want %v", tt.src, got, tt.expect)
			}
		})
	}
}

// --- openSessionFromRecord ---

func TestOpenSessionFromRecordWithMagnet(t *testing.T) {
	sess := &fakeSession{id: "t1"}
	engine := &fakeRestoreEngine{session: sess}
	record := domain.TorrentRecord{
		ID:     "t1",
		Source: domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:abc123"},
	}

	result, err := openSessionFromRecord(context.Background(), engine, record)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != sess {
		t.Fatal("expected returned session to be the fake session")
	}
	if engine.openCalled != 1 {
		t.Fatalf("engine.Open called %d times, want 1", engine.openCalled)
	}
	if engine.openSrc.Magnet != "magnet:?xt=urn:btih:abc123" {
		t.Fatalf("wrong source magnet: %q", engine.openSrc.Magnet)
	}
}

func TestOpenSessionFromRecordWithTorrent(t *testing.T) {
	sess := &fakeSession{id: "t1"}
	engine := &fakeRestoreEngine{session: sess}
	record := domain.TorrentRecord{
		ID:     "t1",
		Source: domain.TorrentSource{Torrent: "/tmp/sintel.torrent"},
	}

	result, err := openSessionFromRecord(context.Background(), engine, record)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != sess {
		t.Fatal("expected returned session to be the fake session")
	}
	if engine.openSrc.Torrent != "/tmp/sintel.torrent" {
		t.Fatalf("wrong source torrent: %q", engine.openSrc.Torrent)
	}
}

func TestOpenSessionFromRecordNoSource(t *testing.T) {
	engine := &fakeRestoreEngine{}
	record := domain.TorrentRecord{
		ID:     "t1",
		Source: domain.TorrentSource{}, // empty
	}

	_, err := openSessionFromRecord(context.Background(), engine, record)
	if !errors.Is(err, errMissingSource) {
		t.Fatalf("expected errMissingSource, got %v", err)
	}
	if engine.openCalled != 0 {
		t.Fatalf("engine.Open should not be called when no source")
	}
}

func TestOpenSessionFromRecordWhitespaceSource(t *testing.T) {
	engine := &fakeRestoreEngine{}
	record := domain.TorrentRecord{
		ID:     "t1",
		Source: domain.TorrentSource{Magnet: "   ", Torrent: " "},
	}

	_, err := openSessionFromRecord(context.Background(), engine, record)
	if !errors.Is(err, errMissingSource) {
		t.Fatalf("expected errMissingSource, got %v", err)
	}
}

func TestOpenSessionFromRecordEngineError(t *testing.T) {
	engine := &fakeRestoreEngine{openErr: errors.New("engine failed to open")}
	record := domain.TorrentRecord{
		ID:     "t1",
		Source: domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:abc123"},
	}

	_, err := openSessionFromRecord(context.Background(), engine, record)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "engine failed to open" {
		t.Fatalf("unexpected error: %v", err)
	}
}
