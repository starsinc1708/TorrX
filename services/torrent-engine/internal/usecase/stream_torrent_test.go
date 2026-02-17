package usecase

import (
	"context"
	"errors"
	"io"
	"testing"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

type fakeStreamReader struct {
	ctx        context.Context
	readahead  int64
	responsive bool
	pos        int64
}

func (f *fakeStreamReader) SetContext(ctx context.Context) { f.ctx = ctx }
func (f *fakeStreamReader) SetReadahead(n int64)           { f.readahead = n }
func (f *fakeStreamReader) SetResponsive()                 { f.responsive = true }
func (f *fakeStreamReader) Read(p []byte) (int, error)     { return 0, io.EOF }
func (f *fakeStreamReader) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.pos = off
	case io.SeekCurrent:
		f.pos += off
	case io.SeekEnd:
		f.pos = off
	default:
		return 0, errors.New("invalid whence")
	}
	if f.pos < 0 {
		f.pos = 0
	}
	return f.pos, nil
}
func (f *fakeStreamReader) Close() error { return nil }

type fakeStreamSession struct {
	files     []domain.FileRef
	reader    ports.StreamReader
	lastFile  domain.FileRef
	lastRange domain.Range
	lastPrio  domain.Priority
	ranges    []domain.Range
	selectErr error
}

func (s *fakeStreamSession) ID() domain.TorrentID { return "t1" }
func (s *fakeStreamSession) Files() []domain.FileRef {
	return append([]domain.FileRef(nil), s.files...)
}
func (s *fakeStreamSession) SelectFile(index int) (domain.FileRef, error) {
	if s.selectErr != nil {
		return domain.FileRef{}, s.selectErr
	}
	if index < 0 || index >= len(s.files) {
		return domain.FileRef{}, domain.ErrNotFound
	}
	return s.files[index], nil
}
func (s *fakeStreamSession) SetPiecePriority(file domain.FileRef, r domain.Range, prio domain.Priority) {
	s.lastRange = r
	s.lastPrio = prio
	s.ranges = append(s.ranges, r)
}
func (s *fakeStreamSession) Start() error { return nil }
func (s *fakeStreamSession) Stop() error  { return nil }
func (s *fakeStreamSession) NewReader(file domain.FileRef) (ports.StreamReader, error) {
	s.lastFile = file
	if s.reader == nil {
		return nil, errors.New("no reader")
	}
	return s.reader, nil
}

type fakeStreamEngine struct {
	session ports.Session
	err     error
}

func (f *fakeStreamEngine) Open(ctx context.Context, src domain.TorrentSource) (ports.Session, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeStreamEngine) Close() error { return nil }
func (f *fakeStreamEngine) GetSessionState(ctx context.Context, id domain.TorrentID) (domain.SessionState, error) {
	return domain.SessionState{}, nil
}
func (f *fakeStreamEngine) ListActiveSessions(ctx context.Context) ([]domain.TorrentID, error) {
	return nil, nil
}
func (f *fakeStreamEngine) StopSession(ctx context.Context, id domain.TorrentID) error   { return nil }
func (f *fakeStreamEngine) StartSession(ctx context.Context, id domain.TorrentID) error  { return nil }
func (f *fakeStreamEngine) RemoveSession(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeStreamEngine) SetPiecePriority(ctx context.Context, id domain.TorrentID, file domain.FileRef, r domain.Range, prio domain.Priority) error {
	return nil
}

func (f *fakeStreamEngine) ListSessions(ctx context.Context) ([]domain.TorrentID, error) {
	return nil, nil
}

func (f *fakeStreamEngine) FocusSession(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeStreamEngine) UnfocusAll(ctx context.Context) error                        { return nil }
func (f *fakeStreamEngine) GetSessionMode(ctx context.Context, id domain.TorrentID) (domain.SessionMode, error) {
	return domain.ModeDownloading, nil
}

func (f *fakeStreamEngine) GetSession(ctx context.Context, id domain.TorrentID) (ports.Session, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.session, nil
}

func TestStreamTorrentSuccess(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "Sintel/Sintel.mp4", Length: 100}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}

	uc := StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}
	result, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.File.Index != 0 || result.File.Length != 100 {
		t.Fatalf("file mismatch: %+v", result.File)
	}
	if reader.ctx == nil {
		t.Fatalf("context not set")
	}
	// readahead is set to the priority window (not the raw ReadaheadBytes)
	// so the torrent client requests pieces well ahead of playback.
	expectedWindow := streamPriorityWindow(2<<20, 100)
	if reader.readahead != expectedWindow {
		t.Fatalf("readahead not set to priority window: got %d, want %d", reader.readahead, expectedWindow)
	}
	if !reader.responsive {
		t.Fatalf("responsive not set")
	}
	if session.lastPrio != domain.PriorityHigh {
		t.Fatalf("priority not set to high")
	}
	if session.lastRange.Length <= 0 {
		t.Fatalf("priority range not set")
	}
}

func TestStreamTorrentNotFound(t *testing.T) {
	uc := StreamTorrent{Engine: &fakeStreamEngine{err: domain.ErrNotFound}}
	_, err := uc.Execute(context.Background(), "t404", 0)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestStreamTorrentInvalidFileIndex(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "Sintel/Sintel.mp4", Length: 100}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}
	uc := StreamTorrent{Engine: engine}

	_, err := uc.Execute(context.Background(), "t1", 5)
	if !errors.Is(err, ErrInvalidFileIndex) {
		t.Fatalf("expected invalid file index, got %v", err)
	}
}

func TestStreamTorrentSlidingPriorityOnSeek(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "big/movie.mkv", Length: 1024}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}

	uc := StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}
	result, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(session.ranges) == 0 {
		t.Fatalf("expected initial priority range")
	}

	if _, err := result.Reader.Seek(64<<20, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}

	if len(session.ranges) < 2 {
		t.Fatalf("expected priority to be updated after seek, got %d calls", len(session.ranges))
	}
	last := session.ranges[len(session.ranges)-1]
	if last.Off <= 0 {
		t.Fatalf("expected sliding priority offset > 0, got %d", last.Off)
	}
	// After seek, the adaptive reader temporarily doubles the window (seek boost).
	baseWindow := streamPriorityWindow(uc.ReadaheadBytes, 1024)
	boostedWindow := baseWindow * 2
	if boostedWindow > maxPriorityWindowBytes {
		boostedWindow = maxPriorityWindowBytes
	}
	if last.Length != boostedWindow {
		t.Fatalf("unexpected priority window length after seek: got %d, want %d (boosted)", last.Length, boostedWindow)
	}
}
