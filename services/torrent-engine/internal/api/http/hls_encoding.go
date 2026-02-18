package apihttp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"torrentstream/internal/metrics"
)

func (m *hlsManager) run(job *hlsJob, key hlsKey) {
	m.logger.Info("hls job starting",
		slog.String("torrentId", string(key.id)),
		slog.Int("fileIndex", key.fileIndex),
		slog.Int("audioTrack", key.audioTrack),
		slog.Int("subtitleTrack", key.subtitleTrack),
	)

	// Log state transitions for observability.
	job.ctrl.OnTransition(func(from, to PlaybackState) {
		m.logger.Info("hls state transition",
			slog.String("torrentId", string(key.id)),
			slog.Int("fileIndex", key.fileIndex),
			slog.String("from", from.String()),
			slog.String("to", to.String()),
			slog.Uint64("generation", job.ctrl.Generation()),
		)
	})

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
		_ = job.ctrl.TransitionWithError(err)
		m.recordJobFailure(job, err)
		job.signalReady()
		m.cleanupJob(key, job)
		return
	}
	if result.Reader == nil {
		job.err = errors.New("stream reader not available")
		_ = job.ctrl.TransitionWithError(job.err)
		m.recordJobFailure(job, job.err)
		job.signalReady()
		m.cleanupJob(key, job)
		return
	}

	// Store generation reference for stale reader detection.
	job.genRef.Store(job.ctrl.Generation())
	// Store consumption rate callback for adaptive download rate control.
	job.consumptionRate = result.ConsumptionRate
	// HLS uses responsive mode: the torrent reader returns EOF immediately
	// when piece data isn't available instead of blocking indefinitely.
	// The bufferedStreamReader retries transient EOFs with exponential
	// backoff, so FFmpeg gets data as soon as pieces are downloaded.
	result.Reader.SetResponsive()

	// On initial play (not a seek), preload the file tail in background.
	// Container formats store seek indices at the end of the file.
	if job.seekSeconds == 0 {
		go m.preloadFileEnds(key, result.File)
	}

	// Build the data source abstraction (replaces inline if/else logic).
	dataSource, subtitleSourcePath := m.newDataSource(result, job, key)
	defer dataSource.Close()

	// Eagerly populate codec/resolution cache for file-backed sources
	// (directFileSource and partialDirectSource). On seek jobs the cache
	// is already warm, making subsequent ffprobe calls instant.
	if filePath := dataSourceFilePath(dataSource); filePath != "" {
		m.isH264FileWithCache(filePath)
		m.isAACAudioWithCache(filePath)
		m.getVideoResolutionWithCache(filePath)
	}

	input, pipeReader := dataSource.InputSpec()
	useReader := pipeReader != nil

	// Wire buffered reader for rate limiting (only available for pipe sources).
	if ps, ok := dataSource.(*pipeSource); ok {
		job.bufferedReader = ps.BufferedReader()
	}

	if key.subtitleTrack >= 0 && subtitleSourcePath == "" {
		job.err = errSubtitleSourceUnavailable
		_ = job.ctrl.TransitionWithError(job.err)
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

	// For pipe sources (incomplete torrent), use a smaller probe window
	// so FFmpeg starts encoding sooner instead of waiting for 10 MB of
	// sequential data through slow piece-by-piece delivery.
	// Use FFmpeg's default probesize (5 MB) to avoid codec detection failures
	// that occur when format metadata is spread across the first few MB.
	analyzeDuration := "20000000" // 20s — generous for seekable file/HTTP inputs
	probeSize := "10000000"       // 10 MB
	if useReader {
		analyzeDuration = "5000000" // 5s — FFmpeg default; sufficient for stream detection
		probeSize = "5000000"       // 5 MB — FFmpeg default; safe for all container formats
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		// -progress must be a global option — place it before any input/output
		// so FFmpeg cannot misinterpret pipe:1 as a second output file.
		"-progress", "pipe:1",
		"-fflags", "+genpts+discardcorrupt",
		"-err_detect", "ignore_err",
		"-analyzeduration", analyzeDuration,
		"-probesize", probeSize,
		"-avoid_negative_ts", "make_zero",
	}

	// Place -ss before -i for fast input-seeking on all source types.
	// For HTTP sources, FFmpeg uses HTTP range requests for container-level
	// seeking. For pipe sources (seekSeconds=0 initial play), no -ss is added.
	if job.seekSeconds > 0 {
		args = append(args, "-ss", strconv.FormatFloat(job.seekSeconds, 'f', 3, 64))
	}

	// HTTP input: reconnect on dropped connection during streaming.
	// Do NOT add -reconnect_at_eof: for partially-downloaded files the HTTP
	// server closes the connection at the download boundary (not at the declared
	// Content-Length). With -reconnect_at_eof FFmpeg would restart from byte 0,
	// ignoring the already-consumed -ss, and produce segments from the beginning
	// of the file instead of the seek position — causing silent/frozen video.
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		args = append(args, "-reconnect", "1", "-reconnect_streamed", "1")
	}

	args = append(args, "-i", input)

	// Snapshot encoding settings under shared lock so the job uses a consistent set.
	m.mu.RLock()
	encPreset := m.preset
	encCRF := m.crf
	encAudioBitrate := m.audioBitrate
	segDur := m.segmentDuration
	m.mu.RUnlock()
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
				// Highest variant: CRF for best quality, with maxrate cap to prevent
				// runaway output on high-bitrate HEVC sources.
				args = append(args, fmt.Sprintf("-crf:v:%d", i), strconv.Itoa(encCRF))
				if v.MaxRate != "" {
					args = append(args,
						fmt.Sprintf("-maxrate:v:%d", i), v.MaxRate,
						fmt.Sprintf("-bufsize:v:%d", i), v.BufSize,
					)
				}
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
	if pipeReader != nil {
		cmd.Stdin = pipeReader
	}
	// Capture FFmpeg progress output via stdout pipe.
	progressR, progressW, progressPipeErr := os.Pipe()
	if progressPipeErr != nil {
		cmd.Stdout = io.Discard
	} else {
		cmd.Stdout = progressW
	}
	cmd.Stderr = &stderr

	ffmpegStart := time.Now()
	if err := cmd.Start(); err != nil {
		if progressR != nil {
			progressR.Close()
		}
		if progressW != nil {
			progressW.Close()
		}
		// buffered reader (if any) is closed by defer above
		m.logger.Error("hls ffmpeg start failed", slog.String("error", err.Error()))
		job.err = err
		_ = job.ctrl.TransitionWithError(err)
		m.recordJobFailure(job, err)
		job.signalReady()
		m.cleanupJob(key, job)
		return
	}
	// Close write end of progress pipe in parent; start parsing goroutine.
	if progressW != nil {
		progressW.Close()
	}
	if progressR != nil {
		go parseFFmpegProgress(progressR, job)
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
				m.logger.Info("hls playlist ready",
					slog.String("dir", job.dir),
					slog.String("state", job.ctrl.State().String()),
				)
				// Transition: Starting → Buffering → Playing
				_ = job.ctrl.Transition(StateBuffering)
				_ = job.ctrl.Transition(StatePlaying)
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
					_ = job.ctrl.TransitionWithError(job.err)
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

	waitErr := cmd.Wait()
	metrics.HLSEncodeDuration.Observe(time.Since(ffmpegStart).Seconds())

	if waitErr != nil && job.err == nil {
		stderrMsg := strings.TrimSpace(stderr.String())
		// Expected path for seek/track switch cancellation.
		if ctx.Err() != nil {
			m.logger.Info("hls ffmpeg exited after context cancellation",
				slog.String("dir", job.dir),
				slog.String("error", waitErr.Error()),
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
					slog.String("error", waitErr.Error()),
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
				_ = job.ctrl.TransitionWithError(job.err)
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
				job.err = fmt.Errorf("ffmpeg: %w: %s", waitErr, stderrMsg)
			} else {
				job.err = fmt.Errorf("ffmpeg: %w", waitErr)
			}
			_ = job.ctrl.TransitionWithError(job.err)
			m.logger.Error("hls ffmpeg exited with error",
				slog.String("dir", job.dir),
				slog.String("error", waitErr.Error()),
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
		_ = job.ctrl.TransitionWithError(job.err)
		m.recordJobFailure(job, job.err)
		job.signalReady()
		m.cleanupJob(key, job)
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
	// Check cache first (update LRU timestamp on hit).
	m.codecCacheMu.Lock()
	if entry, ok := m.codecCache[filePath]; ok {
		entry.lastAccess = time.Now()
		m.codecCacheMu.Unlock()
		return entry.isH264
	}
	m.codecCacheMu.Unlock()

	// Not in cache, perform detection with retry
	result := isH264FileWithRetry(m.ffprobePath, filePath, m.logger)

	// Store in cache
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

// isAACAudioWithCache checks if a file has AAC audio, using cache to avoid
// repeated ffprobe calls.
func (m *hlsManager) isAACAudioWithCache(filePath string) bool {
	// Check cache first (update LRU timestamp on hit).
	m.codecCacheMu.Lock()
	if entry, ok := m.codecCache[filePath]; ok {
		entry.lastAccess = time.Now()
		m.codecCacheMu.Unlock()
		return entry.isAAC
	}
	m.codecCacheMu.Unlock()

	// Not in cache, perform detection with retry
	result := isAACAudioWithRetry(m.ffprobePath, filePath, m.logger)

	// Store in cache
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

// getVideoDuration returns the duration in seconds of the media file.
func getVideoDuration(ffprobePath, filePath string) float64 {
	out, err := exec.Command(
		ffprobePath,
		"-v", "quiet",
		"-show_entries", "format=duration",
		"-of", "csv=p=0",
		filePath,
	).Output()
	if err != nil {
		return 0
	}
	line := strings.TrimSpace(string(out))
	dur, err := strconv.ParseFloat(line, 64)
	if err != nil || dur <= 0 {
		return 0
	}
	return dur
}

// getVideoResolutionWithDuration returns width, height and duration, caching all values.
func (m *hlsManager) getVideoResolutionWithDuration(filePath string) (int, int, float64) {
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
	// Highest variant uses CRF as quality target. MaxRate/BufSize are kept as
	// a ceiling to prevent runaway output size on high-bitrate HEVC sources.
	variants[len(variants)-1].VideoBitrate = ""
	return variants
}

func playlistHasEndList(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "#EXT-X-ENDLIST")
}

// parseFFmpegProgress reads FFmpeg -progress output from r and stores the
// latest out_time_us value in job.ffmpegProgressUs (atomic). The goroutine
// exits when r is closed (FFmpeg process exits).
func parseFFmpegProgress(r *os.File, job *hlsJob) {
	defer r.Close()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "out_time_us=") {
			if us, err := strconv.ParseInt(strings.TrimPrefix(line, "out_time_us="), 10, 64); err == nil {
				atomic.StoreInt64(&job.ffmpegProgressUs, us)
			}
		}
	}
}
