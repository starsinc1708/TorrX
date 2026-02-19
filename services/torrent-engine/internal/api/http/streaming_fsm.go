package apihttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"torrentstream/internal/metrics"
	"torrentstream/internal/usecase"
)

// StreamState represents the FSM state of a StreamJob.
type StreamState int

const (
	StreamIdle      StreamState = iota
	StreamLoading               // Prebuffering torrent data
	StreamReady                 // About to start FFmpeg
	StreamPlaying               // FFmpeg is producing segments
	StreamBuffering             // Stall detected, waiting for data
	StreamSeeking               // User-initiated seek in progress
	StreamCompleted             // FFmpeg finished encoding
	StreamError                 // Terminal error
)

var streamStateNames = [...]string{
	"idle", "loading", "ready", "playing",
	"buffering", "seeking", "completed", "error",
}

func (s StreamState) String() string {
	if int(s) < len(streamStateNames) {
		return streamStateNames[s]
	}
	return fmt.Sprintf("unknown(%d)", int(s))
}

// WindowConfig holds playback window parameters.
type WindowConfig struct {
	RAMBufSize   int64 // RAMBuffer size for pipe source (default 16 MB)
	PreloadBytes int64 // initial buffer before FFmpeg start (default 4 MB)
	BeforeBytes  int64 // keep behind playback (default 8 MB)
	AfterBytes   int64 // readahead (default 32 MB)
}

// DefaultWindowConfig returns sensible default window parameters.
func DefaultWindowConfig() WindowConfig {
	return WindowConfig{
		RAMBufSize:   16 << 20,
		PreloadBytes: 4 << 20,
		BeforeBytes:  8 << 20,
		AfterBytes:   32 << 20,
	}
}

// StreamJob represents one playback instance with an FSM loop.
// It replaces hlsJob + PlaybackController + the run()/watchJobProgress() flow.
type StreamJob struct {
	mu    sync.Mutex
	state StreamState

	ctx    context.Context
	cancel context.CancelFunc

	key      hlsKey
	dir      string // working directory for HLS segments
	playlist string // path to current index.m3u8

	ramBuf   *RAMBuffer
	priority *PriorityManager
	ffmpeg   *FFmpegProcess

	ready     chan struct{} // closed when first segment available
	readyOnce sync.Once
	err       error

	seekSeconds float64 // current seek offset
	seekMu      sync.Mutex
	seekReq     bool    // true when a seek has been requested
	seekTarget  float64 // target time for pending seek

	multiVariant bool
	variants     []qualityVariant

	// Data source (kept alive across the run loop, closed on cleanup).
	dataSource     MediaDataSource
	subtitlePath   string
	streamResult   *usecase.StreamResult
	isPipeSource   bool

	// Monitoring
	lastSegPath      string
	lastSegSize      int64
	lastSegChangedAt time.Time
	stallDuration    time.Duration

	// Config (snapshot from manager at creation time)
	windowCfg WindowConfig

	// Back-reference to manager for accessing caches, paths, and use case.
	mgr *StreamJobManager

	// Cached rewritten playlist.
	rewrittenMu           sync.RWMutex
	rewrittenPlaylist     []byte
	rewrittenPlaylistPath string
	rewrittenPlaylistMod  time.Time
	rewrittenAudioTrack   int
	rewrittenSubTrack     int
	rewrittenCacheTime    time.Time

}

// streamPipeSource wraps a RAMBuffer as a MediaDataSource for FFmpeg.
type streamPipeSource struct {
	buf *RAMBuffer
}

func (s *streamPipeSource) InputSpec() (string, io.ReadCloser) { return "pipe:0", s.buf }
func (s *streamPipeSource) SupportsSeek() bool                 { return false }
func (s *streamPipeSource) SeekTo(int64) error                 { return nil }
func (s *streamPipeSource) Close() error                       { return s.buf.Close() }

var _ MediaDataSource = (*streamPipeSource)(nil)

