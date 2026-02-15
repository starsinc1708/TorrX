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
