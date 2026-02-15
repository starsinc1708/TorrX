package mongo

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"torrentstream/internal/domain"
)

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
