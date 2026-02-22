package apihttp

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"torrentstream/internal/domain"
)

// HLSConfig holds configuration for the HLS streaming subsystem.
type HLSConfig struct {
	FFMPEGPath      string
	FFProbePath     string
	BaseDir         string
	DataDir         string
	Preset          string
	CRF             int
	AudioBitrate    string
	SegmentDuration int
	RAMBufSizeMB    int // RAMBuffer size (default 16)
	PrebufferMB     int // prebuffer before FFmpeg start (default 4)
	WindowBeforeMB  int // priority window behind playback (default 8)
	WindowAfterMB   int // priority window ahead of playback (default 32)
}

// hlsKey uniquely identifies a streaming job.
type hlsKey struct {
	id            domain.TorrentID
	fileIndex     int
	audioTrack    int
	subtitleTrack int
}

// codecCacheEntry caches H.264/AAC detection results for a file path.
type codecCacheEntry struct {
	isH264     bool
	isAAC      bool
	lastAccess time.Time // LRU tracking for eviction
}

// resolutionCacheEntry caches video resolution, duration, and FPS.
type resolutionCacheEntry struct {
	width    int
	height   int
	duration float64 // seconds; 0 if unknown
	fps      float64 // frames per second; 0 if unknown
}

// persistedCodecEntry is the JSON-serializable form of a codec cache entry.
type persistedCodecEntry struct {
	IsH264   bool    `json:"h264"`
	IsAAC    bool    `json:"aac"`
	Width    int     `json:"w,omitempty"`
	Height   int     `json:"h,omitempty"`
	Duration float64 `json:"dur,omitempty"`
	FPS      float64 `json:"fps,omitempty"`
}

const maxCodecCacheEntries = 2000

var errSubtitleSourceUnavailable = errors.New("subtitle source file not ready")

// hlsHealthSnapshot holds health/stats information for the streaming subsystem.
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

// computeProfileHash creates a short hash of encoding settings for directory naming.
func computeProfileHash(preset string, crf int, audioBitrate string, segDur int) string {
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%s:%d:%s:%d", preset, crf, audioBitrate, segDur)
	return fmt.Sprintf("%08x", h.Sum32())
}

// findLastSegment returns the most recently modified .ts file in dir
// (including variant subdirectories for multi-variant jobs).
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

// ---- Codec/resolution detection (ffprobe) -----------------------------------

const (
	ffprobeRetryAttempts = 3
	ffprobeRetryDelay    = 2 * time.Second
)

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

func getVideoFPS(ffprobePath, filePath string) float64 {
	out, err := exec.Command(
		ffprobePath,
		"-v", "quiet",
		"-select_streams", "v:0",
		"-show_entries", "stream=r_frame_rate",
		"-of", "csv=p=0",
		filePath,
	).Output()
	if err != nil {
		return 0
	}
	line := strings.TrimSpace(string(out))
	parts := strings.SplitN(line, "/", 2)
	if len(parts) != 2 {
		return 0
	}
	num, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	den, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil || den == 0 {
		return 0
	}
	fps := num / den
	if fps <= 0 || fps > 120 {
		return 0
	}
	return fps
}

// ---- Quality variants -------------------------------------------------------

// qualityVariant describes a single quality level for multi-variant HLS output.
type qualityVariant struct {
	Height       int
	VideoBitrate string // e.g. "1500k"; empty means use CRF (highest quality variant)
	MaxRate      string // e.g. "2000k"
	BufSize      string // e.g. "3000k"
}

var qualityPresets = []qualityVariant{
	{Height: 480, VideoBitrate: "1500k", MaxRate: "2000k", BufSize: "3000k"},
	{Height: 720, VideoBitrate: "3000k", MaxRate: "4000k", BufSize: "6000k"},
	{Height: 1080, VideoBitrate: "6000k", MaxRate: "7500k", BufSize: "12000k"},
}

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
	variants[len(variants)-1].VideoBitrate = ""
	return variants
}

// ---- Playlist / segment utilities -------------------------------------------

func playlistHasEndList(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "#EXT-X-ENDLIST")
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

// subtitleFilterArg builds the FFmpeg -vf subtitles= filter argument.
func subtitleFilterArg(sourcePath string, subtitleTrack int) string {
	path := strings.ReplaceAll(sourcePath, `\`, `/`)
	path = strings.ReplaceAll(path, `'`, `\'`)
	path = strings.ReplaceAll(path, ":", `\:`)
	return fmt.Sprintf("subtitles='%s':si=%d", path, subtitleTrack)
}

// buildMultiVariantFilterComplex constructs an FFmpeg filter_complex string
// that splits the input video into multiple quality variants.
func buildMultiVariantFilterComplex(variants []qualityVariant, _ string, _ int) string {
	n := len(variants)
	var b strings.Builder

	b.WriteString("[0:v:0]")
	b.WriteString(fmt.Sprintf("split=%d", n))

	for i := 0; i < n; i++ {
		b.WriteString(fmt.Sprintf("[v%d]", i))
	}

	for i := 0; i < n; i++ {
		b.WriteString("; ")
		if i < n-1 {
			b.WriteString(fmt.Sprintf("[v%d]scale=-2:%d[out%d]", i, variants[i].Height, i))
		} else {
			b.WriteString(fmt.Sprintf("[v%d]null[out%d]", i, i))
		}
	}

	return b.String()
}

// m3u8Segment represents a single segment entry in an M3U8 playlist.
type m3u8Segment struct {
	Filename string
	Duration float64 // seconds
}

// parseM3U8Segments parses a playlist file and returns segment filenames with their durations.
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

// ---- Remux types ------------------------------------------------------------

// remuxEntry tracks a background FFmpeg remux (MKV → MP4 codec copy).
type remuxEntry struct {
	path    string             // absolute path to the output .mp4
	ready   chan struct{}      // closed when remux is complete (check err)
	err     error
	started time.Time
	cancel  context.CancelFunc // cancels the FFmpeg process
}

// remuxCacheKey returns a unique key for the remux cache.
func remuxCacheKey(id domain.TorrentID, fileIndex int) string {
	return string(id) + "/" + strconv.Itoa(fileIndex)
}

// ---- Seek types -------------------------------------------------------------

// estimatedRestartCostSec is the approximate time (in seconds) for a hard seek:
// FFmpeg kill + torrent data seek + ffprobe + first segment encode.
const estimatedRestartCostSec = 12.0

// SeekMode describes how a seek request should be handled.
type SeekMode int

const (
	SeekModeSoft SeekMode = iota // Same job, let HLS.js seek within existing segments
	SeekModeHard                 // New FFmpeg job from new byte offset
)

var seekModeNames = [...]string{"soft", "hard"}

func (m SeekMode) String() string {
	if int(m) < len(seekModeNames) {
		return seekModeNames[m]
	}
	return "unknown"
}

// estimateByteOffset estimates the byte position for a given time offset
// using file length and duration. Returns -1 if estimation is not possible.
func estimateByteOffset(targetSec, durationSec float64, fileLength int64) int64 {
	if durationSec <= 0 || fileLength <= 0 || targetSec <= 0 {
		return -1
	}
	ratio := targetSec / durationSec
	if ratio > 1.0 {
		ratio = 1.0
	}
	return int64(ratio * float64(fileLength))
}
