package apihttp

import (
	"log/slog"
	"math"
)

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

	// 2. Soft seek: medium distance that HLS.js can handle within existing segments.
	if absDistance < 20.0 {
		m.logger.Debug("hls seek: soft (small distance)",
			slog.Float64("distance", distance),
			slog.Float64("threshold", 20.0),
		)
		return SeekModeSoft
	}

	// 3. Hard seek: kill FFmpeg, restart at new position.
	m.logger.Debug("hls seek: hard",
		slog.Float64("target", targetSec),
		slog.Float64("distance", distance),
	)
	return SeekModeHard
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
