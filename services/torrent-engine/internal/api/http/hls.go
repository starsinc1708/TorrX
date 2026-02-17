package apihttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
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

func (m *hlsManager) run(job *hlsJob, key hlsKey) {
	m.logger.Info("hls job starting",
		slog.String("torrentId", string(key.id)),
		slog.Int("fileIndex", key.fileIndex),
		slog.Int("audioTrack", key.audioTrack),
		slog.Int("subtitleTrack", key.subtitleTrack),
	)

	// Use job context so cancellation is available before goroutine starts.
	ctx := job.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if job.cancel != nil {
		defer job.cancel()
	}
	defer m.markJobStopped(key, job)

	result, err := m.stream.Execute(ctx, key.id, key.fileIndex)
	if err != nil {
		job.err = err
		m.recordJobFailure(job, err)
		job.signalReady()
		m.cleanupJob(key, job)
		return
	}
	if result.Reader == nil {
		job.err = errors.New("stream reader not available")
		m.recordJobFailure(job, job.err)
		job.signalReady()
		m.cleanupJob(key, job)
		return
	}
	// Reader is closed explicitly on each exit path to avoid double-close panics.

	input := "pipe:0"
	useReader := true
	subtitleSourcePath := ""
	if m.dataDir != "" {
		candidatePath, pathErr := resolveDataFilePath(m.dataDir, result.File.Path)
		if pathErr == nil {
			if info, statErr := os.Stat(candidatePath); statErr == nil && !info.IsDir() {
				subtitleSourcePath = candidatePath
				if result.File.Length <= 0 || info.Size() >= result.File.Length {
					input = candidatePath
					useReader = false
				}
			}
		}
	}

	// When seeking and file is not on disk, use the internal HTTP stream
	// endpoint so FFmpeg can use HTTP range requests for efficient seeking.
	if job.seekSeconds > 0 && useReader && m.listenAddr != "" {
		host := m.listenAddr
		if strings.HasPrefix(host, ":") {
			host = "127.0.0.1" + host
		}
		input = fmt.Sprintf("http://%s/torrents/%s/stream?fileIndex=%d", host, string(key.id), key.fileIndex)
		useReader = false
	}

	if key.subtitleTrack >= 0 && subtitleSourcePath == "" {
		_ = result.Reader.Close()
		job.err = errSubtitleSourceUnavailable
		m.recordJobFailure(job, job.err)
		job.signalReady()
		m.cleanupJob(key, job)
		return
	}

	// Always use event playlist so the m3u8 is written incrementally
	// (vod only writes the playlist after the entire file is encoded,
	// which blocks playback for subtitle-burning jobs).
	playlistType := "event"
	flags := "append_list+independent_segments"

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-fflags", "+genpts+discardcorrupt",
		"-err_detect", "ignore_err",
		"-analyzeduration", "20000000",
		"-probesize", "10000000",
		"-avoid_negative_ts", "make_zero",
	}

	if job.seekSeconds > 0 {
		args = append(args, "-ss", strconv.FormatFloat(job.seekSeconds, 'f', 3, 64))
	}

	// HTTP input: add reconnect flags for resilience.
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		args = append(args, "-reconnect", "1", "-reconnect_streamed", "1")
	}

	args = append(args, "-i", input)

	// Snapshot encoding settings under lock so the job uses a consistent set.
	m.mu.Lock()
	encPreset := m.preset
	encCRF := m.crf
	encAudioBitrate := m.audioBitrate
	segDur := m.segmentDuration
	m.mu.Unlock()
	if segDur <= 0 {
		segDur = 4
	}
	segDurStr := strconv.Itoa(segDur)

	// Detect source resolution for multi-variant encoding.
	isLocalFile := !useReader &&
		!strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://")
	sourceHeight := 0
	if isLocalFile {
		_, sourceHeight = m.getVideoResolutionWithCache(input)
	}

	// When the source is a local H.264 file and no subtitle burning is
	// needed, use stream copy to avoid expensive re-encoding.
	streamCopy := false
	if isLocalFile && key.subtitleTrack < 0 && m.isH264FileWithCache(input) {
		streamCopy = true
	}

	// Multi-variant encoding: only for re-encoding with known source resolution.
	multiVariant := false
	var variants []qualityVariant
	if !streamCopy && sourceHeight > 0 {
		variants = computeVariants(sourceHeight)
		if len(variants) > 0 {
			multiVariant = true
			job.multiVariant = true
			job.variants = variants
			job.playlist = filepath.Join(job.dir, "master.m3u8")
			for i := range variants {
				_ = os.MkdirAll(filepath.Join(job.dir, fmt.Sprintf("v%d", i)), 0o755)
			}
		}
	}

	if streamCopy {
		args = append(args,
			"-map", "0:v:0",
			"-map", fmt.Sprintf("0:a:%d?", key.audioTrack),
			"-c:v", "copy",
		)
		if m.isAACAudioWithCache(input) {
			args = append(args, "-c:a", "copy")
		} else {
			args = append(args, "-c:a", "aac", "-b:a", encAudioBitrate, "-ac", "2")
		}
		m.logger.Info("hls using stream copy mode", slog.String("input", input))
	} else if multiVariant {
		// Build filter_complex: optional subtitle burn → split → per-variant scale.
		args = append(args, "-filter_complex",
			buildMultiVariantFilterComplex(variants, subtitleSourcePath, key.subtitleTrack))

		// Map each variant's video output + shared audio input.
		for i := range variants {
			args = append(args,
				"-map", fmt.Sprintf("[out%d]", i),
				"-map", fmt.Sprintf("0:a:%d?", key.audioTrack),
			)
		}

		args = append(args,
			"-c:v", "libx264",
			"-pix_fmt", "yuv420p",
			"-preset", encPreset,
			"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segDur),
		)

		// Per-variant bitrate / CRF settings.
		for i, v := range variants {
			if v.VideoBitrate != "" {
				args = append(args,
					fmt.Sprintf("-b:v:%d", i), v.VideoBitrate,
					fmt.Sprintf("-maxrate:v:%d", i), v.MaxRate,
					fmt.Sprintf("-bufsize:v:%d", i), v.BufSize,
				)
			} else {
				// Highest variant: CRF for best quality at source resolution.
				args = append(args, fmt.Sprintf("-crf:v:%d", i), strconv.Itoa(encCRF))
			}
		}

		args = append(args, "-c:a", "aac", "-b:a", encAudioBitrate, "-ac", "2")
		m.logger.Info("hls using multi-variant mode",
			slog.String("input", input),
			slog.Int("variants", len(variants)),
			slog.Int("sourceHeight", sourceHeight),
		)
	} else {
		args = append(args,
			"-map", "0:v:0",
			"-map", fmt.Sprintf("0:a:%d?", key.audioTrack),
			"-c:v", "libx264",
			"-pix_fmt", "yuv420p",
			"-preset", encPreset,
			"-crf", strconv.Itoa(encCRF),
			"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segDur),
		)
		if key.subtitleTrack >= 0 {
			args = append(args,
				"-vf", subtitleFilterArg(subtitleSourcePath, key.subtitleTrack),
			)
		}
		args = append(args,
			"-c:a", "aac",
			"-b:a", encAudioBitrate,
			"-ac", "2",
		)
	}

	// HLS muxer output settings.
	if multiVariant {
		// Build var_stream_map: "v:0,a:0 v:1,a:1 ..."
		streamParts := make([]string, len(variants))
		for i := range variants {
			streamParts[i] = fmt.Sprintf("v:%d,a:%d", i, i)
		}
		args = append(args,
			"-f", "hls",
			"-hls_time", segDurStr,
			"-hls_list_size", "0",
			"-hls_playlist_type", playlistType,
			"-hls_flags", flags,
			"-master_pl_name", "master.m3u8",
			"-hls_segment_filename", "v%v/seg-%05d.ts",
			"-var_stream_map", strings.Join(streamParts, " "),
			"v%v/index.m3u8",
		)
	} else {
		args = append(args,
			"-f", "hls",
			"-hls_time", segDurStr,
			"-hls_list_size", "0",
			"-hls_playlist_type", playlistType,
			"-hls_flags", flags,
			"-hls_segment_filename", "seg-%05d.ts",
			"index.m3u8",
		)
	}

	m.logger.Info("hls ffmpeg starting",
		slog.String("input", input),
		slog.Bool("useReader", useReader),
		slog.String("playlistType", playlistType),
		slog.Int("subtitleTrack", key.subtitleTrack),
		slog.String("dir", job.dir),
	)

	cmd := exec.CommandContext(ctx, m.ffmpegPath, args...)
	cmd.Dir = job.dir
	var stderr bytes.Buffer
	if useReader {
		cmd.Stdin = result.Reader
	} else {
		_ = result.Reader.Close()
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr

	ffmpegStart := time.Now()
	if err := cmd.Start(); err != nil {
		// Close reader if it was passed to cmd.Stdin and cmd failed to start
		if useReader {
			_ = result.Reader.Close()
		}
		m.logger.Error("hls ffmpeg start failed", slog.String("error", err.Error()))
		job.err = err
		m.recordJobFailure(job, err)
		job.signalReady()
		m.cleanupJob(key, job)
		return
	}
	m.markJobRunning(key, job)
	go m.watchJobProgress(key, job)
	go m.cacheSegmentsLive(job, key)

	// Monitor for first playlist file — timeout after 120s.
	// Subtitle burning on large files requires ffmpeg to scan the entire
	// subtitle stream before producing the first segment.
	const startupTimeout = 120 * time.Second
	go func() {
		deadline := time.After(startupTimeout)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(job.playlist); err == nil {
				m.logger.Info("hls playlist ready", slog.String("dir", job.dir))
				m.touchJobActivity(key, job)
				m.recordPlaylistReady(job)
				job.signalReady()
				return
			}
			select {
			case <-job.ready:
				return
			case <-deadline:
				if job.err == nil {
					stderrMsg := strings.TrimSpace(stderr.String())
					if stderrMsg != "" {
						job.err = fmt.Errorf("ffmpeg timed out after %s: %s", startupTimeout, stderrMsg)
					} else {
						job.err = fmt.Errorf("ffmpeg timed out after %s waiting for first segment", startupTimeout)
					}
					m.logger.Error("hls ffmpeg startup timeout",
						slog.String("dir", job.dir),
						slog.String("stderr", stderrMsg),
					)
				}
				if job.cancel != nil {
					job.cancel() // kill the ffmpeg process
				}
				job.signalReady()
				return
			case <-ticker.C:
				// retry file check
			}
		}
	}()

	metrics.HLSEncodeDuration.Observe(time.Since(ffmpegStart).Seconds())

	if err := cmd.Wait(); err != nil && job.err == nil {
		stderrMsg := strings.TrimSpace(stderr.String())
		// Expected path for seek/track switch cancellation.
		if ctx.Err() != nil {
			m.logger.Info("hls ffmpeg exited after context cancellation",
				slog.String("dir", job.dir),
				slog.String("error", err.Error()),
			)
			job.signalReady()
			return
		}
		if waitForFile(job.playlist, 1*time.Millisecond) {
			// If playlist has no ENDLIST, FFmpeg died early and the player may
			// stall on the last written segment. Auto-restart the job to continue.
			if !playlistHasEndList(job.playlist) {
				m.logger.Warn("hls ffmpeg exited before playlist completion",
					slog.String("dir", job.dir),
					slog.String("error", err.Error()),
					slog.String("stderr", stderrMsg),
				)
				if _, restarted := m.tryAutoRestart(key, job, "ffmpeg_exit"); restarted {
					return
				}
				if stderrMsg != "" {
					job.err = fmt.Errorf("ffmpeg exited before playlist completion: %s", stderrMsg)
				} else {
					job.err = errors.New("ffmpeg exited before playlist completion")
				}
				m.recordJobFailure(job, job.err)
				job.signalReady()
				m.cleanupJob(key, job)
				return
			}
			m.logger.Info("hls ffmpeg finished after playlist completion",
				slog.String("dir", job.dir),
			)
			job.signalReady()
		} else {
			if stderrMsg != "" {
				job.err = fmt.Errorf("ffmpeg: %w: %s", err, stderrMsg)
			} else {
				job.err = fmt.Errorf("ffmpeg: %w", err)
			}
			m.logger.Error("hls ffmpeg exited with error",
				slog.String("dir", job.dir),
				slog.String("error", err.Error()),
				slog.String("stderr", stderrMsg),
			)
			m.recordJobFailure(job, job.err)
			job.signalReady()
			m.cleanupJob(key, job)
			return
		}
	}

	if job.err == nil {
		m.markJobCompleted(key, job)
	}

	if !waitForFile(job.playlist, 1*time.Millisecond) && job.err == nil {
		job.err = errors.New("hls playlist not produced")
		m.recordJobFailure(job, job.err)
		job.signalReady()
		m.cleanupJob(key, job)
	}
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

