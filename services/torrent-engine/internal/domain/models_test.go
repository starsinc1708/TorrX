package domain

import (
	"reflect"
	"testing"
)

func TestTorrentStatusConstants(t *testing.T) {
	if TorrentPending != "pending" {
		t.Fatalf("TorrentPending = %q", TorrentPending)
	}
	if TorrentActive != "active" {
		t.Fatalf("TorrentActive = %q", TorrentActive)
	}
	if TorrentCompleted != "completed" {
		t.Fatalf("TorrentCompleted = %q", TorrentCompleted)
	}
	if TorrentStopped != "stopped" {
		t.Fatalf("TorrentStopped = %q", TorrentStopped)
	}
	if TorrentError != "error" {
		t.Fatalf("TorrentError = %q", TorrentError)
	}
}

func TestPriorityConstants(t *testing.T) {
	if PriorityNone != -1 {
		t.Fatalf("PriorityNone = %d", PriorityNone)
	}
	if PriorityLow != 0 {
		t.Fatalf("PriorityLow = %d", PriorityLow)
	}
	if PriorityNormal != 1 {
		t.Fatalf("PriorityNormal = %d", PriorityNormal)
	}
	if PriorityReadahead != 2 {
		t.Fatalf("PriorityReadahead = %d", PriorityReadahead)
	}
	if PriorityNext != 3 {
		t.Fatalf("PriorityNext = %d", PriorityNext)
	}
	if PriorityHigh != 4 {
		t.Fatalf("PriorityHigh = %d", PriorityHigh)
	}
}

func TestTorrentSourceJSONTags(t *testing.T) {
	expectJSONTag(t, TorrentSource{}, "Magnet", "magnet,omitempty")
	expectJSONTag(t, TorrentSource{}, "Torrent", "torrent,omitempty")
}

func TestFileRefJSONTags(t *testing.T) {
	expectJSONTag(t, FileRef{}, "Index", "index")
	expectJSONTag(t, FileRef{}, "Path", "path")
	expectJSONTag(t, FileRef{}, "Length", "length")
	expectJSONTag(t, FileRef{}, "PieceStart", "pieceStart,omitempty")
	expectJSONTag(t, FileRef{}, "PieceEnd", "pieceEnd,omitempty")
}

func TestRangeJSONTags(t *testing.T) {
	expectJSONTag(t, Range{}, "Off", "off")
	expectJSONTag(t, Range{}, "Length", "length")
}

func TestTorrentRecordJSONTags(t *testing.T) {
	expectJSONTag(t, TorrentRecord{}, "ID", "id")
	expectJSONTag(t, TorrentRecord{}, "Name", "name")
	expectJSONTag(t, TorrentRecord{}, "Status", "status")
	expectJSONTag(t, TorrentRecord{}, "InfoHash", "infoHash")
	expectJSONTag(t, TorrentRecord{}, "Source", "-")
	expectJSONTag(t, TorrentRecord{}, "Files", "files")
	expectJSONTag(t, TorrentRecord{}, "TotalBytes", "totalBytes")
	expectJSONTag(t, TorrentRecord{}, "DoneBytes", "doneBytes")
	expectJSONTag(t, TorrentRecord{}, "CreatedAt", "createdAt")
	expectJSONTag(t, TorrentRecord{}, "UpdatedAt", "updatedAt")
	expectJSONTag(t, TorrentRecord{}, "Tags", "tags")
}

func TestTorrentFilterJSONTags(t *testing.T) {
	expectJSONTag(t, TorrentFilter{}, "Status", "status,omitempty")
	expectJSONTag(t, TorrentFilter{}, "Search", "search,omitempty")
	expectJSONTag(t, TorrentFilter{}, "Tags", "tags,omitempty")
	expectJSONTag(t, TorrentFilter{}, "SortBy", "sortBy,omitempty")
	expectJSONTag(t, TorrentFilter{}, "SortOrder", "sortOrder,omitempty")
	expectJSONTag(t, TorrentFilter{}, "Limit", "limit,omitempty")
	expectJSONTag(t, TorrentFilter{}, "Offset", "offset,omitempty")
}

