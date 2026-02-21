package usecase

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

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
	prios     []domain.Priority
	callFiles []domain.FileRef
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
	s.prios = append(s.prios, prio)
	s.callFiles = append(s.callFiles, file)
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

func (f *fakeStreamEngine) SetDownloadRateLimit(ctx context.Context, id domain.TorrentID, bytesPerSec int64) error {
	return nil
}
func (f *fakeStreamEngine) GetSession(ctx context.Context, id domain.TorrentID) (ports.Session, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.session, nil
}

// fakeStreamEngineWithOpen extends fakeStreamEngine with configurable Open() for repo fallback tests.
type fakeStreamEngineWithOpen struct {
	fakeStreamEngine
	openSession ports.Session
	openErr     error
	openCalled  int
	focusCalled int
	focusID     domain.TorrentID
}

func (f *fakeStreamEngineWithOpen) Open(ctx context.Context, src domain.TorrentSource) (ports.Session, error) {
	f.openCalled++
	if f.openErr != nil {
		return nil, f.openErr
	}
	return f.openSession, nil
}

func (f *fakeStreamEngineWithOpen) FocusSession(ctx context.Context, id domain.TorrentID) error {
	f.focusCalled++
	f.focusID = id
	return nil
}

// fakeStreamSessionStartErr is a session that fails on Start().
type fakeStreamSessionStartErr struct {
	fakeStreamSession
	startErr  error
	stopCalls int
}

func (s *fakeStreamSessionStartErr) Start() error { return s.startErr }
func (s *fakeStreamSessionStartErr) Stop() error  { s.stopCalls++; return nil }

// fakeStreamRepo is a minimal TorrentRepository for stream tests.
type fakeStreamRepo struct {
	record domain.TorrentRecord
	getErr error
}

func (r *fakeStreamRepo) Create(context.Context, domain.TorrentRecord) error                 { return nil }
func (r *fakeStreamRepo) Update(context.Context, domain.TorrentRecord) error                 { return nil }
func (r *fakeStreamRepo) UpdateProgress(context.Context, domain.TorrentID, domain.ProgressUpdate) error {
	return nil
}
func (r *fakeStreamRepo) Get(_ context.Context, _ domain.TorrentID) (domain.TorrentRecord, error) {
	return r.record, r.getErr
}
func (r *fakeStreamRepo) List(context.Context, domain.TorrentFilter) ([]domain.TorrentRecord, error) {
	return nil, nil
}
func (r *fakeStreamRepo) GetMany(context.Context, []domain.TorrentID) ([]domain.TorrentRecord, error) {
	return nil, nil
}
func (r *fakeStreamRepo) Delete(context.Context, domain.TorrentID) error          { return nil }
func (r *fakeStreamRepo) UpdateTags(context.Context, domain.TorrentID, []string) error { return nil }

func hasPriorityCall(s *fakeStreamSession, fileIndex int, prio domain.Priority) bool {
	for i, file := range s.callFiles {
		if i < len(s.prios) && file.Index == fileIndex && s.prios[i] == prio {
			return true
		}
	}
	return false
}

func TestStreamTorrentNilEngine(t *testing.T) {
	uc := StreamTorrent{}
	_, err := uc.Execute(context.Background(), "t1", 0)
	if err == nil || err.Error() != "engine not configured" {
		t.Fatalf("expected engine not configured error, got %v", err)
	}
}

func TestStreamTorrentEngineError(t *testing.T) {
	engine := &fakeStreamEngine{err: errors.New("connection refused")}
	uc := StreamTorrent{Engine: engine}
	_, err := uc.Execute(context.Background(), "t1", 0)
	if !errors.Is(err, ErrEngine) {
		t.Fatalf("expected engine error, got %v", err)
	}
}

