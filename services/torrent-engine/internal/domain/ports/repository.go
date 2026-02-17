package ports

import (
	"context"

	"torrentstream/internal/domain"
)

type TorrentRepository interface {
	Create(ctx context.Context, t domain.TorrentRecord) error
	Update(ctx context.Context, t domain.TorrentRecord) error
	UpdateProgress(ctx context.Context, id domain.TorrentID, update domain.ProgressUpdate) error
	Get(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error)
	List(ctx context.Context, filter domain.TorrentFilter) ([]domain.TorrentRecord, error)
	GetMany(ctx context.Context, ids []domain.TorrentID) ([]domain.TorrentRecord, error)
	Delete(ctx context.Context, id domain.TorrentID) error
	UpdateTags(ctx context.Context, id domain.TorrentID, tags []string) error
}
