package usecase

import (
	"context"
	"io"
	"testing"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// priorityCall records a single SetPiecePriority invocation.
type priorityCall struct {
	file  domain.FileRef
	rng   domain.Range
	prio  domain.Priority
}

// recordingSession captures all SetPiecePriority calls for assertion.
type recordingSession struct {
	files []domain.FileRef
	calls []priorityCall
	rdr   ports.StreamReader
}

func (s *recordingSession) ID() domain.TorrentID { return "rec-t1" }
func (s *recordingSession) Files() []domain.FileRef {
	return append([]domain.FileRef(nil), s.files...)
}
func (s *recordingSession) SelectFile(i int) (domain.FileRef, error) {
	if i < 0 || i >= len(s.files) {
		return domain.FileRef{}, domain.ErrNotFound
	}
	return s.files[i], nil
}
func (s *recordingSession) SetPiecePriority(f domain.FileRef, r domain.Range, p domain.Priority) {
	s.calls = append(s.calls, priorityCall{file: f, rng: r, prio: p})
}
func (s *recordingSession) Start() error { return nil }
func (s *recordingSession) Stop() error  { return nil }
func (s *recordingSession) NewReader(f domain.FileRef) (ports.StreamReader, error) {
	return s.rdr, nil
}
func (s *recordingSession) reset() { s.calls = nil }

// controllableReader allows tests to control Read/Seek behavior.
type controllableReader struct {
	readN     int // bytes returned per Read; 0 → EOF
	pos       int64
	readahead int64
	seekErr   error
	closeErr  error
}

func (r *controllableReader) SetContext(context.Context) {}
func (r *controllableReader) SetReadahead(n int64)       { r.readahead = n }
func (r *controllableReader) SetResponsive()             {}
func (r *controllableReader) Read(p []byte) (int, error) {
	if r.readN <= 0 {
		return 0, io.EOF
	}
	n := r.readN
	if n > len(p) {
		n = len(p)
	}
	r.pos += int64(n)
	return n, nil
}
func (r *controllableReader) Seek(off int64, whence int) (int64, error) {
	if r.seekErr != nil {
		return 0, r.seekErr
	}
	switch whence {
	case io.SeekStart:
		r.pos = off
	case io.SeekCurrent:
		r.pos += off
	case io.SeekEnd:
		r.pos = off // simplified for tests
	}
	if r.pos < 0 {
		r.pos = 0
	}
	return r.pos, nil
}
func (r *controllableReader) Close() error { return r.closeErr }

// newTestReader creates a slidingPriorityReader with sensible defaults for tests.
func newTestReader(session *recordingSession, file domain.FileRef, window int64, reader *controllableReader) *slidingPriorityReader {
	if reader == nil {
		reader = &controllableReader{readN: 1024}
	}
	return newSlidingPriorityReader(reader, session, file, 16<<20, window, nil, "test-id")
}

// ---------------------------------------------------------------------------
// streamPriorityWindow tests
// ---------------------------------------------------------------------------

func TestStreamPriorityWindow(t *testing.T) {
	MB := int64(1 << 20)
	GB := int64(1 << 30)

	tests := []struct {
		name      string
		readahead int64
		fileLen   int64
		want      int64
	}{
		{
			name:      "default readahead with small file",
			readahead: 0,
			fileLen:   100 * MB,
			want:      64 * MB, // default 16MB * 4 = 64MB > 32MB min
		},
		{
			name:      "small readahead clamps to min 32MB",
			readahead: 1 * MB,
			fileLen:   100 * MB,
			want:      32 * MB, // 1MB * 4 = 4MB < 32MB min
		},
		{
			name:      "large readahead normal range",
			readahead: 32 * MB,
			fileLen:   500 * MB,
			want:      128 * MB, // 32MB * 4 = 128MB
		},
		{
			name:      "1% scaling for large file",
			readahead: 16 * MB,
			fileLen:   25 * GB,
			want:      256 * MB, // 1% of 25GB = 250MB → 256MB max
		},
		{
			name:      "1% scaling mid range",
			readahead: 16 * MB,
			fileLen:   20 * GB,
			want:      20 * GB / 100, // 1% of 20GB = 214748364 (integer division)
		},
		{
			name:      "clamps to max 256MB",
			readahead: 128 * MB,
			fileLen:   50 * GB,
			want:      256 * MB, // 128*4=512 but max is 256
		},
		{
			name:      "negative readahead uses default",
			readahead: -1,
			fileLen:   100 * MB,
			want:      64 * MB, // default 16MB * 4 = 64MB
		},
		{
			name:      "zero file length ignores scaling",
			readahead: 16 * MB,
			fileLen:   0,
			want:      64 * MB, // 16*4=64, no scaling
		},
		{
			name:      "tiny file min clamp",
			readahead: 2 * MB,
			fileLen:   10 * MB,
			want:      32 * MB, // 2*4=8 < 32 min
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := streamPriorityWindow(tc.readahead, tc.fileLen)
			if got != tc.want {
				t.Errorf("streamPriorityWindow(%d, %d) = %d, want %d",
					tc.readahead, tc.fileLen, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// applyStartupGradient tests
// ---------------------------------------------------------------------------

func TestApplyStartupGradient(t *testing.T) {
	MB := int64(1 << 20)

	tests := []struct {
		name      string
		fileLen   int64
		window    int64
		wantBands []struct {
			prio domain.Priority
			off  int64
			len  int64
		}
	}{
		{
			name:    "normal window produces 4 bands",
			fileLen: 1 << 30,
			window:  64 * MB,
			wantBands: []struct {
				prio domain.Priority
				off  int64
				len  int64
			}{
				{domain.PriorityHigh, 0, 4 * MB},      // startup high: 4MB
				{domain.PriorityNext, 4 * MB, 4 * MB},  // startup next: 4MB
				// Readahead: (64-8)MB / 4 = 14MB
				{domain.PriorityReadahead, 8 * MB, 14 * MB},
				// Normal: remaining = 64 - 4 - 4 - 14 = 42MB
				{domain.PriorityNormal, 22 * MB, 42 * MB},
			},
		},
		{
			name:    "small window only high band",
			fileLen: 100,
			window:  2 * MB,
			wantBands: []struct {
				prio domain.Priority
				off  int64
				len  int64
			}{
				{domain.PriorityHigh, 0, 2 * MB}, // clamped to window
			},
		},
		{
			name:    "medium window high + next",
			fileLen: 100 * MB,
			window:  6 * MB,
			wantBands: []struct {
				prio domain.Priority
				off  int64
				len  int64
			}{
				{domain.PriorityHigh, 0, 4 * MB},
				{domain.PriorityNext, 4 * MB, 2 * MB}, // remaining 2MB
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			file := domain.FileRef{Index: 0, Path: "test.mkv", Length: tc.fileLen}
			sess := &recordingSession{files: []domain.FileRef{file}}
			applyStartupGradient(sess, file, tc.window)

			if len(sess.calls) != len(tc.wantBands) {
				t.Fatalf("got %d bands, want %d: %+v", len(sess.calls), len(tc.wantBands), sess.calls)
			}
			for i, want := range tc.wantBands {
				got := sess.calls[i]
				if got.prio != want.prio || got.rng.Off != want.off || got.rng.Length != want.len {
					t.Errorf("band %d: got {prio=%d off=%d len=%d}, want {prio=%d off=%d len=%d}",
						i, got.prio, got.rng.Off, got.rng.Length, want.prio, want.off, want.len)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// newSlidingPriorityReader constructor tests
// ---------------------------------------------------------------------------

func TestNewSlidingPriorityReaderDefaults(t *testing.T) {
	MB := int64(1 << 20)
	file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
	sess := &recordingSession{files: []domain.FileRef{file}}
	reader := &controllableReader{readN: 1024}

	t.Run("step is window/4 for normal window", func(t *testing.T) {
		spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 64*MB, nil, "t1")
		if spr.step != 16*MB {
			t.Errorf("step = %d, want %d", spr.step, 16*MB)
		}
	})

	t.Run("step clamps to minSlidingPriorityStep for small window", func(t *testing.T) {
		spr := newSlidingPriorityReader(reader, sess, file, 1*MB, 2*MB, nil, "t1")
		// 2MB / 4 = 512KB < 1MB min → clamp to 1MB
		if spr.step != minSlidingPriorityStep {
			t.Errorf("step = %d, want %d", spr.step, int64(minSlidingPriorityStep))
		}
	})

	t.Run("backtrack clamped to half window", func(t *testing.T) {
		spr := newSlidingPriorityReader(reader, sess, file, 100*MB, 64*MB, nil, "t1")
		// 100MB readahead > 64MB/2 = 32MB → clamp to 32MB
		if spr.backtrack != 32*MB {
			t.Errorf("backtrack = %d, want %d", spr.backtrack, 32*MB)
		}
	})

	t.Run("negative readahead gives zero backtrack", func(t *testing.T) {
		spr := newSlidingPriorityReader(reader, sess, file, -5, 64*MB, nil, "t1")
		if spr.backtrack != 0 {
			t.Errorf("backtrack = %d, want 0", spr.backtrack)
		}
	})

	t.Run("minWindow and maxWindow set correctly", func(t *testing.T) {
		spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 64*MB, nil, "t1")
		if spr.minWindow != minPriorityWindowBytes {
			t.Errorf("minWindow = %d, want %d", spr.minWindow, minPriorityWindowBytes)
		}
		if spr.maxWindow != maxPriorityWindowBytes {
			t.Errorf("maxWindow = %d, want %d", spr.maxWindow, maxPriorityWindowBytes)
		}
	})
}

// ---------------------------------------------------------------------------
// 4-tier gradient priority tests
// ---------------------------------------------------------------------------

func TestApplyGradientPriority(t *testing.T) {
	MB := int64(1 << 20)

	t.Run("normal window produces 4 bands at correct offsets", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 64*MB, nil)

		sess.reset()
		spr.applyGradientPriority(100 * MB)

		if len(sess.calls) != 4 {
			t.Fatalf("expected 4 gradient bands, got %d: %+v", len(sess.calls), sess.calls)
		}

		// Band 1: High [100MB, 102MB)
		assertCall(t, sess.calls[0], 100*MB, 2*MB, domain.PriorityHigh, "high band")

		// Band 2: Next [102MB, 104MB)
		assertCall(t, sess.calls[1], 102*MB, 2*MB, domain.PriorityNext, "next band")

		// Band 3: Readahead — 25% of (64-2-2)=60MB = 15MB
		assertCall(t, sess.calls[2], 104*MB, 15*MB, domain.PriorityReadahead, "readahead band")

		// Band 4: Normal — remaining: 64-2-2-15 = 45MB
		assertCall(t, sess.calls[3], 119*MB, 45*MB, domain.PriorityNormal, "normal band")

		// Total coverage must equal window
		var total int64
		for _, c := range sess.calls {
			total += c.rng.Length
		}
		if total != 64*MB {
			t.Errorf("total gradient coverage = %d, want %d", total, 64*MB)
		}
	})

	t.Run("small window collapses to fewer bands", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "small.mp4", Length: 100 * MB}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 3*MB, nil)

		sess.reset()
		spr.applyGradientPriority(0)

		// 3MB window: 2MB high, 1MB next, nothing for readahead/normal
		if len(sess.calls) != 2 {
			t.Fatalf("expected 2 bands for 3MB window, got %d: %+v", len(sess.calls), sess.calls)
		}
		assertCall(t, sess.calls[0], 0, 2*MB, domain.PriorityHigh, "high")
		assertCall(t, sess.calls[1], 2*MB, 1*MB, domain.PriorityNext, "next")
	})

	t.Run("tiny window only has high band", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "tiny.mp4", Length: 50 * MB}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 1*MB, nil)

		sess.reset()
		spr.applyGradientPriority(0)

		if len(sess.calls) != 1 {
			t.Fatalf("expected 1 band for 1MB window, got %d", len(sess.calls))
		}
		assertCall(t, sess.calls[0], 0, 1*MB, domain.PriorityHigh, "high only")
	})

	t.Run("exactly 4MB window: high 2MB + next 2MB", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "test.mp4", Length: 100 * MB}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 4*MB, nil)

		sess.reset()
		spr.applyGradientPriority(10 * MB)

		if len(sess.calls) != 2 {
			t.Fatalf("expected 2 bands for 4MB window, got %d", len(sess.calls))
		}
		assertCall(t, sess.calls[0], 10*MB, 2*MB, domain.PriorityHigh, "high")
		assertCall(t, sess.calls[1], 12*MB, 2*MB, domain.PriorityNext, "next")
	})

	t.Run("buffer-low boost expands high band to 6MB", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 64*MB, nil)

		// Activate buffer-low boost
		spr.bufferBoostUntil = time.Now().Add(5 * time.Second)

		sess.reset()
		spr.applyGradientPriority(0)

		// Band 1: High should be 6MB (boosted), not 2MB
		assertCall(t, sess.calls[0], 0, 6*MB, domain.PriorityHigh, "boosted high band")

		// Total still equals window
		var total int64
		for _, c := range sess.calls {
			total += c.rng.Length
		}
		if total != 64*MB {
			t.Errorf("total gradient with boost = %d, want %d", total, 64*MB)
		}
	})

	t.Run("gradient at file offset zero", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 32*MB, nil)

		sess.reset()
		spr.applyGradientPriority(0)

		// First band starts at offset 0
		if sess.calls[0].rng.Off != 0 {
			t.Errorf("first band offset = %d, want 0", sess.calls[0].rng.Off)
		}
	})
}

