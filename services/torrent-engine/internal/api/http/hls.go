package apihttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"torrentstream/internal/domain"
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
	running      bool
	completed    bool
	lastActivity time.Time
	restartCount int
	multiVariant bool             // true when producing multiple quality variants
	variants     []qualityVariant // populated for multi-variant jobs

	// Cached rewritten playlist (avoids re-parsing on every m3u8 GET).
	rewrittenMu           sync.RWMutex
	rewrittenPlaylist     []byte
	rewrittenPlaylistPath string    // source playlist path that was cached
	rewrittenPlaylistMod  time.Time // mtime of source when cached
	rewrittenAudioTrack   int
	rewrittenSubTrack     int

	// Precomputed cumulative time index for segmentTimeOffset (O(1) lookup).
	timeIndexMu   sync.RWMutex
	timeIndex     map[string]float64 // segmentName → absolute start time (seconds)
	timeIndexSize int                // number of segments already indexed
}

type codecCacheEntry struct {
	isH264 bool
	isAAC  bool
}

type resolutionCacheEntry struct {
	width  int
	height int
}

type hlsManager struct {
	stream                StreamTorrentUseCase
	ffmpegPath            string
	ffprobePath           string
	baseDir               string
	dataDir               string
	listenAddr            string
	preset                string
	crf                   int
	audioBitrate          string
	mu                    sync.Mutex
	jobs                  map[hlsKey]*hlsJob
	cache                 *hlsCache
	memBuf                *hlsMemBuffer
	codecCacheMu          sync.RWMutex
	codecCache            map[string]*codecCacheEntry // filePath → codec detection results
	codecCacheDirty       bool                         // true when in-memory cache diverged from disk
	codecCacheSaveTimer   *time.Timer                  // debounced disk write
	resolutionCacheMu     sync.RWMutex
	resolutionCache       map[string]*resolutionCacheEntry // filePath → resolution
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
	hlsWatchdogInterval       = 3 * time.Second
	hlsWatchdogStallThreshold = 12 * time.Second
	hlsMaxAutoRestarts        = 3
)

var errSubtitleSourceUnavailable = errors.New("subtitle source file not ready")

func newHLSManager(stream StreamTorrentUseCase, cfg HLSConfig, logger *slog.Logger) *hlsManager {
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

	mgr := &hlsManager{
		stream:          stream,
		ffmpegPath:      ffmpegPath,
		ffprobePath:     ffprobePath,
		baseDir:         baseDir,
		dataDir:         dataDir,
		listenAddr:      strings.TrimSpace(cfg.ListenAddr),
		preset:          preset,
		crf:             crf,
		audioBitrate:    audioBitrate,
		segmentDuration: 4,
		jobs:            make(map[hlsKey]*hlsJob),
		cache:           cache,
		memBuf:          memBuf,
		codecCache:      make(map[string]*codecCacheEntry),
		resolutionCache: make(map[string]*resolutionCacheEntry),
		logger:          logger,
	}
	mgr.loadCodecCache()
	return mgr
}

// ---- Persistent codec cache ------------------------------------------------

const maxCodecCacheEntries = 2000

// persistedCodecEntry is the JSON-serializable form of a codec cache entry.
type persistedCodecEntry struct {
	IsH264 bool `json:"h264"`
	IsAAC  bool `json:"aac"`
	Width  int  `json:"w,omitempty"`
	Height int  `json:"h,omitempty"`
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
		if e.Width > 0 || e.Height > 0 {
			m.resolutionCache[path] = &resolutionCacheEntry{width: e.Width, height: e.Height}
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
// removing arbitrary entries (codec detection is cheap enough that occasional
// re-probes are acceptable).
func (m *hlsManager) evictCodecCacheIfNeeded() {
	if len(m.codecCache) <= maxCodecCacheEntries {
		return
	}
	excess := len(m.codecCache) - maxCodecCacheEntries
	for path := range m.codecCache {
		if excess <= 0 {
			break
		}
		delete(m.codecCache, path)
		excess--
	}
}

func (m *hlsManager) EncodingPreset() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.preset
}

func (m *hlsManager) EncodingCRF() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.crf
}

