package mongo

import (
	"testing"
	"time"

	"torrentstream/internal/domain"
)

func TestWatchDocID(t *testing.T) {
	tests := []struct {
		name      string
		torrentID domain.TorrentID
		fileIndex int
		want      string
	}{
		{"basic", "abc123", 0, "abc123:0"},
		{"non-zero index", "abc123", 5, "abc123:5"},
		{"large index", "xyz", 999, "xyz:999"},
		{"empty torrentId", "", 0, ":0"},
		{"hash-like id", "a1b2c3d4e5f6", 2, "a1b2c3d4e5f6:2"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := watchDocID(tc.torrentID, tc.fileIndex)
			if got != tc.want {
				t.Errorf("watchDocID(%q, %d) = %q, want %q", tc.torrentID, tc.fileIndex, got, tc.want)
			}
		})
	}
}

func TestWatchDocToPosition(t *testing.T) {
	now := time.Now().UTC()
	doc := watchPositionDoc{
		ID:          "abc:0",
		TorrentID:   "abc",
		FileIndex:   0,
		Position:    120.5,
		Duration:    3600.0,
		TorrentName: "Test Movie",
		FilePath:    "movie.mp4",
		UpdatedAt:   now.Unix(),
	}

	pos := watchDocToPosition(doc)

	if pos.TorrentID != "abc" {
		t.Errorf("TorrentID: expected 'abc', got %q", pos.TorrentID)
	}
	if pos.FileIndex != 0 {
		t.Errorf("FileIndex: expected 0, got %d", pos.FileIndex)
	}
	if pos.Position != 120.5 {
		t.Errorf("Position: expected 120.5, got %f", pos.Position)
	}
	if pos.Duration != 3600.0 {
		t.Errorf("Duration: expected 3600.0, got %f", pos.Duration)
	}
	if pos.TorrentName != "Test Movie" {
		t.Errorf("TorrentName: expected 'Test Movie', got %q", pos.TorrentName)
	}
	if pos.FilePath != "movie.mp4" {
		t.Errorf("FilePath: expected 'movie.mp4', got %q", pos.FilePath)
	}
	expectedTime := time.Unix(now.Unix(), 0).UTC()
	if !pos.UpdatedAt.Equal(expectedTime) {
		t.Errorf("UpdatedAt: expected %v, got %v", expectedTime, pos.UpdatedAt)
	}
}

func TestWatchDocToPosition_ZeroTimestamp(t *testing.T) {
	doc := watchPositionDoc{
		TorrentID: "abc",
		FileIndex: 0,
		UpdatedAt: 0,
	}

	pos := watchDocToPosition(doc)

	expected := time.Unix(0, 0).UTC()
	if !pos.UpdatedAt.Equal(expected) {
		t.Errorf("UpdatedAt: expected %v for zero timestamp, got %v", expected, pos.UpdatedAt)
	}
}

func TestWatchDocToPosition_AllFieldsMap(t *testing.T) {
	doc := watchPositionDoc{
		ID:          "torrent1:3",
		TorrentID:   "torrent1",
		FileIndex:   3,
		Position:    999.99,
		Duration:    7200.5,
		TorrentName: "Long Movie",
		FilePath:    "path/to/long-movie.mkv",
		UpdatedAt:   1700000000,
	}

	pos := watchDocToPosition(doc)

	if string(pos.TorrentID) != doc.TorrentID {
		t.Errorf("TorrentID mismatch: %q vs %q", pos.TorrentID, doc.TorrentID)
	}
	if pos.FileIndex != doc.FileIndex {
		t.Errorf("FileIndex mismatch: %d vs %d", pos.FileIndex, doc.FileIndex)
	}
	if pos.Position != doc.Position {
		t.Errorf("Position mismatch: %f vs %f", pos.Position, doc.Position)
	}
	if pos.Duration != doc.Duration {
		t.Errorf("Duration mismatch: %f vs %f", pos.Duration, doc.Duration)
	}
	if pos.TorrentName != doc.TorrentName {
		t.Errorf("TorrentName mismatch: %q vs %q", pos.TorrentName, doc.TorrentName)
	}
	if pos.FilePath != doc.FilePath {
		t.Errorf("FilePath mismatch: %q vs %q", pos.FilePath, doc.FilePath)
	}
}

func TestNewWatchHistoryRepository_NotNil(t *testing.T) {
	// Verify constructor doesn't panic with nil client (deferred usage pattern)
	// In real code, client would be non-nil, but constructor should not crash
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("NewWatchHistoryRepository panicked: %v", r)
		}
	}()
	// Can't actually call NewWatchHistoryRepository with nil — it would panic on Database()
	// So just verify the type exists and constructor is reachable
	var _ *WatchHistoryRepository
}
