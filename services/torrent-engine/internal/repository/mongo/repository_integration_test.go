package mongo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/mongo/options"

	"torrentstream/internal/domain"
)

// testMongoURI returns the MongoDB connection URI for integration tests.
// Defaults to localhost:27017. Set MONGO_TEST_URI to override.
func testMongoURI() string {
	if uri := os.Getenv("MONGO_TEST_URI"); uri != "" {
		return uri
	}
	return "mongodb://localhost:27017"
}

// setupTestRepo connects to MongoDB and returns a Repository using a unique
// test database. The cleanup function drops the database and disconnects.
// Calls t.Skip if MongoDB is unreachable.
func setupTestRepo(t *testing.T) (*Repository, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	uri := testMongoURI()
	client, err := Connect(ctx, uri, options.Client().SetConnectTimeout(3*time.Second))
	if err != nil {
		t.Skipf("MongoDB not available at %s: %v", uri, err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		t.Skipf("MongoDB ping failed at %s: %v", uri, err)
	}

	dbName := fmt.Sprintf("torrx_test_%d", time.Now().UnixNano())
	repo := NewRepository(client, dbName, "torrents")

	if err := repo.EnsureIndexes(ctx); err != nil {
		_ = client.Disconnect(ctx)
		t.Fatalf("EnsureIndexes: %v", err)
	}

	cleanup := func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		_ = client.Database(dbName).Drop(ctx2)
		_ = client.Disconnect(ctx2)
	}
	return repo, cleanup
}