func newStreamJob(mgr *StreamJobManager, key hlsKey, dir string, seekSeconds float64) *StreamJob {
	ctx, cancel := context.WithCancel(context.Background())
	return &StreamJob{
		state:       StreamIdle,
		ctx:         ctx,
		cancel:      cancel,
		key:         key,
		dir:         dir,
		playlist:    filepath.Join(dir, "index.m3u8"),
		ready:       make(chan struct{}),
		seekSeconds: seekSeconds,
		windowCfg:   mgr.currentWindowConfig(),
		mgr:         mgr,
	}
}

func (j *StreamJob) signalReady() {
	j.readyOnce.Do(func() {
		close(j.ready)
	})
}

// StartPlayback transitions from Idle to Loading and starts the FSM loop.
func (j *StreamJob) StartPlayback() {
	j.mu.Lock()
	if j.state != StreamIdle {
		j.mu.Unlock()
		return
	}
	j.state = StreamLoading
	j.mu.Unlock()
	go j.run()
}

// Seek requests a seek to the given time. The FSM loop detects and processes it.
func (j *StreamJob) Seek(seekSec float64) {
	j.seekMu.Lock()
	j.seekReq = true
	j.seekTarget = seekSec
	j.seekMu.Unlock()
}

// Stop cancels the entire job.
func (j *StreamJob) Stop() {
	j.cancel()
}

func (j *StreamJob) transitionTo(s StreamState) {
	j.mu.Lock()
	from := j.state
	j.state = s
	j.mu.Unlock()
	metrics.HLSStateTransitionsTotal.WithLabelValues(from.String(), s.String()).Inc()
	j.mgr.logger.Info("stream state transition",
		slog.String("torrentId", string(j.key.id)),
		slog.Int("fileIndex", j.key.fileIndex),
		slog.String("from", from.String()),
		slog.String("to", s.String()),
	)
}

func (j *StreamJob) setError(err error) {
	j.mu.Lock()
	j.err = err
	from := j.state
	j.state = StreamError
	j.mu.Unlock()
	metrics.HLSStateTransitionsTotal.WithLabelValues(from.String(), StreamError.String()).Inc()
	j.mgr.logger.Error("stream job error",
		slog.String("torrentId", string(j.key.id)),
		slog.String("state", from.String()),
		slog.String("error", err.Error()),
	)
	j.signalReady()
}

func (j *StreamJob) currentState() StreamState {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state
}

// checkSeekRequested atomically checks and clears the seek request flag.
func (j *StreamJob) checkSeekRequested() (float64, bool) {
	j.seekMu.Lock()
	defer j.seekMu.Unlock()
	if !j.seekReq {
		return 0, false
	}
	target := j.seekTarget
	j.seekReq = false
	return target, true
}

// ---- FSM loop ----

func (j *StreamJob) run() {
	defer j.cleanup()

	for {
		if j.ctx.Err() != nil {
			return
		}

		switch j.currentState() {
		case StreamLoading:
			if err := j.doLoading(); err != nil {
				j.setError(err)
				return
			}
		case StreamReady:
			if err := j.doReady(); err != nil {
				j.setError(err)
				return
			}
		case StreamPlaying:
			j.doPlaying()
			// doPlaying returns when state changes (stall, seek, complete, error)
		case StreamBuffering:
			if err := j.doBuffering(); err != nil {
				j.setError(err)
				return
			}
		case StreamSeeking:
			if err := j.doSeeking(); err != nil {
				j.setError(err)
				return
			}
		case StreamCompleted, StreamError, StreamIdle:
			return
		}
	}
}