func TestStreamTorrentRepoFallbackSuccess(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "movie.mp4", Length: 100}},
		reader: reader,
	}
	engine := &fakeStreamEngineWithOpen{
		fakeStreamEngine: fakeStreamEngine{err: domain.ErrNotFound},
		openSession:      session,
	}
	repo := &fakeStreamRepo{
		record: domain.TorrentRecord{
			ID:     "t1",
			Source: domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:abc"},
			Files:  []domain.FileRef{{Index: 0, Path: "movie.mp4", Length: 100}},
		},
	}

	uc := StreamTorrent{Engine: engine, Repo: repo, ReadaheadBytes: 2 << 20}
	result, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.File.Path != "movie.mp4" {
		t.Fatalf("file path mismatch: %s", result.File.Path)
	}
	if engine.openCalled != 1 {
		t.Fatalf("engine.Open should be called once, got %d", engine.openCalled)
	}
	if engine.focusCalled != 1 {
		t.Fatalf("FocusSession should be called once, got %d", engine.focusCalled)
	}
}

func TestStreamTorrentRepoFallbackNotFound(t *testing.T) {
	engine := &fakeStreamEngineWithOpen{
		fakeStreamEngine: fakeStreamEngine{err: domain.ErrNotFound},
	}
	repo := &fakeStreamRepo{getErr: domain.ErrNotFound}

	uc := StreamTorrent{Engine: engine, Repo: repo}
	_, err := uc.Execute(context.Background(), "t404", 0)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestStreamTorrentRepoFallbackRepoError(t *testing.T) {
	engine := &fakeStreamEngineWithOpen{
		fakeStreamEngine: fakeStreamEngine{err: domain.ErrNotFound},
	}
	repo := &fakeStreamRepo{getErr: errors.New("db connection lost")}

	uc := StreamTorrent{Engine: engine, Repo: repo}
	_, err := uc.Execute(context.Background(), "t1", 0)
	if !errors.Is(err, ErrRepository) {
		t.Fatalf("expected repository error, got %v", err)
	}
}

func TestStreamTorrentRepoFallbackNoRepo(t *testing.T) {
	engine := &fakeStreamEngine{err: domain.ErrNotFound}
	uc := StreamTorrent{Engine: engine} // no Repo
	_, err := uc.Execute(context.Background(), "t1", 0)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found when no repo, got %v", err)
	}
}

func TestStreamTorrentRepoFallbackMissingSource(t *testing.T) {
	engine := &fakeStreamEngineWithOpen{
		fakeStreamEngine: fakeStreamEngine{err: domain.ErrNotFound},
	}
	repo := &fakeStreamRepo{
		record: domain.TorrentRecord{ID: "t1", Source: domain.TorrentSource{}}, // no magnet or torrent
	}

	uc := StreamTorrent{Engine: engine, Repo: repo}
	_, err := uc.Execute(context.Background(), "t1", 0)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found for missing source, got %v", err)
	}
}

func TestStreamTorrentRepoFallbackOpenError(t *testing.T) {
	engine := &fakeStreamEngineWithOpen{
		fakeStreamEngine: fakeStreamEngine{err: domain.ErrNotFound},
		openErr:          errors.New("torrent engine unavailable"),
	}
	repo := &fakeStreamRepo{
		record: domain.TorrentRecord{
			ID:     "t1",
			Source: domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:abc"},
		},
	}

	uc := StreamTorrent{Engine: engine, Repo: repo}
	_, err := uc.Execute(context.Background(), "t1", 0)
	if !errors.Is(err, ErrEngine) {
		t.Fatalf("expected engine error, got %v", err)
	}
}

func TestStreamTorrentRepoFallbackStartError(t *testing.T) {
	session := &fakeStreamSessionStartErr{
		fakeStreamSession: fakeStreamSession{
			files:  []domain.FileRef{{Index: 0, Path: "movie.mp4", Length: 100}},
			reader: &fakeStreamReader{},
		},
		startErr: errors.New("start failed"),
	}
	engine := &fakeStreamEngineWithOpen{
		fakeStreamEngine: fakeStreamEngine{err: domain.ErrNotFound},
		openSession:      session,
	}
	repo := &fakeStreamRepo{
		record: domain.TorrentRecord{
			ID:     "t1",
			Source: domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:abc"},
		},
	}

	uc := StreamTorrent{Engine: engine, Repo: repo}
	_, err := uc.Execute(context.Background(), "t1", 0)
	if !errors.Is(err, ErrEngine) {
		t.Fatalf("expected engine error from Start failure, got %v", err)
	}
	if session.stopCalls != 1 {
		t.Fatalf("session.Stop should be called for cleanup, got %d calls", session.stopCalls)
	}
}

