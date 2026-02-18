package apihttp

import (
	"container/heap"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"torrentstream/internal/metrics"
)

// ---- eviction min-heap (ordered by mtime, oldest first) --------------------

type evictionEntry struct {
	path      string
	mtime     time.Time
	size      int64
	heapIdx   int // maintained by heap.Interface for Fix/Remove
	torrentID string
	fileIndex int
	trackKey  string
}

type evictionMinHeap []*evictionEntry

func (h evictionMinHeap) Len() int            { return len(h) }
func (h evictionMinHeap) Less(i, j int) bool   { return h[i].mtime.Before(h[j].mtime) }
func (h evictionMinHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIdx = i
	h[j].heapIdx = j
}
func (h *evictionMinHeap) Push(x any) {
	e := x.(*evictionEntry)
	e.heapIdx = len(*h)
	*h = append(*h, e)
}
func (h *evictionMinHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.heapIdx = -1
	*h = old[:n-1]
	return e
}

const (
	defaultCacheMaxBytes int64         = 10 << 30 // 10 GB
	defaultCacheMaxAge   time.Duration = 7 * 24 * time.Hour
)

type cachedSegment struct {
	StartTime float64 // seconds from beginning of media
	EndTime   float64
	Path      string // absolute path to .ts file
	Size      int64
}

// hlsCache stores encoded HLS segments keyed by time position so that
// previously viewed positions can be served instantly without re-encoding.
//
// Directory layout:
//
//	{baseDir}/{torrentID}/{fileIndex}/a{audio}-s{sub}/t{start}-{end}.ts
type hlsCache struct {
	baseDir      string
	maxBytes     int64
	maxAge       time.Duration
	mu           sync.RWMutex
	index        map[string]map[int]map[string][]cachedSegment // torrentID → fileIndex → trackKey → sorted segments
	totalSize    int64
	evictHeap    evictionMinHeap              // min-heap by mtime for O(log n) eviction
	evictByPath  map[string]*evictionEntry    // path → heap entry for O(1) lookups
	logger       *slog.Logger
}

func newHLSCache(baseDir string, maxBytes int64, maxAge time.Duration, logger *slog.Logger) *hlsCache {
	if maxBytes <= 0 {
		maxBytes = defaultCacheMaxBytes
	}
	if maxAge <= 0 {
		maxAge = defaultCacheMaxAge
	}
	_ = os.MkdirAll(baseDir, 0o755)

	c := &hlsCache{
		baseDir:     baseDir,
		maxBytes:    maxBytes,
		maxAge:      maxAge,
		index:       make(map[string]map[int]map[string][]cachedSegment),
		evictByPath: make(map[string]*evictionEntry),
		logger:      logger,
	}
	c.rebuild()
	return c
}

// trackKey returns the track directory component for the given audio/subtitle tracks
// and optional quality variant.
func trackKey(audioTrack, subtitleTrack int, variant string) string {
	if variant != "" {
		return fmt.Sprintf("a%d-s%d-%s", audioTrack, subtitleTrack, variant)
	}
	return fmt.Sprintf("a%d-s%d", audioTrack, subtitleTrack)
}

// segmentFilename encodes a time range into a cache-safe filename.
func segmentFilename(startTime, endTime float64) string {
	return fmt.Sprintf("t%010.3f-%010.3f.ts", startTime, endTime)
}

