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

	// gradientHighBand is the byte range at the current read position set to
	// PriorityHigh (PiecePriorityNow). Covers roughly 1 piece.
	gradientHighBand int64 = 2 << 20 // 2 MB

	// gradientNextBand is the byte range immediately after the high band set
	// to PriorityNext (PiecePriorityNext).
	gradientNextBand int64 = 2 << 20 // 2 MB

	// fileBoundaryProtection is the byte range at the start and end of the
	// file that is never deprioritized. Container formats (MP4 moov, MKV
	// SeekHead/Cues) store seek indices at file boundaries.
	fileBoundaryProtection int64 = 8 << 20 // 8 MB
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

	mu                       sync.Mutex
	pos                      int64
	lastOff                  int64
	prevOff                  int64
	prevWindow               int64
	bytesReadSinceLastUpdate int64
	lastUpdateTime           time.Time
	effectiveBytesPerSec     float64
	seekBoostUntil           time.Time

	// Dormancy support: idle readers are put to sleep when multiple readers
	// exist for the same torrent, freeing bandwidth for the active reader.
	lastAccess        time.Time
	lastDormancyCheck time.Time
	dormant           bool
	registry          *readerRegistry
	torrentID         domain.TorrentID
}

func newSlidingPriorityReader(
	reader ports.StreamReader,
	session ports.Session,
	file domain.FileRef,
	readahead int64,
	window int64,
	registry *readerRegistry,
	torrentID domain.TorrentID,
) *slidingPriorityReader {
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

	now := time.Now()
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
		lastUpdateTime: now,
		lastAccess:     now,
		registry:       registry,
		torrentID:      torrentID,
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
		now := time.Now()
		r.mu.Lock()
		r.pos += int64(n)
		r.lastAccess = now
		r.bytesReadSinceLastUpdate += int64(n)
		if r.dormant {
			r.exitDormancyLocked()
		}
		r.adjustWindowLocked()
		r.updatePriorityWindowLocked(false)
		checkDormancy := r.registry != nil && now.Sub(r.lastDormancyCheck) > 5*time.Second
		if checkDormancy {
			r.lastDormancyCheck = now
		}
		r.mu.Unlock()

		if checkDormancy {
			r.registry.enforceDormancy(r.torrentID, r)
		}
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
	r.lastAccess = time.Now()
	if r.dormant {
		r.exitDormancyLocked()
	}
	// Post-seek boost: temporarily double the window to reduce stalls.
	boosted := r.window * 2
	if boosted > r.maxWindow {
		boosted = r.maxWindow
	}
	r.window = boosted
	r.seekBoostUntil = time.Now().Add(10 * time.Second)
	r.updatePriorityWindowLocked(true)
	r.mu.Unlock()

	if r.registry != nil {
		r.registry.enforceDormancy(r.torrentID, r)
	}
	return pos, nil
}

func (r *slidingPriorityReader) Close() error {
	if r.registry != nil {
		r.registry.unregister(r.torrentID, r)
	}
	return r.reader.Close()
}

// enterDormancyLocked puts the reader to sleep: sets readahead to 0 and
// deprioritizes its window. Must be called with r.mu held.
func (r *slidingPriorityReader) enterDormancyLocked() {
	r.dormant = true
	r.reader.SetReadahead(0)
	if r.prevWindow > 0 {
		r.deprioritizeRange(r.prevOff, r.prevWindow)
	}
}

// exitDormancyLocked wakes the reader: restores readahead and reapplies
// the priority window. Must be called with r.mu held.
func (r *slidingPriorityReader) exitDormancyLocked() {
	r.dormant = false
	r.reader.SetReadahead(r.window)
	r.updatePriorityWindowLocked(true)
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

	// Deprioritize the non-overlapping portion of the previous window,
	// but never deprioritize file boundary regions (container headers).
	if r.prevWindow > 0 {
		prevEnd := r.prevOff + r.prevWindow
		newStart := off
		newEnd := off + r.window
		if prevEnd <= newStart || r.prevOff >= newEnd {
			r.deprioritizeRange(r.prevOff, r.prevWindow)
		} else if r.prevOff < newStart {
			r.deprioritizeRange(r.prevOff, newStart-r.prevOff)
		}
	}

	// Apply graduated priority: High → Next → Readahead → Normal.
	// This focuses bandwidth on the immediate read position instead of
	// spreading it evenly across the entire window (TorrServer-style).
	r.applyGradientPriority(off)

	r.prevOff = off
	r.prevWindow = r.window
	r.lastOff = off
}