// ---------------------------------------------------------------------------
// deprioritizeRange + boundary protection tests
// ---------------------------------------------------------------------------

func TestDeprioritizeRange(t *testing.T) {
	MB := int64(1 << 20)

	t.Run("middle of large file is deprioritized", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 64*MB, nil)

		sess.reset()
		spr.deprioritizeRange(100*MB, 50*MB)

		if len(sess.calls) != 1 {
			t.Fatalf("expected 1 deprioritize call, got %d", len(sess.calls))
		}
		assertCall(t, sess.calls[0], 100*MB, 50*MB, domain.PriorityNone, "middle deprioritize")
	})

	t.Run("first 8MB never deprioritized", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 64*MB, nil)

		sess.reset()
		spr.deprioritizeRange(0, 20*MB)

		// Should only deprioritize [8MB, 20MB), skipping head protection
		if len(sess.calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(sess.calls))
		}
		assertCall(t, sess.calls[0], 8*MB, 12*MB, domain.PriorityNone, "after head protection")
	})

	t.Run("last 8MB never deprioritized", func(t *testing.T) {
		fileLen := int64(100 * MB)
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: fileLen}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 64*MB, nil)

		sess.reset()
		spr.deprioritizeRange(85*MB, 15*MB)

		// Should only deprioritize [85MB, 92MB), skipping tail protection (92MB..100MB)
		tailStart := fileLen - 8*MB // 92MB
		if len(sess.calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(sess.calls))
		}
		assertCall(t, sess.calls[0], 85*MB, tailStart-85*MB, domain.PriorityNone, "before tail protection")
	})

	t.Run("range entirely in head protection produces no call", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 64*MB, nil)

		sess.reset()
		spr.deprioritizeRange(0, 5*MB)

		if len(sess.calls) != 0 {
			t.Fatalf("expected no deprioritize calls for range within head protection, got %d", len(sess.calls))
		}
	})

	t.Run("range entirely in tail protection produces no call", func(t *testing.T) {
		fileLen := int64(100 * MB)
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: fileLen}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 64*MB, nil)

		sess.reset()
		spr.deprioritizeRange(95*MB, 5*MB) // 95-100MB, all in tail protection (92-100MB)

		if len(sess.calls) != 0 {
			t.Fatalf("expected no deprioritize calls for range within tail protection, got %d", len(sess.calls))
		}
	})

	t.Run("small file fully protected", func(t *testing.T) {
		// File smaller than 16MB: entire file is protected (head 8MB + tail 8MB overlap)
		file := domain.FileRef{Index: 0, Path: "small.mp4", Length: 10 * MB}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 32*MB, nil)

		sess.reset()
		spr.deprioritizeRange(0, 10*MB)

		if len(sess.calls) != 0 {
			t.Fatalf("expected no deprioritize for fully-protected small file, got %d", len(sess.calls))
		}
	})

	t.Run("zero length is no-op", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 64*MB, nil)

		sess.reset()
		spr.deprioritizeRange(50*MB, 0)

		if len(sess.calls) != 0 {
			t.Fatalf("expected no calls for zero length, got %d", len(sess.calls))
		}
	})

	t.Run("negative length is no-op", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 64*MB, nil)

		sess.reset()
		spr.deprioritizeRange(50*MB, -10*MB)

		if len(sess.calls) != 0 {
			t.Fatalf("expected no calls for negative length, got %d", len(sess.calls))
		}
	})

	t.Run("range spanning both boundaries clips to middle", func(t *testing.T) {
		fileLen := int64(30 * MB)
		file := domain.FileRef{Index: 0, Path: "medium.mp4", Length: fileLen}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 32*MB, nil)

		sess.reset()
		spr.deprioritizeRange(0, 30*MB) // entire file

		// Head protection: [0, 8MB), tail protection: [22MB, 30MB)
		// Deprioritize: [8MB, 22MB) = 14MB
		if len(sess.calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(sess.calls))
		}
		assertCall(t, sess.calls[0], 8*MB, 14*MB, domain.PriorityNone, "middle between boundaries")
	})
}