func TestSessionStateJSONTags(t *testing.T) {
	expectJSONTag(t, SessionState{}, "ID", "id")
	expectJSONTag(t, SessionState{}, "Status", "status")
	expectJSONTag(t, SessionState{}, "Progress", "progress")
	expectJSONTag(t, SessionState{}, "Peers", "peers")
	expectJSONTag(t, SessionState{}, "DownloadSpeed", "downloadSpeed")
	expectJSONTag(t, SessionState{}, "UploadSpeed", "uploadSpeed")
	expectJSONTag(t, SessionState{}, "UpdatedAt", "updatedAt")
}

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from SessionMode
		to   SessionMode
		want bool
	}{
		// From Idle
		{ModeIdle, ModeDownloading, true},
		{ModeIdle, ModePaused, true},
		{ModeIdle, ModeStopped, true},
		{ModeIdle, ModeFocused, false},
		{ModeIdle, ModeCompleted, false},
		// From Downloading
		{ModeDownloading, ModeStopped, true},
		{ModeDownloading, ModeFocused, true},
		{ModeDownloading, ModePaused, true},
		{ModeDownloading, ModeCompleted, true},
		{ModeDownloading, ModeIdle, false},
		// From Focused
		{ModeFocused, ModeDownloading, true},
		{ModeFocused, ModeStopped, true},
		{ModeFocused, ModeCompleted, true},
		{ModeFocused, ModePaused, false},
		{ModeFocused, ModeIdle, false},
		// From Paused
		{ModePaused, ModeDownloading, true},
		{ModePaused, ModeFocused, true},
		{ModePaused, ModeStopped, true},
		{ModePaused, ModeCompleted, false},
		{ModePaused, ModeIdle, false},
		// From Stopped
		{ModeStopped, ModeDownloading, true},
		{ModeStopped, ModePaused, true},
		{ModeStopped, ModeIdle, true},
		{ModeStopped, ModeFocused, false},
		{ModeStopped, ModeCompleted, false},
		// From Completed
		{ModeCompleted, ModeStopped, true},
		{ModeCompleted, ModeFocused, true},
		{ModeCompleted, ModeDownloading, false},
		{ModeCompleted, ModePaused, false},
		{ModeCompleted, ModeIdle, false},
	}

	for _, tt := range tests {
		name := string(tt.from) + " -> " + string(tt.to)
		t.Run(name, func(t *testing.T) {
			if got := CanTransition(tt.from, tt.to); got != tt.want {
				t.Fatalf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestToStatus(t *testing.T) {
	tests := []struct {
		mode   SessionMode
		status TorrentStatus
	}{
		{ModeIdle, TorrentPending},
		{ModeDownloading, TorrentActive},
		{ModeFocused, TorrentActive},
		{ModePaused, TorrentActive},
		{ModeStopped, TorrentStopped},
		{ModeCompleted, TorrentCompleted},
		{SessionMode("unknown"), TorrentError},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			if got := tt.mode.ToStatus(); got != tt.status {
				t.Fatalf("SessionMode(%s).ToStatus() = %q, want %q", tt.mode, got, tt.status)
			}
		})
	}
}

func TestTorrentRecordValidate(t *testing.T) {
	valid := TorrentRecord{
		ID:         "abc123",
		Status:     TorrentActive,
		TotalBytes: 1000,
		DoneBytes:  500,
	}

	tests := []struct {
		name    string
		modify  func(TorrentRecord) TorrentRecord
		wantErr bool
	}{
		{"valid record", func(r TorrentRecord) TorrentRecord { return r }, false},
		{"empty id", func(r TorrentRecord) TorrentRecord { r.ID = ""; return r }, true},
		{"empty status", func(r TorrentRecord) TorrentRecord { r.Status = ""; return r }, true},
		{"invalid status", func(r TorrentRecord) TorrentRecord { r.Status = "bogus"; return r }, true},
		{"negative totalBytes", func(r TorrentRecord) TorrentRecord { r.TotalBytes = -1; return r }, true},
		{"negative doneBytes", func(r TorrentRecord) TorrentRecord { r.DoneBytes = -1; return r }, true},
		{"doneBytes exceeds totalBytes", func(r TorrentRecord) TorrentRecord { r.DoneBytes = 2000; return r }, true},
		{"doneBytes equals totalBytes", func(r TorrentRecord) TorrentRecord { r.DoneBytes = 1000; return r }, false},
		{"zero totalBytes with zero doneBytes", func(r TorrentRecord) TorrentRecord { r.TotalBytes = 0; r.DoneBytes = 0; return r }, false},
		{"status pending", func(r TorrentRecord) TorrentRecord { r.Status = TorrentPending; return r }, false},
		{"status completed", func(r TorrentRecord) TorrentRecord { r.Status = TorrentCompleted; return r }, false},
		{"status stopped", func(r TorrentRecord) TorrentRecord { r.Status = TorrentStopped; return r }, false},
		{"status error", func(r TorrentRecord) TorrentRecord { r.Status = TorrentError; return r }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := tt.modify(valid)
			err := r.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWatchPositionJSONTags(t *testing.T) {
	expectJSONTag(t, WatchPosition{}, "TorrentID", "torrentId")
	expectJSONTag(t, WatchPosition{}, "FileIndex", "fileIndex")
	expectJSONTag(t, WatchPosition{}, "Position", "position")
	expectJSONTag(t, WatchPosition{}, "Duration", "duration")
	expectJSONTag(t, WatchPosition{}, "TorrentName", "torrentName")
	expectJSONTag(t, WatchPosition{}, "FilePath", "filePath")
	expectJSONTag(t, WatchPosition{}, "UpdatedAt", "updatedAt")
}

func TestMediaInfoJSONTags(t *testing.T) {
	expectJSONTag(t, MediaInfo{}, "Tracks", "tracks")
	expectJSONTag(t, MediaInfo{}, "Duration", "duration")
	expectJSONTag(t, MediaInfo{}, "StartTime", "startTime")
	expectJSONTag(t, MediaInfo{}, "SubtitlesReady", "subtitlesReady")
	expectJSONTag(t, MediaInfo{}, "DirectPlaybackCompatible", "directPlaybackCompatible")
}

func TestMediaTrackJSONTags(t *testing.T) {
	expectJSONTag(t, MediaTrack{}, "Index", "index")
	expectJSONTag(t, MediaTrack{}, "Type", "type")
	expectJSONTag(t, MediaTrack{}, "Codec", "codec")
	expectJSONTag(t, MediaTrack{}, "Language", "language")
	expectJSONTag(t, MediaTrack{}, "Title", "title")
	expectJSONTag(t, MediaTrack{}, "Default", "default")
	expectJSONTag(t, MediaTrack{}, "Width", "width,omitempty")
	expectJSONTag(t, MediaTrack{}, "Height", "height,omitempty")
	expectJSONTag(t, MediaTrack{}, "FPS", "fps,omitempty")
	expectJSONTag(t, MediaTrack{}, "Channels", "channels,omitempty")
}

func expectJSONTag(t *testing.T, v interface{}, fieldName, want string) {
	t.Helper()
	typ := reflect.TypeOf(v)
	field, ok := typ.FieldByName(fieldName)
	if !ok {
		t.Fatalf("missing field %s", fieldName)
	}
	if got := field.Tag.Get("json"); got != want {
		t.Fatalf("%s json tag = %q, want %q", fieldName, got, want)
	}
}
