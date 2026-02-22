package anacrolix

import (
	"testing"

	"github.com/anacrolix/torrent"

	"torrentstream/internal/domain"
)

func TestMapPriorityString(t *testing.T) {
	tests := []struct {
		name     string
		input    torrent.PiecePriority
		expected string
	}{
		{"None", torrent.PiecePriorityNone, ""},
		{"Normal", torrent.PiecePriorityNormal, "normal"},
		{"High", torrent.PiecePriorityHigh, "high"},
		{"Readahead", torrent.PiecePriorityReadahead, "normal"},
		{"Now", torrent.PiecePriorityNow, "now"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapPriorityString(tt.input)
			if got != tt.expected {
				t.Errorf("mapPriorityString(%v) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestDeriveTransferPhase(t *testing.T) {
	tests := []struct {
		name              string
		status            domain.TorrentStatus
		mode              domain.SessionMode
		stableCompleted   int64
		verifiedCompleted int64
		wantPhase         domain.TransferPhase
		wantProgress      float64
	}{
		{
			name:              "active downloading",
			status:            domain.TorrentActive,
			mode:              domain.ModeDownloading,
			stableCompleted:   100,
			verifiedCompleted: 100,
			wantPhase:         domain.TransferPhaseDownloading,
			wantProgress:      0,
		},
		{
			name:              "active verifying",
			status:            domain.TorrentActive,
			mode:              domain.ModeDownloading,
			stableCompleted:   1000,
			verifiedCompleted: 250,
			wantPhase:         domain.TransferPhaseVerifying,
			wantProgress:      0.25,
		},
		{
			name:              "completed has no transfer phase",
			status:            domain.TorrentCompleted,
			mode:              domain.ModeCompleted,
			stableCompleted:   1000,
			verifiedCompleted: 1000,
			wantPhase:         "",
			wantProgress:      0,
		},
		{
			name:              "stopped has no transfer phase",
			status:            domain.TorrentStopped,
			mode:              domain.ModeStopped,
			stableCompleted:   1000,
			verifiedCompleted: 200,
			wantPhase:         "",
			wantProgress:      0,
		},
		{
			name:              "clamps progress",
			status:            domain.TorrentActive,
			mode:              domain.ModeFocused,
			stableCompleted:   100,
			verifiedCompleted: 150,
			wantPhase:         domain.TransferPhaseDownloading,
			wantProgress:      0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotPhase, gotProgress := deriveTransferPhase(tc.status, tc.mode, tc.stableCompleted, tc.verifiedCompleted)
			if gotPhase != tc.wantPhase {
				t.Fatalf("phase = %q, want %q", gotPhase, tc.wantPhase)
			}
			if gotProgress != tc.wantProgress {
				t.Fatalf("progress = %v, want %v", gotProgress, tc.wantProgress)
			}
		})
	}
}

func TestNextVerificationPeak(t *testing.T) {
	tests := []struct {
		name            string
		prevPeak        int64
		stableCompleted int64
		verified        int64
		want            int64
	}{
		{
			name:            "increases with verified bytes",
			prevPeak:        100,
			stableCompleted: 1000,
			verified:        220,
			want:            220,
		},
		{
			name:            "stays monotonic on temporary regression",
			prevPeak:        220,
			stableCompleted: 1000,
			verified:        180,
			want:            220,
		},
		{
			name:            "clamps above stable completed",
			prevPeak:        900,
			stableCompleted: 1000,
			verified:        1200,
			want:            1000,
		},
		{
			name:            "clamps negative to zero",
			prevPeak:        0,
			stableCompleted: 1000,
			verified:        -1,
			want:            0,
		},
		{
			name:            "zero when stable is not positive",
			prevPeak:        200,
			stableCompleted: 0,
			verified:        100,
			want:            0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nextVerificationPeak(tc.prevPeak, tc.stableCompleted, tc.verified)
			if got != tc.want {
				t.Fatalf("nextVerificationPeak(%d,%d,%d)=%d, want %d",
					tc.prevPeak, tc.stableCompleted, tc.verified, got, tc.want)
			}
		})
	}
}