// ---------------------------------------------------------------------------
// updatePriorityWindowLocked tests
// ---------------------------------------------------------------------------

func TestUpdatePriorityWindowLocked(t *testing.T) {
	MB := int64(1 << 20)

	t.Run("skips update when delta below step threshold", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 64*MB, nil)

		// First update: force to establish baseline
		spr.pos = 10 * MB
		spr.updatePriorityWindowLocked(true)
		callsAfterFirst := len(sess.calls)

		// Second update: small delta, not forced
		spr.pos = 10*MB + 100 // tiny move
		spr.updatePriorityWindowLocked(false)

		if len(sess.calls) != callsAfterFirst {
			t.Fatalf("expected no new calls for sub-step delta, got %d new calls",
				len(sess.calls)-callsAfterFirst)
		}
	})

	t.Run("force bypasses step threshold", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 64*MB, nil)

		spr.pos = 10 * MB
		spr.updatePriorityWindowLocked(true)
		callsAfterFirst := len(sess.calls)

		// Same position, but forced
		spr.updatePriorityWindowLocked(true)

		if len(sess.calls) <= callsAfterFirst {
			t.Fatal("expected force to produce new priority calls")
		}
	})

	t.Run("deprioritizes old window when moving forward", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		spr := newTestReader(sess, file, 32*MB, nil)

		// Initial position
		spr.pos = 0
		spr.updatePriorityWindowLocked(true)
		callsAfterFirst := len(sess.calls)

		// Move forward past step threshold
		spr.pos = 200 * MB // large jump, non-overlapping windows
		sess.reset()
		spr.updatePriorityWindowLocked(true)

		// Should have deprioritization call(s) + gradient calls
		hasPriorityNone := false
		for _, c := range sess.calls {
			if c.prio == domain.PriorityNone {
				hasPriorityNone = true
				break
			}
		}
		_ = callsAfterFirst
		if !hasPriorityNone {
			t.Fatal("expected PriorityNone call for old window deprioritization")
		}
	})
}

