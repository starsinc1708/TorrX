package apihttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
	"torrentstream/internal/metrics"
)

type HLSConfig struct {
	FFMPEGPath        string
	FFProbePath       string
	BaseDir           string
	DataDir           string
	ListenAddr        string
	Preset            string
	CRF               int
	AudioBitrate      string
	MaxCacheSizeBytes int64
	MaxCacheAge       time.Duration
	MemBufSizeBytes   int64
	SegmentDuration   int
}

type hlsKey struct {
	id            domain.TorrentID
	fileIndex     int
	audioTrack    int
	subtitleTrack int
}

type hlsJob struct {
	dir          string
	playlist     string
	ready        chan struct{}
	readyOnce    sync.Once
	err          error
	startOnce    sync.Once
	seekSeconds  float64
	ctx          context.Context
	cancel       context.CancelFunc
	ctrl         *PlaybackController
	lastActivity time.Time
	restartCount int
	multiVariant bool             // true when producing multiple quality variants
	variants     []qualityVariant // populated for multi-variant jobs
	genRef          *generationRef   // shared generation counter for stale reader detection
	consumptionRate  func() float64         // returns EMA consumer read rate (bytes/sec); nil if unavailable
	bufferedReader   *bufferedStreamReader // non-nil for pipe sources; used for rate limiting
	ffmpegProgressUs  int64  // atomic: last FFmpeg out_time in microseconds
	priorityBoostFunc func() // triggers priority window boost; nil if unavailable
	isPipeSource      bool   // true when reading from torrent pipe (incomplete file)

	// Cached rewritten playlist (avoids re-parsing on every m3u8 GET).
	rewrittenMu           sync.RWMutex
	rewrittenPlaylist     []byte
	rewrittenPlaylistPath string    // source playlist path that was cached
	rewrittenPlaylistMod  time.Time // mtime of source when cached
	rewrittenAudioTrack   int
	rewrittenSubTrack     int
	rewrittenCacheTime    time.Time // when the cache was last stored (TTL anchor)

	// Precomputed cumulative time index for segmentTimeOffset (O(1) lookup).
	timeIndexMu   sync.RWMutex
	timeIndex     map[string]float64 // segmentName → absolute start time (seconds)
	timeIndexSize int                // number of segments already indexed
}

type codecCacheEntry struct {
	isH264     bool
	isAAC      bool
	lastAccess time.Time // LRU tracking for eviction
}

type resolutionCacheEntry struct {
	width    int
	height   int
	duration float64 // seconds; 0 if unknown
	fps      float64 // frames per second; 0 if unknown
}

type hlsManager struct {
	stream                StreamTorrentUseCase
	engine                ports.Engine // optional; enables adaptive download rate limiting
	ffmpegPath            string
	ffprobePath           string
	baseDir               string
	dataDir               string
	listenAddr            string
	preset                string
	crf                   int
	audioBitrate          string
	mu                    sync.RWMutex
	jobs                  map[hlsKey]*hlsJob
	cache                 *hlsCache
	memBuf                *hlsMemBuffer
	codecCacheMu          sync.RWMutex
	codecCache            map[string]*codecCacheEntry // filePath → codec detection results
	codecCacheDirty       bool                         // true when in-memory cache diverged from disk
	codecCacheSaveTimer   *time.Timer                  // debounced disk write
	resolutionCacheMu     sync.RWMutex
	resolutionCache       map[string]*resolutionCacheEntry // filePath → resolution
	segLimiter            *segmentLimiter // per-IP rate limiter for segment requests
	lastHardSeek          map[hlsKey]time.Time // anti-seek-storm: last hard seek per key
	segmentDuration       int
	logger                *slog.Logger
	totalJobStarts        uint64
	totalJobFailures      uint64
	lastJobStartedAt      time.Time
	lastJobError          string
	lastJobErrorAt        time.Time
	lastPlaylistReady     time.Time
	totalSeekRequests     uint64
	totalSeekFailures     uint64
	lastSeekAt            time.Time
	lastSeekTarget        float64
	lastSeekError         string
	lastSeekErrorAt       time.Time
	totalAutoRestarts     uint64
	lastAutoRestartAt     time.Time
	lastAutoRestartReason string

	// Remux cache: background ffmpeg -c copy remux from MKV → MP4.
	remuxCache   map[string]*remuxEntry
	remuxCacheMu sync.Mutex
}

type hlsHealthSnapshot struct {
	ActiveJobs            int        `json:"activeJobs"`
	TotalJobStarts        uint64     `json:"totalJobStarts"`
	TotalJobFailures      uint64     `json:"totalJobFailures"`
	TotalSeekRequests     uint64     `json:"totalSeekRequests"`
	TotalSeekFailures     uint64     `json:"totalSeekFailures"`
	LastJobStartedAt      *time.Time `json:"lastJobStartedAt,omitempty"`
	LastPlaylistReady     *time.Time `json:"lastPlaylistReady,omitempty"`
	LastJobError          string     `json:"lastJobError,omitempty"`
	LastJobErrorAt        *time.Time `json:"lastJobErrorAt,omitempty"`
	LastSeekAt            *time.Time `json:"lastSeekAt,omitempty"`
	LastSeekTarget        float64    `json:"lastSeekTarget,omitempty"`
	LastSeekError         string     `json:"lastSeekError,omitempty"`
	LastSeekErrorAt       *time.Time `json:"lastSeekErrorAt,omitempty"`
	TotalAutoRestarts     uint64     `json:"totalAutoRestarts"`
	LastAutoRestartAt     *time.Time `json:"lastAutoRestartAt,omitempty"`
	LastAutoRestartReason string     `json:"lastAutoRestartReason,omitempty"`
}

const (
	hlsWatchdogInterval       = 5 * time.Second
	hlsWatchdogStallThreshold     = 90 * time.Second
	hlsPipeStallThreshold         = 5 * time.Minute // longer patience for pipe sources (incomplete files)
	hlsMaxAutoRestarts            = 5

	// Stall escalation ladder thresholds (applied before full restart).
	hlsStallEscalation1 = 30 * time.Second // L1: remove rate limit
	hlsStallEscalation2 = 60 * time.Second // L2: boost piece priority
)

