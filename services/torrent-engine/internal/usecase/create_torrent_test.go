package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

type fakeEngine struct {
	openCalled      int
	openSource      domain.TorrentSource
	openErr         error
	stateCalled     int
	listCalled      int
	stopCalled      int
	setPrioCalled   int
	setPrioTorrent  domain.TorrentID
	returnedSession ports.Session
}

func (f *fakeEngine) Open(ctx context.Context, src domain.TorrentSource) (ports.Session, error) {
	f.openCalled++
	f.openSource = src
	if f.openErr != nil {
		return nil, f.openErr
	}
	return f.returnedSession, nil
}

func (f *fakeEngine) Close() error { return nil }

func (f *fakeEngine) GetSessionState(ctx context.Context, id domain.TorrentID) (domain.SessionState, error) {
	f.stateCalled++
	return domain.SessionState{}, nil
}

func (f *fakeEngine) GetSession(ctx context.Context, id domain.TorrentID) (ports.Session, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeEngine) ListActiveSessions(ctx context.Context) ([]domain.TorrentID, error) {
	f.listCalled++
	return nil, nil
}

func (f *fakeEngine) StopSession(ctx context.Context, id domain.TorrentID) error {
	f.stopCalled++
	return nil
}

func (f *fakeEngine) StartSession(ctx context.Context, id domain.TorrentID) error { return nil }

func (f *fakeEngine) RemoveSession(ctx context.Context, id domain.TorrentID) error { return nil }

func (f *fakeEngine) SetPiecePriority(ctx context.Context, id domain.TorrentID, file domain.FileRef, r domain.Range, prio domain.Priority) error {
	f.setPrioCalled++
	f.setPrioTorrent = id
	return nil
}

func (f *fakeEngine) ListSessions(ctx context.Context) ([]domain.TorrentID, error) {
	return nil, nil
}

func (f *fakeEngine) FocusSession(ctx context.Context, id domain.TorrentID) error { return nil }
func (f *fakeEngine) UnfocusAll(ctx context.Context) error                        { return nil }
func (f *fakeEngine) GetSessionMode(ctx context.Context, id domain.TorrentID) (domain.SessionMode, error) {
	return domain.ModeDownloading, nil
}
func (f *fakeEngine) SetDownloadRateLimit(ctx context.Context, id domain.TorrentID, bytesPerSec int64) error {
	return nil
}

type fakeSession struct {
	id       domain.TorrentID
	files    []domain.FileRef
	startErr error
	stopErr  error
	stopCnt  int
}

func (s *fakeSession) ID() domain.TorrentID { return s.id }

func (s *fakeSession) Files() []domain.FileRef { return s.files }

func (s *fakeSession) SelectFile(index int) (domain.FileRef, error) {
	if index < 0 || index >= len(s.files) {
		return domain.FileRef{}, errors.New("out of range")
	}
	return s.files[index], nil
}

func (s *fakeSession) SetPiecePriority(file domain.FileRef, r domain.Range, prio domain.Priority) {}

func (s *fakeSession) Start() error { return s.startErr }

func (s *fakeSession) Stop() error {
	s.stopCnt++
	return s.stopErr
}

func (s *fakeSession) NewReader(file domain.FileRef) (ports.StreamReader, error) {
	return nil, errors.New("not implemented")
}

type fakeRepo struct {
	createCalled int
	createRecord domain.TorrentRecord
	createErr    error
}

func (r *fakeRepo) Create(ctx context.Context, t domain.TorrentRecord) error {
	r.createCalled++
	r.createRecord = t
	return r.createErr
}

func (r *fakeRepo) Update(ctx context.Context, t domain.TorrentRecord) error { return nil }

func (r *fakeRepo) UpdateProgress(ctx context.Context, id domain.TorrentID, update domain.ProgressUpdate) error {
	return nil
}

func (r *fakeRepo) Get(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error) {
	return domain.TorrentRecord{}, errors.New("not implemented")
}