// ---------------------------------------------------------------------------
// Seek boost tests
// ---------------------------------------------------------------------------

func TestSeekBoost(t *testing.T) {
	MB := int64(1 << 20)

	t.Run("seek doubles window and sets 10s boost", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		reader := &controllableReader{readN: 1024}
		spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 64*MB, nil, "t1")

		windowBefore := spr.window

		_, err := spr.Seek(100*MB, io.SeekStart)
		if err != nil {
			t.Fatalf("Seek: %v", err)
		}

		spr.mu.Lock()
		windowAfter := spr.window
		boostUntil := spr.seekBoostUntil
		spr.mu.Unlock()

		expected := windowBefore * 2
		if expected > maxPriorityWindowBytes {
			expected = maxPriorityWindowBytes
		}
		if windowAfter != expected {
			t.Errorf("window after seek = %d, want %d", windowAfter, expected)
		}
		if time.Until(boostUntil) < 9*time.Second {
			t.Errorf("seekBoostUntil too soon: %v", time.Until(boostUntil))
		}
	})

	t.Run("seek boost clamps to maxWindow", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		reader := &controllableReader{readN: 1024}
		spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 200*MB, nil, "t1")

		_, _ = spr.Seek(100*MB, io.SeekStart)

		spr.mu.Lock()
		windowAfter := spr.window
		spr.mu.Unlock()

		// 200*2=400 > 256 max → clamped
		if windowAfter != maxPriorityWindowBytes {
			t.Errorf("window after boost clamp = %d, want %d", windowAfter, maxPriorityWindowBytes)
		}
	})

	t.Run("seek updates position", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		reader := &controllableReader{readN: 1024}
		spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 64*MB, nil, "t1")

		pos, _ := spr.Seek(42*MB, io.SeekStart)

		spr.mu.Lock()
		sprPos := spr.pos
		spr.mu.Unlock()

		if pos != 42*MB {
			t.Errorf("Seek returned %d, want %d", pos, 42*MB)
		}
		if sprPos != 42*MB {
			t.Errorf("internal pos = %d, want %d", sprPos, 42*MB)
		}
	})

	t.Run("seek wakes dormant reader", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		reader := &controllableReader{readN: 1024}
		spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 64*MB, nil, "t1")

		spr.mu.Lock()
		spr.enterDormancyLocked()
		spr.mu.Unlock()

		spr.mu.Lock()
		wasDormant := spr.dormant
		spr.mu.Unlock()
		if !wasDormant {
			t.Fatal("expected dormant before seek")
		}

		_, _ = spr.Seek(50*MB, io.SeekStart)

		spr.mu.Lock()
		isDormant := spr.dormant
		spr.mu.Unlock()
		if isDormant {
			t.Fatal("expected reader to wake after seek")
		}
	})
}

