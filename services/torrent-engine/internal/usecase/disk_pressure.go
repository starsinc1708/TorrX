package usecase

import (
	"context"
	"log/slog"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

// DiskPressure periodically checks available disk space on the download
// directory and stops all active non-focused downloads when free space drops
// below MinFreeBytes. Stopped sessions are resumed once free space exceeds
// ResumeBytes (hysteresis prevents rapid stop/resume cycles).
type DiskPressure struct {
	Engine       ports.Engine
	Logger       *slog.Logger
	DataDir      string
	MinFreeBytes int64 // threshold below which downloads are paused
	ResumeBytes  int64 // threshold above which downloads may resume
	Interval     time.Duration
}

// Run starts the periodic disk pressure check loop. It blocks until ctx is
// cancelled.
func (dp DiskPressure) Run(ctx context.Context) {
	interval := dp.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if dp.ResumeBytes <= dp.MinFreeBytes {
		dp.ResumeBytes = dp.MinFreeBytes * 2
	}

	paused := false
	stoppedByPressure := make(map[domain.TorrentID]struct{})

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			free, err := diskFreeBytes(dp.DataDir)
			if err != nil {
				dp.Logger.Warn("disk_pressure: failed to check disk space",
					slog.String("path", dp.DataDir),
					slog.String("error", err.Error()),
				)
				continue
			}

			if !paused && free < dp.MinFreeBytes {
				dp.Logger.Warn("disk_pressure: low disk space, stopping active downloads",
					slog.Int64("freeBytes", free),
					slog.Int64("thresholdBytes", dp.MinFreeBytes),
				)
				dp.stopActiveDownloads(ctx, stoppedByPressure)
				paused = true
			} else if paused && free >= dp.ResumeBytes {
				dp.Logger.Info("disk_pressure: disk space recovered, resuming downloads",
					slog.Int64("freeBytes", free),
					slog.Int64("resumeBytes", dp.ResumeBytes),
				)
				dp.resumeStoppedDownloads(ctx, stoppedByPressure)
				paused = false
			}
		}
	}
}

// stopActiveDownloads stops all active sessions except the focused one and
// records their IDs so they can be resumed later.
func (dp DiskPressure) stopActiveDownloads(ctx context.Context, stopped map[domain.TorrentID]struct{}) {
	ids, err := dp.Engine.ListActiveSessions(ctx)
	if err != nil {
		dp.Logger.Warn("disk_pressure: list active sessions failed",
			slog.String("error", err.Error()),
		)
		return
	}

	for _, id := range ids {
		mode, err := dp.Engine.GetSessionMode(ctx, id)
		if err != nil {
			continue
		}
		// Skip the focused session — it is actively being streamed.
		if mode == domain.ModeFocused {
			continue
		}
		if err := dp.Engine.StopSession(ctx, id); err != nil {
			dp.Logger.Warn("disk_pressure: stop session failed",
				slog.String("id", string(id)),
				slog.String("error", err.Error()),
			)
		} else {
			stopped[id] = struct{}{}
			dp.Logger.Info("disk_pressure: stopped session",
				slog.String("id", string(id)),
			)
		}
	}
}

// resumeStoppedDownloads restarts sessions that were previously stopped due
// to disk pressure.
func (dp DiskPressure) resumeStoppedDownloads(ctx context.Context, stopped map[domain.TorrentID]struct{}) {
	for id := range stopped {
		if err := dp.Engine.StartSession(ctx, id); err != nil {
			dp.Logger.Warn("disk_pressure: resume session failed",
				slog.String("id", string(id)),
				slog.String("error", err.Error()),
			)
		} else {
			dp.Logger.Info("disk_pressure: resumed session",
				slog.String("id", string(id)),
			)
		}
		delete(stopped, id)
	}
}
