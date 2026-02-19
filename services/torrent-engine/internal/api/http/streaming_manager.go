package apihttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"torrentstream/internal/app"
	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
	"torrentstream/internal/metrics"
	"torrentstream/internal/usecase"
)

// StreamJobManager replaces hlsManager. It manages StreamJob instances and
// owns codec/resolution caches, remux cache, and encoding settings.
// It does NOT own an HLS segment cache or memory buffer.
type StreamJobManager struct {
	stream StreamTorrentUseCase
	engine ports.Engine

	ffmpegPath  string
	ffprobePath string
	baseDir     string
	dataDir     string

	mu              sync.RWMutex
	preset          string
	crf             int
	audioBitrate    string
	segmentDuration int

	jobs         map[hlsKey]*StreamJob
	lastHardSeek map[hlsKey]time.Time

	// Codec/resolution caches (metadata, not segments).
	codecCacheMu        sync.RWMutex
	codecCache          map[string]*codecCacheEntry
	codecCacheDirty     bool
	codecCacheSaveTimer *time.Timer
	resolutionCacheMu   sync.RWMutex
	resolutionCache     map[string]*resolutionCacheEntry

	// Remux cache: background ffmpeg -c copy remux from MKV → MP4.
	remuxCache   map[string]*remuxEntry
	remuxCacheMu sync.Mutex

	// Streaming window config (configurable via HLS settings API).
	ramBufSize     int64 // RAMBuffer size in bytes
	prebufferSize  int64 // prebuffer before FFmpeg start
	windowBefore   int64 // priority window behind playback
	windowAfter    int64 // priority window ahead of playback

	logger *slog.Logger

	// Health stats.
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

func newStreamJobManager(stream StreamTorrentUseCase, engine ports.Engine, cfg HLSConfig, logger *slog.Logger) *StreamJobManager {
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
	segDur := cfg.SegmentDuration
	if segDur <= 0 {
		segDur = 2
	}

	defaults := DefaultWindowConfig()
	ramBufMB := cfg.RAMBufSizeMB
	if ramBufMB <= 0 {
		ramBufMB = int(defaults.RAMBufSize / (1024 * 1024))
	}
	prebufMB := cfg.PrebufferMB
	if prebufMB <= 0 {
		prebufMB = int(defaults.PreloadBytes / (1024 * 1024))
	}
	winBeforeMB := cfg.WindowBeforeMB
	if winBeforeMB <= 0 {
		winBeforeMB = int(defaults.BeforeBytes / (1024 * 1024))
	}
	winAfterMB := cfg.WindowAfterMB
	if winAfterMB <= 0 {
		winAfterMB = int(defaults.AfterBytes / (1024 * 1024))
	}

	mgr := &StreamJobManager{
		stream:          stream,
		engine:          engine,
		ffmpegPath:      ffmpegPath,
		ffprobePath:     ffprobePath,
		baseDir:         baseDir,
		dataDir:         dataDir,
		preset:          preset,
		crf:             crf,
		audioBitrate:    audioBitrate,
		segmentDuration: segDur,
		jobs:            make(map[hlsKey]*StreamJob),
		lastHardSeek:    make(map[hlsKey]time.Time),
		codecCache:      make(map[string]*codecCacheEntry),
		resolutionCache: make(map[string]*resolutionCacheEntry),
		remuxCache:      make(map[string]*remuxEntry),
		ramBufSize:      int64(ramBufMB) * 1024 * 1024,
		prebufferSize:   int64(prebufMB) * 1024 * 1024,
		windowBefore:    int64(winBeforeMB) * 1024 * 1024,
		windowAfter:     int64(winAfterMB) * 1024 * 1024,
		logger:          logger,
	}
	mgr.loadCodecCache()
	return mgr
}

// ---- Job lifecycle ----------------------------------------------------------

// buildJobDir constructs the job directory path for a given key.
func (m *StreamJobManager) buildJobDir(key hlsKey) string {
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

// EnsureJob returns an existing or new StreamJob for the given key.
func (m *StreamJobManager) EnsureJob(id domain.TorrentID, fileIndex, audioTrack, subtitleTrack int) (*StreamJob, error) {
	if m.stream == nil {
		return nil, errors.New("stream use case not configured")
	}

	key := hlsKey{
		id:            id,
		fileIndex:     fileIndex,
		audioTrack:    audioTrack,
		subtitleTrack: subtitleTrack,
	}

	dir := m.buildJobDir(key)

	// Fast path: job already exists.
	m.mu.RLock()
	job, ok := m.jobs[key]
	m.mu.RUnlock()
	if ok {
		return job, nil
	}

	// Slow path: create new job.
	m.mu.Lock()
	// Double-check.
	job, ok = m.jobs[key]
	if ok {
		m.mu.Unlock()
		return job, nil
	}

	// Check for completed job from a previous run (persistent volume).
	masterPlaylist := filepath.Join(dir, "master.m3u8")
	if _, statErr := os.Stat(masterPlaylist); statErr == nil {
		v0Playlist := filepath.Join(dir, "v0", "index.m3u8")
		if playlistHasEndList(v0Playlist) {
			job = newStreamJob(m, key, dir, 0)
			job.multiVariant = true
			job.playlist = masterPlaylist
			job.mu.Lock()
			job.state = StreamCompleted
			job.mu.Unlock()
			job.signalReady()
			m.jobs[key] = job
			m.mu.Unlock()
			m.logger.Info("stream reusing cached multi-variant transcode", slog.String("dir", dir))
			return job, nil
		}
	}
	playlist := filepath.Join(dir, "index.m3u8")
	if playlistHasEndList(playlist) {
		job = newStreamJob(m, key, dir, 0)
		job.mu.Lock()
		job.state = StreamCompleted
		job.mu.Unlock()
		job.signalReady()
		m.jobs[key] = job
		m.mu.Unlock()
		m.logger.Info("stream reusing cached transcode", slog.String("dir", dir))
		return job, nil
	}

	// Clean and start fresh.
	if err := os.RemoveAll(dir); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	job = newStreamJob(m, key, dir, 0)
	m.jobs[key] = job
	m.totalJobStarts++
	m.lastJobStartedAt = time.Now().UTC()
	metrics.HLSJobStartsTotal.Inc()
	metrics.HLSActiveJobs.Set(float64(len(m.jobs)))
	m.mu.Unlock()

	job.StartPlayback()
	return job, nil
}

// SeekJob handles a seek request for the given key.
// Returns the job, the chosen seek mode, and any error.
func (m *StreamJobManager) SeekJob(id domain.TorrentID, fileIndex, audioTrack, subtitleTrack int, seekSeconds float64) (*StreamJob, SeekMode, error) {
	if m.stream == nil {
		return nil, SeekModeHard, errors.New("stream use case not configured")
	}

	key := hlsKey{
		id:            id,
		fileIndex:     fileIndex,
		audioTrack:    audioTrack,
		subtitleTrack: subtitleTrack,
	}

	baseDir := m.buildJobDir(key)

	// Pre-compute seek mode without holding write lock.
	m.mu.RLock()
	preLockOld, preLockHasOld := m.jobs[key]
	preLockSegDur := m.segmentDuration
	m.mu.RUnlock()

	var preLockMode SeekMode = SeekModeHard
	if preLockHasOld {
		preLockMode = m.chooseSeekMode(key, preLockOld, seekSeconds, preLockSegDur)
	}

	m.mu.Lock()
	m.totalSeekRequests++
	m.lastSeekAt = time.Now().UTC()
	m.lastSeekTarget = seekSeconds
	metrics.HLSSeekTotal.Inc()

	seekModeEmitted := false
	if old, ok := m.jobs[key]; ok {
		var seekMode SeekMode
		if old == preLockOld {
			seekMode = preLockMode
		} else {
			seekMode = SeekModeHard
		}
		metrics.HLSSeekModeTotal.WithLabelValues(seekMode.String()).Inc()
		seekModeEmitted = true

		if seekMode == SeekModeSoft {
			m.mu.Unlock()
			m.logger.Info("stream seek — soft (no FFmpeg restart)",
				slog.String("torrentId", string(id)),
				slog.Float64("targetSec", seekSeconds),
			)
			return old, seekMode, nil
		}
	}

	// Hard seek: stop old job, start new one.
	var deferredCancel context.CancelFunc
	oldDir := ""
	if old, ok := m.jobs[key]; ok {
		delete(m.jobs, key)
		deferredCancel = old.cancel
		oldDir = old.dir
	}

	// Anti-seek-storm.
	now := time.Now()
	if prev, ok := m.lastHardSeek[key]; ok && now.Sub(prev) < 150*time.Millisecond {
		m.logger.Debug("stream seek storm detected",
			slog.String("torrentId", string(id)),
			slog.Duration("interval", now.Sub(prev)),
		)
	}
	m.lastHardSeek[key] = now

	dir := baseDir + fmt.Sprintf("-seek-%d", time.Now().UnixNano())
	if err := os.RemoveAll(dir); err != nil {
		m.mu.Unlock()
		return nil, SeekModeHard, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.mu.Unlock()
		return nil, SeekModeHard, err
	}
	job := newStreamJob(m, key, dir, seekSeconds)
	m.jobs[key] = job
	m.totalJobStarts++
	m.lastJobStartedAt = time.Now().UTC()
	metrics.HLSJobStartsTotal.Inc()
	metrics.HLSActiveJobs.Set(float64(len(m.jobs)))
	m.mu.Unlock()

	// Pre-boost priority at seek target.
	m.preSeekPriorityBoost(key, seekSeconds)

	job.StartPlayback()

	// Deferred old-job cleanup.
	if deferredCancel != nil || (oldDir != "" && oldDir != dir) {
		capturedOldDir := oldDir
		capturedNewDir := dir
		go func() {
			select {
			case <-job.ready:
			case <-time.After(8 * time.Second):
			}
			if deferredCancel != nil {
				deferredCancel()
			}
			if capturedOldDir != "" && capturedOldDir != capturedNewDir {
				_ = os.RemoveAll(capturedOldDir)
			}
		}()
	}

	if !seekModeEmitted {
		metrics.HLSSeekModeTotal.WithLabelValues(SeekModeHard.String()).Inc()
	}

	return job, SeekModeHard, nil
}

// StopJob cancels and removes the job for the given key.
func (m *StreamJobManager) StopJob(key hlsKey) {
	m.mu.Lock()
	job, ok := m.jobs[key]
	if ok {
		delete(m.jobs, key)
	}
	m.mu.Unlock()
	if ok && job != nil {
		job.Stop()
	}
	metrics.HLSActiveJobs.Set(float64(m.countRunningJobs()))
}

func (m *StreamJobManager) countRunningJobs() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, j := range m.jobs {
		if j.IsRunning() {
			n++
		}
	}
	return n
}

// CleanupJob removes a job if it matches the expected instance.
func (m *StreamJobManager) CleanupJob(key hlsKey, job *StreamJob) {
	m.mu.Lock()
	if current, ok := m.jobs[key]; ok && current == job {
		delete(m.jobs, key)
	}
	m.mu.Unlock()
	metrics.HLSActiveJobs.Set(float64(m.countRunningJobs()))
}

// ---- Seek mode decision -----------------------------------------------------

// chooseSeekMode decides how to handle a seek request.
// Simplified from hlsManager: only soft and hard (no cache mode).
func (m *StreamJobManager) chooseSeekMode(key hlsKey, job *StreamJob, targetSec float64, segDurInt int) SeekMode {
	if job == nil {
		return SeekModeHard
	}

	segDur := float64(segDurInt)
	if segDur <= 0 {
		segDur = 4
	}
	currentSec := job.seekSeconds

	distance := targetSec - currentSec
	absDistance := math.Abs(distance)

	// Soft: target within 2×segDur of job seekSeconds.
	if absDistance < 2*segDur {
		return SeekModeSoft
	}

	// Forward soft: FFmpeg already encoded past target, or gap small enough.
	if distance > 0 && job.ffmpeg != nil {
		progressUs := job.ffmpeg.ProgressUs()
		encoded := job.seekSeconds + float64(progressUs)/1e6
		gap := targetSec - encoded
		if gap < estimatedRestartCostSec {
			return SeekModeSoft
		}
	}

	return SeekModeHard
}

// ---- Data source creation ---------------------------------------------------

// newStreamDataSource determines the best data source for a StreamJob.
// Returns the data source and subtitle path.
func (m *StreamJobManager) newStreamDataSource(result usecase.StreamResult, job *StreamJob) (MediaDataSource, string) {
	fileComplete := result.File.Length <= 0 ||
		(result.File.BytesCompleted > 0 && result.File.BytesCompleted >= result.File.Length)

	subtitleSourcePath := ""

	if m.dataDir != "" {
		candidatePath, pathErr := resolveDataFilePath(m.dataDir, result.File.Path)
		if pathErr == nil {
			if info, statErr := os.Stat(candidatePath); statErr == nil && !info.IsDir() {
				subtitleSourcePath = candidatePath
				if fileComplete {
					m.logger.Info("stream using directFileSource",
						slog.String("path", candidatePath),
					)
					return &directFileSource{path: candidatePath, reader: result.Reader}, subtitleSourcePath
				}
			}
		}
	}

	// Pipe through RAMBuffer for incomplete files.
	m.logger.Info("stream using pipeSource (RAMBuffer)")
	bufSize := m.ramBufSize
	if bufSize <= 0 {
		bufSize = defaultRAMBufSize
	}
	ramBuf := NewRAMBuffer(result.Reader, int(bufSize), m.logger)
	job.ramBuf = ramBuf
	return &streamPipeSource{buf: ramBuf}, subtitleSourcePath
}

// ---- Encoding settings (implements app.EncodingSettingsEngine) ---------------

func (m *StreamJobManager) EncodingPreset() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.preset
}