// ---------------------------------------------------------------------------
// Read behavior tests
// ---------------------------------------------------------------------------

func TestReadUpdatesPosAndPriority(t *testing.T) {
	MB := int64(1 << 20)
	file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
	sess := &recordingSession{files: []domain.FileRef{file}}
	reader := &controllableReader{readN: 4096}
	spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 64*MB, nil, "t1")

	buf := make([]byte, 4096)
	n, err := spr.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != 4096 {
		t.Fatalf("Read returned %d bytes, want 4096", n)
	}

	spr.mu.Lock()
	pos := spr.pos
	spr.mu.Unlock()

	if pos != 4096 {
		t.Errorf("pos = %d, want 4096", pos)
	}
}

func TestReadWakesDormantReader(t *testing.T) {
	MB := int64(1 << 20)
	file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
	sess := &recordingSession{files: []domain.FileRef{file}}
	reader := &controllableReader{readN: 256}
	spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 64*MB, nil, "t1")

	spr.mu.Lock()
	spr.enterDormancyLocked()
	spr.mu.Unlock()

	buf := make([]byte, 256)
	n, _ := spr.Read(buf)
	if n == 0 {
		t.Fatal("expected data from Read")
	}

	spr.mu.Lock()
	isDormant := spr.dormant
	spr.mu.Unlock()
	if isDormant {
		t.Fatal("expected reader to wake after Read with data")
	}
}