// harvestSegmentsToCache parses the m3u8 playlist(s) in dir, computes time
// offsets from seekSeconds + cumulative EXTINF durations, and copies each
// segment file into the cache. For multi-variant jobs, each variant directory
// is harvested independently.
func (m *hlsManager) harvestSegmentsToCache(key hlsKey, dir string, seekSeconds float64) {
	if m.cache == nil {
		return
	}

	type variantInfo struct {
		dir      string
		playlist string
		variant  string
	}
	var variantsToParse []variantInfo

	// Check for multi-variant (master.m3u8 exists).
	masterPlaylist := filepath.Join(dir, "master.m3u8")
	if _, err := os.Stat(masterPlaylist); err == nil {
		for i := 0; ; i++ {
			vDir := filepath.Join(dir, fmt.Sprintf("v%d", i))
			vPlaylist := filepath.Join(vDir, "index.m3u8")
			if _, err := os.Stat(vPlaylist); err != nil {
				break
			}
			variantsToParse = append(variantsToParse, variantInfo{
				dir: vDir, playlist: vPlaylist, variant: fmt.Sprintf("v%d", i),
			})
		}
	}
	if len(variantsToParse) == 0 {
		variantsToParse = append(variantsToParse, variantInfo{
			dir: dir, playlist: filepath.Join(dir, "index.m3u8"), variant: "",
		})
	}

	for _, vi := range variantsToParse {
		segments, err := parseM3U8Segments(vi.playlist)
		if err != nil {
			continue
		}
		cumTime := seekSeconds
		for _, seg := range segments {
			startTime := cumTime
			endTime := cumTime + seg.Duration
			srcPath := filepath.Join(vi.dir, seg.Filename)
			if _, err := os.Stat(srcPath); err != nil {
				cumTime = endTime
				continue
			}
			if err := m.cache.Store(
				string(key.id), key.fileIndex, key.audioTrack, key.subtitleTrack,
				vi.variant, startTime, endTime, srcPath,
			); err != nil {
				m.logger.Warn("hls cache store failed",
					slog.String("segment", seg.Filename),
					slog.String("variant", vi.variant),
					slog.String("error", err.Error()),
				)
			} else if m.memBuf != nil {
				if raw, readErr := os.ReadFile(srcPath); readErr == nil {
					cachePath := m.cache.SegmentPath(
						string(key.id), key.fileIndex, key.audioTrack, key.subtitleTrack,
						vi.variant, startTime, endTime,
					)
					m.memBuf.Put(cachePath, raw)
				}
			}
			cumTime = endTime
		}
	}
}