func (m *StreamJobManager) EncodingCRF() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.crf
}

func (m *StreamJobManager) EncodingAudioBitrate() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.audioBitrate
}

func (m *StreamJobManager) UpdateEncodingSettings(preset string, crf int, audioBitrate string) {
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

// ---- HLS settings (implements app.HLSSettingsEngine) ------------------------

func (m *StreamJobManager) SegmentDuration() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.segmentDuration <= 0 {
		return 2
	}
	return m.segmentDuration
}

func (m *StreamJobManager) RAMBufSizeBytes() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ramBufSize
}

func (m *StreamJobManager) PrebufferBytes() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.prebufferSize
}

func (m *StreamJobManager) WindowBeforeBytes() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.windowBefore
}

func (m *StreamJobManager) WindowAfterBytes() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.windowAfter
}

func (m *StreamJobManager) UpdateHLSSettings(settings app.HLSSettings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if settings.SegmentDuration > 0 {
		m.segmentDuration = settings.SegmentDuration
	}
	if settings.RAMBufSizeMB > 0 {
		m.ramBufSize = int64(settings.RAMBufSizeMB) * 1024 * 1024
	}
	if settings.PrebufferMB > 0 {
		m.prebufferSize = int64(settings.PrebufferMB) * 1024 * 1024
	}
	if settings.WindowBeforeMB > 0 {
		m.windowBefore = int64(settings.WindowBeforeMB) * 1024 * 1024
	}
	if settings.WindowAfterMB > 0 {
		m.windowAfter = int64(settings.WindowAfterMB) * 1024 * 1024
	}
}