func TestReadEOFPassthrough(t *testing.T) {
	file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
	sess := &recordingSession{files: []domain.FileRef{file}}
	reader := &controllableReader{readN: 0} // returns EOF
	spr := newSlidingPriorityReader(reader, sess, file, 16<<20, 64<<20, nil, "t1")

	buf := make([]byte, 256)
	_, err := spr.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// adjustWindowLocked / EMA tests
// ---------------------------------------------------------------------------

func TestAdjustWindowEMA(t *testing.T) {
	MB := int64(1 << 20)
	file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
	sess := &recordingSession{files: []domain.FileRef{file}}
	reader := &controllableReader{readN: 1024}
	spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 64*MB, nil, "t1")

	t.Run("no update when elapsed < 500ms", func(t *testing.T) {
		spr.mu.Lock()
		spr.lastUpdateTime = time.Now() // just now
		spr.bytesReadSinceLastUpdate = 10 * MB
		windowBefore := spr.window
		spr.adjustWindowLocked()
		windowAfter := spr.window
		spr.mu.Unlock()

		if windowBefore != windowAfter {
			t.Errorf("window changed within 500ms: %d → %d", windowBefore, windowAfter)
		}
	})

	t.Run("first EMA sets rate directly", func(t *testing.T) {
		spr.mu.Lock()
		spr.effectiveBytesPerSec = 0
		spr.bytesReadSinceLastUpdate = 10 * MB
		spr.lastUpdateTime = time.Now().Add(-1 * time.Second) // 1s ago
		spr.seekBoostUntil = time.Time{}                       // no boost
		spr.adjustWindowLocked()
		rate := spr.effectiveBytesPerSec
		spr.mu.Unlock()

		// First measurement: should be close to 10MB/s
		if rate < 9*float64(MB) || rate > 11*float64(MB) {
			t.Errorf("first EMA rate = %f, want ~%d", rate, 10*MB)
		}
	})

	t.Run("subsequent EMA smooths with alpha=0.3", func(t *testing.T) {
		spr.mu.Lock()
		spr.effectiveBytesPerSec = 5 * float64(MB) // previous rate: 5 MB/s
		spr.bytesReadSinceLastUpdate = 10 * MB      // instant: 10 MB/s
		spr.lastUpdateTime = time.Now().Add(-1 * time.Second)
		spr.seekBoostUntil = time.Time{}
		spr.adjustWindowLocked()
		rate := spr.effectiveBytesPerSec
		spr.mu.Unlock()

		// EMA: 0.7 * 5MB + 0.3 * 10MB = 6.5MB/s
		expected := 0.7*5*float64(MB) + 0.3*10*float64(MB)
		delta := rate - expected
		if delta < 0 {
			delta = -delta
		}
		if delta > 0.1*float64(MB) {
			t.Errorf("EMA rate = %f, want ~%f (0.7*5MB + 0.3*10MB)", rate, expected)
		}
	})

	t.Run("dynamic window targets 30s buffer", func(t *testing.T) {
		spr.mu.Lock()
		spr.effectiveBytesPerSec = 2 * float64(MB) // 2 MB/s
		spr.bytesReadSinceLastUpdate = 2 * MB
		spr.lastUpdateTime = time.Now().Add(-1 * time.Second)
		spr.seekBoostUntil = time.Time{}
		spr.bufferFillFunc = nil // no buffer-low
		spr.adjustWindowLocked()
		window := spr.window
		spr.mu.Unlock()

		// Target: rate * 30s = ~2MB * 30 = 60MB
		// EMA: 0.7*2MB + 0.3*2MB = 2MB → 2*30 = 60MB
		if window < 50*MB || window > 70*MB {
			t.Errorf("dynamic window = %d, want ~60MB", window)
		}
	})

	t.Run("dynamic window clamps to min", func(t *testing.T) {
		spr.mu.Lock()
		spr.effectiveBytesPerSec = 0.1 * float64(MB) // very slow: 0.1 MB/s
		spr.bytesReadSinceLastUpdate = int64(0.1 * float64(MB))
		spr.lastUpdateTime = time.Now().Add(-1 * time.Second)
		spr.seekBoostUntil = time.Time{}
		spr.bufferFillFunc = nil
		spr.adjustWindowLocked()
		window := spr.window
		spr.mu.Unlock()

		// 0.1 * 30 = 3MB → clamps to 32MB min
		if window != minPriorityWindowBytes {
			t.Errorf("window = %d, want min %d", window, minPriorityWindowBytes)
		}
	})

	t.Run("skips dynamic adjustment during seek boost", func(t *testing.T) {
		spr.mu.Lock()
		spr.seekBoostUntil = time.Now().Add(5 * time.Second)
		windowBefore := spr.window
		spr.bytesReadSinceLastUpdate = 50 * MB
		spr.lastUpdateTime = time.Now().Add(-1 * time.Second)
		spr.adjustWindowLocked()
		windowAfter := spr.window
		spr.mu.Unlock()

		if windowBefore != windowAfter {
			t.Errorf("window changed during seek boost: %d → %d", windowBefore, windowAfter)
		}
	})
}

// ---------------------------------------------------------------------------
// Buffer-low boost tests
// ---------------------------------------------------------------------------