var errSubtitleSourceUnavailable = errors.New("subtitle source file not ready")

func newHLSManager(stream StreamTorrentUseCase, engine ports.Engine, cfg HLSConfig, logger *slog.Logger) *hlsManager {
	baseDir := strings.TrimSpace(cfg.BaseDir)
	if baseDir == "" {
		baseDir = filepath.Join(os.TempDir(), "torrentstream-hls")
	}
	baseDir = filepath.Clean(baseDir)
	ffmpegPath := strings.TrimSpace(cfg.FFMPEGPath)
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	ffprobePath := strings.TrimSpace(cfg.FFProbePath)
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}

	dataDir := strings.TrimSpace(cfg.DataDir)
	if dataDir != "" {
		if abs, err := filepath.Abs(dataDir); err == nil {
			dataDir = abs
		}
		dataDir = filepath.Clean(dataDir)
	}

	if logger == nil {
		logger = slog.Default()
	}

	preset := strings.TrimSpace(cfg.Preset)
	if preset == "" {
		preset = "veryfast"
	}
	crf := cfg.CRF
	if crf <= 0 {
		crf = 23
	}
	audioBitrate := strings.TrimSpace(cfg.AudioBitrate)
	if audioBitrate == "" {
		audioBitrate = "128k"
	}

	cacheDir := filepath.Join(baseDir, "cache")
	cache := newHLSCache(cacheDir, cfg.MaxCacheSizeBytes, cfg.MaxCacheAge, logger)
	memBuf := newHLSMemBuffer(cfg.MemBufSizeBytes)

	segDur := cfg.SegmentDuration
	if segDur <= 0 {
		segDur = 2
	}

	mgr := &hlsManager{
		stream:          stream,
		engine:          engine,
		ffmpegPath:      ffmpegPath,
		ffprobePath:     ffprobePath,
		baseDir:         baseDir,
		dataDir:         dataDir,
		listenAddr:      strings.TrimSpace(cfg.ListenAddr),
		preset:          preset,
		crf:             crf,
		audioBitrate:    audioBitrate,
		segmentDuration: segDur,
		jobs:            make(map[hlsKey]*hlsJob),
		lastHardSeek:    make(map[hlsKey]time.Time),
		cache:           cache,
		memBuf:          memBuf,
		codecCache:      make(map[string]*codecCacheEntry),
		resolutionCache: make(map[string]*resolutionCacheEntry),
		remuxCache:      make(map[string]*remuxEntry),
		logger:          logger,
	}
	mgr.segLimiter = newSegmentLimiter(50, 20) // 50 req/s sustained, burst 20
	mgr.loadCodecCache()
	return mgr
}

// computeProfileHash returns an 8-char hex string that uniquely identifies
// the encoding configuration. Used to version the transcode cache directory.
func computeProfileHash(preset string, crf int, audioBitrate string, segDur int) string {
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%s:%d:%s:%d", preset, crf, audioBitrate, segDur)
	return fmt.Sprintf("%08x", h.Sum32())
}

// buildJobDir constructs the job directory path for a given key using
// the current encoding profile hash.
func (m *hlsManager) buildJobDir(key hlsKey) string {
	m.mu.RLock()
	preset := m.preset
	crf := m.crf
	audioBitrate := m.audioBitrate
	segDur := m.segmentDuration
	m.mu.RUnlock()

	if segDur <= 0 {
		segDur = 2
	}
	hash := computeProfileHash(preset, crf, audioBitrate, segDur)
	dir := filepath.Join(
		m.baseDir,
		string(key.id),
		strconv.Itoa(key.fileIndex),
		fmt.Sprintf("a%d-s%d-p%s", key.audioTrack, key.subtitleTrack, hash),
	)
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// segmentLimiter enforces per-IP rate limits on HLS segment requests.
// Stale entries are evicted periodically to prevent unbounded memory growth.
type segmentLimiter struct {
	mu       sync.Mutex
	limiters map[string]*limiterEntry
	r        rate.Limit
	b        int
	stopCh   chan struct{}
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newSegmentLimiter(rps float64, burst int) *segmentLimiter {
	l := &segmentLimiter{
		limiters: make(map[string]*limiterEntry),
		r:        rate.Limit(rps),
		b:        burst,
		stopCh:   make(chan struct{}),
	}
	go l.cleanupLoop()
	return l
}

func (l *segmentLimiter) Allow(ip string) bool {
	now := time.Now()
	l.mu.Lock()
	entry, ok := l.limiters[ip]
	if !ok {
		entry = &limiterEntry{limiter: rate.NewLimiter(l.r, l.b)}
		l.limiters[ip] = entry
	}
	entry.lastSeen = now
	l.mu.Unlock()
	return entry.limiter.Allow()
}

// cleanupLoop evicts IPs that haven't been seen for 10 minutes.
func (l *segmentLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.evictStale(10 * time.Minute)
		case <-l.stopCh:
			return
		}
	}
}

func (l *segmentLimiter) evictStale(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	l.mu.Lock()
	for ip, entry := range l.limiters {
		if entry.lastSeen.Before(cutoff) {
			delete(l.limiters, ip)
		}
	}
	l.mu.Unlock()
}

func (l *segmentLimiter) Stop() {
	close(l.stopCh)
}

// ---- Persistent codec cache ------------------------------------------------

const maxCodecCacheEntries = 2000

// persistedCodecEntry is the JSON-serializable form of a codec cache entry.
type persistedCodecEntry struct {
	IsH264   bool    `json:"h264"`
	IsAAC    bool    `json:"aac"`
	Width    int     `json:"w,omitempty"`
	Height   int     `json:"h,omitempty"`
	Duration float64 `json:"dur,omitempty"`
	FPS      float64 `json:"fps,omitempty"`
}

func (m *hlsManager) codecCachePath() string {
	return filepath.Join(m.baseDir, "codec_cache.json")
}

