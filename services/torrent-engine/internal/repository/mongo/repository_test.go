package mongo

import (
	"math"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"torrentstream/internal/domain"
)

// ---------------------------------------------------------------------------
// toDoc / fromDoc roundtrip
// ---------------------------------------------------------------------------

func TestToDocFromDocRoundtrip(t *testing.T) {
	now := time.Date(2026, 2, 19, 10, 0, 0, 0, time.UTC)
	record := domain.TorrentRecord{
		ID:       "abc123",
		Name:     "Big Buck Bunny",
		Status:   domain.TorrentActive,
		InfoHash: "d2354e",
		Source:   domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:d2354e"},
		Files: []domain.FileRef{
			{Index: 0, Path: "video.mkv", Length: 1024, BytesCompleted: 512},
			{Index: 1, Path: "subs.srt", Length: 4096, BytesCompleted: 4096},
		},
		TotalBytes: 5120,
		DoneBytes:  4608,
		CreatedAt:  now,
		UpdatedAt:  now.Add(time.Minute),
		Tags:       []string{"movie", "animation"},
	}

	doc := toDoc(record)
	got := fromDoc(doc)

	if got.ID != record.ID {
		t.Errorf("ID: got %q, want %q", got.ID, record.ID)
	}
	if got.Name != record.Name {
		t.Errorf("Name: got %q, want %q", got.Name, record.Name)
	}
	if got.Status != record.Status {
		t.Errorf("Status: got %q, want %q", got.Status, record.Status)
	}
	if got.InfoHash != record.InfoHash {
		t.Errorf("InfoHash: got %q, want %q", got.InfoHash, record.InfoHash)
	}
	if got.Source.Magnet != record.Source.Magnet {
		t.Errorf("Magnet: got %q, want %q", got.Source.Magnet, record.Source.Magnet)
	}
	if got.TotalBytes != record.TotalBytes {
		t.Errorf("TotalBytes: got %d, want %d", got.TotalBytes, record.TotalBytes)
	}
	if got.DoneBytes != record.DoneBytes {
		t.Errorf("DoneBytes: got %d, want %d", got.DoneBytes, record.DoneBytes)
	}
	if len(got.Files) != len(record.Files) {
		t.Fatalf("Files length: got %d, want %d", len(got.Files), len(record.Files))
	}
	for i, f := range got.Files {
		if f != record.Files[i] {
			t.Errorf("Files[%d]: got %+v, want %+v", i, f, record.Files[i])
		}
	}
	if !reflect.DeepEqual(got.Tags, record.Tags) {
		t.Errorf("Tags: got %v, want %v", got.Tags, record.Tags)
	}
	// Time loses sub-second precision through Unix conversion.
	if got.CreatedAt.Unix() != record.CreatedAt.Unix() {
		t.Errorf("CreatedAt: got %v, want %v", got.CreatedAt, record.CreatedAt)
	}
	if got.UpdatedAt.Unix() != record.UpdatedAt.Unix() {
		t.Errorf("UpdatedAt: got %v, want %v", got.UpdatedAt, record.UpdatedAt)
	}
}

func TestToDocWithTorrentFileSource(t *testing.T) {
	record := domain.TorrentRecord{
		ID:     "t1",
		Name:   "Test",
		Status: domain.TorrentPending,
		Source: domain.TorrentSource{Torrent: "/path/to/file.torrent"},
	}

	doc := toDoc(record)
	if doc.Torrent != "/path/to/file.torrent" {
		t.Errorf("Torrent: got %q, want %q", doc.Torrent, "/path/to/file.torrent")
	}
	if doc.Magnet != "" {
		t.Errorf("Magnet should be empty, got %q", doc.Magnet)
	}

	got := fromDoc(doc)
	if got.Source.Torrent != record.Source.Torrent {
		t.Errorf("Source.Torrent roundtrip: got %q, want %q", got.Source.Torrent, record.Source.Torrent)
	}
}

func TestToDocEmptyFiles(t *testing.T) {
	record := domain.TorrentRecord{
		ID: "t1", Name: "Empty", Status: domain.TorrentPending,
	}
	doc := toDoc(record)
	if len(doc.Files) != 0 {
		t.Errorf("expected empty files slice, got %d", len(doc.Files))
	}
	got := fromDoc(doc)
	if len(got.Files) != 0 {
		t.Errorf("expected empty files in roundtrip, got %d", len(got.Files))
	}
}

// ---------------------------------------------------------------------------
// toDoc progress calculation
// ---------------------------------------------------------------------------

