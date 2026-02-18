package apihttp

import (
	"context"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"torrentstream/internal/metrics"
)

// ---- Buffered stream reader (absorbs torrent download stalls) ---------------

const (
	defaultStreamBufSize = 8 * 1024 * 1024 // 8 MB ring buffer
	streamBufReadChunk   = 256 * 1024       // 256 KB per source read
	streamBufStallWarn   = 30 * time.Second  // warn after 30s of no data

	// Adaptive sizing constants.
	minStreamBufSize     = 8 * 1024 * 1024  // 8 MB floor
	maxStreamBufSize     = 64 * 1024 * 1024 // 64 MB ceiling
	targetBufferDuration = 5 * time.Second   // aim to buffer 5s of content
	resizeCheckInterval  = 3 * time.Second   // check resize every 3s
	resizeThreshold      = 0.25              // only resize if >25% difference
	memoryPressureLimit  = 512 * 1024 * 1024 // 512 MB Go heap limit
)

// bufferedStreamReader wraps an io.Reader with an adaptively-sized ring buffer.
// A background goroutine continuously fills the buffer from the source.
// Read() blocks only when the buffer is empty, with a configurable timeout
// to avoid indefinite hangs that trigger the HLS watchdog.
type bufferedStreamReader struct {
	source io.ReadCloser
	buf    []byte
	size   int // capacity
	rPos   int // read position in ring
	wPos   int // write position in ring
	count  int // bytes currently buffered

	mu       sync.Mutex
	cond     *sync.Cond
	srcErr   error // sticky error from source
	closed   bool
	ctx      context.Context
	cancel   context.CancelFunc
	logger   *slog.Logger

	// Adaptive sizing fields.
	bytesConsumed    int64     // total bytes read by consumer since last resize check
	lastResizeCheck  time.Time // when we last evaluated whether to resize
	effectiveBitrate float64   // EMA of consumer read rate (bytes/sec)

	// Rate limiting: bytes/sec cap on source reads. 0 = unlimited.
	rateLimitBPS atomic.Int64
}