func (r *fakeRepo) List(ctx context.Context, filter domain.TorrentFilter) ([]domain.TorrentRecord, error) {
	return nil, errors.New("not implemented")
}

func (r *fakeRepo) GetMany(ctx context.Context, ids []domain.TorrentID) ([]domain.TorrentRecord, error) {
	return nil, errors.New("not implemented")
}

func (r *fakeRepo) Delete(ctx context.Context, id domain.TorrentID) error {
	return errors.New("not implemented")
}

func (r *fakeRepo) UpdateTags(ctx context.Context, id domain.TorrentID, tags []string) error {
	return errors.New("not implemented")
}

func TestCreateTorrentInvalidSource(t *testing.T) {
	uc := CreateTorrent{Engine: &fakeEngine{}, Repo: &fakeRepo{}, Now: func() time.Time { return time.Unix(0, 0).UTC() }}

	_, err := uc.Execute(context.Background(), CreateTorrentInput{})
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource, got %v", err)
	}

	_, err = uc.Execute(context.Background(), CreateTorrentInput{Source: domain.TorrentSource{Magnet: "m", Torrent: "t"}})
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource, got %v", err)
	}
}

func TestCreateTorrentEngineError(t *testing.T) {
	engineErr := errors.New("open failed")
	engine := &fakeEngine{openErr: engineErr}
	uc := CreateTorrent{Engine: engine, Repo: &fakeRepo{}, Now: func() time.Time { return time.Unix(0, 0).UTC() }}

	_, err := uc.Execute(context.Background(), CreateTorrentInput{Source: domain.TorrentSource{Magnet: "m"}})
	if !errors.Is(err, ErrEngine) {
		t.Fatalf("expected engine error, got %v", err)
	}
	if engine.openCalled != 1 {
		t.Fatalf("expected Open called once, got %d", engine.openCalled)
	}
}

func TestCreateTorrentStartError(t *testing.T) {
	startErr := errors.New("start failed")
	session := &fakeSession{
		startErr: startErr,
		files:    []domain.FileRef{{Index: 0, Path: "video.mkv", Length: 1}},
	}
	engine := &fakeEngine{returnedSession: session}
	repo := &fakeRepo{}
	uc := CreateTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return time.Unix(0, 0).UTC() }}

	_, err := uc.Execute(context.Background(), CreateTorrentInput{Source: domain.TorrentSource{Magnet: "m"}})
	if !errors.Is(err, ErrEngine) {
		t.Fatalf("expected engine error, got %v", err)
	}
	if repo.createCalled != 0 {
		t.Fatalf("expected repo not called")
	}
}

func TestCreateTorrentRepoErrorStopsSession(t *testing.T) {
	repoErr := errors.New("repo failed")
	session := &fakeSession{id: "t1"}
	engine := &fakeEngine{returnedSession: session}
	repo := &fakeRepo{createErr: repoErr}
	uc := CreateTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return time.Unix(0, 0).UTC() }}

	_, err := uc.Execute(context.Background(), CreateTorrentInput{Source: domain.TorrentSource{Magnet: "m"}})
	if !errors.Is(err, ErrRepository) {
		t.Fatalf("expected repo error, got %v", err)
	}
	if session.stopCnt != 1 {
		t.Fatalf("expected session.Stop called once, got %d", session.stopCnt)
	}
}