// loadCodecCache reads the on-disk codec cache into memory.
func (m *hlsManager) loadCodecCache() {
	data, err := os.ReadFile(m.codecCachePath())
	if err != nil {
		return // no file yet — not an error
	}
	var entries map[string]*persistedCodecEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		m.logger.Warn("failed to parse codec cache", slog.String("err", err.Error()))
		return
	}

	m.codecCacheMu.Lock()
	m.resolutionCacheMu.Lock()
	for path, e := range entries {
		m.codecCache[path] = &codecCacheEntry{isH264: e.IsH264, isAAC: e.IsAAC}
		if e.Width > 0 || e.Height > 0 || e.Duration > 0 || e.FPS > 0 {
			m.resolutionCache[path] = &resolutionCacheEntry{
				width:    e.Width,
				height:   e.Height,
				duration: e.Duration,
				fps:      e.FPS,
			}
		}
	}
	m.resolutionCacheMu.Unlock()
	m.codecCacheMu.Unlock()

	m.logger.Info("loaded codec cache", slog.Int("entries", len(entries)))
}

// saveCodecCache writes the in-memory codec+resolution caches to disk atomically.
func (m *hlsManager) saveCodecCache() {
	m.codecCacheMu.RLock()
	m.resolutionCacheMu.RLock()

	entries := make(map[string]*persistedCodecEntry, len(m.codecCache))
	for path, c := range m.codecCache {
		e := &persistedCodecEntry{IsH264: c.isH264, IsAAC: c.isAAC}
		if r, ok := m.resolutionCache[path]; ok {
			e.Width = r.width
			e.Height = r.height
			e.Duration = r.duration
			e.FPS = r.fps
		}
		entries[path] = e
	}

	m.resolutionCacheMu.RUnlock()
	m.codecCacheMu.RUnlock()

	data, err := json.Marshal(entries)
	if err != nil {
		m.logger.Warn("failed to marshal codec cache", slog.String("err", err.Error()))
		return
	}

	tmp := m.codecCachePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		m.logger.Warn("failed to write codec cache", slog.String("err", err.Error()))
		return
	}
	if err := os.Rename(tmp, m.codecCachePath()); err != nil {
		m.logger.Warn("failed to rename codec cache", slog.String("err", err.Error()))
	}
}

// scheduleCodecCacheSave debounces disk writes — at most one write per 5 seconds.
func (m *hlsManager) scheduleCodecCacheSave() {
	m.codecCacheMu.Lock()
	m.codecCacheDirty = true
	if m.codecCacheSaveTimer == nil {
		m.codecCacheSaveTimer = time.AfterFunc(5*time.Second, func() {
			m.codecCacheMu.Lock()
			m.codecCacheDirty = false
			m.codecCacheSaveTimer = nil
			m.codecCacheMu.Unlock()
			m.saveCodecCache()
		})
	}
	m.codecCacheMu.Unlock()
}

// evictCodecCacheIfNeeded trims the codec cache to maxCodecCacheEntries by
// removing least-recently-accessed entries first (LRU).
func (m *hlsManager) evictCodecCacheIfNeeded() {
	if len(m.codecCache) <= maxCodecCacheEntries {
		return
	}
	type pathTime struct {
		path string
		t    time.Time
	}
	entries := make([]pathTime, 0, len(m.codecCache))
	for p, e := range m.codecCache {
		entries = append(entries, pathTime{path: p, t: e.lastAccess})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].t.Before(entries[j].t)
	})
	excess := len(m.codecCache) - maxCodecCacheEntries
	for i := 0; i < excess && i < len(entries); i++ {
		delete(m.codecCache, entries[i].path)
	}
}

// profileHash returns the current encoding profile hash.
func (m *hlsManager) profileHash() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	segDur := m.segmentDuration
	if segDur <= 0 {
		segDur = 2
	}
	return computeProfileHash(m.preset, m.crf, m.audioBitrate, segDur)
}

// cacheVariant qualifies a variant name with the encoding profile hash
// so that cached segments are separated by encoding settings.
func (m *hlsManager) cacheVariant(variant string) string {
	ph := m.profileHash()
	if variant != "" {
		return variant + "-" + ph
	}
	return ph
}

// cacheVariantLocked is the lock-free variant of cacheVariant for use when
// the caller already holds m.mu.
func (m *hlsManager) cacheVariantLocked(variant string) string {
	segDur := m.segmentDuration
	if segDur <= 0 {
		segDur = 2
	}
	ph := computeProfileHash(m.preset, m.crf, m.audioBitrate, segDur)
	if variant != "" {
		return variant + "-" + ph
	}
	return ph
}

func (m *hlsManager) EncodingPreset() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.preset
}

func (m *hlsManager) EncodingCRF() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.crf
}

func (m *hlsManager) EncodingAudioBitrate() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.audioBitrate
}

func (m *hlsManager) UpdateEncodingSettings(preset string, crf int, audioBitrate string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if preset != "" {
		m.preset = preset
	}
	if crf > 0 {
		m.crf = crf
	}
	if audioBitrate != "" {
		m.audioBitrate = audioBitrate
	}
}

func (m *hlsManager) SegmentDuration() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.segmentDuration <= 0 {
		return 2
	}
	return m.segmentDuration
}

func (m *hlsManager) MemBufSizeBytes() int64 {
	if m.memBuf == nil {
		return 0
	}
	return m.memBuf.MaxBytes()
}

func (m *hlsManager) CacheSizeBytes() int64 {
	if m.cache == nil {
		return 0
	}
	return m.cache.MaxBytes()
}

func (m *hlsManager) CacheMaxAge() time.Duration {
	if m.cache == nil {
		return 0
	}
	return m.cache.MaxAge()
}

func (m *hlsManager) UpdateHLSSettings(memBufSize, cacheSizeBytes, cacheMaxAgeHours int64, segmentDuration int) {
	if m.memBuf != nil && memBufSize > 0 {
		m.memBuf.Resize(memBufSize)
	}
	if m.cache != nil {
		if cacheSizeBytes > 0 {
			m.cache.SetMaxBytes(cacheSizeBytes)
		}
		if cacheMaxAgeHours > 0 {
			m.cache.SetMaxAge(time.Duration(cacheMaxAgeHours) * time.Hour)
		}
	}
	if segmentDuration > 0 {
		m.mu.Lock()
		m.segmentDuration = segmentDuration
		m.mu.Unlock()
	}
}