func newBufferedStreamReader(source io.ReadCloser, bufSize int, logger *slog.Logger) *bufferedStreamReader {
	if bufSize <= 0 {
		bufSize = defaultStreamBufSize
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &bufferedStreamReader{
		source:          source,
		buf:             make([]byte, bufSize),
		size:            bufSize,
		ctx:             ctx,
		cancel:          cancel,
		logger:          logger,
		lastResizeCheck: time.Now(),
	}
	b.cond = sync.NewCond(&b.mu)
	go b.fillLoop()
	return b
}

// fillLoop continuously reads from the source into the ring buffer.
// EOF from a responsive torrent reader may be transient (piece data not yet
// downloaded). Instead of treating every EOF as terminal, the loop retries
// with exponential backoff so that FFmpeg keeps waiting for data to arrive.
func (b *bufferedStreamReader) fillLoop() {
	const (
		initialBackoff   = 10 * time.Millisecond
		maxBackoff       = 200 * time.Millisecond // keep low to reduce latency when pieces arrive
		maxStallDuration = 3 * time.Minute
	)

	tmp := make([]byte, streamBufReadChunk)
	backoff := initialBackoff
	var stallSince time.Time

	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}

		n, err := b.source.Read(tmp)
		if n > 0 {
			b.mu.Lock()
			b.writeToRing(tmp[:n])
			b.cond.Broadcast()
			b.mu.Unlock()
			// Data arrived — reset backoff state.
			backoff = initialBackoff
			stallSince = time.Time{}

			// Rate limit: sleep proportionally to bytes read.
			if limit := b.rateLimitBPS.Load(); limit > 0 {
				sleepDur := time.Duration(float64(n) / float64(limit) * float64(time.Second))
				if sleepDur > 0 {
					select {
					case <-time.After(sleepDur):
					case <-b.ctx.Done():
						return
					}
				}
			}
		}
		if err != nil {
			// Non-EOF errors (context cancelled, closed pipe) are terminal.
			if err != io.EOF {
				b.mu.Lock()
				b.srcErr = err
				b.cond.Broadcast()
				b.mu.Unlock()
				return
			}

			// EOF may be transient: the responsive torrent reader returns EOF
			// when a piece hasn't been downloaded yet. Retry with backoff
			// until the max stall duration is reached.
			if stallSince.IsZero() {
				stallSince = time.Now()
			}
			if time.Since(stallSince) >= maxStallDuration {
				b.logger.Warn("stream source EOF after max stall duration",
					slog.Duration("stalled", time.Since(stallSince)))
				b.mu.Lock()
				b.srcErr = io.EOF
				b.cond.Broadcast()
				b.mu.Unlock()
				return
			}

			select {
			case <-time.After(backoff):
			case <-b.ctx.Done():
				return
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// writeToRing copies data into the ring buffer, returning bytes written.
// Caller must hold b.mu.
func (b *bufferedStreamReader) writeToRing(data []byte) int {
	written := 0
	for len(data) > 0 && b.count < b.size {
		// Space available from wPos to end of buffer or to rPos.
		space := b.size - b.wPos
		if space > b.size-b.count {
			space = b.size - b.count
		}
		if space > len(data) {
			space = len(data)
		}
		copy(b.buf[b.wPos:b.wPos+space], data[:space])
		b.wPos = (b.wPos + space) % b.size
		b.count += space
		written += space
		data = data[space:]
	}
	return written
}

// Read implements io.Reader. Blocks until data is available, source EOF,
// or the stall timeout is reached.
func (b *bufferedStreamReader) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Wait for data to become available.
	for b.count == 0 && b.srcErr == nil && !b.closed {
		// sync.Cond has no native timed wait. Bridge to a channel with a
		// single goroutine that checks the full predicate so it only
		// returns when data is truly available (or error/close).
		condDone := make(chan struct{})
		go func() {
			b.mu.Lock()
			for b.count == 0 && b.srcErr == nil && !b.closed {
				b.cond.Wait()
			}
			b.mu.Unlock()
			close(condDone)
		}()
		b.mu.Unlock()

		// Wait for the cond goroutine, logging periodic stall warnings.
		// Only one goroutine is alive per outer-loop iteration — the
		// inner loop just resets the timer without spawning more.
		stallTimer := time.NewTimer(streamBufStallWarn)
		gotData := false
		for !gotData {
			select {
			case <-condDone:
				stallTimer.Stop()
				gotData = true
			case <-stallTimer.C:
				b.logger.Warn("stream buffer stall: no data for 30s")
				stallTimer.Reset(streamBufStallWarn)
			case <-b.ctx.Done():
				stallTimer.Stop()
				b.mu.Lock()
				return 0, b.ctx.Err()
			}
		}
		b.mu.Lock()
	}

	if b.closed {
		return 0, io.ErrClosedPipe
	}
	if b.count == 0 && b.srcErr != nil {
		return 0, b.srcErr
	}

	// Read from ring buffer.
	n := b.readFromRing(p)

	// Track consumer throughput for adaptive sizing.
	b.bytesConsumed += int64(n)
	b.maybeResizeLocked()

	return n, nil
}

// readFromRing copies data from the ring buffer into p. Caller must hold b.mu.
func (b *bufferedStreamReader) readFromRing(p []byte) int {
	n := 0
	for len(p) > 0 && b.count > 0 {
		avail := b.size - b.rPos
		if avail > b.count {
			avail = b.count
		}
		if avail > len(p) {
			avail = len(p)
		}
		copy(p[:avail], b.buf[b.rPos:b.rPos+avail])
		b.rPos = (b.rPos + avail) % b.size
		b.count -= avail
		n += avail
		p = p[avail:]
	}
	return n
}

// maybeResizeLocked checks whether the buffer should be resized based on
// the observed consumer read rate. Caller must hold b.mu.
func (b *bufferedStreamReader) maybeResizeLocked() {
	elapsed := time.Since(b.lastResizeCheck)
	if elapsed < resizeCheckInterval {
		return
	}

	// Calculate instantaneous consumer read rate.
	elapsedSec := elapsed.Seconds()
	if elapsedSec <= 0 {
		return
	}
	instantRate := float64(b.bytesConsumed) / elapsedSec

	// Update EMA (alpha = 0.3).
	if b.effectiveBitrate <= 0 {
		b.effectiveBitrate = instantRate
	} else {
		b.effectiveBitrate = 0.7*b.effectiveBitrate + 0.3*instantRate
	}
	b.bytesConsumed = 0
	b.lastResizeCheck = time.Now()

	// Compute target buffer size: bitrate * target duration.
	targetSize := int(b.effectiveBitrate * targetBufferDuration.Seconds())

	// Clamp to [min, max].
	if targetSize < minStreamBufSize {
		targetSize = minStreamBufSize
	}
	if targetSize > maxStreamBufSize {
		targetSize = maxStreamBufSize
	}

	// Under memory pressure, shrink to minimum.
	if memoryPressureHigh() {
		targetSize = minStreamBufSize
	}

	// Only resize if difference exceeds threshold to avoid thrashing.
	current := b.size
	diff := targetSize - current
	if diff < 0 {
		diff = -diff
	}
	if float64(diff)/float64(current) < resizeThreshold {
		return
	}

	b.resizeLocked(targetSize)
}

// resizeLocked allocates a new ring buffer and copies existing data.
// Caller must hold b.mu.
func (b *bufferedStreamReader) resizeLocked(newSize int) {
	if newSize == b.size {
		return
	}

	oldSize := b.size
	newBuf := make([]byte, newSize)

	// Copy existing buffered data to the new buffer.
	copied := 0
	if b.count > 0 {
		// If data would exceed new size, truncate (keep most recent data).
		toCopy := b.count
		if toCopy > newSize {
			// Skip oldest data.
			skip := toCopy - newSize
			b.rPos = (b.rPos + skip) % b.size
			b.count -= skip
			toCopy = newSize
		}

		// Copy from ring buffer to linear new buffer.
		pos := b.rPos
		for copied < toCopy {
			avail := b.size - pos
			if avail > toCopy-copied {
				avail = toCopy - copied
			}
			copy(newBuf[copied:copied+avail], b.buf[pos:pos+avail])
			pos = (pos + avail) % b.size
			copied += avail
		}
	}

	b.buf = newBuf
	b.size = newSize
	b.rPos = 0
	b.wPos = copied
	b.count = copied

	b.logger.Info("stream buffer resized",
		slog.Int("from", oldSize),
		slog.Int("to", newSize),
		slog.Float64("bitrate_mbps", b.effectiveBitrate/1024/1024),
	)
	metrics.HLSBufferResizesTotal.Inc()
	metrics.HLSBufferSizeBytes.Set(float64(newSize))
}

// Close stops the fill loop and closes the underlying source.
func (b *bufferedStreamReader) Close() error {
	b.mu.Lock()
	b.closed = true
	b.cond.Broadcast()
	b.mu.Unlock()
	b.cancel()
	return b.source.Close()
}

// Buffered returns the number of bytes currently in the buffer.
func (b *bufferedStreamReader) Buffered() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

// EffectiveBitrate returns the EMA-smoothed consumer read rate in bytes/sec.
func (b *bufferedStreamReader) EffectiveBitrate() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.effectiveBitrate
}

// SetRateLimit sets the download rate limit in bytes/sec. Pass 0 to remove.
func (b *bufferedStreamReader) SetRateLimit(bytesPerSec int64) {
	b.rateLimitBPS.Store(bytesPerSec)
}

// RateLimit returns the current rate limit in bytes/sec (0 = unlimited).
func (b *bufferedStreamReader) RateLimit() int64 {
	return b.rateLimitBPS.Load()
}

// memoryPressureHigh returns true when the Go heap exceeds the configured limit.
func memoryPressureHigh() bool {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Alloc > memoryPressureLimit
}