func TestCreateTorrentSuccessMagnet(t *testing.T) {
	files := []domain.FileRef{
		{Index: 0, Path: "Sintel/Sintel.mp4", Length: 10},
		{Index: 1, Path: "Sintel/Sintel.srt", Length: 5},
	}
	session := &fakeSession{id: "t1", files: files}
	engine := &fakeEngine{returnedSession: session}
	repo := &fakeRepo{}
	now := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)
	uc := CreateTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return now }}

	magnet := "magnet:?xt=urn:btih:08ada5a7a6183aae1e09d831df6748d566095a10&dn=Sintel"

	got, err := uc.Execute(context.Background(), CreateTorrentInput{Source: domain.TorrentSource{Magnet: magnet}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.ID != "t1" {
		t.Fatalf("ID = %q", got.ID)
	}
	if got.Status != domain.TorrentActive {
		t.Fatalf("Status = %q", got.Status)
	}
	if got.InfoHash != "08ada5a7a6183aae1e09d831df6748d566095a10" {
		t.Fatalf("InfoHash = %q", got.InfoHash)
	}
	if got.Name != "Sintel" {
		t.Fatalf("Name = %q", got.Name)
	}
	if got.TotalBytes != 15 {
		t.Fatalf("TotalBytes = %d", got.TotalBytes)
	}
	if got.DoneBytes != 0 {
		t.Fatalf("DoneBytes = %d", got.DoneBytes)
	}
	if !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps not set to now")
	}
	if repo.createCalled != 1 {
		t.Fatalf("expected repo.Create called once, got %d", repo.createCalled)
	}
}

func TestCreateTorrentInfoHashFallback(t *testing.T) {
	files := []domain.FileRef{{Index: 0, Path: "file.mp4", Length: 10}}
	session := &fakeSession{id: "hash123", files: files}
	engine := &fakeEngine{returnedSession: session}
	repo := &fakeRepo{}
	now := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)
	uc := CreateTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return now }}

	got, err := uc.Execute(context.Background(), CreateTorrentInput{Source: domain.TorrentSource{Torrent: "file.torrent"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.InfoHash != "hash123" {
		t.Fatalf("InfoHash = %q", got.InfoHash)
	}
}

func TestCreateTorrentPendingWhenNoFiles(t *testing.T) {
	session := &fakeSession{id: "t1", files: nil}
	engine := &fakeEngine{returnedSession: session}
	repo := &fakeRepo{}
	now := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)
	uc := CreateTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return now }}

	got, err := uc.Execute(context.Background(), CreateTorrentInput{Source: domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:abc"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != domain.TorrentPending {
		t.Fatalf("Status = %q, want pending", got.Status)
	}
	// Session.Start() should NOT be called when no files available
	if session.stopCnt != 0 {
		t.Fatalf("session should not be stopped")
	}
}

func TestCreateTorrentCustomName(t *testing.T) {
	files := []domain.FileRef{{Index: 0, Path: "Sintel/Sintel.mp4", Length: 10}}
	session := &fakeSession{id: "t1", files: files}
	engine := &fakeEngine{returnedSession: session}
	repo := &fakeRepo{}
	now := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)
	uc := CreateTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return now }}

	got, err := uc.Execute(context.Background(), CreateTorrentInput{
		Source: domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:abc"},
		Name:   "My Custom Torrent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "My Custom Torrent" {
		t.Fatalf("Name = %q, want My Custom Torrent", got.Name)
	}
}

func TestCreateTorrentWhitespaceSource(t *testing.T) {
	uc := CreateTorrent{Engine: &fakeEngine{}, Repo: &fakeRepo{}, Now: func() time.Time { return time.Unix(0, 0).UTC() }}

	_, err := uc.Execute(context.Background(), CreateTorrentInput{Source: domain.TorrentSource{Magnet: "   "}})
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource for whitespace-only magnet, got %v", err)
	}
}

type fakeRepoWithGet struct {
	fakeRepo
	getRecord domain.TorrentRecord
	getErr    error
}

func (r *fakeRepoWithGet) Get(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error) {
	if r.getErr != nil {
		return domain.TorrentRecord{}, r.getErr
	}
	return r.getRecord, nil
}

func TestCreateTorrentAlreadyExistsReturnsExisting(t *testing.T) {
	existing := domain.TorrentRecord{ID: "t1", Name: "Existing", Status: domain.TorrentActive}
	session := &fakeSession{id: "t1", files: []domain.FileRef{{Index: 0, Path: "f.mp4", Length: 1}}}
	engine := &fakeEngine{returnedSession: session}
	repo := &fakeRepoWithGet{getRecord: existing}
	now := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)
	uc := CreateTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return now }}

	got, err := uc.Execute(context.Background(), CreateTorrentInput{Source: domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:abc"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "t1" || got.Name != "Existing" {
		t.Fatalf("expected existing record, got %+v", got)
	}
	// Repo.Create should not be called since the record already exists
	if repo.createCalled != 0 {
		t.Fatalf("expected repo.Create not called, got %d", repo.createCalled)
	}
}