func (m *hlsManager) ensureJob(id domain.TorrentID, fileIndex, audioTrack, subtitleTrack int) (*hlsJob, error) {
	if m.stream == nil {
		return nil, errors.New("stream use case not configured")
	}

	key := hlsKey{
		id:            id,
		fileIndex:     fileIndex,
		audioTrack:    audioTrack,
		subtitleTrack: subtitleTrack,
	}

	// Compute directory path before any locking (buildJobDir takes RLock internally).
	dir := m.buildJobDir(key)

	// Fast path: job already exists (shared lock, non-blocking for concurrent readers).
	m.mu.RLock()
	job, ok := m.jobs[key]
	m.mu.RUnlock()
	if ok {
		job.startOnce.Do(func() {
			go m.run(job, key)
		})
		return job, nil
	}

	// Slow path: create new job (exclusive lock).
	m.mu.Lock()
	// Double-check after acquiring exclusive lock.
	job, ok = m.jobs[key]
	if ok {
		m.mu.Unlock()
		job.startOnce.Do(func() {
			go m.run(job, key)
		})
		return job, nil
	}

	// Check for completed job from a previous run (survived restart via persistent volume).
	// Multi-variant jobs write master.m3u8; check for it first.
	masterPlaylist := filepath.Join(dir, "master.m3u8")
	if _, statErr := os.Stat(masterPlaylist); statErr == nil {
		v0Playlist := filepath.Join(dir, "v0", "index.m3u8")
		if playlistHasEndList(v0Playlist) {
			job = newHLSJob(dir, 0)
			job.multiVariant = true
			job.playlist = masterPlaylist
			// Transition: Idle → Starting → Completed (cached)
			_ = job.ctrl.Transition(StateStarting)
			_ = job.ctrl.Transition(StateBuffering)
			_ = job.ctrl.Transition(StatePlaying)
			_ = job.ctrl.Transition(StateCompleted)
			job.signalReady()
			m.jobs[key] = job
			m.mu.Unlock()
			m.logger.Info("hls reusing cached multi-variant transcode", slog.String("dir", dir))
			return job, nil
		}
	}
	playlist := filepath.Join(dir, "index.m3u8")
	if playlistHasEndList(playlist) {
		job = newHLSJob(dir, 0)
		_ = job.ctrl.Transition(StateStarting)
		_ = job.ctrl.Transition(StateBuffering)
		_ = job.ctrl.Transition(StatePlaying)
		_ = job.ctrl.Transition(StateCompleted)
		job.signalReady()
		m.jobs[key] = job
		m.mu.Unlock()
		m.logger.Info("hls reusing cached transcode", slog.String("dir", dir))
		return job, nil
	}

	// No reusable cache — clean and start fresh.
	if err := os.RemoveAll(dir); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	job = newHLSJob(dir, 0)
	m.jobs[key] = job
	m.totalJobStarts++
	m.lastJobStartedAt = time.Now().UTC()
	metrics.HLSJobStartsTotal.Inc()
	metrics.HLSActiveJobs.Set(float64(len(m.jobs)))
	m.mu.Unlock()

	job.startOnce.Do(func() {
		go m.run(job, key)
	})

	return job, nil
}

// seekJobResult contains the result of a seek operation.
type seekJobResult struct {
	job      *hlsJob
	seekMode SeekMode
}

func (m *hlsManager) seekJob(id domain.TorrentID, fileIndex, audioTrack, subtitleTrack int, seekSeconds float64) (*hlsJob, SeekMode, error) {
	if m.stream == nil {
		return nil, SeekModeHard, errors.New("stream use case not configured")
	}

	key := hlsKey{
		id:            id,
		fileIndex:     fileIndex,
		audioTrack:    audioTrack,
		subtitleTrack: subtitleTrack,
	}

	// Compute base directory before any locking (buildJobDir takes RLock internally).
	baseDir := m.buildJobDir(key)

	// Pre-compute seek mode WITHOUT holding the write lock.
	// chooseSeekModeLocked calls m.cache.LookupRange which acquires c.mu.RLock.
	// Performing this while m.mu is write-locked creates a lock-chain deadlock:
	//   seekJob holds m.mu.Lock → LookupRange waits for c.mu.RLock
	//   → Store eviction holds c.mu.Lock during os.Remove → deadlock.
	// We snapshot the current job under m.mu.RLock, release it, compute the mode
	// (including the cache lookup), then re-acquire m.mu.Lock for the mutation.
	m.mu.RLock()
	preLockOld, preLockHasOld := m.jobs[key]
	preLockSegDur := m.segmentDuration
	m.mu.RUnlock()

	var preLockMode SeekMode = SeekModeHard
	if preLockHasOld {
		// c.mu.RLock is acquired inside chooseSeekModeLocked; no m.mu held here.
		preLockMode = m.chooseSeekModeLocked(key, preLockOld, seekSeconds, preLockSegDur)
	}

	m.mu.Lock()
	m.totalSeekRequests++
	m.lastSeekAt = time.Now().UTC()
	m.lastSeekTarget = seekSeconds
	metrics.HLSSeekTotal.Inc()

	// Use the pre-computed seek mode, verifying the job hasn't changed since we
	// released m.mu.RLock. If the job was replaced by another goroutine in the
	// interim, fall back to hard seek (conservative but correct).
	seekModeEmitted := false
	if old, ok := m.jobs[key]; ok {
		var seekMode SeekMode
		if old == preLockOld {
			// Same job instance — pre-computed mode is still valid.
			seekMode = preLockMode
		} else {
			// Job was replaced between read-lock and write-lock — be conservative.
			seekMode = SeekModeHard
		}
		metrics.HLSSeekModeTotal.WithLabelValues(seekMode.String()).Inc()
		seekModeEmitted = true
		if seekMode == SeekModeCache || seekMode == SeekModeSoft {
			m.mu.Unlock()
			m.logger.Info("hls seek — no FFmpeg restart",
				slog.String("torrentId", string(id)),
				slog.Float64("targetSec", seekSeconds),
				slog.String("seekMode", seekMode.String()),
			)
			return old, seekMode, nil
		}
	}

	oldDir := ""
	oldSeekSeconds := float64(0)
	var deferredCancel context.CancelFunc
	if old, ok := m.jobs[key]; ok {
		// Transition existing job to Seeking state if possible.
		_ = old.ctrl.Transition(StateSeeking)
		old.ctrl.IncrementGeneration()
		delete(m.jobs, key)
		oldSeekSeconds = old.seekSeconds
		// Defer cancel: keep old FFmpeg running while the new job starts so
		// the old HLS.js stream can continue playing from its buffer.
		deferredCancel = old.cancel
		// Immediately reset rate limit so the torrent isn't left throttled
		// while the old job is running in parallel.
		m.resetRateLimit(key, old)
		oldDir = old.dir
	}

	// Anti-seek-storm: log when two hard seeks happen within 150ms.
	now := time.Now()
	if prev, ok := m.lastHardSeek[key]; ok && now.Sub(prev) < 150*time.Millisecond {
		m.logger.Debug("hls seek storm detected: rapid consecutive hard seeks",
			slog.String("torrentId", string(id)),
			slog.Duration("interval", now.Sub(prev)),
		)
	}
	m.lastHardSeek[key] = now

	// Use a unique directory per seek so cleanup of previous jobs
	// can't race and remove files of the newly started ffmpeg process.
	dir := baseDir + fmt.Sprintf("-seek-%d", time.Now().UnixNano())
	if err := os.RemoveAll(dir); err != nil {
		m.mu.Unlock()
		return nil, SeekModeHard, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.mu.Unlock()
		return nil, SeekModeHard, err
	}
	job := newHLSJob(dir, seekSeconds)
	m.jobs[key] = job
	m.totalJobStarts++
	m.lastJobStartedAt = time.Now().UTC()
	metrics.HLSJobStartsTotal.Inc()
	metrics.HLSActiveJobs.Set(float64(len(m.jobs)))
	m.mu.Unlock()

	// Pre-warm torrent data at the target position for faster FFmpeg startup.
	m.preSeekPriorityBoost(key, seekSeconds)

	job.startOnce.Do(func() {
		go m.run(job, key)
	})

	// Defer old-job cancellation and cleanup until the new job signals ready
	// (or an 8-second timeout). This keeps the old FFmpeg alive during new-job
	// startup so the frontend can keep playing from the old stream's buffer,
	// eliminating the black-screen gap on hard seeks.
	if deferredCancel != nil || (oldDir != "" && oldDir != dir) {
		capturedOldDir := oldDir
		capturedOldSeekSec := oldSeekSeconds
		capturedNewDir := dir
		go func() {
			select {
			case <-job.ready:
			case <-time.After(8 * time.Second):
				m.logger.Debug("hls seek: deferred old-job cancel timeout",
					slog.String("torrentId", string(key.id)),
				)
			}
			if deferredCancel != nil {
				deferredCancel()
			}
			if capturedOldDir != "" && capturedOldDir != capturedNewDir {
				m.harvestSegmentsToCache(key, capturedOldDir, capturedOldSeekSec)
				if m.memBuf != nil {
					m.memBuf.PurgePrefix(capturedOldDir)
				}
				_ = os.RemoveAll(capturedOldDir)
			}
		}()
	}

	// Emit metric for hard seeks when no existing job was found to evaluate.
	if !seekModeEmitted {
		metrics.HLSSeekModeTotal.WithLabelValues(SeekModeHard.String()).Inc()
	}

	return job, SeekModeHard, nil
}