func TestToDocProgress(t *testing.T) {
	tests := []struct {
		name       string
		totalBytes int64
		doneBytes  int64
		want       float64
	}{
		{"zero total", 0, 0, 0.0},
		{"zero done", 1000, 0, 0.0},
		{"half done", 1000, 500, 0.5},
		{"complete", 1000, 1000, 1.0},
		{"large file", 10_000_000_000, 7_500_000_000, 0.75},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := domain.TorrentRecord{
				ID: "t", Name: "N", Status: domain.TorrentActive,
				TotalBytes: tt.totalBytes, DoneBytes: tt.doneBytes,
			}
			doc := toDoc(rec)
			if math.Abs(doc.Progress-tt.want) > 1e-9 {
				t.Errorf("progress: got %f, want %f", doc.Progress, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// toUpdateDoc
// ---------------------------------------------------------------------------

func TestToUpdateDocOmitsID(t *testing.T) {
	record := domain.TorrentRecord{
		ID:         "t1",
		Name:       "Sintel",
		Status:     domain.TorrentActive,
		InfoHash:   "hash",
		Files:      []domain.FileRef{{Index: 0, Path: "file.mp4", Length: 10}},
		TotalBytes: 10,
		DoneBytes:  5,
		CreatedAt:  time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 2, 10, 12, 1, 0, 0, time.UTC),
	}

	update := toUpdateDoc(record)
	raw, err := bson.Marshal(update)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var doc bson.M
	if err := bson.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := doc["_id"]; ok {
		t.Fatalf("_id should not be present in update doc")
	}
	if doc["name"] != "Sintel" {
		t.Fatalf("name mismatch: %v", doc["name"])
	}
	if doc["status"] != string(domain.TorrentActive) {
		t.Fatalf("status mismatch: %v", doc["status"])
	}
}

func TestToUpdateDocPreservesProgress(t *testing.T) {
	rec := domain.TorrentRecord{
		ID: "t1", Name: "X", Status: domain.TorrentActive,
		TotalBytes: 200, DoneBytes: 100,
	}
	update := toUpdateDoc(rec)
	if math.Abs(update.Progress-0.5) > 1e-9 {
		t.Errorf("progress: got %f, want 0.5", update.Progress)
	}
}

func TestToUpdateDocAllFieldsPresent(t *testing.T) {
	rec := domain.TorrentRecord{
		ID: "t1", Name: "Movie", Status: domain.TorrentCompleted,
		InfoHash:   "abc",
		Source:     domain.TorrentSource{Magnet: "magnet:?xt=hash"},
		Files:      []domain.FileRef{{Index: 0, Path: "a.mp4", Length: 100}},
		TotalBytes: 100, DoneBytes: 100,
		Tags:      []string{"hd"},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	update := toUpdateDoc(rec)
	raw, err := bson.Marshal(update)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc bson.M
	if err := bson.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	requiredFields := []string{"name", "status", "infoHash", "magnet", "files", "totalBytes", "doneBytes", "progress", "createdAt", "updatedAt", "tags"}
	for _, f := range requiredFields {
		if _, ok := doc[f]; !ok {
			t.Errorf("missing field %q in update doc", f)
		}
	}
}

// ---------------------------------------------------------------------------
// normalizeTags
// ---------------------------------------------------------------------------

func TestNormalizeTags(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, nil},
		{"empty slice", []string{}, nil},
		{"single", []string{"movie"}, []string{"movie"}},
		{"dedup case insensitive", []string{"Movie", "movie", "MOVIE"}, []string{"Movie"}},
		{"trims whitespace", []string{"  movie  ", " hd "}, []string{"movie", "hd"}},
		{"removes empty", []string{"", "  ", "ok"}, []string{"ok"}},
		{"preserves order", []string{"b", "a", "c"}, []string{"b", "a", "c"}},
		{"mixed dedup", []string{"HD", "  hd  ", "SD", "sd"}, []string{"HD", "SD"}},
		{"all empty", []string{"", " ", "  "}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeTags(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("normalizeTags(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// mongoSortField
// ---------------------------------------------------------------------------

func TestMongoSortField(t *testing.T) {
	tests := []struct {
		input     string
		wantField string
		wantOK    bool
	}{
		{"name", "name", true},
		{"createdAt", "createdAt", true},
		{"updatedAt", "updatedAt", true},
		{"totalBytes", "totalBytes", true},
		{"progress", "progress", true},
		{"unknown", "", false},
		{"", "", false},
		{"NAME", "", false}, // case-sensitive
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			field, ok := mongoSortField(tt.input)
			if field != tt.wantField || ok != tt.wantOK {
				t.Errorf("mongoSortField(%q) = (%q, %v), want (%q, %v)", tt.input, field, ok, tt.wantField, tt.wantOK)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// progressOfRecord
// ---------------------------------------------------------------------------

func TestProgressOfRecord(t *testing.T) {
	tests := []struct {
		name       string
		totalBytes int64
		doneBytes  int64
		want       float64
	}{
		{"zero total", 0, 0, 0},
		{"negative total", -100, 50, 0},
		{"zero done", 1000, 0, 0},
		{"half", 1000, 500, 0.5},
		{"complete", 1000, 1000, 1.0},
		{"overflow capped", 1000, 2000, 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := domain.TorrentRecord{TotalBytes: tt.totalBytes, DoneBytes: tt.doneBytes}
			got := progressOfRecord(rec)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("progressOfRecord = %f, want %f", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// timeFromUnix
// ---------------------------------------------------------------------------

func TestTimeFromUnix(t *testing.T) {
	tests := []struct {
		name  string
		value int64
		want  time.Time
	}{
		{"epoch", 0, time.Unix(0, 0).UTC()},
		{"specific", 1708329600, time.Unix(1708329600, 0).UTC()},
		{"recent", 1740000000, time.Unix(1740000000, 0).UTC()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := timeFromUnix(tt.value)
			if !got.Equal(tt.want) {
				t.Errorf("timeFromUnix(%d) = %v, want %v", tt.value, got, tt.want)
			}
			if got.Location() != time.UTC {
				t.Errorf("expected UTC, got %v", got.Location())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// fromDocs
// ---------------------------------------------------------------------------

func TestFromDocsEmpty(t *testing.T) {
	got := fromDocs(nil)
	if len(got) != 0 {
		t.Errorf("expected empty result for nil input, got %d", len(got))
	}
}

func TestFromDocsMultiple(t *testing.T) {
	docs := []torrentDoc{
		{ID: "a", Name: "First", Status: "active"},
		{ID: "b", Name: "Second", Status: "completed"},
	}
	got := fromDocs(docs)
	if len(got) != 2 {
		t.Fatalf("expected 2 records, got %d", len(got))
	}
	if string(got[0].ID) != "a" || string(got[1].ID) != "b" {
		t.Errorf("IDs mismatch: %q, %q", got[0].ID, got[1].ID)
	}
}

// ---------------------------------------------------------------------------
// BSON serialization integrity
// ---------------------------------------------------------------------------

func TestToDocBSONRoundtrip(t *testing.T) {
	now := time.Date(2026, 2, 19, 12, 0, 0, 0, time.UTC)
	record := domain.TorrentRecord{
		ID: "bson-test", Name: "BSON Test", Status: domain.TorrentCompleted,
		InfoHash: "deadbeef",
		Source:   domain.TorrentSource{Magnet: "magnet:?xt=hash"},
		Files: []domain.FileRef{
			{Index: 0, Path: "a.mp4", Length: 500, BytesCompleted: 500},
		},
		TotalBytes: 500, DoneBytes: 500,
		CreatedAt: now, UpdatedAt: now,
		Tags: []string{"test"},
	}

	doc := toDoc(record)
	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded torrentDoc
	if err := bson.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != doc.ID {
		t.Errorf("ID mismatch after BSON roundtrip")
	}
	if decoded.Name != doc.Name {
		t.Errorf("Name mismatch after BSON roundtrip")
	}
	if math.Abs(decoded.Progress-1.0) > 1e-9 {
		t.Errorf("Progress: got %f, want 1.0", decoded.Progress)
	}
	if len(decoded.Files) != 1 {
		t.Fatalf("Files: got %d, want 1", len(decoded.Files))
	}
	if decoded.Files[0].Path != "a.mp4" {
		t.Errorf("File path: got %q, want %q", decoded.Files[0].Path, "a.mp4")
	}
}

func TestToDocIDMappedTo_id(t *testing.T) {
	doc := toDoc(domain.TorrentRecord{
		ID: "myid", Name: "N", Status: domain.TorrentPending,
	})
	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m bson.M
	if err := bson.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["_id"] != "myid" {
		t.Errorf("expected _id=myid, got %v", m["_id"])
	}
}

// ---------------------------------------------------------------------------
// EnsureIndexes nil safety
// ---------------------------------------------------------------------------

func TestEnsureIndexesNilRepository(t *testing.T) {
	var r *Repository
	err := r.EnsureIndexes(nil)
	if err != nil {
		t.Errorf("expected nil error for nil repository, got %v", err)
	}
}

func TestEnsureIndexesNilCollection(t *testing.T) {
	r := &Repository{collection: nil}
	err := r.EnsureIndexes(nil)
	if err != nil {
		t.Errorf("expected nil error for nil collection, got %v", err)
	}
}