func TestStreamTorrentNewReaderNil(t *testing.T) {
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "movie.mp4", Length: 100}},
		reader: nil, // NewReader returns nil
	}
	engine := &fakeStreamEngine{session: session}
	// NewReader returns nil, error — fakeStreamSession returns nil, "no reader"
	uc := StreamTorrent{Engine: engine}
	_, err := uc.Execute(context.Background(), "t1", 0)
	if err == nil {
		t.Fatalf("expected error for nil reader")
	}
}

func TestStreamTorrentDefaultReadahead(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "movie.mp4", Length: 1 << 30}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}

	// ReadaheadBytes = 0 triggers defaultStreamReadahead.
	uc := StreamTorrent{Engine: engine, ReadaheadBytes: 0}
	result, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	expectedWindow := streamPriorityWindow(defaultStreamReadahead, 1<<30)
	if reader.readahead != expectedWindow {
		t.Fatalf("readahead = %d, want %d (default window)", reader.readahead, expectedWindow)
	}
	if result.ConsumptionRate == nil {
		t.Fatalf("ConsumptionRate should be non-nil")
	}
}

func TestExecuteRawSuccess(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "movie.mp4", Length: 100}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}

	uc := StreamTorrent{Engine: engine}
	result, err := uc.ExecuteRaw(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("ExecuteRaw: %v", err)
	}
	if result.File.Path != "movie.mp4" {
		t.Fatalf("file mismatch: %s", result.File.Path)
	}
	// ExecuteRaw returns the raw reader (not a slidingPriorityReader).
	if _, ok := result.Reader.(*slidingPriorityReader); ok {
		t.Fatalf("ExecuteRaw should return raw reader, not slidingPriorityReader")
	}
	// ConsumptionRate should be nil for raw readers.
	if result.ConsumptionRate != nil {
		t.Fatalf("ConsumptionRate should be nil for ExecuteRaw")
	}
}

func TestStreamTorrentEnforcesActiveFileOnly(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files: []domain.FileRef{
			{Index: 0, Path: "s01e01.mkv", Length: 100 << 20},
			{Index: 1, Path: "s01e02.mkv", Length: 120 << 20},
			{Index: 2, Path: "s01e03.mkv", Length: 130 << 20},
		},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}
	uc := StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}

	_, err := uc.Execute(context.Background(), "t1", 1)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !hasPriorityCall(session, 1, domain.PriorityNormal) {
		t.Fatalf("expected PriorityNormal call for active file index=1")
	}
	if !hasPriorityCall(session, 0, domain.PriorityNone) {
		t.Fatalf("expected PriorityNone call for non-active file index=0")
	}
	if !hasPriorityCall(session, 2, domain.PriorityNone) {
		t.Fatalf("expected PriorityNone call for non-active file index=2")
	}
}

func TestExecuteRawEnforcesActiveFileOnly(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files: []domain.FileRef{
			{Index: 0, Path: "movie-part1.mkv", Length: 700 << 20},
			{Index: 1, Path: "movie-part2.mkv", Length: 650 << 20},
		},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}
	uc := StreamTorrent{Engine: engine}

	_, err := uc.ExecuteRaw(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("ExecuteRaw: %v", err)
	}

	if !hasPriorityCall(session, 0, domain.PriorityNormal) {
		t.Fatalf("expected PriorityNormal call for active file index=0")
	}
	if !hasPriorityCall(session, 1, domain.PriorityNone) {
		t.Fatalf("expected PriorityNone call for non-active file index=1")
	}
}

func TestExecuteRawNilEngine(t *testing.T) {
	uc := StreamTorrent{}
	_, err := uc.ExecuteRaw(context.Background(), "t1", 0)
	if err == nil || err.Error() != "engine not configured" {
		t.Fatalf("expected engine not configured, got %v", err)
	}
}