func newHLSJob(dir string, seekSeconds float64) *hlsJob {
	ctx, cancel := context.WithCancel(context.Background())
	ctrl := NewPlaybackController()
	return &hlsJob{
		dir:          dir,
		playlist:     filepath.Join(dir, "index.m3u8"),
		ready:        make(chan struct{}),
		seekSeconds:  seekSeconds,
		ctx:          ctx,
		cancel:       cancel,
		ctrl:         ctrl,
		lastActivity: time.Now().UTC(),
		genRef:       newGenerationRef(ctrl.Generation()),
	}
}

// segmentTimeOffset computes the absolute time offset (in seconds) for the
// given segment filename. It maintains a lazily-built cumulative time index
// per job so that repeated lookups are O(1) instead of O(n).
// For multi-variant segments (e.g. "v0/seg-00001.ts"), the variant prefix
// is parsed to locate the correct variant playlist.
func segmentTimeOffset(job *hlsJob, segmentName string) (float64, bool) {
	if job == nil {
		return 0, false
	}

	playlist := job.playlist
	parsedSegName := segmentName

	// Multi-variant: parse "v0/seg-00001.ts" → variant="v0", seg="seg-00001.ts"
	if job.multiVariant {
		if idx := strings.IndexByte(segmentName, '/'); idx > 0 && segmentName[0] == 'v' {
			variantPrefix := segmentName[:idx]
			playlist = filepath.Join(job.dir, variantPrefix, "index.m3u8")
			parsedSegName = segmentName[idx+1:]
		}
	}

	// Fast path: check existing index.
	job.timeIndexMu.RLock()
	if t, ok := job.timeIndex[parsedSegName]; ok {
		job.timeIndexMu.RUnlock()
		return t, true
	}
	job.timeIndexMu.RUnlock()

	// Slow path: parse playlist and extend the index.
	segments, err := parseM3U8Segments(playlist)
	if err != nil {
		return 0, false
	}

	job.timeIndexMu.Lock()
	defer job.timeIndexMu.Unlock()

	// Only index new segments (playlist is append-only).
	if len(segments) > job.timeIndexSize {
		if job.timeIndex == nil {
			job.timeIndex = make(map[string]float64, len(segments))
		}
		cumTime := job.seekSeconds
		for i, seg := range segments {
			if i < job.timeIndexSize {
				// Already indexed — just advance cumTime.
				cumTime += seg.Duration
				continue
			}
			job.timeIndex[seg.Filename] = cumTime
			cumTime += seg.Duration
		}
		job.timeIndexSize = len(segments)
	}

	if t, ok := job.timeIndex[parsedSegName]; ok {
		return t, true
	}
	return 0, false
}

func (j *hlsJob) signalReady() {
	j.readyOnce.Do(func() {
		close(j.ready)
	})
}

func (m *hlsManager) cleanupJob(key hlsKey, job *hlsJob) {
	m.mu.Lock()
	removeDir := false
	if current, ok := m.jobs[key]; ok && current == job {
		delete(m.jobs, key)
		removeDir = true
	}
	m.mu.Unlock()
	// Reset rate limit so torrent isn't left throttled after stream ends.
	m.resetRateLimit(key, job)
	if removeDir {
		m.harvestSegmentsToCache(key, job.dir, job.seekSeconds)
		if m.memBuf != nil {
			m.memBuf.PurgePrefix(job.dir)
		}
		_ = os.RemoveAll(job.dir)
	}
}

