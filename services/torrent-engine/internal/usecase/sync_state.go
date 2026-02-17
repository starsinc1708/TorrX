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

		doneBytes := sumBytesCompleted(state.Files)

		// Build an atomic progress update using $max for DoneBytes
		// to avoid race conditions between concurrent sync cycles.
		update := domain.ProgressUpdate{
			DoneBytes: doneBytes,
		}
		changed := false

		if doneBytes > record.DoneBytes {
			changed = true
		}

		if state.Status != record.Status {
			update.Status = state.Status
			changed = true
		}

		// Update files with per-file progress.
		if len(state.Files) > 0 && len(state.Files) != len(record.Files) {
			update.Files = state.Files
			update.TotalBytes = sumFileLengths(state.Files)
			changed = true
		} else if len(state.Files) > 0 {
			filesChanged := false
			merged := make([]domain.FileRef, len(state.Files))
			copy(merged, state.Files)
			for i, sf := range state.Files {
				if i < len(record.Files) && sf.BytesCompleted > record.Files[i].BytesCompleted {
					filesChanged = true
				} else if i < len(record.Files) {
					merged[i].BytesCompleted = record.Files[i].BytesCompleted
				}
			}
			if filesChanged {
				update.Files = merged
				changed = true
			}
		}

		if record.Name == "" && len(state.Files) > 0 {
			update.Name = deriveName(state.Files)
			update.TotalBytes = sumFileLengths(state.Files)
			changed = true
		}

		if !changed {
			continue
		}

		if update.TotalBytes == 0 && record.TotalBytes > 0 {
			update.TotalBytes = record.TotalBytes
		}

		if err := s.Repo.UpdateProgress(ctx, id, update); err != nil {
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
