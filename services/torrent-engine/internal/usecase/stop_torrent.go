package usecase

import (
	"context"
	"errors"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

type StopTorrent struct {
	Engine ports.Engine
	Repo   ports.TorrentRepository
	Now    func() time.Time
}

func (uc StopTorrent) Execute(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error) {
	now := time.Now
	if uc.Now != nil {
		now = uc.Now
	}

	record, err := uc.Repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.TorrentRecord{}, err
		}
		return domain.TorrentRecord{}, wrapRepo(err)
	}

	if err := uc.Engine.StopSession(ctx, id); err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return domain.TorrentRecord{}, wrapEngine(err)
		}
	}

	record.Status = domain.TorrentStopped
	record.UpdatedAt = now()

	if err := uc.Repo.Update(ctx, record); err != nil {
		return domain.TorrentRecord{}, wrapRepo(err)
	}

	return record, nil
}
