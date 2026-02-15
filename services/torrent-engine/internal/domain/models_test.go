package domain

import (
	"reflect"
	"testing"
)

func TestTorrentStatusConstants(t *testing.T) {
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
	if PriorityLow != 0 {
		t.Fatalf("PriorityLow = %d", PriorityLow)
	}
	if PriorityNormal != 1 {
		t.Fatalf("PriorityNormal = %d", PriorityNormal)
	}
	if PriorityHigh != 2 {
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
