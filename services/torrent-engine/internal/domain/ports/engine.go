package ports

import (
	"context"

	"torrentstream/internal/domain"
)

type Engine interface {
	Open(ctx context.Context, src domain.TorrentSource) (Session, error)
	Close() error
	GetSessionState(ctx context.Context, id domain.TorrentID) (domain.SessionState, error)
	GetSession(ctx context.Context, id domain.TorrentID) (Session, error)
	ListActiveSessions(ctx context.Context) ([]domain.TorrentID, error)
	StopSession(ctx context.Context, id domain.TorrentID) error
	StartSession(ctx context.Context, id domain.TorrentID) error
	RemoveSession(ctx context.Context, id domain.TorrentID) error
	SetPiecePriority(ctx context.Context, id domain.TorrentID, file domain.FileRef, r domain.Range, prio domain.Priority) error
	ListSessions(ctx context.Context) ([]domain.TorrentID, error)
	FocusSession(ctx context.Context, id domain.TorrentID) error
	UnfocusAll(ctx context.Context) error
	GetSessionMode(ctx context.Context, id domain.TorrentID) (domain.SessionMode, error)
	// SetDownloadRateLimit sets a per-torrent download rate limit in bytes/sec.
	// Pass 0 to remove the limit (unlimited).
	SetDownloadRateLimit(ctx context.Context, id domain.TorrentID, bytesPerSec int64) error
}