// parseSegmentFilename extracts start/end times from a cached segment filename.
func parseSegmentFilename(name string) (start, end float64, ok bool) {
	// Expected format: t0000000.000-0000000.000.ts
	if !strings.HasPrefix(name, "t") || !strings.HasSuffix(name, ".ts") {
		return 0, 0, false
	}
	body := name[1 : len(name)-3] // strip "t" and ".ts"
	parts := strings.SplitN(body, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	s, err1 := strconv.ParseFloat(parts[0], 64)
	e, err2 := strconv.ParseFloat(parts[1], 64)
	if err1 != nil || err2 != nil || e <= s {
		return 0, 0, false
	}
	return s, e, true
}

// rebuild scans the cache directory on startup and repopulates the in-memory index.
func (c *hlsCache) rebuild() {
	entries, err := os.ReadDir(c.baseDir)
	if err != nil {
		return
	}
	for _, torrentEntry := range entries {
		if !torrentEntry.IsDir() {
			continue
		}
		torrentID := torrentEntry.Name()
		torrentDir := filepath.Join(c.baseDir, torrentID)
		fileEntries, err := os.ReadDir(torrentDir)
		if err != nil {
			continue
		}
		for _, fileEntry := range fileEntries {
			if !fileEntry.IsDir() {
				continue
			}
			fileIndex, err := strconv.Atoi(fileEntry.Name())
			if err != nil {
				continue
			}
			trackDir := filepath.Join(torrentDir, fileEntry.Name())
			trackEntries, err := os.ReadDir(trackDir)
			if err != nil {
				continue
			}
			for _, tEntry := range trackEntries {
				if !tEntry.IsDir() {
					continue
				}
				tk := tEntry.Name()
				segDir := filepath.Join(trackDir, tk)
				segEntries, err := os.ReadDir(segDir)
				if err != nil {
					continue
				}
				for _, segEntry := range segEntries {
					if segEntry.IsDir() {
						continue
					}
					startT, endT, ok := parseSegmentFilename(segEntry.Name())
					if !ok {
						continue
					}
					info, err := segEntry.Info()
					if err != nil {
						continue
					}
					segPath := filepath.Join(segDir, segEntry.Name())
					seg := cachedSegment{
						StartTime: startT,
						EndTime:   endT,
						Path:      segPath,
						Size:      info.Size(),
					}
					c.addToIndex(torrentID, fileIndex, tk, seg)
					c.totalSize += seg.Size

					// Add to eviction heap.
					e := &evictionEntry{
						path:      segPath,
						mtime:     info.ModTime(),
						size:      info.Size(),
						torrentID: torrentID,
						fileIndex: fileIndex,
						trackKey:  tk,
					}
					c.evictHeap = append(c.evictHeap, e)
					c.evictByPath[segPath] = e
				}
			}
		}
	}
	// Build heap in O(n) after collecting all entries.
	heap.Init(&c.evictHeap)
}

// addToIndex inserts a segment into the in-memory index, maintaining sort order.
func (c *hlsCache) addToIndex(torrentID string, fileIndex int, tk string, seg cachedSegment) {
	if c.index[torrentID] == nil {
		c.index[torrentID] = make(map[int]map[string][]cachedSegment)
	}
	if c.index[torrentID][fileIndex] == nil {
		c.index[torrentID][fileIndex] = make(map[string][]cachedSegment)
	}
	segs := c.index[torrentID][fileIndex][tk]

	// Check for duplicate/overlapping segment.
	for _, existing := range segs {
		if existing.StartTime == seg.StartTime && existing.EndTime == seg.EndTime {
			return // already cached
		}
	}

	segs = append(segs, seg)
	sort.Slice(segs, func(i, j int) bool { return segs[i].StartTime < segs[j].StartTime })
	c.index[torrentID][fileIndex][tk] = segs
}

// Store copies a segment file into the cache directory.
// variant is the quality variant identifier (e.g. "v0", "v1") for multi-variant
// jobs, or empty string for single-variant.
func (c *hlsCache) Store(torrentID string, fileIndex, audioTrack, subtitleTrack int, variant string, startTime, endTime float64, srcPath string) error {
	if endTime <= startTime {
		return nil
	}

	tk := trackKey(audioTrack, subtitleTrack, variant)
	dir := filepath.Join(c.baseDir, torrentID, strconv.Itoa(fileIndex), tk)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	dstName := segmentFilename(startTime, endTime)
	dstPath := filepath.Join(dir, dstName)

	// Skip if already cached.
	if info, err := os.Stat(dstPath); err == nil {
		c.mu.Lock()
		c.addToIndex(torrentID, fileIndex, tk, cachedSegment{
			StartTime: startTime,
			EndTime:   endTime,
			Path:      dstPath,
			Size:      info.Size(),
		})
		c.mu.Unlock()
		return nil
	}

	size, err := copyFile(srcPath, dstPath)
	if err != nil {
		return err
	}

	seg := cachedSegment{
		StartTime: startTime,
		EndTime:   endTime,
		Path:      dstPath,
		Size:      size,
	}

	c.mu.Lock()
	c.addToIndex(torrentID, fileIndex, tk, seg)
	c.totalSize += size

	// Push to eviction heap.
	e := &evictionEntry{
		path:      dstPath,
		mtime:     time.Now(),
		size:      size,
		torrentID: torrentID,
		fileIndex: fileIndex,
		trackKey:  tk,
	}
	heap.Push(&c.evictHeap, e)
	c.evictByPath[dstPath] = e

	// Collect eviction candidates under the lock (index/heap ops only).
	// File deletion happens after releasing the lock so that c.mu is not held
	// during potentially-slow os.Remove calls. This prevents a lock-chain
	// deadlock where a goroutine blocked in LookupRange (waiting for c.mu.RLock)
	// holds m.mu.Lock, starving all other HLS operations.
	var toEvict []*evictionEntry
	for c.totalSize > c.maxBytes && c.evictHeap.Len() > 0 {
		oldest := heap.Pop(&c.evictHeap).(*evictionEntry)
		delete(c.evictByPath, oldest.path)
		c.totalSize -= oldest.size
		c.removeSegmentFromIndex(oldest.torrentID, oldest.fileIndex, oldest.trackKey, oldest.path)
		toEvict = append(toEvict, oldest)
	}
	if c.totalSize < 0 {
		c.totalSize = 0
	}
	c.mu.Unlock()

	// Delete evicted files without holding the cache lock.
	for _, oldest := range toEvict {
		if err := os.Remove(oldest.path); err != nil && !os.IsNotExist(err) {
			c.logger.Warn("hls cache evict failed",
				slog.String("path", oldest.path),
				slog.String("error", err.Error()),
			)
			metrics.HLSCacheCleanupErrors.Inc()
		}
	}

	return nil
}

// Lookup finds a cached segment covering the given time position.
func (c *hlsCache) Lookup(torrentID string, fileIndex, audioTrack, subtitleTrack int, variant string, timeSec float64) (cachedSegment, bool) {
	tk := trackKey(audioTrack, subtitleTrack, variant)

	c.mu.RLock()
	defer c.mu.RUnlock()

	segs := c.getSegments(torrentID, fileIndex, tk)
	if len(segs) == 0 {
		return cachedSegment{}, false
	}

	// Binary search for the segment containing timeSec.
	idx := sort.Search(len(segs), func(i int) bool {
		return segs[i].EndTime > timeSec
	})
	if idx < len(segs) && segs[idx].StartTime <= timeSec && segs[idx].EndTime > timeSec {
		return segs[idx], true
	}
	return cachedSegment{}, false
}

// LookupRange returns all contiguous cached segments starting from fromTime.
func (c *hlsCache) LookupRange(torrentID string, fileIndex, audioTrack, subtitleTrack int, variant string, fromTime float64) []cachedSegment {
	tk := trackKey(audioTrack, subtitleTrack, variant)

	c.mu.RLock()
	defer c.mu.RUnlock()

	segs := c.getSegments(torrentID, fileIndex, tk)
	if len(segs) == 0 {
		return nil
	}

	// Find first segment containing or starting at fromTime.
	idx := sort.Search(len(segs), func(i int) bool {
		return segs[i].EndTime > fromTime
	})
	if idx >= len(segs) || segs[idx].StartTime > fromTime+0.5 {
		return nil
	}

	// Collect contiguous segments (gap tolerance: 0.5s).
	result := []cachedSegment{segs[idx]}
	for i := idx + 1; i < len(segs); i++ {
		prev := result[len(result)-1]
		if segs[i].StartTime-prev.EndTime > 0.5 {
			break
		}
		result = append(result, segs[i])
	}
	return result
}

// PurgeTorrent removes all cached segments for a torrent.
func (c *hlsCache) PurgeTorrent(torrentID string) {
	c.mu.Lock()
	segs := c.index[torrentID]
	var purgedSize int64
	for _, byFile := range segs {
		for _, byTrack := range byFile {
			for _, seg := range byTrack {
				purgedSize += seg.Size
				// Remove from eviction heap.
				if e, ok := c.evictByPath[seg.Path]; ok {
					if e.heapIdx >= 0 {
						heap.Remove(&c.evictHeap, e.heapIdx)
					}
					delete(c.evictByPath, seg.Path)
				}
			}
		}
	}
	delete(c.index, torrentID)
	c.totalSize -= purgedSize
	if c.totalSize < 0 {
		c.totalSize = 0
	}
	c.mu.Unlock()

	dir := filepath.Join(c.baseDir, torrentID)
	if err := os.RemoveAll(dir); err != nil {
		time.Sleep(500 * time.Millisecond)
		if retryErr := os.RemoveAll(dir); retryErr != nil {
			c.logger.Error("hls cache purge failed",
				slog.String("dir", dir),
				slog.String("error", retryErr.Error()),
			)
			metrics.HLSCacheCleanupErrors.Inc()
		}
	}
}

// BaseDir returns the root directory of the disk cache.
func (c *hlsCache) BaseDir() string {
	return c.baseDir
}

// SegmentPath returns the absolute file path for a cached segment.
func (c *hlsCache) SegmentPath(torrentID string, fileIndex, audioTrack, subtitleTrack int, variant string, startTime, endTime float64) string {
	tk := trackKey(audioTrack, subtitleTrack, variant)
	return filepath.Join(c.baseDir, torrentID, strconv.Itoa(fileIndex), tk, segmentFilename(startTime, endTime))
}

// MaxBytes returns the maximum cache size in bytes.
func (c *hlsCache) MaxBytes() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.maxBytes
}