func TestCreateTorrentConcurrentDuplicate(t *testing.T) {
	existing := domain.TorrentRecord{ID: "t1", Name: "Concurrent", Status: domain.TorrentActive}
	session := &fakeSession{id: "t1", files: []domain.FileRef{{Index: 0, Path: "f.mp4", Length: 1}}}
	engine := &fakeEngine{returnedSession: session}

	// Repo.Get returns not found first (so Create is attempted), but Create fails with AlreadyExists
	// and then re-fetch succeeds
	repo := &fakeRepoConcurrent{
		fakeRepo:    fakeRepo{createErr: domain.ErrAlreadyExists},
		getErrFirst: errors.New("not found"),
		getRecord:   existing,
	}
	now := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)
	uc := CreateTorrent{Engine: engine, Repo: repo, Now: func() time.Time { return now }}

	got, err := uc.Execute(context.Background(), CreateTorrentInput{Source: domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:abc"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "t1" || got.Name != "Concurrent" {
		t.Fatalf("expected re-fetched record, got %+v", got)
	}
}

type fakeRepoConcurrent struct {
	fakeRepo
	getErrFirst error
	getRecord   domain.TorrentRecord
	getCalled   int
}

func (r *fakeRepoConcurrent) Get(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error) {
	r.getCalled++
	if r.getCalled == 1 && r.getErrFirst != nil {
		return domain.TorrentRecord{}, r.getErrFirst
	}
	return r.getRecord, nil
}

func TestValidateSourceBothSet(t *testing.T) {
	err := validateSource(domain.TorrentSource{Magnet: "m", Torrent: "t"})
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource, got %v", err)
	}
}

func TestValidateSourceNeitherSet(t *testing.T) {
	err := validateSource(domain.TorrentSource{})
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource, got %v", err)
	}
}

func TestSumFileLengths(t *testing.T) {
	tests := []struct {
		name  string
		files []domain.FileRef
		want  int64
	}{
		{"nil", nil, 0},
		{"empty", []domain.FileRef{}, 0},
		{"single", []domain.FileRef{{Length: 100}}, 100},
		{"multiple", []domain.FileRef{{Length: 100}, {Length: 200}, {Length: 50}}, 350},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sumFileLengths(tt.files)
			if got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDeriveName(t *testing.T) {
	tests := []struct {
		name  string
		files []domain.FileRef
		want  string
	}{
		{"nil_files", nil, ""},
		{"empty_files", []domain.FileRef{}, ""},
		{"single_file", []domain.FileRef{{Path: "movie.mp4"}}, "movie.mp4"},
		{"nested_path", []domain.FileRef{{Path: "Sintel/Sintel.mp4"}}, "Sintel"},
		{"windows_path", []domain.FileRef{{Path: "Sintel\\Sintel.mp4"}}, "Sintel"},
		{"empty_path", []domain.FileRef{{Path: ""}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveName(tt.files)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseInfoHash(t *testing.T) {
	tests := []struct {
		name   string
		magnet string
		want   domain.InfoHash
	}{
		{"empty", "", ""},
		{"no_xt", "magnet:?dn=Sintel", ""},
		{"valid", "magnet:?xt=urn:btih:08ada5a7a6183aae1e09d831df6748d566095a10&dn=Sintel", "08ada5a7a6183aae1e09d831df6748d566095a10"},
		{"no_dn", "magnet:?xt=urn:btih:abc123", "abc123"},
		{"case_insensitive", "magnet:?XT=URN:BTIH:ABC123&dn=test", "ABC123"},
		{"whitespace", "  magnet:?xt=urn:btih:abc  ", "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseInfoHash(tt.magnet)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