// doLoading gets the torrent reader, creates data source, prebuffers.
func (j *StreamJob) doLoading() error {
	j.mgr.logger.Info("stream loading",
		slog.String("torrentId", string(j.key.id)),
		slog.Int("fileIndex", j.key.fileIndex),
		slog.Float64("seekSeconds", j.seekSeconds),
	)

	// Get torrent reader via use case (raw = no slidingPriorityReader wrapping;
	// PriorityManager handles download priorities instead).
	result, err := j.mgr.stream.ExecuteRaw(j.ctx, j.key.id, j.key.fileIndex)
	if err != nil {
		return fmt.Errorf("stream execute: %w", err)
	}
	if result.Reader == nil {
		return errors.New("stream reader not available")
	}
	result.Reader.SetResponsive()
	j.streamResult = &result

	// On initial play, preload file tail for container seek indices.
	if j.seekSeconds == 0 {
		j.mgr.preloadFileEnds(j.key, result.File)
	}

	// Determine data source.
	ds, subPath := j.mgr.newStreamDataSource(result, j)
	j.dataSource = ds
	j.subtitlePath = subPath

	// Eagerly populate codec/resolution cache for file-backed sources.
	if filePath := dataSourceFilePath(ds); filePath != "" {
		j.mgr.isH264FileWithCache(filePath)
		j.mgr.isAACAudioWithCache(filePath)
		j.mgr.getVideoResolutionWithCache(filePath)
		j.mgr.getVideoFPSWithCache(filePath)
	}

	// For pipe sources: create RAMBuffer and prebuffer.
	if _, ok := ds.(*streamPipeSource); ok {
		j.isPipeSource = true
		prebufBytes := j.windowCfg.PreloadBytes
		if prebufBytes <= 0 {
			prebufBytes = 4 << 20
		}
		const prebufTimeout = 15 * time.Second
		if err := j.ramBuf.Prebuffer(j.ctx, prebufBytes, prebufTimeout); err != nil {
			return fmt.Errorf("prebuffer: %w", err)
		}
		j.mgr.logger.Info("stream prebuffer complete",
			slog.Int64("buffered", j.ramBuf.Buffered()))
	}

	// Set up PriorityManager for incomplete files.
	if j.mgr.engine != nil && j.isPipeSource {
		j.priority = NewPriorityManager(j.mgr.engine, j.key.id, result.File, j.mgr.logger)
		windowStart := int64(0)
		if j.seekSeconds > 0 && result.File.Length > 0 {
			// Estimate byte offset for the seek target.
			filePath := dataSourceFilePath(ds)
			if filePath == "" && j.mgr.dataDir != "" {
				if p, err := resolveDataFilePath(j.mgr.dataDir, result.File.Path); err == nil {
					filePath = p
				}
			}
			if filePath != "" {
				_, _, dur := j.mgr.getVideoResolutionWithDuration(filePath)
				if dur > 0 {
					windowStart = estimateByteOffset(j.seekSeconds, dur, result.File.Length)
					if windowStart < 0 {
						windowStart = 0
					}
				}
			}
		}
		windowEnd := windowStart + j.windowCfg.AfterBytes
		j.priority.Apply(j.ctx, windowStart, windowEnd)
	}

	// Subtitle check.
	if j.key.subtitleTrack >= 0 && j.subtitlePath == "" {
		return errSubtitleSourceUnavailable
	}

	j.transitionTo(StreamReady)
	return nil
}

