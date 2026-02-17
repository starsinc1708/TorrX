package apihttp

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"
)

// ---- Buffered stream reader (absorbs torrent download stalls) ---------------

const (
	defaultStreamBufSize = 8 * 1024 * 1024 // 8 MB ring buffer
	streamBufReadChunk   = 256 * 1024       // 256 KB per source read
	streamBufStallWarn   = 30 * time.Second  // warn after 30s of no data
)

// bufferedStreamReader wraps an io.Reader with a fixed-size ring buffer.
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
}

func newBufferedStreamReader(source io.ReadCloser, bufSize int, logger *slog.Logger) *bufferedStreamReader {
	if bufSize <= 0 {
		bufSize = defaultStreamBufSize
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &bufferedStreamReader{
		source: source,
		buf:    make([]byte, bufSize),
		size:   bufSize,
		ctx:    ctx,
		cancel: cancel,
		logger: logger,
	}
	b.cond = sync.NewCond(&b.mu)
	go b.fillLoop()
	return b
}

// fillLoop continuously reads from the source into the ring buffer.
func (b *bufferedStreamReader) fillLoop() {
	tmp := make([]byte, streamBufReadChunk)
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}

		n, err := b.source.Read(tmp)
		if n > 0 {
			b.mu.Lock()
			written := b.writeToRing(tmp[:n])
			_ = written
			b.cond.Broadcast()
			b.mu.Unlock()
		}
		if err != nil {
			b.mu.Lock()
			b.srcErr = err
			b.cond.Broadcast()
			b.mu.Unlock()
			return
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