// cacheSegmentsLive runs alongside an active ffmpeg job, periodically
// parsing the growing m3u8 playlist(s) and caching new segments as they
// appear. For multi-variant jobs, each variant playlist is tracked
// independently.
func (m *hlsManager) cacheSegmentsLive(job *hlsJob, key hlsKey) {
	if m.cache == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	type variantState struct {
		dir      string
		playlist string
		variant  string
		cached   int
	}

	var variants []*variantState
	initialized := false

	for {
		select {
		case <-job.ctx.Done():
			return
		case <-ticker.C:
			// Lazy-initialize variant list after ffmpeg has started and
			// potentially set job.multiVariant.
			if !initialized {
				if job.multiVariant {
					for i := range job.variants {
						vDir := filepath.Join(job.dir, fmt.Sprintf("v%d", i))
						variants = append(variants, &variantState{
							dir:      vDir,
							playlist: filepath.Join(vDir, "index.m3u8"),
							variant:  fmt.Sprintf("v%d", i),
						})
					}
				} else {
					variants = append(variants, &variantState{
						dir:      job.dir,
						playlist: job.playlist,
						variant:  "",
					})
				}
				initialized = true
			}

			for _, vs := range variants {
				segments, err := parseM3U8Segments(vs.playlist)
				if err != nil || len(segments) <= vs.cached {
					continue
				}
				cumTime := job.seekSeconds
				for i := 0; i < vs.cached && i < len(segments); i++ {
					cumTime += segments[i].Duration
				}
				for i := vs.cached; i < len(segments); i++ {
					seg := segments[i]
					startTime := cumTime
					endTime := cumTime + seg.Duration
					srcPath := filepath.Join(vs.dir, seg.Filename)
					if _, statErr := os.Stat(srcPath); statErr == nil {
						_ = m.cache.Store(
							string(key.id), key.fileIndex, key.audioTrack, key.subtitleTrack,
							vs.variant, startTime, endTime, srcPath,
						)
						if m.memBuf != nil {
							if raw, readErr := os.ReadFile(srcPath); readErr == nil {
								m.memBuf.Put(srcPath, raw)
							}
						}
					}
					cumTime = endTime
				}
				vs.cached = len(segments)
			}
		}
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

func subtitleFilterArg(sourcePath string, subtitleTrack int) string {
	path := strings.ReplaceAll(sourcePath, `\`, `/`)
	path = strings.ReplaceAll(path, `'`, `\'`)
	path = strings.ReplaceAll(path, ":", `\:`)
	return fmt.Sprintf("subtitles='%s':si=%d", path, subtitleTrack)
}

// buildMultiVariantFilterComplex constructs an FFmpeg filter_complex string
// that splits the input video into multiple quality variants with optional
// subtitle burning applied before the split.
func buildMultiVariantFilterComplex(variants []qualityVariant, subtitleSourcePath string, subtitleTrack int) string {
	n := len(variants)
	var b strings.Builder

	// Input chain: [0:v:0] → optional subtitle burn → split=N
	b.WriteString("[0:v:0]")
	if subtitleTrack >= 0 && subtitleSourcePath != "" {
		b.WriteString(subtitleFilterArg(subtitleSourcePath, subtitleTrack))
		b.WriteString(",")
	}
	b.WriteString(fmt.Sprintf("split=%d", n))

	// Split output labels: [v0][v1]...[vN]
	for i := 0; i < n; i++ {
		b.WriteString(fmt.Sprintf("[v%d]", i))
	}

	// Per-variant scaling filters.
	for i := 0; i < n; i++ {
		b.WriteString("; ")
		if i < n-1 {
			// Lower variants: scale to target height, preserve aspect ratio.
			b.WriteString(fmt.Sprintf("[v%d]scale=-2:%d[out%d]", i, variants[i].Height, i))
		} else {
			// Highest variant: pass through at source resolution.
			b.WriteString(fmt.Sprintf("[v%d]null[out%d]", i, i))
		}
	}

	return b.String()
}

func safeSegmentPath(base, name string) (string, error) {
	cleaned := filepath.Clean(name)
	if strings.Contains(cleaned, "..") || strings.HasPrefix(cleaned, string(filepath.Separator)) {
		return "", errors.New("invalid segment path")
	}
	full := filepath.Join(base, cleaned)
	if !strings.HasPrefix(full, base+string(filepath.Separator)) && full != base {
		return "", errors.New("invalid segment path")
	}
	return full, nil
}

const (
	ffprobeRetryAttempts = 3
	ffprobeRetryDelay    = 2 * time.Second
)

// isH264FileWithCache checks if a file is H.264 encoded, using cache to avoid
// repeated ffprobe calls that can block HLS startup for up to 6 seconds.
func (m *hlsManager) isH264FileWithCache(filePath string) bool {
	// Check cache first
	m.codecCacheMu.RLock()
	if entry, ok := m.codecCache[filePath]; ok {
		m.codecCacheMu.RUnlock()
		return entry.isH264
	}
	m.codecCacheMu.RUnlock()

	// Not in cache, perform detection with retry
	result := isH264FileWithRetry(m.ffprobePath, filePath, m.logger)

	// Store in cache
	m.codecCacheMu.Lock()
	if m.codecCache[filePath] == nil {
		m.codecCache[filePath] = &codecCacheEntry{}
	}
	m.codecCache[filePath].isH264 = result
	m.evictCodecCacheIfNeeded()
	m.codecCacheMu.Unlock()

	m.scheduleCodecCacheSave()
	return result
}

// isAACAudioWithCache checks if a file has AAC audio, using cache to avoid
// repeated ffprobe calls.
func (m *hlsManager) isAACAudioWithCache(filePath string) bool {
	// Check cache first
	m.codecCacheMu.RLock()
	if entry, ok := m.codecCache[filePath]; ok {
		m.codecCacheMu.RUnlock()
		return entry.isAAC
	}
	m.codecCacheMu.RUnlock()

	// Not in cache, perform detection with retry
	result := isAACAudioWithRetry(m.ffprobePath, filePath, m.logger)

	// Store in cache
	m.codecCacheMu.Lock()
	if m.codecCache[filePath] == nil {
		m.codecCache[filePath] = &codecCacheEntry{}
	}
	m.codecCache[filePath].isAAC = result
	m.evictCodecCacheIfNeeded()
	m.codecCacheMu.Unlock()

	m.scheduleCodecCacheSave()
	return result
}

func isH264FileWithRetry(ffprobePath, filePath string, logger *slog.Logger) bool {
	for i := 0; i < ffprobeRetryAttempts; i++ {
		if isH264File(ffprobePath, filePath) {
			return true
		}
		if i < ffprobeRetryAttempts-1 {
			logger.Debug("ffprobe h264 check failed, retrying",
				slog.String("file", filePath),
				slog.Int("attempt", i+1),
			)
			time.Sleep(ffprobeRetryDelay)
		}
	}
	return false
}

func isAACAudioWithRetry(ffprobePath, filePath string, logger *slog.Logger) bool {
	for i := 0; i < ffprobeRetryAttempts; i++ {
		if isAACAudio(ffprobePath, filePath) {
			return true
		}
		if i < ffprobeRetryAttempts-1 {
			logger.Debug("ffprobe aac check failed, retrying",
				slog.String("file", filePath),
				slog.Int("attempt", i+1),
			)
			time.Sleep(ffprobeRetryDelay)
		}
	}
	return false
}

func isH264File(ffprobePath, filePath string) bool {
	out, err := exec.Command(
		ffprobePath,
		"-v", "quiet",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name",
		"-of", "csv=p=0",
		filePath,
	).Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.TrimSpace(string(out)), "h264")
}

func isAACAudio(ffprobePath, filePath string) bool {
	out, err := exec.Command(
		ffprobePath,
		"-v", "quiet",
		"-select_streams", "a:0",
		"-show_entries", "stream=codec_name",
		"-of", "csv=p=0",
		filePath,
	).Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.TrimSpace(string(out)), "aac")
}

// getVideoResolution returns the width and height of the first video stream.
func getVideoResolution(ffprobePath, filePath string) (int, int) {
	out, err := exec.Command(
		ffprobePath,
		"-v", "quiet",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=p=0",
		filePath,
	).Output()
	if err != nil {
		return 0, 0
	}
	line := strings.TrimSpace(string(out))
	parts := strings.SplitN(line, ",", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0
	}
	return w, h
}

// getVideoResolutionWithCache returns the cached video resolution, detecting
// it with ffprobe on first call.
func (m *hlsManager) getVideoResolutionWithCache(filePath string) (int, int) {
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

// qualityVariant describes a single quality level for multi-variant HLS output.
type qualityVariant struct {
	Height       int
	VideoBitrate string // e.g. "1500k"; empty means use CRF (highest quality variant)
	MaxRate      string // e.g. "2000k"
	BufSize      string // e.g. "3000k"
}

// qualityPresets defines the available quality levels. Only presets whose
// height does not exceed the source are included. Sorted by height ascending.
var qualityPresets = []qualityVariant{
	{Height: 480, VideoBitrate: "1500k", MaxRate: "2000k", BufSize: "3000k"},
	{Height: 720, VideoBitrate: "3000k", MaxRate: "4000k", BufSize: "6000k"},
	{Height: 1080, VideoBitrate: "6000k", MaxRate: "7500k", BufSize: "12000k"},
}

// computeVariants returns the quality variants to produce for the given source
// resolution. Returns nil when only one variant qualifies (single-variant mode).
func computeVariants(sourceHeight int) []qualityVariant {
	var variants []qualityVariant
	for _, preset := range qualityPresets {
		if sourceHeight >= preset.Height {
			variants = append(variants, preset)
		}
	}
	if len(variants) <= 1 {
		return nil
	}
	// Highest variant uses CRF at source resolution (no bitrate cap).
	variants[len(variants)-1].VideoBitrate = ""
	variants[len(variants)-1].MaxRate = ""
	variants[len(variants)-1].BufSize = ""
	return variants
}

func playlistHasEndList(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "#EXT-X-ENDLIST")
}