// doReady builds FFmpeg args, starts FFmpeg, waits for first segment.
func (j *StreamJob) doReady() error {
	input, pipeReader := j.dataSource.InputSpec()
	useReader := pipeReader != nil
	isLocalFile := !useReader &&
		!strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://")

	// Snapshot encoding settings.
	j.mgr.mu.RLock()
	preset := j.mgr.preset
	crf := j.mgr.crf
	audioBitrate := j.mgr.audioBitrate
	segDur := j.mgr.segmentDuration
	j.mgr.mu.RUnlock()
	if segDur <= 0 {
		segDur = 2
	}

	// Detect source properties.
	sourceHeight := 0
	sourceFPS := float64(0)
	if isLocalFile {
		_, sourceHeight = j.mgr.getVideoResolutionWithCache(input)
		sourceFPS = j.mgr.getVideoFPSWithCache(input)
	}

	streamCopy := false
	if isLocalFile && j.key.subtitleTrack < 0 && j.mgr.isH264FileWithCache(input) {
		streamCopy = true
		if strings.ToLower(filepath.Ext(input)) == ".mkv" {
			go j.mgr.triggerRemux(j.key.id, j.key.fileIndex, input)
		}
	}

	// Reset FPS for stream copy (not needed for keyframe alignment).
	if streamCopy {
		sourceFPS = 0
	}

	multiVariant := false
	var variants []qualityVariant
	if !streamCopy && sourceHeight > 0 {
		variants = computeVariants(sourceHeight)
		if len(variants) > 0 {
			multiVariant = true
			j.multiVariant = true
			j.variants = variants
			j.playlist = filepath.Join(j.dir, "master.m3u8")
			for i := range variants {
				_ = os.MkdirAll(filepath.Join(j.dir, fmt.Sprintf("v%d", i)), 0o755)
			}
		}
	}

	isAAC := false
	if isLocalFile && streamCopy {
		isAAC = j.mgr.isAACAudioWithCache(input)
	}

	args := buildStreamingFFmpegArgs(FFmpegArgConfig{
		FFmpegPath:      j.mgr.ffmpegPath,
		Input:           input,
		OutputDir:       j.dir,
		SeekSeconds:     j.seekSeconds,
		SegmentDuration: segDur,
		Preset:          preset,
		CRF:             crf,
		AudioBitrate:    audioBitrate,
		StreamCopy:      streamCopy,
		IsAACSource:     isAAC,
		MultiVariant:    multiVariant,
		Variants:        variants,
		SubtitleTrack:   j.key.subtitleTrack,
		SubtitleFile:    j.subtitlePath,
		SourceHeight:    sourceHeight,
		SourceFPS:       sourceFPS,
		IsLocalFile:     isLocalFile,
		UseReader:       useReader,
		AudioTrack:      j.key.audioTrack,
	})

	j.mgr.logger.Info("stream ffmpeg starting",
		slog.String("input", input),
		slog.Bool("useReader", useReader),
		slog.Bool("streamCopy", streamCopy),
		slog.String("dir", j.dir),
	)

	j.ffmpeg = NewFFmpegProcess(j.ctx, j.mgr.ffmpegPath, args, j.dir, pipeReader)
	if err := j.ffmpeg.Start(); err != nil {
		return fmt.Errorf("ffmpeg start: %w", err)
	}

	// Wait for first playlist file — timeout after 120s.
	const startupTimeout = 120 * time.Second
	deadline := time.After(startupTimeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		if _, statErr := os.Stat(j.playlist); statErr == nil {
			j.mgr.logger.Info("stream playlist ready", slog.String("dir", j.dir))
			j.lastSegChangedAt = time.Now()
			metrics.HLSEncodeDuration.Observe(0) // placeholder
			j.signalReady()
			j.transitionTo(StreamPlaying)
			return nil
		}

		// Check if FFmpeg already exited.
		if j.ffmpeg.IsDone() {
			// FFmpeg exited before producing a playlist.
			if j.ctx.Err() != nil {
				return j.ctx.Err()
			}
			stderr := j.ffmpeg.Stderr()
			if stderr != "" {
				return fmt.Errorf("ffmpeg exited before first segment: %s", stderr)
			}
			return errors.New("ffmpeg exited before producing playlist")
		}

		select {
		case <-ticker.C:
			// retry
		case <-deadline:
			j.ffmpeg.Stop()
			stderr := j.ffmpeg.Stderr()
			if stderr != "" {
				return fmt.Errorf("ffmpeg timed out after %s: %s", startupTimeout, stderr)
			}
			return fmt.Errorf("ffmpeg timed out after %s waiting for first segment", startupTimeout)
		case <-j.ctx.Done():
			return j.ctx.Err()
		}
	}
}