func makeTorrent(id string, status domain.TorrentStatus) domain.TorrentRecord {
	now := time.Now().UTC().Truncate(time.Second)
	return domain.TorrentRecord{
		ID:         domain.TorrentID(id),
		Name:       "Torrent " + id,
		Status:     status,
		InfoHash:   domain.InfoHash("hash_" + id),
		Source:     domain.TorrentSource{Magnet: "magnet:?xt=urn:btih:" + id},
		Files:      []domain.FileRef{{Index: 0, Path: id + ".mkv", Length: 1000, BytesCompleted: 0}},
		TotalBytes: 1000,
		DoneBytes:  0,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

func TestIntegrationCreate(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	rec := makeTorrent("create1", domain.TorrentPending)
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestIntegrationCreateDuplicate(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	rec := makeTorrent("dup1", domain.TorrentPending)
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	err := repo.Create(ctx, rec)
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("expected ErrAlreadyExists, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Get
// ---------------------------------------------------------------------------

func TestIntegrationGetRoundtrip(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	rec := makeTorrent("get1", domain.TorrentActive)
	rec.Tags = []string{"movie", "hd"}
	rec.DoneBytes = 500
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, "get1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != rec.ID {
		t.Errorf("ID: got %q, want %q", got.ID, rec.ID)
	}
	if got.Name != rec.Name {
		t.Errorf("Name: got %q, want %q", got.Name, rec.Name)
	}
	if got.Status != rec.Status {
		t.Errorf("Status: got %q, want %q", got.Status, rec.Status)
	}
	if got.InfoHash != rec.InfoHash {
		t.Errorf("InfoHash: got %q, want %q", got.InfoHash, rec.InfoHash)
	}
	if got.Source.Magnet != rec.Source.Magnet {
		t.Errorf("Magnet: got %q, want %q", got.Source.Magnet, rec.Source.Magnet)
	}
	if got.TotalBytes != rec.TotalBytes {
		t.Errorf("TotalBytes: got %d, want %d", got.TotalBytes, rec.TotalBytes)
	}
	if got.DoneBytes != rec.DoneBytes {
		t.Errorf("DoneBytes: got %d, want %d", got.DoneBytes, rec.DoneBytes)
	}
	if len(got.Files) != 1 || got.Files[0].Path != rec.Files[0].Path {
		t.Errorf("Files mismatch: got %+v", got.Files)
	}
	if !reflect.DeepEqual(got.Tags, rec.Tags) {
		t.Errorf("Tags: got %v, want %v", got.Tags, rec.Tags)
	}
	if got.CreatedAt.Unix() != rec.CreatedAt.Unix() {
		t.Errorf("CreatedAt: got %v, want %v", got.CreatedAt, rec.CreatedAt)
	}
}

func TestIntegrationGetNotFound(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	_, err := repo.Get(context.Background(), "nonexistent")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func TestIntegrationUpdate(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	rec := makeTorrent("upd1", domain.TorrentPending)
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rec.Name = "Updated Name"
	rec.Status = domain.TorrentActive
	rec.DoneBytes = 500
	rec.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	if err := repo.Update(ctx, rec); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.Get(ctx, "upd1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Updated Name" {
		t.Errorf("Name: got %q, want %q", got.Name, "Updated Name")
	}
	if got.Status != domain.TorrentActive {
		t.Errorf("Status: got %q, want %q", got.Status, domain.TorrentActive)
	}
	if got.DoneBytes != 500 {
		t.Errorf("DoneBytes: got %d, want 500", got.DoneBytes)
	}
}

func TestIntegrationUpdateNotFound(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	rec := makeTorrent("ghost", domain.TorrentActive)
	err := repo.Update(context.Background(), rec)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// UpdateProgress — atomic $max behavior
// ---------------------------------------------------------------------------

func TestIntegrationUpdateProgress(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	rec := makeTorrent("prog1", domain.TorrentActive)
	rec.TotalBytes = 10000
	rec.DoneBytes = 1000
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Progress forward.
	err := repo.UpdateProgress(ctx, "prog1", domain.ProgressUpdate{
		DoneBytes:  5000,
		TotalBytes: 10000,
		Status:     domain.TorrentActive,
	})
	if err != nil {
		t.Fatalf("UpdateProgress forward: %v", err)
	}

	got, _ := repo.Get(ctx, "prog1")
	if got.DoneBytes != 5000 {
		t.Errorf("DoneBytes after forward: got %d, want 5000", got.DoneBytes)
	}

	// $max prevents regression: sending lower doneBytes should NOT decrease.
	err = repo.UpdateProgress(ctx, "prog1", domain.ProgressUpdate{
		DoneBytes:  3000,
		TotalBytes: 10000,
	})
	if err != nil {
		t.Fatalf("UpdateProgress lower: %v", err)
	}

	got, _ = repo.Get(ctx, "prog1")
	if got.DoneBytes != 5000 {
		t.Errorf("DoneBytes after $max: got %d, want 5000 (should not regress)", got.DoneBytes)
	}

	// Higher value should still work.
	err = repo.UpdateProgress(ctx, "prog1", domain.ProgressUpdate{
		DoneBytes:  8000,
		TotalBytes: 10000,
		Name:       "Renamed",
	})
	if err != nil {
		t.Fatalf("UpdateProgress higher: %v", err)
	}

	got, _ = repo.Get(ctx, "prog1")
	if got.DoneBytes != 8000 {
		t.Errorf("DoneBytes after higher: got %d, want 8000", got.DoneBytes)
	}
	if got.Name != "Renamed" {
		t.Errorf("Name: got %q, want %q", got.Name, "Renamed")
	}
}

func TestIntegrationUpdateProgressNotFound(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	err := repo.UpdateProgress(context.Background(), "missing", domain.ProgressUpdate{
		DoneBytes: 100, TotalBytes: 1000,
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestIntegrationUpdateProgressWithFiles(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	rec := makeTorrent("progf1", domain.TorrentActive)
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}

	files := []domain.FileRef{
		{Index: 0, Path: "video.mkv", Length: 1000, BytesCompleted: 500},
		{Index: 1, Path: "subs.srt", Length: 200, BytesCompleted: 200},
	}
	err := repo.UpdateProgress(ctx, "progf1", domain.ProgressUpdate{
		DoneBytes:  700,
		TotalBytes: 1200,
		Files:      files,
	})
	if err != nil {
		t.Fatalf("UpdateProgress with files: %v", err)
	}

	got, _ := repo.Get(ctx, "progf1")
	if len(got.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(got.Files))
	}
	if got.Files[1].BytesCompleted != 200 {
		t.Errorf("file[1] bytesCompleted: got %d, want 200", got.Files[1].BytesCompleted)
	}
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func TestIntegrationDelete(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	rec := makeTorrent("del1", domain.TorrentStopped)
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Delete(ctx, "del1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := repo.Get(ctx, "del1")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestIntegrationDeleteNotFound(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	err := repo.Delete(context.Background(), "missing")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// List — filters, search, sort, pagination
// ---------------------------------------------------------------------------

func seedTorrents(t *testing.T, repo *Repository, count int) {
	t.Helper()
	ctx := context.Background()
	statuses := []domain.TorrentStatus{
		domain.TorrentActive, domain.TorrentCompleted, domain.TorrentStopped,
		domain.TorrentPending, domain.TorrentActive,
	}
	for i := 0; i < count; i++ {
		rec := makeTorrent(fmt.Sprintf("seed%02d", i), statuses[i%len(statuses)])
		rec.Name = fmt.Sprintf("Torrent_%02d", i)
		rec.TotalBytes = int64((i + 1) * 1000)
		rec.DoneBytes = int64(i * 100)
		rec.Tags = []string{fmt.Sprintf("tag%d", i%3)}
		rec.CreatedAt = time.Now().UTC().Add(time.Duration(i) * time.Minute).Truncate(time.Second)
		rec.UpdatedAt = rec.CreatedAt
		if err := repo.Create(ctx, rec); err != nil {
			t.Fatalf("seed Create %d: %v", i, err)
		}
	}
}

func TestIntegrationListAll(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	seedTorrents(t, repo, 10)

	results, err := repo.List(context.Background(), domain.TorrentFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(results) != 10 {
		t.Errorf("expected 10 results, got %d", len(results))
	}
}

func TestIntegrationListFilterStatus(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	seedTorrents(t, repo, 10)
	status := domain.TorrentActive

	results, err := repo.List(context.Background(), domain.TorrentFilter{Status: &status})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// With 10 seeded, active appears at indices 0,4,5,9 → 4 active.
	for _, r := range results {
		if r.Status != domain.TorrentActive {
			t.Errorf("expected status active, got %q for %q", r.Status, r.ID)
		}
	}
	if len(results) == 0 {
		t.Error("expected at least one active torrent")
	}
}

func TestIntegrationListSearch(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	// Insert records with specific names.
	ubuntu := makeTorrent("search1", domain.TorrentActive)
	ubuntu.Name = "Ubuntu 22.04 Desktop"
	fedora := makeTorrent("search2", domain.TorrentActive)
	fedora.Name = "Fedora Workstation 39"
	if err := repo.Create(ctx, ubuntu); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, fedora); err != nil {
		t.Fatal(err)
	}

	results, err := repo.List(ctx, domain.TorrentFilter{Search: "ubuntu"})
	if err != nil {
		t.Fatalf("List search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'ubuntu', got %d", len(results))
	}
	if string(results[0].ID) != "search1" {
		t.Errorf("expected search1, got %q", results[0].ID)
	}
}

func TestIntegrationListSearchCaseInsensitive(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	rec := makeTorrent("ci1", domain.TorrentActive)
	rec.Name = "UPPERCASE TITLE"
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatal(err)
	}

	results, err := repo.List(ctx, domain.TorrentFilter{Search: "uppercase"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 case-insensitive match, got %d", len(results))
	}
}

func TestIntegrationListSearchSpecialChars(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	rec := makeTorrent("sp1", domain.TorrentActive)
	rec.Name = "Movie (2026) [1080p]"
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatal(err)
	}

	// Search with regex special chars — should be escaped via QuoteMeta.
	results, err := repo.List(ctx, domain.TorrentFilter{Search: "(2026)"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for '(2026)', got %d", len(results))
	}
}

func TestIntegrationListFilterTags(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	rec1 := makeTorrent("tag1", domain.TorrentActive)
	rec1.Tags = []string{"movie", "hd"}
	rec2 := makeTorrent("tag2", domain.TorrentActive)
	rec2.Tags = []string{"movie", "sd"}
	rec3 := makeTorrent("tag3", domain.TorrentActive)
	rec3.Tags = []string{"tv"}
	for _, r := range []domain.TorrentRecord{rec1, rec2, rec3} {
		if err := repo.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	// Filter by single tag.
	results, err := repo.List(ctx, domain.TorrentFilter{Tags: []string{"movie"}})
	if err != nil {
		t.Fatalf("List tags: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results for tag 'movie', got %d", len(results))
	}

	// Filter by multiple tags (AND).
	results, err = repo.List(ctx, domain.TorrentFilter{Tags: []string{"movie", "hd"}})
	if err != nil {
		t.Fatalf("List multi-tags: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for tags [movie, hd], got %d", len(results))
	}
}

func TestIntegrationListSortByName(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	for _, name := range []string{"Ccc", "Aaa", "Bbb"} {
		rec := makeTorrent(name, domain.TorrentActive)
		rec.Name = name
		if err := repo.Create(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	results, err := repo.List(ctx, domain.TorrentFilter{SortBy: "name", SortOrder: domain.SortAsc})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3, got %d", len(results))
	}
	if results[0].Name != "Aaa" || results[1].Name != "Bbb" || results[2].Name != "Ccc" {
		t.Errorf("wrong order: %q, %q, %q", results[0].Name, results[1].Name, results[2].Name)
	}
}

func TestIntegrationListSortByProgress(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	for i, done := range []int64{500, 100, 900} {
		rec := makeTorrent(fmt.Sprintf("prog_%d", i), domain.TorrentActive)
		rec.TotalBytes = 1000
		rec.DoneBytes = done
		if err := repo.Create(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	results, err := repo.List(ctx, domain.TorrentFilter{SortBy: "progress", SortOrder: domain.SortDesc})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(results) < 3 {
		t.Fatalf("expected 3, got %d", len(results))
	}
	// Desc order: 900/1000=0.9, 500/1000=0.5, 100/1000=0.1.
	if results[0].DoneBytes != 900 {
		t.Errorf("first result doneBytes: got %d, want 900", results[0].DoneBytes)
	}
	if results[2].DoneBytes != 100 {
		t.Errorf("last result doneBytes: got %d, want 100", results[2].DoneBytes)
	}
}

func TestIntegrationListSortDefaultDesc(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 3; i++ {
		rec := makeTorrent(fmt.Sprintf("def_%d", i), domain.TorrentActive)
		rec.UpdatedAt = base.Add(time.Duration(i) * time.Hour)
		if err := repo.Create(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	// No SortBy/SortOrder: defaults to updatedAt desc.
	results, err := repo.List(ctx, domain.TorrentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 3 {
		t.Fatalf("expected 3, got %d", len(results))
	}
	if results[0].UpdatedAt.Before(results[2].UpdatedAt) {
		t.Error("expected descending updatedAt order by default")
	}
}

func TestIntegrationListPagination(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		rec := makeTorrent(fmt.Sprintf("page%02d", i), domain.TorrentActive)
		rec.CreatedAt = time.Now().UTC().Add(time.Duration(i) * time.Minute).Truncate(time.Second)
		rec.UpdatedAt = rec.CreatedAt
		if err := repo.Create(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	// Page 1: limit 3.
	page1, err := repo.List(ctx, domain.TorrentFilter{Limit: 3, SortBy: "createdAt", SortOrder: domain.SortAsc})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 3 {
		t.Fatalf("page1: expected 3, got %d", len(page1))
	}

	// Page 2: offset 3, limit 3.
	page2, err := repo.List(ctx, domain.TorrentFilter{Limit: 3, Offset: 3, SortBy: "createdAt", SortOrder: domain.SortAsc})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 3 {
		t.Fatalf("page2: expected 3, got %d", len(page2))
	}

	// Ensure no overlap.
	if page1[0].ID == page2[0].ID {
		t.Error("page1 and page2 should not overlap")
	}
}

func TestIntegrationListCombinedFilters(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	// Create mix of records.
	for i := 0; i < 6; i++ {
		status := domain.TorrentActive
		if i%2 == 0 {
			status = domain.TorrentCompleted
		}
		rec := makeTorrent(fmt.Sprintf("combo%d", i), status)
		rec.Name = fmt.Sprintf("Movie Part %d", i)
		rec.Tags = []string{"movie"}
		if i >= 3 {
			rec.Tags = append(rec.Tags, "hd")
		}
		if err := repo.Create(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	// Active + tag "hd" + search "Movie".
	status := domain.TorrentActive
	results, err := repo.List(ctx, domain.TorrentFilter{
		Status: &status,
		Tags:   []string{"hd"},
		Search: "Movie",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Status != domain.TorrentActive {
			t.Errorf("expected active, got %q", r.Status)
		}
	}
}

func TestIntegrationListUnknownSortFieldFallsBack(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	rec := makeTorrent("fb1", domain.TorrentActive)
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatal(err)
	}

	// Unknown sort field should fall back to updatedAt without error.
	results, err := repo.List(ctx, domain.TorrentFilter{SortBy: "nonexistent"})
	if err != nil {
		t.Fatalf("List with unknown sort: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// GetMany
// ---------------------------------------------------------------------------

func TestIntegrationGetMany(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	for _, id := range []string{"m1", "m2", "m3"} {
		if err := repo.Create(ctx, makeTorrent(id, domain.TorrentActive)); err != nil {
			t.Fatal(err)
		}
	}

	results, err := repo.GetMany(ctx, []domain.TorrentID{"m1", "m3"})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2, got %d", len(results))
	}
}

func TestIntegrationGetManyEmpty(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	results, err := repo.GetMany(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetMany nil: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for empty IDs, got %v", results)
	}
}

func TestIntegrationGetManyPartialMatch(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	if err := repo.Create(ctx, makeTorrent("exist1", domain.TorrentActive)); err != nil {
		t.Fatal(err)
	}

	results, err := repo.GetMany(ctx, []domain.TorrentID{"exist1", "nonexistent"})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 match, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// UpdateTags
// ---------------------------------------------------------------------------

func TestIntegrationUpdateTags(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	rec := makeTorrent("tags1", domain.TorrentActive)
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatal(err)
	}

	if err := repo.UpdateTags(ctx, "tags1", []string{"movie", "hd", "4k"}); err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}

	got, _ := repo.Get(ctx, "tags1")
	if !reflect.DeepEqual(got.Tags, []string{"movie", "hd", "4k"}) {
		t.Errorf("Tags: got %v, want [movie hd 4k]", got.Tags)
	}
}

func TestIntegrationUpdateTagsNotFound(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	err := repo.UpdateTags(context.Background(), "missing", []string{"a"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestIntegrationUpdateTagsNormalized(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	rec := makeTorrent("tagsn1", domain.TorrentActive)
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatal(err)
	}

	// Duplicates and whitespace should be cleaned.
	if err := repo.UpdateTags(ctx, "tagsn1", []string{"HD", "  hd  ", "movie", "MOVIE"}); err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}

	got, _ := repo.Get(ctx, "tagsn1")
	if len(got.Tags) != 2 {
		t.Errorf("expected 2 normalized tags, got %v", got.Tags)
	}
}

// ---------------------------------------------------------------------------
// EnsureIndexes
// ---------------------------------------------------------------------------

func TestIntegrationEnsureIndexes(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	// EnsureIndexes was already called in setupTestRepo; call again to verify idempotency.
	if err := repo.EnsureIndexes(ctx); err != nil {
		t.Fatalf("second EnsureIndexes: %v", err)
	}

	// Verify indexes exist by listing them.
	cursor, err := repo.collection.Indexes().List(ctx)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	defer cursor.Close(ctx)

	var indexes []struct {
		Key map[string]interface{} `bson:"key"`
	}
	if err := cursor.All(ctx, &indexes); err != nil {
		t.Fatalf("decode indexes: %v", err)
	}

	// Expect: _id (default) + 5 custom = 6 indexes.
	if len(indexes) < 6 {
		t.Errorf("expected at least 6 indexes, got %d", len(indexes))
	}

	// Check that expected keys are present.
	expectedKeys := map[string]bool{"tags": false, "createdAt": false, "updatedAt": false, "progress": false}
	for _, idx := range indexes {
		for k := range idx.Key {
			if _, ok := expectedKeys[k]; ok {
				expectedKeys[k] = true
			}
			// Text index shows up as "_fts" key.
			if k == "_fts" {
				expectedKeys["_fts"] = true
			}
		}
	}
	for k, found := range expectedKeys {
		if !found && k != "_fts" {
			t.Errorf("missing index on field %q", k)
		}
	}
}

// ---------------------------------------------------------------------------
// UpdateProgress — cached progress field
// ---------------------------------------------------------------------------

func TestIntegrationUpdateProgressCachesProgressField(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	rec := makeTorrent("pcache1", domain.TorrentActive)
	rec.TotalBytes = 1000
	rec.DoneBytes = 0
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatal(err)
	}

	err := repo.UpdateProgress(ctx, "pcache1", domain.ProgressUpdate{
		DoneBytes:  750,
		TotalBytes: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Read the raw document to check the progress field.
	var doc torrentDoc
	if err := repo.collection.FindOne(ctx, map[string]string{"_id": "pcache1"}).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if math.Abs(doc.Progress-0.75) > 1e-9 {
		t.Errorf("cached progress: got %f, want 0.75", doc.Progress)
	}
}