func (m *hlsManager) markJobRunning(key hlsKey, job *hlsJob) {
	m.mu.Lock()
	if current, ok := m.jobs[key]; ok && current == job {
		_ = job.ctrl.Transition(StateStarting)
		job.err = nil
		job.lastActivity = time.Now().UTC()
	}
	m.mu.Unlock()
}

func (m *hlsManager) markJobStopped(key hlsKey, job *hlsJob) {
	m.mu.Lock()
	m.mu.Unlock()
	metrics.HLSActiveJobs.Set(float64(m.countRunningJobs()))
}

func (m *hlsManager) countRunningJobs() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, j := range m.jobs {
		if j.ctrl.IsRunning() {
			n++
		}
	}
	return n
}

func (m *hlsManager) markJobCompleted(key hlsKey, job *hlsJob) {
	m.mu.Lock()
	if current, ok := m.jobs[key]; ok && current == job {
		_ = job.ctrl.Transition(StateCompleted)
		job.lastActivity = time.Now().UTC()
	}
	m.mu.Unlock()
	// Reset rate limit so torrent isn't left throttled after encoding completes.
	m.resetRateLimit(key, job)
}

// resetRateLimit removes the download rate limit for the torrent so it isn't
// left throttled after streaming ends.
func (m *hlsManager) resetRateLimit(key hlsKey, job *hlsJob) {
	if job != nil && job.bufferedReader != nil {
		job.bufferedReader.SetRateLimit(0)
	}
	if m.engine == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = m.engine.SetDownloadRateLimit(ctx, key.id, 0)
}

func (m *hlsManager) touchJobActivity(key hlsKey, job *hlsJob) {
	m.mu.Lock()
	if current, ok := m.jobs[key]; ok && current == job {
		job.lastActivity = time.Now().UTC()
	}
	m.mu.Unlock()
}

// applyRatePolicy adjusts the torrent download rate limit based on the current
// playback state and consumption rate. Called periodically by the watchdog.
func (m *hlsManager) applyRatePolicy(key hlsKey, job *hlsJob) {
	if m.engine == nil {
		return
	}
	rateFn := job.consumptionRate
	if rateFn == nil {
		return
	}

	state := job.ctrl.State()
	rate := rateFn()

	// Compute TWO separate rate limits:
	// 1) pipeLimit — controls how fast the buffered reader feeds FFmpeg.
	//    Keeps FFmpeg from racing far ahead of the download frontier.
	// 2) dlLimit — controls how fast the torrent downloads pieces.
	//    Must be much higher so pieces are ready when FFmpeg needs them.
	var pipeLimit, dlLimit int64
	switch state {
	case StatePlaying:
		pipeLimit = int64(rate * 1.5)
		dlLimit = int64(rate * 5.0) // 5× consumption rate: download well ahead of FFmpeg
	case StateStalled:
		pipeLimit = int64(rate * 2.0)
		dlLimit = 0 // unlimited: recover from stall as fast as possible
	default:
		// Buffering, Seeking, Completed, Idle, Error, Starting, Restarting: unlimited
		pipeLimit = 0
		dlLimit = 0
	}

	// Floor: never throttle below 2 MB/s. Low-bitrate content or EMA lag
	// can cause starvation if the floor is too low.
	const minSafeRateBPS int64 = 2 * 1024 * 1024
	if pipeLimit > 0 && pipeLimit < minSafeRateBPS {
		pipeLimit = minSafeRateBPS
	}
	if dlLimit > 0 && dlLimit < minSafeRateBPS {
		dlLimit = minSafeRateBPS
	}

	metrics.HLSRateLimitBytesPerSec.Set(float64(dlLimit))

	// Apply pipe rate limit to the buffered reader for local throttling.
	if job.bufferedReader != nil {
		job.bufferedReader.SetRateLimit(pipeLimit)
	}

	// Only set the engine-level rate limit when this is the sole job for
	// the torrent. Multiple concurrent jobs (e.g. different fileIndexes)
	// would overwrite each other's limits every 5s, causing oscillation.
	m.mu.RLock()
	jobCount := 0
	for k := range m.jobs {
		if k.id == key.id {
			jobCount++
		}
	}
	m.mu.RUnlock()
	if jobCount > 1 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.engine.SetDownloadRateLimit(ctx, key.id, dlLimit); err != nil {
		m.logger.Warn("failed to set download rate limit",
			slog.String("torrentId", string(key.id)),
			slog.Int64("limitBytesPerSec", dlLimit),
			slog.String("state", state.String()),
			slog.String("error", err.Error()),
		)
	}
}

