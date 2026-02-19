package apihttp

import (
	"context"
	"log/slog"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

const (
	priorityHighBand   int64 = 4 << 20 // 4 MB
	priorityMedBand    int64 = 8 << 20 // 8 MB
	priorityProtectEnd int64 = 8 << 20 // 8 MB — protect first/last for container headers
)

// PriorityManager sets per-piece download priorities based on a sliding window.
// It uses 3 bands (High, Medium, Normal) and protects container header regions.
type PriorityManager struct {
	engine    ports.Engine
	torrentID domain.TorrentID
	file      domain.FileRef
	logger    *slog.Logger

	prevStart int64
	prevEnd   int64
}

// NewPriorityManager creates a priority manager for the given torrent file.
func NewPriorityManager(engine ports.Engine, id domain.TorrentID, file domain.FileRef, logger *slog.Logger) *PriorityManager {
	return &PriorityManager{
		engine:    engine,
		torrentID: id,
		file:      file,
		logger:    logger,
	}
}

// Apply sets the 3-band priority window: High (4MB) | Medium (8MB) | Normal (rest).
// Old regions that no longer overlap the new window are deprioritized.
func (p *PriorityManager) Apply(ctx context.Context, windowStart, windowEnd int64) {
	if p.engine == nil || p.file.Length <= 0 {
		return
	}
	if windowEnd > p.file.Length {
		windowEnd = p.file.Length
	}
	if windowStart < 0 {
		windowStart = 0
	}
	if windowStart >= windowEnd {
		return
	}

	// Deprioritize old region that doesn't overlap new window.
	p.deprioritizeOld(ctx, windowStart, windowEnd)

	remaining := windowEnd - windowStart
	off := windowStart

	// High band.
	h := priorityHighBand
	if h > remaining {
		h = remaining
	}
	p.setPriority(ctx, off, h, domain.PriorityHigh)
	off += h
	remaining -= h

	// Medium band.
	if remaining > 0 {
		m := priorityMedBand
		if m > remaining {
			m = remaining
		}
		p.setPriority(ctx, off, m, domain.PriorityNext)
		off += m
		remaining -= m
	}

	// Normal band (rest).
	if remaining > 0 {
		p.setPriority(ctx, off, remaining, domain.PriorityReadahead)
	}

	// Protect container headers: first and last 8 MB always at least Normal.
	if p.file.Length > priorityProtectEnd*2 {
		p.setPriority(ctx, 0, priorityProtectEnd, domain.PriorityNormal)
		tailStart := p.file.Length - priorityProtectEnd
		p.setPriority(ctx, tailStart, priorityProtectEnd, domain.PriorityNormal)
	}

	p.prevStart = windowStart
	p.prevEnd = windowEnd
}

// EnhanceHigh expands the high-priority band from 4 MB to 12 MB.
// Used in the Buffering state to accelerate data arrival.
func (p *PriorityManager) EnhanceHigh(ctx context.Context, windowStart int64) {
	if p.engine == nil || p.file.Length <= 0 {
		return
	}
	enhanced := priorityHighBand * 3
	if windowStart+enhanced > p.file.Length {
		enhanced = p.file.Length - windowStart
	}
	if enhanced <= 0 {
		return
	}
	p.setPriority(ctx, windowStart, enhanced, domain.PriorityHigh)
}

// Deprioritize sets the entire file to PriorityNone (cleanup on job stop).
func (p *PriorityManager) Deprioritize(ctx context.Context) {
	if p.engine == nil || p.file.Length <= 0 {
		return
	}
	p.setPriority(ctx, 0, p.file.Length, domain.PriorityNone)
}

// deprioritizeOld sets old window regions that don't overlap the new window
// to PriorityNone, respecting protected header/tail regions.
func (p *PriorityManager) deprioritizeOld(ctx context.Context, newStart, newEnd int64) {
	if p.prevStart == 0 && p.prevEnd == 0 {
		return
	}

	// Deprioritize [prevStart, newStart) — the part before the new window.
	if p.prevStart < newStart {
		end := newStart
		if end > p.prevEnd {
			end = p.prevEnd
		}
		start := p.prevStart
		if start < priorityProtectEnd {
			start = priorityProtectEnd
		}
		if start < end {
			p.setPriority(ctx, start, end-start, domain.PriorityNone)
		}
	}

	// Deprioritize [newEnd, prevEnd) — the part after the new window.
	if p.prevEnd > newEnd {
		start := newEnd
		if start < p.prevStart {
			start = p.prevStart
		}
		end := p.prevEnd
		if p.file.Length > priorityProtectEnd && end > p.file.Length-priorityProtectEnd {
			end = p.file.Length - priorityProtectEnd
		}
		if start < end {
			p.setPriority(ctx, start, end-start, domain.PriorityNone)
		}
	}
}

func (p *PriorityManager) setPriority(ctx context.Context, off, length int64, prio domain.Priority) {
	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := p.engine.SetPiecePriority(ctx2, p.torrentID, p.file,
		domain.Range{Off: off, Length: length}, prio); err != nil {
		p.logger.Debug("priority set failed",
			slog.String("torrentId", string(p.torrentID)),
			slog.Int64("off", off),
			slog.Int64("length", length),
			slog.String("error", err.Error()),
		)
	}
}
