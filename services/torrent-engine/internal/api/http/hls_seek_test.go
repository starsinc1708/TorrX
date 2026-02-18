package apihttp

import (
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
)

func newTestHLSManager(segmentDuration int) *hlsManager {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &hlsManager{
		segmentDuration: segmentDuration,
		logger:          logger,
		// cache intentionally nil for basic seek mode tests
	}
}

func TestChooseSeekModeSoftSmallDistance(t *testing.T) {
	m := newTestHLSManager(4)
	key := hlsKey{id: "t1", fileIndex: 0}
	job := &hlsJob{seekSeconds: 100}

	// Target within 2*segDur (8s) of seekSeconds → soft (minimum band).
	mode := m.chooseSeekModeLocked(key, job, 105, 4)
	if mode != SeekModeSoft {
		t.Fatalf("expected SeekModeSoft for small distance, got %s", mode)
	}

	// Backward small distance within 2×segDur → soft.
	mode = m.chooseSeekModeLocked(key, job, 94, 4)
	if mode != SeekModeSoft {
		t.Fatalf("expected SeekModeSoft for small backward distance, got %s", mode)
	}
}

func TestChooseSeekModeHardLargeDistance(t *testing.T) {
	m := newTestHLSManager(4)
	key := hlsKey{id: "t1", fileIndex: 0}
	job := &hlsJob{seekSeconds: 100}

	// Forward target > estimatedRestartCostSec (12s) past encoded position
	// (with zero ffmpegProgressUs, encoded = seekSeconds = 100).
	mode := m.chooseSeekModeLocked(key, job, 200, 4)
	if mode != SeekModeHard {
		t.Fatalf("expected SeekModeHard for large forward distance, got %s", mode)
	}

	// Backward large distance (> 2×segDur from seekSeconds).
	mode = m.chooseSeekModeLocked(key, job, 10, 4)
	if mode != SeekModeHard {
		t.Fatalf("expected SeekModeHard for large backward distance, got %s", mode)
	}
}

func TestChooseSeekModeNilJob(t *testing.T) {
	m := newTestHLSManager(4)
	key := hlsKey{id: "t1", fileIndex: 0}

	mode := m.chooseSeekModeLocked(key, nil, 100, 4)
	if mode != SeekModeHard {
		t.Fatalf("expected SeekModeHard for nil job, got %s", mode)
	}
}

func TestChooseSeekModeProgressAwareForwardSoft(t *testing.T) {
	m := newTestHLSManager(4)
	key := hlsKey{id: "t1", fileIndex: 0}

	// FFmpeg started at seekSeconds=100 and has encoded 60s of content
	// (ffmpegProgressUs = 60_000_000). Encoded time = 100 + 60 = 160s.
	job := &hlsJob{seekSeconds: 100}
	atomic.StoreInt64(&job.ffmpegProgressUs, 60_000_000) // 60s

	// Target at 150s is within encoded range (150 < 160) → soft.
	mode := m.chooseSeekModeLocked(key, job, 150, 4)
	if mode != SeekModeSoft {
		t.Fatalf("expected SeekModeSoft for target within encoded range, got %s", mode)
	}

	// Target at 165s is 5s ahead of encoded (< 12s restart cost) → soft.
	mode = m.chooseSeekModeLocked(key, job, 165, 4)
	if mode != SeekModeSoft {
		t.Fatalf("expected SeekModeSoft for target slightly ahead of encoded, got %s", mode)
	}

	// Target at 180s is 20s ahead of encoded (> 12s restart cost) → hard.
	mode = m.chooseSeekModeLocked(key, job, 180, 4)
	if mode != SeekModeHard {
		t.Fatalf("expected SeekModeHard for target far ahead of encoded, got %s", mode)
	}
}

func TestChooseSeekModeBackwardPastSeekSeconds(t *testing.T) {
	m := newTestHLSManager(4)
	key := hlsKey{id: "t1", fileIndex: 0}

	job := &hlsJob{seekSeconds: 100}
	atomic.StoreInt64(&job.ffmpegProgressUs, 60_000_000) // encoded to 160s

	// Target at 85s is 15s backward from seekSeconds (> 2×segDur=8s) → hard.
	// Even though FFmpeg has encoded far ahead, backward past seekSeconds
	// means no segments exist for that range.
	mode := m.chooseSeekModeLocked(key, job, 85, 4)
	if mode != SeekModeHard {
		t.Fatalf("expected SeekModeHard for backward past seekSeconds beyond band, got %s", mode)
	}

	// Target at 95s is 5s backward (< 2×segDur=8s) → still soft (minimum band).
	mode = m.chooseSeekModeLocked(key, job, 95, 4)
	if mode != SeekModeSoft {
		t.Fatalf("expected SeekModeSoft for backward within minimum band, got %s", mode)
	}
}

func TestFfmpegEncodedTimeSec(t *testing.T) {
	job := &hlsJob{seekSeconds: 50}

	// No progress yet → returns seekSeconds.
	if got := ffmpegEncodedTimeSec(job); got != 50.0 {
		t.Fatalf("expected 50.0 with zero progress, got %f", got)
	}

	// With 30s of encoding progress.
	atomic.StoreInt64(&job.ffmpegProgressUs, 30_000_000)
	if got := ffmpegEncodedTimeSec(job); got != 80.0 {
		t.Fatalf("expected 80.0 with 30s progress, got %f", got)
	}
}

func TestEstimateByteOffset(t *testing.T) {
	tests := []struct {
		name       string
		targetSec  float64
		durationSec float64
		fileLength int64
		expected   int64
	}{
		{
			name:       "midpoint",
			targetSec:  50,
			durationSec: 100,
			fileLength: 1000,
			expected:   500,
		},
		{
			name:       "start",
			targetSec:  0,
			durationSec: 100,
			fileLength: 1000,
			expected:   -1, // targetSec <= 0 returns -1
		},
		{
			name:       "quarter",
			targetSec:  25,
			durationSec: 100,
			fileLength: 1000,
			expected:   250,
		},
		{
			name:       "past end clamps to 1.0",
			targetSec:  150,
			durationSec: 100,
			fileLength: 1000,
			expected:   1000,
		},
		{
			name:       "large file",
			targetSec:  3600,
			durationSec: 7200,
			fileLength: 4 * 1024 * 1024 * 1024, // 4 GB
			expected:   2 * 1024 * 1024 * 1024,  // 2 GB
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := estimateByteOffset(tc.targetSec, tc.durationSec, tc.fileLength)
			if result != tc.expected {
				t.Fatalf("estimateByteOffset(%v, %v, %v) = %d, want %d",
					tc.targetSec, tc.durationSec, tc.fileLength, result, tc.expected)
			}
		})
	}
}

func TestEstimateByteOffsetEdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		targetSec  float64
		durationSec float64
		fileLength int64
	}{
		{"zero duration", 10, 0, 1000},
		{"zero file length", 10, 100, 0},
		{"negative duration", 10, -100, 1000},
		{"negative file length", 10, 100, -1000},
		{"negative target", -10, 100, 1000},
		{"all zero", 0, 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := estimateByteOffset(tc.targetSec, tc.durationSec, tc.fileLength)
			if result != -1 {
				t.Fatalf("expected -1 for edge case %q, got %d", tc.name, result)
			}
		})
	}
}