// doPlaying monitors FFmpeg segment production, updates priorities, detects stalls.
func (j *StreamJob) doPlaying() {
	const (
		watchInterval       = 5 * time.Second
		stallThreshold      = 90 * time.Second
		pipeStallThreshold  = 5 * time.Minute
		stallEscL1          = 30 * time.Second // remove rate limit (N/A in FSM, but boost priority)
		stallEscL2          = 60 * time.Second // enhance high priority
	)

	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()

	j.lastSegChangedAt = time.Now()
	escalationLevel := 0

	for {
		select {
		case <-ticker.C:
		case <-j.ctx.Done():
			return
		}

		// Check for seek request.
		if target, ok := j.checkSeekRequested(); ok {
			j.seekSeconds = target
			j.transitionTo(StreamSeeking)
			return
		}

		// Check if FFmpeg exited.
		if j.ffmpeg.IsDone() {
			if j.ctx.Err() != nil {
				return
			}
			// Check if playlist has ENDLIST — means clean exit.
			if playlistHasEndList(j.playlist) {
				j.transitionTo(StreamCompleted)
				j.signalReady()
				return
			}
			// FFmpeg died without completing.
			stderr := j.ffmpeg.Stderr()
			if stderr != "" {
				j.setError(fmt.Errorf("ffmpeg exited: %s", stderr))
			} else {
				j.setError(errors.New("ffmpeg exited unexpectedly"))
			}
			return
		}

		// Track segment production.
		if segPath, segSize := findLastSegment(j.dir); segPath != "" {
			changed := segPath != j.lastSegPath || segSize != j.lastSegSize
			if changed {
				j.lastSegPath = segPath
				j.lastSegSize = segSize
				j.lastSegChangedAt = time.Now()
				escalationLevel = 0
			}
		}

		// Update priority window based on FFmpeg progress.
		if j.priority != nil {
			progressSec := j.seekSeconds + j.ffmpeg.Progress()
			if j.streamResult != nil && j.streamResult.File.Length > 0 {
				filePath := ""
				if j.mgr.dataDir != "" {
					if p, err := resolveDataFilePath(j.mgr.dataDir, j.streamResult.File.Path); err == nil {
						filePath = p
					}
				}
				if filePath != "" {
					_, _, dur := j.mgr.getVideoResolutionWithDuration(filePath)
					if dur > 0 {
						bytePos := estimateByteOffset(progressSec, dur, j.streamResult.File.Length)
						if bytePos >= 0 {
							windowEnd := bytePos + j.windowCfg.AfterBytes
							j.priority.Apply(j.ctx, bytePos, windowEnd)
						}
					}
				}
			}
		}

		// Stall detection.
		stallDuration := time.Since(j.lastSegChangedAt)
		j.stallDuration = stallDuration

		// Escalation L1: boost priority at 30s stall.
		if stallDuration >= stallEscL1 && escalationLevel < 1 {
			escalationLevel = 1
			if j.priority != nil && j.streamResult != nil {
				bytePos := int64(0)
				filePath := ""
				if j.mgr.dataDir != "" {
					if p, err := resolveDataFilePath(j.mgr.dataDir, j.streamResult.File.Path); err == nil {
						filePath = p
					}
				}
				if filePath != "" {
					progressSec := j.seekSeconds + j.ffmpeg.Progress()
					_, _, dur := j.mgr.getVideoResolutionWithDuration(filePath)
					if dur > 0 {
						bytePos = estimateByteOffset(progressSec, dur, j.streamResult.File.Length)
						if bytePos < 0 {
							bytePos = 0
						}
					}
				}
				j.priority.EnhanceHigh(j.ctx, bytePos)
			}
			j.mgr.logger.Warn("stream stall escalation L1: priority enhanced",
				slog.String("torrentId", string(j.key.id)),
				slog.Duration("stalled", stallDuration),
			)
		}

		// Escalation L2: transition to Buffering at 60s stall.
		if stallDuration >= stallEscL2 && escalationLevel < 2 {
			escalationLevel = 2
			j.mgr.logger.Warn("stream stall escalation L2: entering buffering",
				slog.String("torrentId", string(j.key.id)),
				slog.Duration("stalled", stallDuration),
			)
			j.transitionTo(StreamBuffering)
			return
		}

		// Hard stall threshold: error.
		threshold := stallThreshold
		if j.isPipeSource {
			threshold = pipeStallThreshold
		}
		if stallDuration >= threshold {
			j.setError(fmt.Errorf("segment production stalled for %s", stallDuration))
			return
		}
	}
}