// MaxAge returns the maximum segment age.
func (c *hlsCache) MaxAge() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.maxAge
}

// SetMaxBytes updates the maximum cache size and triggers eviction if needed.
func (c *hlsCache) SetMaxBytes(v int64) {
	c.mu.Lock()
	c.maxBytes = v
	needEvict := c.totalSize > c.maxBytes
	c.mu.Unlock()
	if needEvict {
		c.evict()
	}
}

// SetMaxAge updates the maximum segment age.
func (c *hlsCache) SetMaxAge(v time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxAge = v
}

// TotalSize returns the current total cache size in bytes.
func (c *hlsCache) TotalSize() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.totalSize
}

func (c *hlsCache) getSegments(torrentID string, fileIndex int, tk string) []cachedSegment {
	byFile, ok := c.index[torrentID]
	if !ok {
		return nil
	}
	byTrack, ok := byFile[fileIndex]
	if !ok {
		return nil
	}
	return byTrack[tk]
}

// evict removes the oldest segment files until totalSize is under maxBytes
// and removes segments older than maxAge. Uses the min-heap for O(log n)
// per evicted segment instead of the previous O(n log n) full-scan approach.
func (c *hlsCache) evict() {
	c.mu.Lock()
	defer c.mu.Unlock()

	cutoff := time.Now().Add(-c.maxAge)

	for c.evictHeap.Len() > 0 {
		// Peek at the oldest entry.
		oldest := c.evictHeap[0]
		overSize := c.totalSize > c.maxBytes
		overAge := oldest.mtime.Before(cutoff)
		if !overSize && !overAge {
			break
		}

		heap.Pop(&c.evictHeap)
		delete(c.evictByPath, oldest.path)
		if err := os.Remove(oldest.path); err != nil && !os.IsNotExist(err) {
			c.logger.Warn("hls cache evict failed",
				slog.String("path", oldest.path),
				slog.String("error", err.Error()),
			)
			metrics.HLSCacheCleanupErrors.Inc()
			continue
		}
		c.totalSize -= oldest.size
		c.removeSegmentFromIndex(oldest.torrentID, oldest.fileIndex, oldest.trackKey, oldest.path)
	}

	if c.totalSize < 0 {
		c.totalSize = 0
	}
}

