package apihttp

import (
	"context"
	"log/slog"
	"math"
	"sync/atomic"
	"time"

	"torrentstream/internal/domain"
)

// estimatedRestartCostSec is the approximate time (in seconds) for a hard seek:
// FFmpeg kill + torrent data seek + ffprobe + first segment encode.
const estimatedRestartCostSec = 12.0

// ffmpegEncodedTimeSec returns the absolute time (in seconds from file start)
// that FFmpeg has encoded up to, based on the job's seek offset plus the
// FFmpeg -progress out_time_us value.
func ffmpegEncodedTimeSec(job *hlsJob) float64 {
	progressUs := atomic.LoadInt64(&job.ffmpegProgressUs)
	if progressUs <= 0 {
		return job.seekSeconds
	}
	return job.seekSeconds + float64(progressUs)/1e6
}

// SeekMode describes how a seek request should be handled.
type SeekMode int

const (
	SeekModeCache   SeekMode = iota // Fully served from cache, no FFmpeg interaction
	SeekModeSoft                    // Same job, let HLS.js seek within existing segments
	SeekModeHard                    // New FFmpeg job from new byte offset
	SeekModeRestart                 // Full restart (codec change, track switch)
)

var seekModeNames = [...]string{"cache", "soft", "hard", "restart"}

func (m SeekMode) String() string {
	if int(m) < len(seekModeNames) {
		return seekModeNames[m]
	}
	return "unknown"
}

// chooseSeekMode decides how to handle a seek request based on the distance
// from the current position and cached segment availability.
// Caller must NOT hold m.mu — or must pass segmentDuration directly via
// chooseSeekModeLocked if holding the lock.
func (m *hlsManager) chooseSeekMode(key hlsKey, job *hlsJob, targetSec float64) SeekMode {
	return m.chooseSeekModeLocked(key, job, targetSec, m.segmentDuration)
}

// chooseSeekModeLocked is the lock-safe variant that accepts segmentDuration
// directly (caller already holds m.mu).
func (m *hlsManager) chooseSeekModeLocked(key hlsKey, job *hlsJob, targetSec float64, segDurInt int) SeekMode {
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

	// 1. Cache seek: check if cached segments cover the target position (any distance).
	if m.cache != nil {
		variant := ""
		if job.multiVariant {
			variant = "v0" // check primary variant
		}
		cached := m.cache.LookupRange(
			string(key.id), key.fileIndex,
			key.audioTrack, key.subtitleTrack,
			m.cacheVariantLocked(variant), targetSec,
		)
		if len(cached) > 0 {
			coverageEnd := cached[len(cached)-1].EndTime
			if coverageEnd-targetSec >= 2*segDur {
				m.logger.Debug("hls seek: cache (full coverage)",
					slog.Float64("target", targetSec),
					slog.Float64("cacheEnd", coverageEnd),
					slog.Int("cachedSegments", len(cached)),
				)
				return SeekModeCache
			}
		}
	}

	// 2. Minimum soft band: target within 2×segDur of job.seekSeconds.
	//    Covers small backward seeks where HLS.js still has segments buffered.
	if absDistance < 2*segDur {
		m.logger.Debug("hls seek: soft (minimum band)",
			slog.Float64("distance", distance),
			slog.Float64("threshold", 2*segDur),
		)
		return SeekModeSoft
	}

	// 3. Progress-aware forward soft: if FFmpeg has already encoded past
	//    the target, or the gap is small enough that waiting beats restarting.
	if distance > 0 {
		encoded := ffmpegEncodedTimeSec(job)
		gap := targetSec - encoded
		if gap < estimatedRestartCostSec {
			m.logger.Debug("hls seek: soft (progress-aware forward)",
				slog.Float64("target", targetSec),
				slog.Float64("encoded", encoded),
				slog.Float64("gap", gap),
			)
			return SeekModeSoft
		}
	}

	// 4. Hard seek: kill FFmpeg, restart at new position.
	m.logger.Debug("hls seek: hard",
		slog.Float64("target", targetSec),
		slog.Float64("distance", distance),
	)
	return SeekModeHard
}

// preSeekPriorityBoost boosts piece priority at the estimated byte region
// for the seek target so torrent data is available when FFmpeg starts probing.
func (m *hlsManager) preSeekPriorityBoost(key hlsKey, seekSeconds float64) {
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

	// Get cached duration via resolved file path.
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

	const boostWindow = 16 << 20 // 16 MB
	start := estByte - boostWindow/2
	if start < 0 {
		start = 0
	}
	_ = m.engine.SetPiecePriority(ctx, key.id, file,
		domain.Range{Off: start, Length: boostWindow}, domain.PriorityHigh)

	m.logger.Debug("pre-seek priority boost",
		slog.String("torrentId", string(key.id)),
		slog.Float64("seekSeconds", seekSeconds),
		slog.Int64("estByte", estByte),
		slog.Int64("start", start),
	)
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