// doBuffering waits for the buffer to refill, with enhanced priority.
func (j *StreamJob) doBuffering() error {
	const bufferTimeout = 90 * time.Second

	j.mgr.logger.Info("stream buffering",
		slog.String("torrentId", string(j.key.id)),
		slog.Duration("stalled", j.stallDuration),
	)

	// Enhance priority to accelerate data arrival.
	if j.priority != nil && j.streamResult != nil {
		bytePos := int64(0)
		filePath := ""
		if j.mgr.dataDir != "" {
			if p, err := resolveDataFilePath(j.mgr.dataDir, j.streamResult.File.Path); err == nil {
				filePath = p
			}
		}
		if filePath != "" {
			progressSec := j.seekSeconds + j.ffmpeg.Progress()
			_, _, dur := j.mgr.getVideoResolutionWithDuration(filePath)
			if dur > 0 {
				bytePos = estimateByteOffset(progressSec, dur, j.streamResult.File.Length)
				if bytePos < 0 {
					bytePos = 0
				}
			}
		}
		j.priority.EnhanceHigh(j.ctx, bytePos)
	}

	// Wait for new segments to appear.
	deadline := time.After(bufferTimeout)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Check for seek.
			if target, ok := j.checkSeekRequested(); ok {
				j.seekSeconds = target
				j.transitionTo(StreamSeeking)
				return nil
			}

			// Check if a new segment appeared.
			if segPath, segSize := findLastSegment(j.dir); segPath != "" {
				if segPath != j.lastSegPath || segSize != j.lastSegSize {
					j.lastSegPath = segPath
					j.lastSegSize = segSize
					j.lastSegChangedAt = time.Now()
					j.mgr.logger.Info("stream buffering resolved",
						slog.String("torrentId", string(j.key.id)))
					j.transitionTo(StreamPlaying)
					return nil
				}
			}

			// Check if FFmpeg exited.
			if j.ffmpeg.IsDone() {
				if playlistHasEndList(j.playlist) {
					j.transitionTo(StreamCompleted)
					j.signalReady()
					return nil
				}
				return fmt.Errorf("ffmpeg exited during buffering: %s", j.ffmpeg.Stderr())
			}

		case <-deadline:
			return fmt.Errorf("buffering timeout after %s", bufferTimeout)

		case <-j.ctx.Done():
			return j.ctx.Err()
		}
	}
}

// doSeeking stops FFmpeg, cleans up, and restarts from the new position.
func (j *StreamJob) doSeeking() error {
	j.mgr.logger.Info("stream seeking",
		slog.String("torrentId", string(j.key.id)),
		slog.Float64("seekTarget", j.seekSeconds),
	)

	// Stop FFmpeg.
	if j.ffmpeg != nil {
		j.ffmpeg.Stop()
		<-j.ffmpeg.Done()
		j.ffmpeg = nil
	}

	// Close data source (and RAMBuffer if pipe).
	if j.dataSource != nil {
		_ = j.dataSource.Close()
		j.dataSource = nil
		j.ramBuf = nil
	}

	// Clean job directory: create a new one to avoid races.
	newDir := j.mgr.buildJobDir(j.key) + fmt.Sprintf("-seek-%d", time.Now().UnixNano())
	oldDir := j.dir

	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return fmt.Errorf("seek mkdir: %w", err)
	}

	j.dir = newDir
	j.playlist = filepath.Join(newDir, "index.m3u8")
	j.multiVariant = false
	j.variants = nil
	j.lastSegPath = ""
	j.lastSegSize = 0
	j.isPipeSource = false

	// Clear cached playlist.
	j.rewrittenMu.Lock()
	j.rewrittenPlaylist = nil
	j.rewrittenMu.Unlock()

	// Create a new ready channel.
	j.readyOnce = sync.Once{}
	j.ready = make(chan struct{})

	// Clean old directory asynchronously.
	go func() {
		time.Sleep(5 * time.Second) // allow old segments to drain
		_ = os.RemoveAll(oldDir)
	}()

	j.transitionTo(StreamLoading)
	return nil
}

func (j *StreamJob) cleanup() {
	if j.ffmpeg != nil {
		j.ffmpeg.Stop()
		<-j.ffmpeg.Done()
	}
	if j.dataSource != nil {
		_ = j.dataSource.Close()
	}
	if j.priority != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		j.priority.Deprioritize(ctx)
		cancel()
	}
	j.signalReady()
}

// IsRunning returns true if the job is in an active encoding state.
func (j *StreamJob) IsRunning() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state == StreamLoading || j.state == StreamReady ||
		j.state == StreamPlaying || j.state == StreamBuffering
}

// IsCompleted returns true if the job is in a terminal completed state.
func (j *StreamJob) IsCompleted() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state == StreamCompleted
}

