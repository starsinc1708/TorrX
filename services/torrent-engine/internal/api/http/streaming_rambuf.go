package apihttp

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultRAMBufSize = 16 * 1024 * 1024 // 16 MB ring buffer
	ramBufReadChunk   = 256 * 1024       // 256 KB per source read
	ramBufMaxStall    = 3 * time.Minute
)

// RAMBuffer wraps an io.ReadCloser with a fixed-size ring buffer.
// A background goroutine fills the buffer from the source. Read() blocks
// when the buffer is empty. Unlike bufferedStreamReader, there is no rate
// limiting or adaptive sizing — the FSM controls flow externally.
type RAMBuffer struct {
	source io.ReadCloser
	buf    []byte
	size   int
	rPos   int
	wPos   int
	count  int

	mu     sync.Mutex
	dataCh chan struct{}
	srcErr error
	closed bool
	ctx    context.Context
	cancel context.CancelFunc
	logger *slog.Logger
}

// NewRAMBuffer creates a ring buffer that continuously fills from source.
func NewRAMBuffer(source io.ReadCloser, size int, logger *slog.Logger) *RAMBuffer {
	if size <= 0 {
		size = defaultRAMBufSize
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &RAMBuffer{
		source: source,
		buf:    make([]byte, size),
		size:   size,
		dataCh: make(chan struct{}, 1),
		ctx:    ctx,
		cancel: cancel,
		logger: logger,
	}
	go b.fillLoop()
	return b
}

func (b *RAMBuffer) signal() {
	select {
	case b.dataCh <- struct{}{}:
	default:
	}
}

// fillLoop reads from the source into the ring buffer. Transient EOF from
// a responsive torrent reader is retried with exponential backoff.
func (b *RAMBuffer) fillLoop() {
	const (
		initialBackoff = 10 * time.Millisecond
		maxBackoff     = 200 * time.Millisecond
	)

	tmp := make([]byte, ramBufReadChunk)
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
			b.signal()
			b.mu.Unlock()
			backoff = initialBackoff
			stallSince = time.Time{}
		}
		if err != nil {
			if err != io.EOF {
				b.mu.Lock()
				b.srcErr = err
				b.signal()
				b.mu.Unlock()
				return
			}
			if stallSince.IsZero() {
				stallSince = time.Now()
			}
			if time.Since(stallSince) >= ramBufMaxStall {
				b.logger.Warn("RAMBuffer source EOF after max stall",
					slog.Duration("stalled", time.Since(stallSince)))
				b.mu.Lock()
				b.srcErr = io.EOF
				b.signal()
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

// writeToRing copies data into the ring buffer. Caller must hold b.mu.
func (b *RAMBuffer) writeToRing(data []byte) int {
	written := 0
	for len(data) > 0 && b.count < b.size {
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
// or the context is cancelled.
func (b *RAMBuffer) Read(p []byte) (int, error) {
	b.mu.Lock()
	if b.count > 0 {
		n := b.readFromRing(p)
		b.mu.Unlock()
		return n, nil
	}
	for b.count == 0 && b.srcErr == nil && !b.closed {
		b.mu.Unlock()
		select {
		case <-b.dataCh:
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		}
		b.mu.Lock()
	}
	if b.closed {
		b.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	if b.count == 0 && b.srcErr != nil {
		err := b.srcErr
		b.mu.Unlock()
		return 0, err
	}
	n := b.readFromRing(p)
	b.mu.Unlock()
	return n, nil
}

// readFromRing copies data from the ring buffer into p. Caller must hold b.mu.
func (b *RAMBuffer) readFromRing(p []byte) int {
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

// Buffered returns the number of bytes currently in the buffer.
func (b *RAMBuffer) Buffered() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int64(b.count)
}

// Clear resets the buffer for seek. Does NOT close the source.
func (b *RAMBuffer) Clear() {
	b.mu.Lock()
	b.rPos = 0
	b.wPos = 0
	b.count = 0
	b.srcErr = nil
	b.mu.Unlock()
}

// Close stops the fill loop and closes the underlying source.
func (b *RAMBuffer) Close() error {
	b.mu.Lock()
	b.closed = true
	b.signal()
	b.mu.Unlock()
	b.cancel()
	return b.source.Close()
}

// Prebuffer blocks until at least minBytes are buffered, the source errors,
// the reader is closed, the context is cancelled, or the timeout fires.
func (b *RAMBuffer) Prebuffer(ctx context.Context, minBytes int64, timeout time.Duration) error {
	deadline := time.After(timeout)
	b.mu.Lock()
	for int64(b.count) < minBytes && b.srcErr == nil && !b.closed {
		b.mu.Unlock()
		select {
		case <-b.dataCh:
		case <-deadline:
			return nil // best-effort: start FFmpeg with whatever we have
		case <-ctx.Done():
			return ctx.Err()
		}
		b.mu.Lock()
	}
	b.mu.Unlock()
	return nil
}