func (m *hlsManager) watchJobProgress(key hlsKey, job *hlsJob) {
	ticker := time.NewTicker(hlsWatchdogInterval)
	defer ticker.Stop()
	lastSeenPlaylistMod := time.Time{}
	stallWarnLogged := false
	lastSegPath := ""
	lastSegSize := int64(0)
	lastSegChangedAt := time.Now()
	// FFmpeg -progress tracking for within-segment stall detection.
	var lastProgressUs int64
	lastProgressChangeAt := time.Now()

	// Escalation ladder: tracks which stall mitigations have been applied.
	// 0 = none, 1 = rate limit removed, 2 = priority boosted, 3 = restart.
	escalationLevel := 0

	for range ticker.C {
		readyClosed := false
		select {
		case <-job.ready:
			readyClosed = true
		default:
		}

		m.mu.RLock()
		current, ok := m.jobs[key]
		if !ok || current != job {
			m.mu.RUnlock()
			return
		}
		state := job.ctrl.State()
		if !job.ctrl.IsRunning() || state == StateCompleted {
			m.mu.RUnlock()
			return
		}
		restartCount := job.restartCount
		playlistPath := job.playlist
		m.mu.RUnlock()

		// Adjust torrent download rate based on playback state,
		// but skip when escalation has already removed the rate limit.
		if escalationLevel < 1 {
			m.applyRatePolicy(key, job)
		}

		// Track playlist modifications for the initial ready signal,
		// but do NOT use playlist mtime to reset the stall timer.
		// FFmpeg can rewrite the .m3u8 periodically without adding
		// new segments, which would prevent the stall from escalating.
		if info, err := os.Stat(playlistPath); err == nil {
			modified := info.ModTime().UTC()
			if modified.After(lastSeenPlaylistMod) {
				lastSeenPlaylistMod = modified
			}
		}

		// Check last segment for stuck encoder. Use segment changes
		// (not playlist mtime) as the authoritative progress signal.
		if segPath, segSize := findLastSegment(job.dir); segPath != "" {
			changed := segPath != lastSegPath || segSize != lastSegSize
			if changed {
				lastSegPath = segPath
				lastSegSize = segSize
				lastSegChangedAt = time.Now()
				m.touchJobActivity(key, job)
				stallWarnLogged = false
				// New segment appeared — stall cleared, reset escalation.
				escalationLevel = 0
			} else if time.Since(lastSegChangedAt) >= 45*time.Second && segSize < 256*1024 {
				m.logger.Warn("hls watchdog: last segment appears stuck (tiny and unchanged)",
					slog.String("torrentId", string(key.id)),
					slog.String("segPath", lastSegPath),
					slog.Int64("segSize", lastSegSize),
					slog.Duration("unchanged", time.Since(lastSegChangedAt)),
				)
			}
		}

		// Track FFmpeg encoding progress (out_time_us from -progress pipe:1).
		// If progress advances, FFmpeg is alive even if no new segment file appeared.
		currentProgressUs := atomic.LoadInt64(&job.ffmpegProgressUs)
		if currentProgressUs > 0 {
			if currentProgressUs != lastProgressUs {
				lastProgressUs = currentProgressUs
				lastProgressChangeAt = time.Now()
			}
		}

		if !readyClosed {
			continue
		}

		stallDuration := time.Since(lastSegChangedAt)

		// ---- Escalation ladder ----
		// L1 (30s): remove rate limit so torrent downloads at full speed.
		if stallDuration >= hlsStallEscalation1 && escalationLevel < 1 {
			escalationLevel = 1
			if job.bufferedReader != nil {
				job.bufferedReader.SetRateLimit(0)
			}
			if m.engine != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = m.engine.SetDownloadRateLimit(ctx, key.id, 0)
				cancel()
			}
			m.logger.Warn("hls watchdog escalation L1: rate limit removed",
				slog.String("torrentId", string(key.id)),
				slog.Int("fileIndex", key.fileIndex),
				slog.Duration("stalled", stallDuration),
			)
		}

		// L2 (60s): boost piece priority window.
		if stallDuration >= hlsStallEscalation2 && escalationLevel < 2 {
			escalationLevel = 2
			if job.priorityBoostFunc != nil {
				job.priorityBoostFunc()
			}
			m.logger.Warn("hls watchdog escalation L2: priority boost applied",
				slog.String("torrentId", string(key.id)),
				slog.Int("fileIndex", key.fileIndex),
				slog.Duration("stalled", stallDuration),
			)
		}

		// Log a warning once when the stall exceeds 30 seconds, so
		// operators can see that FFmpeg is blocked waiting for data.
		if stallDuration >= 30*time.Second && !stallWarnLogged {
			m.logger.Warn("hls watchdog: segment production stalled, waiting for torrent data",
				slog.String("torrentId", string(key.id)),
				slog.Int("fileIndex", key.fileIndex),
				slog.Duration("stalled", stallDuration),
				slog.Int64("ffmpegProgressUs", currentProgressUs),
			)
			stallWarnLogged = true
		}

		// L3: kill + restart FFmpeg.
		// Pipe sources (incomplete torrent files) get much longer patience
		// because restarting destroys all encoded progress and the data
		// will arrive eventually as the torrent downloads.
		stallThreshold := hlsWatchdogStallThreshold
		if job.isPipeSource {
			stallThreshold = hlsPipeStallThreshold
		}
		segStalled := stallDuration >= stallThreshold
		progressStalled := lastProgressUs > 0 && time.Since(lastProgressChangeAt) >= stallThreshold

		if !segStalled && !progressStalled {
			continue
		}
		if restartCount >= hlsMaxAutoRestarts {
			m.logger.Error("hls watchdog restart limit reached",
				slog.String("torrentId", string(key.id)),
				slog.Int("fileIndex", key.fileIndex),
				slog.Int("restartCount", restartCount),
			)
			return
		}

		if _, restarted := m.tryAutoRestart(key, job, "watchdog_stall"); restarted {
			return
		}
		return
	}
}

func (m *hlsManager) tryAutoRestart(key hlsKey, expected *hlsJob, reason string) (*hlsJob, bool) {
	if expected == nil {
		return nil, false
	}

	m.mu.Lock()
	current, ok := m.jobs[key]
	if !ok || current != expected {
		m.mu.Unlock()
		return current, false
	}
	if expected.restartCount >= hlsMaxAutoRestarts {
		m.mu.Unlock()
		return expected, false
	}
	// Transition: current → Stalled → Restarting
	_ = expected.ctrl.Transition(StateStalled)
	_ = expected.ctrl.Transition(StateRestarting)
	nextRestart := expected.restartCount + 1
	dir := expected.dir
	seekSeconds := expected.seekSeconds
	// Resume from last FFmpeg progress position instead of re-encoding
	// from the original seek point. This preserves encoded progress when
	// the stall was caused by waiting for torrent data.
	if progressUs := atomic.LoadInt64(&expected.ffmpegProgressUs); progressUs > 0 {
		seekSeconds = float64(progressUs) / 1_000_000.0
	}
	if expected.cancel != nil {
		expected.cancel()
	}
	m.mu.Unlock()

	// Preserve already encoded segments before restarting FFmpeg in the same dir.
	m.harvestSegmentsToCache(key, dir, seekSeconds)

	if err := os.RemoveAll(dir); err != nil {
		m.recordJobFailure(expected, fmt.Errorf("auto-restart cleanup failed: %w", err))
		return expected, false
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.recordJobFailure(expected, fmt.Errorf("auto-restart mkdir failed: %w", err))
		return expected, false
	}

	next := newHLSJob(dir, seekSeconds)
	next.restartCount = nextRestart

	m.mu.Lock()
	current, ok = m.jobs[key]
	if !ok || current != expected {
		m.mu.Unlock()
		return current, false
	}
	now := time.Now().UTC()
	m.jobs[key] = next
	m.totalAutoRestarts++
	m.lastAutoRestartAt = now
	m.lastAutoRestartReason = reason
	m.totalJobStarts++
	m.lastJobStartedAt = now
	m.mu.Unlock()

	m.logger.Warn("hls auto-restart",
		slog.String("torrentId", string(key.id)),
		slog.Int("fileIndex", key.fileIndex),
		slog.Int("audioTrack", key.audioTrack),
		slog.Int("subtitleTrack", key.subtitleTrack),
		slog.Int("restartCount", nextRestart),
		slog.String("reason", reason),
	)
	metrics.HLSAutoRestartsTotal.WithLabelValues(reason).Inc()

	next.startOnce.Do(func() {
		go m.run(next, key)
	})
	return next, true
}