func TestExecuteRawNotFound(t *testing.T) {
	engine := &fakeStreamEngine{err: domain.ErrNotFound}
	uc := StreamTorrent{Engine: engine}
	_, err := uc.ExecuteRaw(context.Background(), "t1", 0)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestExecuteRawRepoFallback(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: 500}},
		reader: reader,
	}
	engine := &fakeStreamEngineWithOpen{
		fakeStreamEngine: fakeStreamEngine{err: domain.ErrNotFound},
		openSession:      session,
	}
	repo := &fakeStreamRepo{
		record: domain.TorrentRecord{
			ID:     "t1",
			Source: domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:def"},
		},
	}

	uc := StreamTorrent{Engine: engine, Repo: repo}
	result, err := uc.ExecuteRaw(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("ExecuteRaw: %v", err)
	}
	if result.File.Path != "movie.mkv" {
		t.Fatalf("file mismatch: %s", result.File.Path)
	}
	if engine.openCalled != 1 {
		t.Fatalf("engine.Open should be called once")
	}
}

func TestExecuteRawInvalidFileIndex(t *testing.T) {
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "movie.mp4", Length: 100}},
		reader: &fakeStreamReader{},
	}
	engine := &fakeStreamEngine{session: session}
	uc := StreamTorrent{Engine: engine}

	_, err := uc.ExecuteRaw(context.Background(), "t1", 99)
	if !errors.Is(err, ErrInvalidFileIndex) {
		t.Fatalf("expected invalid file index, got %v", err)
	}
}

func TestExecuteRawNilReader(t *testing.T) {
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "movie.mp4", Length: 100}},
		reader: nil,
	}
	engine := &fakeStreamEngine{session: session}
	uc := StreamTorrent{Engine: engine}

	_, err := uc.ExecuteRaw(context.Background(), "t1", 0)
	if err == nil {
		t.Fatalf("expected error for nil reader")
	}
}

func TestExecuteRawEngineError(t *testing.T) {
	engine := &fakeStreamEngine{err: errors.New("disk full")}
	uc := StreamTorrent{Engine: engine}
	_, err := uc.ExecuteRaw(context.Background(), "t1", 0)
	if !errors.Is(err, ErrEngine) {
		t.Fatalf("expected engine error, got %v", err)
	}
}

func TestExecuteRawRepoFallbackStartError(t *testing.T) {
	session := &fakeStreamSessionStartErr{
		fakeStreamSession: fakeStreamSession{
			files:  []domain.FileRef{{Index: 0, Path: "movie.mp4", Length: 100}},
			reader: &fakeStreamReader{},
		},
		startErr: errors.New("cannot start"),
	}
	engine := &fakeStreamEngineWithOpen{
		fakeStreamEngine: fakeStreamEngine{err: domain.ErrNotFound},
		openSession:      session,
	}
	repo := &fakeStreamRepo{
		record: domain.TorrentRecord{
			ID:     "t1",
			Source: domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:abc"},
		},
	}

	uc := StreamTorrent{Engine: engine, Repo: repo}
	_, err := uc.ExecuteRaw(context.Background(), "t1", 0)
	if !errors.Is(err, ErrEngine) {
		t.Fatalf("expected engine error, got %v", err)
	}
	if session.stopCalls != 1 {
		t.Fatalf("session.Stop should be called, got %d", session.stopCalls)
	}
}

func TestStreamPriorityWindowCases(t *testing.T) {
	tests := []struct {
		name       string
		readahead  int64
		fileLength int64
		want       int64
	}{
		{"default readahead", 0, 100, defaultStreamReadahead * priorityWindowMultiplier},              // 16MB×4=64MB
		{"small readahead", 2 << 20, 100, minPriorityWindowBytes},                                    // 2MB×4=8MB→clamped to 32MB
		{"large readahead", 128 << 20, 100, maxPriorityWindowBytes},                                  // 128MB×4=512MB→clamped to 256MB
		{"1pct scaling", 16 << 20, 50 << 30, maxPriorityWindowBytes},                                 // 50GB file, 1% = 500MB, clamped to 256MB
		{"1pct within bounds", 16 << 20, 10 << 30, 10 << 30 / 100},                                  // 10GB → 1% = ~107MB
		{"negative readahead", -1, 100, defaultStreamReadahead * priorityWindowMultiplier},            // fallback to default: 16MB×4=64MB
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := streamPriorityWindow(tc.readahead, tc.fileLength)
			if got != tc.want {
				t.Fatalf("streamPriorityWindow(%d, %d) = %d, want %d", tc.readahead, tc.fileLength, got, tc.want)
			}
		})
	}
}