// applyGradientPriority sets a 4-tier priority gradient on the current window:
//
//	[off, off+highBand)        → PriorityHigh      (PiecePriorityNow)
//	[off+highBand, +nextBand)  → PriorityNext       (PiecePriorityNext)
//	[+nextBand, +readahead)    → PriorityReadahead   (PiecePriorityReadahead)
//	[+readahead, off+window)   → PriorityNormal      (PiecePriorityNormal)
func (r *slidingPriorityReader) applyGradientPriority(off int64) {
	remaining := r.window

	// Band 1: PriorityHigh (immediate need)
	highLen := gradientHighBand
	if highLen > remaining {
		highLen = remaining
	}
	r.session.SetPiecePriority(r.file,
		domain.Range{Off: off, Length: highLen},
		domain.PriorityHigh)
	remaining -= highLen

	// Band 2: PriorityNext
	if remaining > 0 {
		nextLen := gradientNextBand
		if nextLen > remaining {
			nextLen = remaining
		}
		r.session.SetPiecePriority(r.file,
			domain.Range{Off: off + highLen, Length: nextLen},
			domain.PriorityNext)
		remaining -= nextLen
	}

	// Band 3: PriorityReadahead (up to ~25% of remaining window)
	if remaining > 0 {
		readaheadLen := remaining / 4
		if readaheadLen < gradientHighBand {
			readaheadLen = remaining // small window: everything is readahead
		}
		if readaheadLen > remaining {
			readaheadLen = remaining
		}
		bandOff := off + highLen + gradientNextBand
		r.session.SetPiecePriority(r.file,
			domain.Range{Off: bandOff, Length: readaheadLen},
			domain.PriorityReadahead)
		remaining -= readaheadLen
	}

	// Band 4: PriorityNormal (rest of window)
	if remaining > 0 {
		normalOff := off + r.window - remaining
		r.session.SetPiecePriority(r.file,
			domain.Range{Off: normalOff, Length: remaining},
			domain.PriorityNormal)
	}
}

// deprioritizeRange sets a byte range to PriorityNone, but preserves file
// boundary regions (first/last 8 MB) which contain container headers.
func (r *slidingPriorityReader) deprioritizeRange(off, length int64) {
	if length <= 0 {
		return
	}
	end := off + length
	fileLen := r.file.Length

	// Compute the protected zones.
	headEnd := fileBoundaryProtection
	if headEnd > fileLen {
		headEnd = fileLen
	}
	tailStart := fileLen - fileBoundaryProtection
	if tailStart < headEnd {
		tailStart = headEnd // file smaller than 2× protection; all protected
	}

	// Clip the deprioritization range to exclude protected zones.
	// We may produce up to two non-contiguous ranges: one between head and
	// tail protection zones, or just the middle portion.
	deprioritizeSegment := func(s, e int64) {
		if s >= e {
			return
		}
		r.session.SetPiecePriority(r.file,
			domain.Range{Off: s, Length: e - s},
			domain.PriorityNone)
	}

	// Effective range after clipping head protection.
	clippedStart := off
	if clippedStart < headEnd {
		clippedStart = headEnd
	}
	// Effective range after clipping tail protection.
	clippedEnd := end
	if clippedEnd > tailStart {
		clippedEnd = tailStart
	}

	deprioritizeSegment(clippedStart, clippedEnd)
}

// EffectiveBytesPerSec returns the EMA-smoothed read throughput.
func (r *slidingPriorityReader) EffectiveBytesPerSec() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.effectiveBytesPerSec
}

var _ ports.StreamReader = (*slidingPriorityReader)(nil)
var _ io.ReadSeekCloser = (*slidingPriorityReader)(nil)