func (m *hlsManager) recordJobFailure(job *hlsJob, err error) {
	if err == nil {
		return
	}
	now := time.Now().UTC()
	msg := strings.TrimSpace(err.Error())
	m.mu.Lock()
	m.totalJobFailures++
	m.lastJobError = msg
	m.lastJobErrorAt = now
	metrics.HLSJobFailuresTotal.Inc()
	if job != nil && job.seekSeconds > 0 {
		m.totalSeekFailures++
		m.lastSeekError = msg
		m.lastSeekErrorAt = now
	}
	m.mu.Unlock()
}

func (m *hlsManager) recordPlaylistReady(job *hlsJob) {
	now := time.Now().UTC()
	m.mu.Lock()
	m.lastPlaylistReady = now
	if job != nil && job.seekSeconds > 0 {
		m.lastSeekError = ""
		m.lastSeekErrorAt = time.Time{}
	}
	m.mu.Unlock()
}

func (m *hlsManager) healthSnapshot() hlsHealthSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := hlsHealthSnapshot{
		ActiveJobs:            len(m.jobs),
		TotalJobStarts:        m.totalJobStarts,
		TotalJobFailures:      m.totalJobFailures,
		TotalSeekRequests:     m.totalSeekRequests,
		TotalSeekFailures:     m.totalSeekFailures,
		TotalAutoRestarts:     m.totalAutoRestarts,
		LastJobError:          m.lastJobError,
		LastSeekTarget:        m.lastSeekTarget,
		LastSeekError:         m.lastSeekError,
		LastAutoRestartReason: m.lastAutoRestartReason,
	}
	if !m.lastJobStartedAt.IsZero() {
		ts := m.lastJobStartedAt
		s.LastJobStartedAt = &ts
	}
	if !m.lastPlaylistReady.IsZero() {
		ts := m.lastPlaylistReady
		s.LastPlaylistReady = &ts
	}
	if !m.lastJobErrorAt.IsZero() {
		ts := m.lastJobErrorAt
		s.LastJobErrorAt = &ts
	}
	if !m.lastSeekAt.IsZero() {
		ts := m.lastSeekAt
		s.LastSeekAt = &ts
	}
	if !m.lastSeekErrorAt.IsZero() {
		ts := m.lastSeekErrorAt
		s.LastSeekErrorAt = &ts
	}
	if !m.lastAutoRestartAt.IsZero() {
		ts := m.lastAutoRestartAt
		s.LastAutoRestartAt = &ts
	}
	return s
}

// preloadFileEnds boosts the priority of the last 16 MB of the media file.
// Container formats (MKV, MP4) store seek indices at the tail, so preloading
// this region enables faster FFmpeg startup on initial play.
func (m *hlsManager) preloadFileEnds(key hlsKey, file domain.FileRef) {
	if m.engine == nil || file.Length <= 0 {
		return
	}
	const tailSize int64 = 16 << 20
	tailStart := file.Length - tailSize
	if tailStart < 0 {
		tailStart = 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = m.engine.SetPiecePriority(ctx, key.id, file,
		domain.Range{Off: tailStart, Length: tailSize}, domain.PriorityNormal)
}

// shutdown cancels all active HLS jobs, saves the codec cache, and stops
// background timers. Called during graceful server shutdown.
func (m *hlsManager) shutdown() {
	if m.segLimiter != nil {
		m.segLimiter.Stop()
	}
	m.mu.Lock()
	for key, job := range m.jobs {
		if job.cancel != nil {
			job.cancel()
		}
		delete(m.jobs, key)
	}
	m.mu.Unlock()

	// Stop the debounced codec cache timer and flush to disk.
	m.codecCacheMu.Lock()
	if m.codecCacheSaveTimer != nil {
		m.codecCacheSaveTimer.Stop()
		m.codecCacheSaveTimer = nil
	}
	dirty := m.codecCacheDirty
	m.codecCacheDirty = false
	m.codecCacheMu.Unlock()
	if dirty {
		m.saveCodecCache()
	}
}

// findLastSegment returns the path and size of the most recently modified
// .ts segment file in dir. Returns ("", 0) if no segments exist.
func findLastSegment(dir string) (path string, size int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0
	}
	var latestMod time.Time
	for _, e := range entries {
		if e.IsDir() {
			// Check variant subdirectories (v0/, v1/, ...) for multi-variant jobs.
			if len(e.Name()) >= 2 && e.Name()[0] == 'v' {
				subDir := filepath.Join(dir, e.Name())
				subEntries, subErr := os.ReadDir(subDir)
				if subErr != nil {
					continue
				}
				for _, se := range subEntries {
					if se.IsDir() || !strings.HasSuffix(se.Name(), ".ts") {
						continue
					}
					info, infoErr := se.Info()
					if infoErr != nil {
						continue
					}
					if info.ModTime().After(latestMod) {
						latestMod = info.ModTime()
						path = filepath.Join(subDir, se.Name())
						size = info.Size()
					}
				}
			}
			continue
		}
		if !strings.HasSuffix(e.Name(), ".ts") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			path = filepath.Join(dir, e.Name())
			size = info.Size()
		}
	}
	return path, size
}

func waitForFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(120 * time.Millisecond)
	}
}