func TestBufferLowBoost(t *testing.T) {
	MB := int64(1 << 20)

	t.Run("buffer below 30% doubles window for 5s", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		reader := &controllableReader{readN: 1024}
		spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 64*MB, nil, "t1")

		spr.mu.Lock()
		spr.bufferFillFunc = func() float64 { return 0.1 } // 10% < 30%
		spr.bytesReadSinceLastUpdate = 5 * MB
		spr.lastUpdateTime = time.Now().Add(-1 * time.Second)
		spr.effectiveBytesPerSec = 5 * float64(MB)
		spr.seekBoostUntil = time.Time{} // no seek boost
		spr.bufferBoostUntil = time.Time{} // expired
		spr.adjustWindowLocked()
		window := spr.window
		boostUntil := spr.bufferBoostUntil
		spr.mu.Unlock()

		if window != 128*MB {
			t.Errorf("window during buffer-low boost = %d, want %d", window, 128*MB)
		}
		if time.Until(boostUntil) < 4*time.Second {
			t.Errorf("bufferBoostUntil too soon: %v", time.Until(boostUntil))
		}
	})

	t.Run("buffer above 30% does not trigger boost", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		reader := &controllableReader{readN: 1024}
		spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 64*MB, nil, "t1")

		spr.mu.Lock()
		spr.bufferFillFunc = func() float64 { return 0.5 } // 50% > 30%
		spr.bytesReadSinceLastUpdate = 2 * MB
		spr.lastUpdateTime = time.Now().Add(-1 * time.Second)
		spr.effectiveBytesPerSec = 2 * float64(MB)
		spr.seekBoostUntil = time.Time{}
		spr.bufferBoostUntil = time.Time{}
		spr.adjustWindowLocked()
		boostUntil := spr.bufferBoostUntil
		spr.mu.Unlock()

		if !boostUntil.IsZero() {
			t.Error("buffer-low boost should not trigger when fill > 30%")
		}
	})

	t.Run("buffer-low boost clamps to maxWindow", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		reader := &controllableReader{readN: 1024}
		spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 200*MB, nil, "t1")

		spr.mu.Lock()
		spr.bufferFillFunc = func() float64 { return 0.1 }
		spr.bytesReadSinceLastUpdate = 5 * MB
		spr.lastUpdateTime = time.Now().Add(-1 * time.Second)
		spr.effectiveBytesPerSec = 5 * float64(MB)
		spr.seekBoostUntil = time.Time{}
		spr.bufferBoostUntil = time.Time{}
		spr.adjustWindowLocked()
		window := spr.window
		spr.mu.Unlock()

		// 200*2=400 > 256 → clamped
		if window != maxPriorityWindowBytes {
			t.Errorf("window = %d, want max %d", window, maxPriorityWindowBytes)
		}
	})

	t.Run("no re-trigger while boost active", func(t *testing.T) {
		file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
		sess := &recordingSession{files: []domain.FileRef{file}}
		reader := &controllableReader{readN: 1024}
		spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 64*MB, nil, "t1")

		spr.mu.Lock()
		spr.bufferFillFunc = func() float64 { return 0.1 }
		spr.bufferBoostUntil = time.Now().Add(3 * time.Second) // still active
		spr.bytesReadSinceLastUpdate = 5 * MB
		spr.lastUpdateTime = time.Now().Add(-1 * time.Second)
		spr.effectiveBytesPerSec = 2 * float64(MB)
		spr.seekBoostUntil = time.Time{}
		windowBefore := spr.window
		spr.adjustWindowLocked()
		windowAfter := spr.window
		spr.mu.Unlock()

		// Should fall through to dynamic window, not re-trigger boost
		// Dynamic: EMA(0.7*2 + 0.3*5)*30 ≈ 90MB (clamped)
		// The key thing is it shouldn't have doubled again
		if windowAfter == windowBefore*2 {
			t.Error("boost should not re-trigger while already active")
		}
	})
}

// ---------------------------------------------------------------------------
// Close tests
// ---------------------------------------------------------------------------

func TestCloseUnregistersFromRegistry(t *testing.T) {
	file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
	sess := &recordingSession{files: []domain.FileRef{file}}
	reader := &controllableReader{readN: 1024}
	reg := newReaderRegistry()

	spr := newSlidingPriorityReader(reader, sess, file, 16<<20, 64<<20, reg, "t1")
	reg.register("t1", spr)

	reg.mu.Lock()
	before := len(reg.readers["t1"])
	reg.mu.Unlock()
	if before != 1 {
		t.Fatalf("expected 1 reader before close, got %d", before)
	}

	_ = spr.Close()

	reg.mu.Lock()
	after := len(reg.readers["t1"])
	reg.mu.Unlock()
	if after != 0 {
		t.Fatalf("expected 0 readers after close, got %d", after)
	}
}

func TestCloseWithNilRegistry(t *testing.T) {
	file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
	sess := &recordingSession{files: []domain.FileRef{file}}
	reader := &controllableReader{readN: 1024}

	spr := newSlidingPriorityReader(reader, sess, file, 16<<20, 64<<20, nil, "t1")

	// Should not panic
	err := spr.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Dormancy enter/exit tests
// ---------------------------------------------------------------------------

func TestEnterExitDormancy(t *testing.T) {
	MB := int64(1 << 20)
	file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
	sess := &recordingSession{files: []domain.FileRef{file}}
	reader := &controllableReader{readN: 1024}
	spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 64*MB, nil, "t1")

	// Force initial window so prevWindow > 0
	spr.mu.Lock()
	spr.updatePriorityWindowLocked(true)
	spr.mu.Unlock()

	t.Run("enter dormancy sets readahead to 0 and dormant flag", func(t *testing.T) {
		spr.mu.Lock()
		spr.enterDormancyLocked()
		isDormant := spr.dormant
		spr.mu.Unlock()

		if !isDormant {
			t.Fatal("expected dormant=true")
		}
		if reader.readahead != 0 {
			t.Errorf("readahead = %d, want 0", reader.readahead)
		}
	})

	t.Run("exit dormancy restores readahead and clears flag", func(t *testing.T) {
		spr.mu.Lock()
		spr.exitDormancyLocked()
		isDormant := spr.dormant
		spr.mu.Unlock()

		if isDormant {
			t.Fatal("expected dormant=false after exit")
		}
		if reader.readahead == 0 {
			t.Error("readahead should be restored (non-zero) after exit")
		}
	})
}

