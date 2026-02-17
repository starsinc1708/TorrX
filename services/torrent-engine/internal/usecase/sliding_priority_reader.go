package usecase

import (
	"context"
	"io"
	"sync"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

const (
	minSlidingPriorityStep = 1 << 20
)

type slidingPriorityReader struct {
	reader    ports.StreamReader
	session   ports.Session
	file      domain.FileRef
	window    int64
	minWindow int64
	maxWindow int64
	backtrack int64
	step      int64

	mu                      sync.Mutex
	pos                     int64
	lastOff                 int64
	bytesReadSinceLastUpdate int64
	lastUpdateTime          time.Time
	effectiveBytesPerSec    float64
	seekBoostUntil          time.Time
}

func newSlidingPriorityReader(
	reader ports.StreamReader,
	session ports.Session,
	file domain.FileRef,
	readahead int64,
	window int64,
) ports.StreamReader {
	backtrack := readahead
	if backtrack < 0 {
		backtrack = 0
	}
	if backtrack > window/2 {
		backtrack = window / 2
	}

	step := window / 4
	if step < minSlidingPriorityStep {
		step = minSlidingPriorityStep
	}

	return &slidingPriorityReader{
		reader:         reader,
		session:        session,
		file:           file,
		window:         window,
		minWindow:      minPriorityWindowBytes,
		maxWindow:      maxPriorityWindowBytes,
		backtrack:      backtrack,
		step:           step,
		lastOff:        0,
		lastUpdateTime: time.Now(),
	}
}

func (r *slidingPriorityReader) SetContext(ctx context.Context) {
	r.reader.SetContext(ctx)
}

func (r *slidingPriorityReader) SetReadahead(n int64) {
	r.reader.SetReadahead(n)
}

func (r *slidingPriorityReader) SetResponsive() {
	r.reader.SetResponsive()
}

func (r *slidingPriorityReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.mu.Lock()
		r.pos += int64(n)
		r.bytesReadSinceLastUpdate += int64(n)
		r.adjustWindowLocked()
		r.updatePriorityWindowLocked(false)
		r.mu.Unlock()
	}
	if err != nil {
		return n, err
	}
	return n, nil
}

func (r *slidingPriorityReader) Seek(offset int64, whence int) (int64, error) {
	pos, err := r.reader.Seek(offset, whence)
	if err != nil {
		return pos, err
	}
	r.mu.Lock()
	r.pos = pos
	// Post-seek boost: temporarily double the window to reduce stalls.
	boosted := r.window * 2
	if boosted > r.maxWindow {
		boosted = r.maxWindow
	}
	r.window = boosted
	r.seekBoostUntil = time.Now().Add(10 * time.Second)
	r.updatePriorityWindowLocked(true)
	r.mu.Unlock()
	return pos, nil
}

func (r *slidingPriorityReader) Close() error {
	return r.reader.Close()
}

const adaptiveTargetBufferSeconds = 30.0

// adjustWindowLocked recalculates the priority window based on observed
// read throughput (EMA smoothed). Called on every Read; actual recalculation
// only happens every 500ms to avoid thrashing.
func (r *slidingPriorityReader) adjustWindowLocked() {
	now := time.Now()
	elapsed := now.Sub(r.lastUpdateTime).Seconds()
	if elapsed < 0.5 {
		return
	}

	instantRate := float64(r.bytesReadSinceLastUpdate) / elapsed
	if r.effectiveBytesPerSec <= 0 {
		r.effectiveBytesPerSec = instantRate
	} else {
		// Exponential moving average (alpha = 0.3).
		r.effectiveBytesPerSec = 0.7*r.effectiveBytesPerSec + 0.3*instantRate
	}
	r.bytesReadSinceLastUpdate = 0
	r.lastUpdateTime = now

	// After seek boost expires, allow dynamic adjustment again.
	if now.Before(r.seekBoostUntil) {
		return
	}

	// Target: buffer ~30 seconds of content ahead.
	dynamicWindow := int64(r.effectiveBytesPerSec * adaptiveTargetBufferSeconds)
	if dynamicWindow < r.minWindow {
		dynamicWindow = r.minWindow
	}
	if dynamicWindow > r.maxWindow {
		dynamicWindow = r.maxWindow
	}
	r.window = dynamicWindow
}

func (r *slidingPriorityReader) updatePriorityWindowLocked(force bool) {
	off := r.pos - r.backtrack
	if off < 0 {
		off = 0
	}

	if !force {
		delta := off - r.lastOff
		if delta < 0 {
			delta = -delta
		}
		if delta < r.step {
			return
		}
	}

	r.session.SetPiecePriority(
		r.file,
		domain.Range{Off: off, Length: r.window},
		domain.PriorityHigh,
	)
	r.lastOff = off
}

var _ ports.StreamReader = (*slidingPriorityReader)(nil)
var _ io.ReadSeekCloser = (*slidingPriorityReader)(nil)