func (c *hlsCache) removeSegmentFromIndex(torrentID string, fileIndex int, tk, path string) {
	segs := c.index[torrentID][fileIndex][tk]
	for i, seg := range segs {
		if seg.Path == path {
			c.index[torrentID][fileIndex][tk] = append(segs[:i], segs[i+1:]...)
			break
		}
	}
	// Clean up empty maps.
	if len(c.index[torrentID][fileIndex][tk]) == 0 {
		delete(c.index[torrentID][fileIndex], tk)
	}
	if len(c.index[torrentID][fileIndex]) == 0 {
		delete(c.index[torrentID], fileIndex)
	}
	if len(c.index[torrentID]) == 0 {
		delete(c.index, torrentID)
	}
}

// copyFile copies src to dst and returns the number of bytes written.
func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if closeErr := out.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(dst)
		return 0, err
	}
	return n, nil
}

// parseM3U8Segments parses a playlist file and returns segment filenames with their durations.
type m3u8Segment struct {
	Filename string
	Duration float64 // seconds
}

func parseM3U8Segments(playlistPath string) ([]m3u8Segment, error) {
	data, err := os.ReadFile(playlistPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	var segments []m3u8Segment
	var nextDuration float64

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXTINF:") {
			durStr := strings.TrimPrefix(line, "#EXTINF:")
			if idx := strings.IndexByte(durStr, ','); idx >= 0 {
				durStr = durStr[:idx]
			}
			nextDuration, _ = strconv.ParseFloat(durStr, 64)
		} else if !strings.HasPrefix(line, "#") && line != "" && nextDuration > 0 {
			segments = append(segments, m3u8Segment{
				Filename: line,
				Duration: nextDuration,
			})
			nextDuration = 0
		}
	}
	return segments, nil
}
