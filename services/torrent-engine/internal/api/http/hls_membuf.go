package apihttp

import (
	"container/list"
	"strings"
	"sync"

	"torrentstream/internal/metrics"
)

// hlsMemBuffer is an LRU in-memory buffer for HLS segments.
// It keeps the most recently accessed segments in RAM so that
// hot segments near the playhead are served without any disk I/O.
type hlsMemBuffer struct {
	mu       sync.Mutex
	maxBytes int64
	used     int64
	items    map[string]*list.Element
	order    *list.List // front = most recently used
}

type memBufEntry struct {
	key  string
	data []byte
}

func newHLSMemBuffer(maxBytes int64) *hlsMemBuffer {
	if maxBytes <= 0 {
		return nil
	}
	return &hlsMemBuffer{
		maxBytes: maxBytes,
		items:    make(map[string]*list.Element),
		order:    list.New(),
	}
}

// Put stores segment data under the given path key.
// If the buffer is over budget after insertion, the least recently
// used entries are evicted until the budget is satisfied.
func (b *hlsMemBuffer) Put(path string, data []byte) {
	if b == nil || len(data) == 0 {
		return
	}
	size := int64(len(data))
	if size > b.maxBytes {
		return // single segment exceeds entire budget
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Update existing entry.
	if el, ok := b.items[path]; ok {
		old := el.Value.(*memBufEntry)
		b.used -= int64(len(old.data))
		old.data = data
		b.used += size
		b.order.MoveToFront(el)
		b.evictLocked()
		b.updateMetrics()
		return
	}

	// Evict to make room.
	for b.used+size > b.maxBytes && b.order.Len() > 0 {
		b.evictOldestLocked()
	}

	entry := &memBufEntry{key: path, data: data}
	el := b.order.PushFront(entry)
	b.items[path] = el
	b.used += size
	b.updateMetrics()
}

// Get retrieves segment data and promotes it in the LRU.
func (b *hlsMemBuffer) Get(path string) ([]byte, bool) {
	if b == nil {
		return nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	el, ok := b.items[path]
	if !ok {
		metrics.HLSMemBufMissesTotal.Inc()
		return nil, false
	}
	b.order.MoveToFront(el)
	metrics.HLSMemBufHitsTotal.Inc()
	return el.Value.(*memBufEntry).data, true
}

// PurgePrefix removes all entries whose key starts with the given prefix.
func (b *hlsMemBuffer) PurgePrefix(prefix string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	for key, el := range b.items {
		if strings.HasPrefix(key, prefix) {
			b.used -= int64(len(el.Value.(*memBufEntry).data))
			b.order.Remove(el)
			delete(b.items, key)
		}
	}
	b.updateMetrics()
}

// MaxBytes returns the maximum memory budget in bytes.
func (b *hlsMemBuffer) MaxBytes() int64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.maxBytes
}

// Resize updates the maximum memory budget and evicts if over the new limit.
func (b *hlsMemBuffer) Resize(newMaxBytes int64) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maxBytes = newMaxBytes
	b.evictLocked()
	b.updateMetrics()
}

// TotalSize returns the current memory usage in bytes.
func (b *hlsMemBuffer) TotalSize() int64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// Len returns the number of cached segments.
func (b *hlsMemBuffer) Len() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.items)
}

func (b *hlsMemBuffer) evictLocked() {
	for b.used > b.maxBytes && b.order.Len() > 0 {
		b.evictOldestLocked()
	}
}

func (b *hlsMemBuffer) evictOldestLocked() {
	el := b.order.Back()
	if el == nil {
		return
	}
	entry := el.Value.(*memBufEntry)
	b.used -= int64(len(entry.data))
	b.order.Remove(el)
	delete(b.items, entry.key)
	metrics.HLSMemBufEvictionsTotal.Inc()
}

func (b *hlsMemBuffer) updateMetrics() {
	metrics.HLSMemBufSizeBytes.Set(float64(b.used))
	metrics.HLSMemBufEntries.Set(float64(len(b.items)))
}
