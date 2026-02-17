package apihttp

import (
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"torrentstream/internal/metrics"
)

func init() {
	// Register metrics once for the test binary so that membuf metric
	// updates don't panic on nil collectors.
	reg := prometheus.NewRegistry()
	metrics.Register(reg)
}

func TestMemBuf_PutGet(t *testing.T) {
	buf := newHLSMemBuffer(1024)
	if buf == nil {
		t.Fatal("expected non-nil buffer")
	}

	buf.Put("/tmp/seg1.ts", []byte("hello"))
	data, ok := buf.Get("/tmp/seg1.ts")
	if !ok {
		t.Fatal("expected hit")
	}
	if string(data) != "hello" {
		t.Fatalf("got %q, want %q", data, "hello")
	}
}

func TestMemBuf_Miss(t *testing.T) {
	buf := newHLSMemBuffer(1024)
	_, ok := buf.Get("/tmp/nonexistent.ts")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestMemBuf_Eviction(t *testing.T) {
	// Budget for 2 entries of 10 bytes each.
	buf := newHLSMemBuffer(20)

	buf.Put("/a", make([]byte, 10))
	buf.Put("/b", make([]byte, 10))
	if buf.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", buf.Len())
	}

	// Adding /c should evict /a (LRU).
	buf.Put("/c", make([]byte, 10))
	if buf.Len() != 2 {
		t.Fatalf("expected 2 entries after eviction, got %d", buf.Len())
	}
	if _, ok := buf.Get("/a"); ok {
		t.Fatal("/a should have been evicted")
	}
	if _, ok := buf.Get("/b"); !ok {
		t.Fatal("/b should still be present")
	}
	if _, ok := buf.Get("/c"); !ok {
		t.Fatal("/c should be present")
	}
}

func TestMemBuf_LRUPromotion(t *testing.T) {
	buf := newHLSMemBuffer(30)

	buf.Put("/a", make([]byte, 10))
	buf.Put("/b", make([]byte, 10))
	buf.Put("/c", make([]byte, 10))

	// Access /a to promote it — /b is now LRU.
	buf.Get("/a")

	// Adding /d should evict /b (LRU), not /a.
	buf.Put("/d", make([]byte, 10))
	if _, ok := buf.Get("/b"); ok {
		t.Fatal("/b should have been evicted")
	}
	if _, ok := buf.Get("/a"); !ok {
		t.Fatal("/a should still be present after promotion")
	}
}

func TestMemBuf_PurgePrefix(t *testing.T) {
	buf := newHLSMemBuffer(1024)

	buf.Put("/hls/abc/seg1.ts", []byte("1"))
	buf.Put("/hls/abc/seg2.ts", []byte("2"))
	buf.Put("/hls/def/seg1.ts", []byte("3"))

	buf.PurgePrefix("/hls/abc/")
	if buf.Len() != 1 {
		t.Fatalf("expected 1 entry after purge, got %d", buf.Len())
	}
	if _, ok := buf.Get("/hls/def/seg1.ts"); !ok {
		t.Fatal("/hls/def/seg1.ts should survive purge")
	}
}

func TestMemBuf_TotalSize(t *testing.T) {
	buf := newHLSMemBuffer(1024)
	buf.Put("/a", make([]byte, 100))
	buf.Put("/b", make([]byte, 200))
	if buf.TotalSize() != 300 {
		t.Fatalf("expected 300, got %d", buf.TotalSize())
	}
}

func TestMemBuf_UpdateExisting(t *testing.T) {
	buf := newHLSMemBuffer(1024)
	buf.Put("/a", make([]byte, 100))
	buf.Put("/a", make([]byte, 50))
	if buf.TotalSize() != 50 {
		t.Fatalf("expected 50 after update, got %d", buf.TotalSize())
	}
	if buf.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", buf.Len())
	}
}

func TestMemBuf_NilSafe(t *testing.T) {
	var buf *hlsMemBuffer
	buf.Put("/a", []byte("x"))
	if _, ok := buf.Get("/a"); ok {
		t.Fatal("nil buffer should return miss")
	}
	buf.PurgePrefix("/")
	if buf.TotalSize() != 0 {
		t.Fatal("nil buffer size should be 0")
	}
	if buf.Len() != 0 {
		t.Fatal("nil buffer len should be 0")
	}
}

func TestMemBuf_ZeroBudget(t *testing.T) {
	buf := newHLSMemBuffer(0)
	if buf != nil {
		t.Fatal("zero budget should return nil")
	}
}

func TestMemBuf_OversizedEntry(t *testing.T) {
	buf := newHLSMemBuffer(10)
	buf.Put("/big", make([]byte, 20))
	if buf.Len() != 0 {
		t.Fatal("oversized entry should not be stored")
	}
}

func TestMemBuf_ManyEntries(t *testing.T) {
	buf := newHLSMemBuffer(1000)
	for i := 0; i < 100; i++ {
		buf.Put(fmt.Sprintf("/seg%d", i), make([]byte, 10))
	}
	if buf.Len() != 100 {
		t.Fatalf("expected 100 entries, got %d", buf.Len())
	}
	if buf.TotalSize() != 1000 {
		t.Fatalf("expected 1000 bytes, got %d", buf.TotalSize())
	}
	// Adding one more should evict the oldest.
	buf.Put("/seg100", make([]byte, 10))
	if buf.Len() != 100 {
		t.Fatalf("expected 100 entries after eviction, got %d", buf.Len())
	}
}