// ---------------------------------------------------------------------------
// BoostWindow tests
// ---------------------------------------------------------------------------

func TestBoostWindowMethod(t *testing.T) {
	MB := int64(1 << 20)
	file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
	sess := &recordingSession{files: []domain.FileRef{file}}
	reader := &controllableReader{readN: 1024}
	spr := newSlidingPriorityReader(reader, sess, file, 16*MB, 64*MB, nil, "t1")

	t.Run("doubles window and sets seekBoostUntil", func(t *testing.T) {
		sess.reset()
		spr.BoostWindow(10 * time.Second)

		spr.mu.Lock()
		window := spr.window
		until := spr.seekBoostUntil
		spr.mu.Unlock()

		if window != 128*MB {
			t.Errorf("window = %d, want %d", window, 128*MB)
		}
		if time.Until(until) < 9*time.Second {
			t.Error("seekBoostUntil not set correctly")
		}
	})

	t.Run("produces priority update calls", func(t *testing.T) {
		sess.reset()
		spr.BoostWindow(5 * time.Second)

		if len(sess.calls) == 0 {
			t.Fatal("expected priority update calls after BoostWindow")
		}
	})

	t.Run("clamps to maxWindow", func(t *testing.T) {
		// Set window close to max
		spr.mu.Lock()
		spr.window = 200 * MB
		spr.mu.Unlock()

		spr.BoostWindow(5 * time.Second)

		spr.mu.Lock()
		window := spr.window
		spr.mu.Unlock()

		if window != maxPriorityWindowBytes {
			t.Errorf("window = %d, want max %d", window, maxPriorityWindowBytes)
		}
	})
}

// ---------------------------------------------------------------------------
// EffectiveBytesPerSec tests
// ---------------------------------------------------------------------------

func TestEffectiveBytesPerSec(t *testing.T) {
	file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
	sess := &recordingSession{files: []domain.FileRef{file}}
	reader := &controllableReader{readN: 1024}
	spr := newSlidingPriorityReader(reader, sess, file, 16<<20, 64<<20, nil, "t1")

	// Initially zero
	if rate := spr.EffectiveBytesPerSec(); rate != 0 {
		t.Errorf("initial rate = %f, want 0", rate)
	}

	// Set a rate
	spr.mu.Lock()
	spr.effectiveBytesPerSec = 5.5e6
	spr.mu.Unlock()

	if rate := spr.EffectiveBytesPerSec(); rate != 5.5e6 {
		t.Errorf("rate = %f, want 5.5e6", rate)
	}
}

// ---------------------------------------------------------------------------
// Passthrough delegation tests
// ---------------------------------------------------------------------------

func TestSetContextDelegation(t *testing.T) {
	file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
	sess := &recordingSession{files: []domain.FileRef{file}}
	reader := &controllableReader{}
	spr := newSlidingPriorityReader(reader, sess, file, 16<<20, 64<<20, nil, "t1")

	ctx := context.WithValue(context.Background(), "key", "val")
	spr.SetContext(ctx)
	// controllableReader.SetContext is no-op but verify no panic
}

func TestSetReadaheadDelegation(t *testing.T) {
	file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
	sess := &recordingSession{files: []domain.FileRef{file}}
	reader := &controllableReader{}
	spr := newSlidingPriorityReader(reader, sess, file, 16<<20, 64<<20, nil, "t1")

	spr.SetReadahead(42)
	if reader.readahead != 42 {
		t.Errorf("readahead = %d, want 42", reader.readahead)
	}
}

func TestSetResponsiveDelegation(t *testing.T) {
	file := domain.FileRef{Index: 0, Path: "movie.mkv", Length: 1 << 30}
	sess := &recordingSession{files: []domain.FileRef{file}}
	reader := &controllableReader{}
	spr := newSlidingPriorityReader(reader, sess, file, 16<<20, 64<<20, nil, "t1")

	// Should not panic
	spr.SetResponsive()
}

// ---------------------------------------------------------------------------
// Interface compliance
// ---------------------------------------------------------------------------

func TestSlidingPriorityReaderImplementsStreamReader(t *testing.T) {
	var _ ports.StreamReader = (*slidingPriorityReader)(nil)
}

func TestSlidingPriorityReaderImplementsReadSeekCloser(t *testing.T) {
	var _ io.ReadSeekCloser = (*slidingPriorityReader)(nil)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func assertCall(t *testing.T, got priorityCall, wantOff, wantLen int64, wantPrio domain.Priority, label string) {
	t.Helper()
	if got.rng.Off != wantOff || got.rng.Length != wantLen || got.prio != wantPrio {
		t.Errorf("%s: got {off=%d len=%d prio=%d}, want {off=%d len=%d prio=%d}",
			label, got.rng.Off, got.rng.Length, got.prio, wantOff, wantLen, wantPrio)
	}
}
