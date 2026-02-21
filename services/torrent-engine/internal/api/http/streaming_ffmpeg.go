package apihttp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
)

// FFmpegArgConfig holds all parameters for building FFmpeg command-line arguments.
// This is a value type — pass it by value to buildStreamingFFmpegArgs().
type FFmpegArgConfig struct {
	FFmpegPath      string
	Input           string // file path, http URL, or "pipe:0"
	OutputDir       string
	SeekSeconds     float64
	SegmentDuration int
	Preset          string
	CRF             int
	AudioBitrate    string
	StreamCopy      bool
	IsAACSource     bool
	MultiVariant    bool
	Variants        []qualityVariant
	SubtitleTrack   int    // -1 means no subtitles
	SubtitleFile    string // path to subtitle source file
	SourceHeight    int
	SourceFPS       float64
	IsLocalFile     bool
	UseReader       bool // true when input is "pipe:0"
	AudioTrack      int
}

// buildStreamingFFmpegArgs constructs the FFmpeg argument list from config.
// This is a pure function with no side effects.
func buildStreamingFFmpegArgs(cfg FFmpegArgConfig) []string {
	segDur := cfg.SegmentDuration
	if segDur <= 0 {
		segDur = 2
	}
	segDurStr := strconv.Itoa(segDur)

	// Probe settings: smaller for pipe sources so FFmpeg starts sooner.
	analyzeDuration := "20000000" // 20s
	probeSize := "10000000"       // 10 MB
	if cfg.UseReader {
		analyzeDuration = "5000000" // 5s
		probeSize = "5000000"       // 5 MB
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-progress", "pipe:1",
		"-fflags", "+genpts+discardcorrupt",
		"-err_detect", "ignore_err",
		"-analyzeduration", analyzeDuration,
		"-probesize", probeSize,
		"-avoid_negative_ts", "make_zero",
	}

	if cfg.SeekSeconds > 0 {
		args = append(args, "-ss", strconv.FormatFloat(cfg.SeekSeconds, 'f', 3, 64))
	}

	if strings.HasPrefix(cfg.Input, "http://") || strings.HasPrefix(cfg.Input, "https://") {
		args = append(args, "-reconnect", "1", "-reconnect_streamed", "1")
	}

	args = append(args, "-i", cfg.Input)

	// Keyframe alignment.
	gopArgs := []string{"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segDur)}
	if cfg.SourceFPS > 0 {
		gopSize := int(math.Round(cfg.SourceFPS * float64(segDur)))
		if gopSize > 0 {
			gopArgs = []string{"-g", strconv.Itoa(gopSize), "-sc_threshold", "0"}
		}
	}

	if cfg.StreamCopy {
		args = append(args,
			"-map", "0:v:0",
			"-map", fmt.Sprintf("0:a:%d?", cfg.AudioTrack),
			"-c:v", "copy",
		)
		if cfg.IsAACSource {
			args = append(args, "-c:a", "copy")
		} else {
			args = append(args, "-c:a", "aac", "-b:a", cfg.AudioBitrate, "-ac", "2")
		}
	} else if cfg.MultiVariant && len(cfg.Variants) > 0 {
		args = append(args, "-filter_complex",
			buildMultiVariantFilterComplex(cfg.Variants, cfg.SubtitleFile, cfg.SubtitleTrack))

		for i := range cfg.Variants {
			args = append(args,
				"-map", fmt.Sprintf("[out%d]", i),
				"-map", fmt.Sprintf("0:a:%d?", cfg.AudioTrack),
			)
		}

		args = append(args,
			"-c:v", "libx264",
			"-pix_fmt", "yuv420p",
			"-preset", cfg.Preset,
		)
		args = append(args, gopArgs...)

		for i, v := range cfg.Variants {
			if v.VideoBitrate != "" {
				args = append(args,
					fmt.Sprintf("-b:v:%d", i), v.VideoBitrate,
					fmt.Sprintf("-maxrate:v:%d", i), v.MaxRate,
					fmt.Sprintf("-bufsize:v:%d", i), v.BufSize,
				)
			} else {
				args = append(args, fmt.Sprintf("-crf:v:%d", i), strconv.Itoa(cfg.CRF))
				if v.MaxRate != "" {
					args = append(args,
						fmt.Sprintf("-maxrate:v:%d", i), v.MaxRate,
						fmt.Sprintf("-bufsize:v:%d", i), v.BufSize,
					)
				}
			}
		}

		args = append(args, "-c:a", "aac", "-b:a", cfg.AudioBitrate, "-ac", "2")
	} else {
		args = append(args,
			"-map", "0:v:0",
			"-map", fmt.Sprintf("0:a:%d?", cfg.AudioTrack),
			"-c:v", "libx264",
			"-pix_fmt", "yuv420p",
			"-preset", cfg.Preset,
			"-crf", strconv.Itoa(cfg.CRF),
		)
		args = append(args, gopArgs...)
		args = append(args,
			"-c:a", "aac",
			"-b:a", cfg.AudioBitrate,
			"-ac", "2",
		)
	}

	// HLS muxer output.
	playlistType := "event"
	flags := "append_list+independent_segments"

	if cfg.MultiVariant && len(cfg.Variants) > 0 {
		streamParts := make([]string, len(cfg.Variants))
		for i := range cfg.Variants {
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

	return args
}

// FFmpegProcess wraps an exec.Cmd for FFmpeg with progress tracking.
type FFmpegProcess struct {
	cmd        *exec.Cmd
	ctx        context.Context
	cancel     context.CancelFunc
	dir        string
	progressUs int64 // atomic: FFmpeg out_time_us
	done       chan struct{}
	err        error
	stderrBuf  bytes.Buffer
}

// NewFFmpegProcess creates a new FFmpeg process but does not start it.
func NewFFmpegProcess(ctx context.Context, ffmpegPath string, args []string, dir string, stdin io.ReadCloser) *FFmpegProcess {
	ctx2, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx2, ffmpegPath, args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = stdin
	}
	return &FFmpegProcess{
		cmd:    cmd,
		ctx:    ctx2,
		cancel: cancel,
		dir:    dir,
		done:   make(chan struct{}),
	}
}

// Start starts the FFmpeg process and begins progress monitoring.
func (f *FFmpegProcess) Start() error {
	progressR, progressW, pipeErr := os.Pipe()
	if pipeErr != nil {
		f.cmd.Stdout = io.Discard
	} else {
		f.cmd.Stdout = progressW
	}
	f.cmd.Stderr = &f.stderrBuf

	if err := f.cmd.Start(); err != nil {
		if progressR != nil {
			progressR.Close()
		}
		if progressW != nil {
			progressW.Close()
		}
		return err
	}

	if progressW != nil {
		progressW.Close()
	}
	if progressR != nil {
		go f.parseProgress(progressR)
	}

	go func() {
		f.err = f.cmd.Wait()
		close(f.done)
	}()

	return nil
}

// Stop cancels the FFmpeg process context.
func (f *FFmpegProcess) Stop() {
	f.cancel()
}

// Wait blocks until the FFmpeg process exits.
func (f *FFmpegProcess) Wait() error {
	<-f.done
	return f.err
}

// Done returns a channel that is closed when the process exits.
func (f *FFmpegProcess) Done() <-chan struct{} {
	return f.done
}

// Progress returns the encoded time in seconds.
func (f *FFmpegProcess) Progress() float64 {
	us := atomic.LoadInt64(&f.progressUs)
	if us <= 0 {
		return 0
	}
	return float64(us) / 1e6
}

// ProgressUs returns the raw progress in microseconds.
func (f *FFmpegProcess) ProgressUs() int64 {
	return atomic.LoadInt64(&f.progressUs)
}

// Stderr returns the accumulated stderr output.
func (f *FFmpegProcess) Stderr() string {
	return strings.TrimSpace(f.stderrBuf.String())
}

// IsDone returns true if the process has exited.
func (f *FFmpegProcess) IsDone() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

// Err returns the exit error (nil if still running or exited cleanly).
func (f *FFmpegProcess) Err() error {
	return f.err
}

func (f *FFmpegProcess) parseProgress(r *os.File) {
	defer r.Close()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "out_time_us=") {
			if us, err := strconv.ParseInt(strings.TrimPrefix(line, "out_time_us="), 10, 64); err == nil {
				atomic.StoreInt64(&f.progressUs, us)
			}
		}
	}
}
