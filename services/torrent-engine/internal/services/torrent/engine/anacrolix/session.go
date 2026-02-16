package anacrolix

import (
	"context"
	"time"

	"github.com/anacrolix/torrent"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

type Session struct {
	engine  *Engine
	torrent *torrent.Torrent
	id      domain.TorrentID
	files   []domain.FileRef
	ready   bool
}

func (s *Session) ID() domain.TorrentID {
	return s.id
}

func (s *Session) Files() []domain.FileRef {
	// If metadata arrived since creation, refresh files
	if !s.ready && s.torrent != nil {
		select {
		case <-s.torrent.GotInfo():
			s.files = mapFiles(s.torrent)
			s.ready = true
		default:
		}
	}
	return append([]domain.FileRef(nil), s.files...)
}

func (s *Session) Ready() bool {
	if s.ready {
		return true
	}
	if s.torrent == nil {
		return false
	}
	select {
	case <-s.torrent.GotInfo():
		s.ready = true
		return true
	default:
		return false
	}
}

func (s *Session) SelectFile(index int) (domain.FileRef, error) {
	// Wait for metadata with timeout if not ready yet (fixes "invalid file index"
	// for partially downloaded torrents where metadata exists but isn't immediately ready)
	if !s.ready && s.torrent != nil {
		select {
		case <-s.torrent.GotInfo():
			s.files = mapFiles(s.torrent)
			s.ready = true
		case <-time.After(10 * time.Second):
			// Metadata still not available after timeout
		}
	}

	files := s.Files()
	if index < 0 || index >= len(files) {
		return domain.FileRef{}, ErrSessionNotFound
	}
	return files[index], nil
}

func (s *Session) SetPiecePriority(file domain.FileRef, r domain.Range, prio domain.Priority) {
	if s.torrent == nil {
		return
	}
	if !s.Ready() {
		return
	}
	if s.engine == nil {
		return
	}
	_ = s.engine.SetPiecePriority(context.Background(), s.id, file, r, prio)
}

func (s *Session) Start() error {
	if s.engine == nil || s.torrent == nil {
		return ErrSessionNotFound
	}
	return s.engine.StartSession(context.Background(), s.id)
}

func (s *Session) Stop() error {
	return s.engine.StopSession(context.Background(), s.id)
}

func (s *Session) NewReader(file domain.FileRef) (ports.StreamReader, error) {
	if s.torrent == nil {
		return nil, ErrSessionNotFound
	}
	if !s.Ready() {
		return nil, ErrSessionNotFound
	}
	files := s.torrent.Files()
	if file.Index < 0 || file.Index >= len(files) {
		return nil, ErrSessionNotFound
	}
	return files[file.Index].NewReader(), nil
}