func TestApplyStartupGradientBands(t *testing.T) {
	session := &fakeStreamSession{
		files: []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: 1 << 30}},
	}
	file := session.files[0]

	// With a window of 64MB, expect 4 bands: High(4MB), Next(4MB), Readahead(14MB), Normal(42MB)
	window := int64(64 << 20)
	applyStartupGradient(session, file, window)

	if len(session.prios) < 3 {
		t.Fatalf("expected at least 3 bands, got %d", len(session.prios))
	}
	if session.prios[0] != domain.PriorityHigh {
		t.Fatalf("band 0: got %d, want PriorityHigh", session.prios[0])
	}
	if session.prios[1] != domain.PriorityNext {
		t.Fatalf("band 1: got %d, want PriorityNext", session.prios[1])
	}
	if session.prios[2] != domain.PriorityReadahead {
		t.Fatalf("band 2: got %d, want PriorityReadahead", session.prios[2])
	}

	// Total coverage should equal window.
	var total int64
	for _, r := range session.ranges {
		total += r.Length
	}
	if total != window {
		t.Fatalf("total band coverage = %d, want %d", total, window)
	}
}

func TestApplyStartupGradientSmallWindow(t *testing.T) {
	session := &fakeStreamSession{
		files: []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: 1 << 30}},
	}
	file := session.files[0]

	// With a window of 2MB (< 4MB high band), only a single High band should be produced.
	window := int64(2 << 20)
	applyStartupGradient(session, file, window)

	if len(session.prios) != 1 {
		t.Fatalf("expected 1 band for small window, got %d", len(session.prios))
	}
	if session.prios[0] != domain.PriorityHigh {
		t.Fatalf("band 0: got %d, want PriorityHigh", session.prios[0])
	}
	if session.ranges[0].Length != window {
		t.Fatalf("band length = %d, want %d", session.ranges[0].Length, window)
	}
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
	if reader.responsive {
		t.Fatalf("responsive should not be set by use case (callers opt in when needed)")
	}
	// The startup gradient sets the first band to PriorityHigh.
	if len(session.prios) == 0 || session.prios[0] != domain.PriorityHigh {
		t.Fatalf("first startup gradient band not set to PriorityHigh, got prios=%v", session.prios)
	}
	if session.ranges[0].Off != 0 || session.ranges[0].Length <= 0 {
		t.Fatalf("first startup gradient band has unexpected range: %+v", session.ranges[0])
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
	// Record how many ranges the startup gradient produced.
	rangesBeforeSeek := len(session.ranges)

	if _, err := result.Reader.Seek(64<<20, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}

	if len(session.ranges) <= rangesBeforeSeek {
		t.Fatalf("expected priority to be updated after seek, got %d calls total (same as before)", len(session.ranges))
	}

	// After seek, the adaptive reader temporarily doubles the window (seek boost)
	// and applies a graduated priority across multiple bands. Verify that:
	// 1. There are post-seek gradient bands with a positive offset.
	// 2. The total coverage of non-deprioritization bands equals the boosted window.
	baseWindow := streamPriorityWindow(uc.ReadaheadBytes, 1024)
	boostedWindow := baseWindow * 2
	if boostedWindow > maxPriorityWindowBytes {
		boostedWindow = maxPriorityWindowBytes
	}

	// Skip the startup gradient ranges; the seek produces deprioritization
	// (PriorityNone) followed by the new gradient bands.
	var totalGradientLen int64
	for i := rangesBeforeSeek; i < len(session.ranges); i++ {
		if session.prios[i] == domain.PriorityNone {
			continue // skip deprioritization ranges
		}
		totalGradientLen += session.ranges[i].Length
	}

	if totalGradientLen != boostedWindow {
		t.Fatalf("total gradient coverage after seek: got %d, want %d (boosted)", totalGradientLen, boostedWindow)
	}
}

