package apihttp

import (
	"io"
	"log/slog"
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

	// Target within 2*segDur (8s) of current position.
	mode := m.chooseSeekModeLocked(key, job, 105, 4)
	if mode != SeekModeSoft {
		t.Fatalf("expected SeekModeSoft for small distance, got %s", mode)
	}

	// Also test backward small distance.
	mode = m.chooseSeekModeLocked(key, job, 94, 4)
	if mode != SeekModeSoft {
		t.Fatalf("expected SeekModeSoft for small backward distance, got %s", mode)
	}
}

func TestChooseSeekModeHardLargeDistance(t *testing.T) {
	m := newTestHLSManager(4)
	key := hlsKey{id: "t1", fileIndex: 0}
	job := &hlsJob{seekSeconds: 100}

	// Target > 60s away from current position.
	mode := m.chooseSeekModeLocked(key, job, 200, 4)
	if mode != SeekModeHard {
		t.Fatalf("expected SeekModeHard for large distance, got %s", mode)
	}

	// Backward large distance.
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