// currentWindowConfig builds a WindowConfig snapshot from the current settings.
func (m *StreamJobManager) currentWindowConfig() WindowConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return WindowConfig{
		RAMBufSize:   m.ramBufSize,
		PreloadBytes: m.prebufferSize,
		BeforeBytes:  m.windowBefore,
		AfterBytes:   m.windowAfter,
	}
}

// ---- Profile hash -----------------------------------------------------------

func (m *StreamJobManager) profileHash() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	segDur := m.segmentDuration
	if segDur <= 0 {
		segDur = 2
	}
	return computeProfileHash(m.preset, m.crf, m.audioBitrate, segDur)
}

// ---- Codec / resolution / FPS caches ----------------------------------------

func (m *StreamJobManager) codecCachePath() string {
	return filepath.Join(m.baseDir, "codec_cache.json")
}

func (m *StreamJobManager) loadCodecCache() {
	data, err := os.ReadFile(m.codecCachePath())
	if err != nil {
		return
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
				width: e.Width, height: e.Height, duration: e.Duration, fps: e.FPS,
			}
		}
	}
	m.resolutionCacheMu.Unlock()
	m.codecCacheMu.Unlock()
	m.logger.Info("loaded codec cache", slog.Int("entries", len(entries)))
}

func (m *StreamJobManager) saveCodecCache() {
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

func (m *StreamJobManager) scheduleCodecCacheSave() {
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

func (m *StreamJobManager) evictCodecCacheIfNeeded() {
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

func (m *StreamJobManager) isH264FileWithCache(filePath string) bool {
	m.codecCacheMu.Lock()
	if entry, ok := m.codecCache[filePath]; ok {
		entry.lastAccess = time.Now()
		m.codecCacheMu.Unlock()
		return entry.isH264
	}
	m.codecCacheMu.Unlock()

	result := isH264FileWithRetry(m.ffprobePath, filePath, m.logger)

	m.codecCacheMu.Lock()
	if m.codecCache[filePath] == nil {
		m.codecCache[filePath] = &codecCacheEntry{}
	}
	m.codecCache[filePath].isH264 = result
	m.codecCache[filePath].lastAccess = time.Now()
	m.evictCodecCacheIfNeeded()
	m.codecCacheMu.Unlock()
	m.scheduleCodecCacheSave()
	return result
}

func (m *StreamJobManager) isAACAudioWithCache(filePath string) bool {
	m.codecCacheMu.Lock()
	if entry, ok := m.codecCache[filePath]; ok {
		entry.lastAccess = time.Now()
		m.codecCacheMu.Unlock()
		return entry.isAAC
	}
	m.codecCacheMu.Unlock()

	result := isAACAudioWithRetry(m.ffprobePath, filePath, m.logger)

	m.codecCacheMu.Lock()
	if m.codecCache[filePath] == nil {
		m.codecCache[filePath] = &codecCacheEntry{}
	}
	m.codecCache[filePath].isAAC = result
	m.codecCache[filePath].lastAccess = time.Now()
	m.evictCodecCacheIfNeeded()
	m.codecCacheMu.Unlock()
	m.scheduleCodecCacheSave()
	return result
}

func (m *StreamJobManager) getVideoResolutionWithCache(filePath string) (int, int) {
	m.resolutionCacheMu.RLock()
	if entry, ok := m.resolutionCache[filePath]; ok {
		m.resolutionCacheMu.RUnlock()
		return entry.width, entry.height
	}
	m.resolutionCacheMu.RUnlock()

	w, h := getVideoResolution(m.ffprobePath, filePath)

	m.resolutionCacheMu.Lock()
	m.resolutionCache[filePath] = &resolutionCacheEntry{width: w, height: h}
	m.resolutionCacheMu.Unlock()
	m.scheduleCodecCacheSave()
	return w, h
}

func (m *StreamJobManager) getVideoResolutionWithDuration(filePath string) (int, int, float64) {
	m.resolutionCacheMu.RLock()
	if entry, ok := m.resolutionCache[filePath]; ok {
		m.resolutionCacheMu.RUnlock()
		return entry.width, entry.height, entry.duration
	}
	m.resolutionCacheMu.RUnlock()

	w, h := getVideoResolution(m.ffprobePath, filePath)
	dur := getVideoDuration(m.ffprobePath, filePath)

	m.resolutionCacheMu.Lock()
	m.resolutionCache[filePath] = &resolutionCacheEntry{width: w, height: h, duration: dur}
	m.resolutionCacheMu.Unlock()
	m.scheduleCodecCacheSave()
	return w, h, dur
}

func (m *StreamJobManager) getVideoFPSWithCache(filePath string) float64 {
	m.resolutionCacheMu.RLock()
	if entry, ok := m.resolutionCache[filePath]; ok {
		fps := entry.fps
		m.resolutionCacheMu.RUnlock()
		return fps
	}
	m.resolutionCacheMu.RUnlock()

	fps := getVideoFPS(m.ffprobePath, filePath)

	m.resolutionCacheMu.Lock()
	if entry, ok := m.resolutionCache[filePath]; ok {
		entry.fps = fps
	} else {
		m.resolutionCache[filePath] = &resolutionCacheEntry{fps: fps}
	}
	m.resolutionCacheMu.Unlock()
	m.scheduleCodecCacheSave()
	return fps
}

// ---- Remux cache (MKV → MP4) ------------------------------------------------

func (m *StreamJobManager) getRemuxPath(id domain.TorrentID, fileIndex int) string {
	return filepath.Join(m.baseDir, "remux", string(id), strconv.Itoa(fileIndex)+".mp4")
}

func (m *StreamJobManager) checkRemux(id domain.TorrentID, fileIndex int) (path string, ready bool) {
	key := remuxCacheKey(id, fileIndex)

	m.remuxCacheMu.Lock()
	entry, ok := m.remuxCache[key]
	m.remuxCacheMu.Unlock()

	if !ok {
		p := m.getRemuxPath(id, fileIndex)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Size() > 0 {
			e := &remuxEntry{path: p, ready: make(chan struct{}), started: info.ModTime()}
			close(e.ready)
			m.remuxCacheMu.Lock()
			m.remuxCache[key] = e
			m.remuxCacheMu.Unlock()
			return p, true
		}
		return "", false
	}

	select {
	case <-entry.ready:
		if entry.err != nil {
			return "", false
		}
		return entry.path, true
	default:
		return entry.path, false
	}
}

func (m *StreamJobManager) triggerRemux(id domain.TorrentID, fileIndex int, inputPath string) {
	key := remuxCacheKey(id, fileIndex)

	m.remuxCacheMu.Lock()
	if _, ok := m.remuxCache[key]; ok {
		m.remuxCacheMu.Unlock()
		return
	}
	outPath := m.getRemuxPath(id, fileIndex)
	entry := &remuxEntry{
		path:    outPath,
		ready:   make(chan struct{}),
		started: time.Now(),
	}
	m.remuxCache[key] = entry
	m.remuxCacheMu.Unlock()

	go m.runRemux(entry, inputPath, key)
}

func (m *StreamJobManager) runRemux(entry *remuxEntry, inputPath, cacheKey string) {
	defer close(entry.ready)

	outDir := filepath.Dir(entry.path)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		entry.err = fmt.Errorf("remux mkdir: %w", err)
		m.logger.Warn("remux mkdir failed", slog.String("error", err.Error()))
		return
	}

	tmpPath := entry.path + ".tmp"
	audioArgs := []string{"-c:a", "aac", "-b:a", "128k"}
	if m.isAACAudioWithCache(inputPath) {
		audioArgs = []string{"-c:a", "copy"}
	}

	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-i", inputPath,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-c:v", "copy",
	}
	args = append(args, audioArgs...)
	args = append(args, "-movflags", "+faststart", "-y", tmpPath)

	m.logger.Info("remux starting",
		slog.String("input", inputPath),
		slog.String("output", entry.path),
	)

	start := time.Now()
	cmd := exec.Command(m.ffmpegPath, args...)
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpPath)
		entry.err = fmt.Errorf("remux ffmpeg: %w: %s", err, strings.TrimSpace(stderrBuf.String()))
		m.logger.Warn("remux failed",
			slog.String("input", inputPath),
			slog.String("error", err.Error()),
		)
		m.remuxCacheMu.Lock()
		if current, ok := m.remuxCache[cacheKey]; ok && current == entry {
			delete(m.remuxCache, cacheKey)
		}
		m.remuxCacheMu.Unlock()
		return
	}

	if err := os.Rename(tmpPath, entry.path); err != nil {
		_ = os.Remove(tmpPath)
		entry.err = fmt.Errorf("remux rename: %w", err)
		m.logger.Warn("remux rename failed", slog.String("error", err.Error()))
		return
	}

	m.logger.Info("remux complete",
		slog.String("output", entry.path),
		slog.Duration("elapsed", time.Since(start)),
	)
}

