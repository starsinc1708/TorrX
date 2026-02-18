package apihttp

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// slowReader yields data in small chunks with a delay between each.
type slowReader struct {
	data  []byte
	pos   int
	delay time.Duration
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func (r *slowReader) Close() error { return nil }

func TestPrebuffer_ReachesMinBytes(t *testing.T) {
	// Source has 1 MB of data; prebuffer asks for 512 KB.
	data := make([]byte, 1<<20)
	src := &slowReader{data: data, delay: time.Millisecond}
	br := newBufferedStreamReader(src, 2<<20, slog.Default())
	defer br.Close()

	err := br.Prebuffer(context.Background(), 512*1024, 5*time.Second)
	if err != nil {
		t.Fatalf("Prebuffer returned error: %v", err)
	}
	if br.Buffered() < 512*1024 {
		t.Fatalf("expected >= 512 KB buffered, got %d", br.Buffered())
	}
}

func TestPrebuffer_TimeoutBestEffort(t *testing.T) {
	// Source blocks forever (never writes data). Timeout should fire and
	// Prebuffer should return nil (best-effort).
	r, w := io.Pipe()
	_ = w // keep write end open so Read blocks
	defer r.Close()
	defer w.Close()

	br := newBufferedStreamReader(r, 2<<20, slog.Default())
	defer br.Close()

	start := time.Now()
	err := br.Prebuffer(context.Background(), 6<<20, 200*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Prebuffer should return nil on timeout, got: %v", err)
	}
	if elapsed < 150*time.Millisecond {
		t.Fatalf("expected ~200ms timeout, returned in %v", elapsed)
	}
}

func TestPrebuffer_ContextCancelled(t *testing.T) {
	r, w := io.Pipe()
	_ = w
	defer r.Close()
	defer w.Close()

	br := newBufferedStreamReader(r, 2<<20, slog.Default())
	defer br.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := br.Prebuffer(ctx, 6<<20, 10*time.Second)
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

// errorReader returns a non-EOF error after delivering some data.
type errorReader struct {
	data []byte
	pos  int
}

func (r *errorReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.ErrUnexpectedEOF // terminal (non-EOF) error
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func (r *errorReader) Close() error { return nil }

func TestPrebuffer_SourceErrorReturnsEarly(t *testing.T) {
	// Source has only 100 bytes then returns a terminal error; prebuffer
	// asks for 1 MB. Should return promptly when srcErr is set.
	data := make([]byte, 100)
	src := &errorReader{data: data}
	br := newBufferedStreamReader(src, 2<<20, slog.Default())
	defer br.Close()

	// Wait for fillLoop to read all data and encounter the terminal error.
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	err := br.Prebuffer(context.Background(), 1<<20, 5*time.Second)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Prebuffer returned error: %v", err)
	}
	// Should return almost immediately (srcErr set), not after 5s timeout.
	if elapsed > 1*time.Second {
		t.Fatalf("Prebuffer took too long after source error: %v", elapsed)
	}
}