func (m *hlsManager) EncodingAudioBitrate() string {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.segmentDuration <= 0 {
		return 4
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

	m.mu.Lock()
	job, ok := m.jobs[key]
	if !ok {
		dir := filepath.Join(
			m.baseDir,
			string(id),
			strconv.Itoa(fileIndex),
			fmt.Sprintf("a%d-s%d", audioTrack, subtitleTrack),
		)
		absDir, err := filepath.Abs(dir)
		if err == nil {
			dir = absDir
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
				job.completed = true
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
			job.completed = true
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
	}
	m.mu.Unlock()

	job.startOnce.Do(func() {
		go m.run(job, key)
	})

	return job, nil
}

func (m *hlsManager) seekJob(id domain.TorrentID, fileIndex, audioTrack, subtitleTrack int, seekSeconds float64) (*hlsJob, error) {
	if m.stream == nil {
		return nil, errors.New("stream use case not configured")
	}

	key := hlsKey{
		id:            id,
		fileIndex:     fileIndex,
		audioTrack:    audioTrack,
		subtitleTrack: subtitleTrack,
	}

	m.mu.Lock()
	m.totalSeekRequests++
	m.lastSeekAt = time.Now().UTC()
	m.lastSeekTarget = seekSeconds
	metrics.HLSSeekTotal.Inc()
	oldDir := ""
	oldSeekSeconds := float64(0)
	if old, ok := m.jobs[key]; ok {
		delete(m.jobs, key)
		oldSeekSeconds = old.seekSeconds
		if old.cancel != nil {
			old.cancel()
		}
		oldDir = old.dir
	}

	// Use a unique directory per seek so cleanup of previous jobs
	// can't race and remove files of the newly started ffmpeg process.
	dir := filepath.Join(
		m.baseDir,
		string(id),
		strconv.Itoa(fileIndex),
		fmt.Sprintf("a%d-s%d-seek-%d", audioTrack, subtitleTrack, time.Now().UnixNano()),
	)
	absDir, err := filepath.Abs(dir)
	if err == nil {
		dir = absDir
	}
	if err := os.RemoveAll(dir); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	job := newHLSJob(dir, seekSeconds)
	m.jobs[key] = job
	m.totalJobStarts++
	m.lastJobStartedAt = time.Now().UTC()
	metrics.HLSJobStartsTotal.Inc()
	metrics.HLSActiveJobs.Set(float64(len(m.jobs)))
	m.mu.Unlock()

	if oldDir != "" && oldDir != dir {
		go func(path string, seekSec float64) {
			m.harvestSegmentsToCache(key, path, seekSec)
			if m.memBuf != nil {
				m.memBuf.PurgePrefix(path)
			}
			_ = os.RemoveAll(path)
		}(oldDir, oldSeekSeconds)
	}

	job.startOnce.Do(func() {
		go m.run(job, key)
	})

	return job, nil
}

func newHLSJob(dir string, seekSeconds float64) *hlsJob {
	ctx, cancel := context.WithCancel(context.Background())
	return &hlsJob{
		dir:          dir,
		playlist:     filepath.Join(dir, "index.m3u8"),
		ready:        make(chan struct{}),
		seekSeconds:  seekSeconds,
		ctx:          ctx,
		cancel:       cancel,
		lastActivity: time.Now().UTC(),
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
		job.running = true
		job.completed = false
		job.err = nil
		job.lastActivity = time.Now().UTC()
	}
	m.mu.Unlock()
}

func (m *hlsManager) markJobStopped(key hlsKey, job *hlsJob) {
	m.mu.Lock()
	if current, ok := m.jobs[key]; ok && current == job {
		job.running = false
	}
	metrics.HLSActiveJobs.Set(float64(m.countRunningJobsLocked()))
	m.mu.Unlock()
}

func (m *hlsManager) countRunningJobsLocked() int {
	n := 0
	for _, j := range m.jobs {
		if j.running {
			n++
		}
	}
	return n
}

func (m *hlsManager) markJobCompleted(key hlsKey, job *hlsJob) {
	m.mu.Lock()
	if current, ok := m.jobs[key]; ok && current == job {
		job.completed = true
		job.running = false
		job.lastActivity = time.Now().UTC()
	}
	m.mu.Unlock()
}

func (m *hlsManager) touchJobActivity(key hlsKey, job *hlsJob) {
	m.mu.Lock()
	if current, ok := m.jobs[key]; ok && current == job {
		job.lastActivity = time.Now().UTC()
	}
	m.mu.Unlock()
}

func (m *hlsManager) watchJobProgress(key hlsKey, job *hlsJob) {
	ticker := time.NewTicker(hlsWatchdogInterval)
	defer ticker.Stop()
	lastSeenPlaylistMod := time.Time{}

	for range ticker.C {
		readyClosed := false
		select {
		case <-job.ready:
			readyClosed = true
		default:
		}

		m.mu.Lock()
		current, ok := m.jobs[key]
		if !ok || current != job {
			m.mu.Unlock()
			return
		}
		if !job.running || job.completed {
			m.mu.Unlock()
			return
		}
		lastActivity := job.lastActivity
		restartCount := job.restartCount
		playlistPath := job.playlist
		m.mu.Unlock()

		if info, err := os.Stat(playlistPath); err == nil {
			modified := info.ModTime().UTC()
			if modified.After(lastSeenPlaylistMod) {
				lastSeenPlaylistMod = modified
				m.touchJobActivity(key, job)
				continue
			}
		}

		if !readyClosed {
			continue
		}
		if time.Since(lastActivity) < hlsWatchdogStallThreshold {
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
	nextRestart := expected.restartCount + 1
	dir := expected.dir
	seekSeconds := expected.seekSeconds
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
	m.mu.Lock()
	defer m.mu.Unlock()
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