// ---- Priority boost for seek ------------------------------------------------

func (m *StreamJobManager) preSeekPriorityBoost(key hlsKey, seekSeconds float64) {
	if m.engine == nil || m.dataDir == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	state, err := m.engine.GetSessionState(ctx, key.id)
	if err != nil || key.fileIndex >= len(state.Files) {
		return
	}
	file := state.Files[key.fileIndex]
	if file.Length <= 0 {
		return
	}

	candidatePath, pathErr := resolveDataFilePath(m.dataDir, file.Path)
	if pathErr != nil {
		return
	}
	_, _, dur := m.getVideoResolutionWithDuration(candidatePath)
	if dur <= 0 {
		return
	}

	estByte := estimateByteOffset(seekSeconds, dur, file.Length)
	if estByte < 0 {
		return
	}

	const boostWindow = 16 << 20
	start := estByte - boostWindow/2
	if start < 0 {
		start = 0
	}
	_ = m.engine.SetPiecePriority(ctx, key.id, file,
		domain.Range{Off: start, Length: boostWindow}, domain.PriorityHigh)
}

// preloadFileEnds boosts the priority of the last 16 MB for container seek indices.
func (m *StreamJobManager) preloadFileEnds(key hlsKey, file domain.FileRef) {
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

// ---- Health snapshot --------------------------------------------------------

func (m *StreamJobManager) healthSnapshot() hlsHealthSnapshot {
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

func (m *StreamJobManager) recordJobFailure(job *StreamJob, err error) {
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

func (m *StreamJobManager) recordPlaylistReady(job *StreamJob) {
	now := time.Now().UTC()
	m.mu.Lock()
	m.lastPlaylistReady = now
	if job != nil && job.seekSeconds > 0 {
		m.lastSeekError = ""
		m.lastSeekErrorAt = time.Time{}
	}
	m.mu.Unlock()
}

// PurgeTorrent stops all jobs for the given torrent and removes their working directories.
func (m *StreamJobManager) PurgeTorrent(id domain.TorrentID) {
	m.mu.Lock()
	var toStop []*StreamJob
	var dirs []string
	for key, job := range m.jobs {
		if key.id == id {
			toStop = append(toStop, job)
			dirs = append(dirs, job.dir)
			delete(m.jobs, key)
		}
	}
	m.mu.Unlock()

	for _, job := range toStop {
		job.Stop()
	}

	// Also remove the torrent's base directory from the HLS working area.
	torrentDir := filepath.Join(m.baseDir, string(id))
	_ = os.RemoveAll(torrentDir)

	// Clean up any seek directories that may have been created outside the base.
	for _, d := range dirs {
		_ = os.RemoveAll(d)
	}
}

// ---- Shutdown ---------------------------------------------------------------

func (m *StreamJobManager) shutdown() {
	m.mu.Lock()
	for key, job := range m.jobs {
		job.Stop()
		delete(m.jobs, key)
	}
	m.mu.Unlock()

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

// ---- Compile-time interface checks ------------------------------------------

// Ensure StreamJobManager satisfies the app settings engine interfaces.
var (
	_ interface {
		EncodingPreset() string
		EncodingCRF() int
		EncodingAudioBitrate() string
		UpdateEncodingSettings(string, int, string)
	} = (*StreamJobManager)(nil)

	_ app.HLSSettingsEngine = (*StreamJobManager)(nil)
)