func TestStreamTorrentTailPreload(t *testing.T) {
	// File must be large enough (> 32 MB) to trigger tail preload.
	fileLen := int64(100 << 20) // 100 MB
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: fileLen}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}

	uc := &StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}
	_, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Should have at least 2 priority ranges: initial window + tail preload.
	if len(session.ranges) < 2 {
		t.Fatalf("expected >= 2 priority ranges, got %d", len(session.ranges))
	}

	const tailPreloadSize int64 = 16 << 20
	expectedTailOff := fileLen - tailPreloadSize
	foundTail := false
	for i, r := range session.ranges {
		if r.Off == expectedTailOff && r.Length == tailPreloadSize {
			if session.prios[i] != domain.PriorityReadahead {
				t.Fatalf("tail preload priority: got %d, want %d", session.prios[i], domain.PriorityReadahead)
			}
			foundTail = true
		}
	}
	if !foundTail {
		t.Fatalf("tail preload range not found in %+v", session.ranges)
	}
}

func TestStreamTorrentNoTailPreloadSmallFile(t *testing.T) {
	// File smaller than 2× tail preload size should NOT get tail preload.
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "small.mp4", Length: 100}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}

	uc := &StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}
	_, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// The startup gradient produces up to 4 bands (High, Next, Readahead, Normal).
	// For small files (< 2× tail preload size), no tail preload is added.
	if len(session.ranges) > 4 {
		t.Fatalf("expected at most 4 startup gradient bands for small file (no tail preload), got %d", len(session.ranges))
	}
	// Verify no range has PriorityReadahead at the file tail offset.
	const tailPreloadSize int64 = 16 << 20
	expectedTailOff := int64(100) - tailPreloadSize
	for i, r := range session.ranges {
		if r.Off == expectedTailOff && r.Length == tailPreloadSize && session.prios[i] == domain.PriorityReadahead {
			t.Fatalf("unexpected tail preload range for small file: %+v", r)
		}
	}
}

func TestBoostWindow(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: 1 << 30}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}

	uc := StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}
	result, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	spr, ok := result.Reader.(*slidingPriorityReader)
	if !ok {
		t.Fatalf("expected *slidingPriorityReader, got %T", result.Reader)
	}

	// Record the window before boost.
	spr.mu.Lock()
	windowBefore := spr.window
	spr.mu.Unlock()

	rangesBefore := len(session.ranges)

	// BoostWindow should double the window and force a priority update.
	spr.BoostWindow(10 * time.Second)

	spr.mu.Lock()
	windowAfter := spr.window
	boostUntil := spr.seekBoostUntil
	spr.mu.Unlock()

	expectedWindow := windowBefore * 2
	if expectedWindow > maxPriorityWindowBytes {
		expectedWindow = maxPriorityWindowBytes
	}
	if windowAfter != expectedWindow {
		t.Fatalf("BoostWindow: window got %d, want %d", windowAfter, expectedWindow)
	}
	if time.Until(boostUntil) < 9*time.Second || time.Until(boostUntil) > 11*time.Second {
		t.Fatalf("BoostWindow: seekBoostUntil not set correctly, expires in %v", time.Until(boostUntil))
	}
	// Should have produced new priority ranges.
	if len(session.ranges) <= rangesBefore {
		t.Fatalf("BoostWindow: expected new priority ranges, got none")
	}
}

func TestSetBufferFillFunc(t *testing.T) {
	reader := &fakeStreamReader{}
	session := &fakeStreamSession{
		files:  []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: 1 << 30}},
		reader: reader,
	}
	engine := &fakeStreamEngine{session: session}

	uc := StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}
	result, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	spr, ok := result.Reader.(*slidingPriorityReader)
	if !ok {
		t.Fatalf("expected *slidingPriorityReader, got %T", result.Reader)
	}

	// Verify SetBufferFillFunc stores the callback.
	called := false
	spr.SetBufferFillFunc(func() float64 {
		called = true
		return 0.1 // below threshold
	})

	spr.mu.Lock()
	if spr.bufferFillFunc == nil {
		spr.mu.Unlock()
		t.Fatalf("SetBufferFillFunc: callback not stored")
	}
	spr.mu.Unlock()

	// The callback is invoked during adjustWindowLocked; verify it's wired.
	_ = spr.bufferFillFunc()
	if !called {
		t.Fatalf("SetBufferFillFunc: callback not invoked")
	}
}
