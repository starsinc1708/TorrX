package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

type SyncState struct {
	Engine   ports.Engine
	Repo     ports.TorrentRepository
	Logger   *slog.Logger
	Interval time.Duration
}

func (s SyncState) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = 10 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sync(ctx)
		}
	}
}

func (s SyncState) sync(ctx context.Context) {
	ids, err := s.Engine.ListSessions(ctx)
	if err != nil {
		s.Logger.Warn("sync: list sessions failed", slog.String("error", err.Error()))
		return
	}
	if len(ids) == 0 {
		return
	}

	records, err := s.Repo.GetMany(ctx, ids)
	if err != nil {
		s.Logger.Warn("sync: fetch records failed", slog.String("error", err.Error()))
		return
	}

	recordMap := make(map[domain.TorrentID]domain.TorrentRecord, len(records))
	for _, r := range records {
		recordMap[r.ID] = r
	}

	now := time.Now().UTC()

	for _, id := range ids {
		state, err := s.Engine.GetSessionState(ctx, id)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			s.Logger.Warn("sync: get session state failed",
				slog.String("id", string(id)),
				slog.String("error", err.Error()))
			continue
		}

		record, ok := recordMap[id]
		if !ok {
			continue
		}

		changed := false

		// Update progress bytes (never decrease — paused torrents may report 0).
		doneBytes := sumBytesCompleted(state.Files)
		if doneBytes > record.DoneBytes {
			record.DoneBytes = doneBytes
			changed = true
		}

		// Update status.
		if state.Status != record.Status {
			record.Status = state.Status
			changed = true
		}

		// Update files with per-file progress.
		if len(state.Files) > 0 && len(state.Files) != len(record.Files) {
			record.Files = state.Files
			record.TotalBytes = sumFileLengths(state.Files)
			changed = true
		} else if len(state.Files) > 0 {
			for i, sf := range state.Files {
				if i < len(record.Files) && record.Files[i].BytesCompleted != sf.BytesCompleted {
					record.Files[i].BytesCompleted = sf.BytesCompleted
					changed = true
				}
			}
		}

		// Update name if it was empty (pending → metadata arrived).
		if record.Name == "" && len(state.Files) > 0 {
			record.Name = deriveName(state.Files)
			record.TotalBytes = sumFileLengths(state.Files)
			changed = true
		}

		if !changed {
			continue
		}

		record.UpdatedAt = now
		if err := s.Repo.Update(ctx, record); err != nil {
			s.Logger.Warn("sync: update record failed",
				slog.String("id", string(id)),
				slog.String("error", err.Error()))
		}
	}
}

func sumBytesCompleted(files []domain.FileRef) int64 {
	var total int64
	for _, f := range files {
		total += f.BytesCompleted
	}
	return total
}
