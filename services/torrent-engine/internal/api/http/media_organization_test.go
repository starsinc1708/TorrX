package apihttp

import (
	"testing"

	"torrentstream/internal/domain"
)

func TestBuildMediaOrganizationSeries(t *testing.T) {
	files := []domain.FileRef{
		{Index: 0, Path: "Show/S01E01.mkv", Length: 100},
		{Index: 1, Path: "Show/S01E02.mkv", Length: 100},
		{Index: 2, Path: "Show/S02E01.mkv", Length: 100},
	}

	org := buildMediaOrganization(files)
	if org == nil {
		t.Fatal("organization should not be nil")
	}
	if org.ContentType != "series" {
		t.Fatalf("contentType = %q, want series", org.ContentType)
	}
	if len(org.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(org.Groups))
	}
	if org.Groups[0].Type != "series" || org.Groups[0].Season != 1 {
		t.Fatalf("group[0] mismatch: %+v", org.Groups[0])
	}
	if len(org.Groups[0].Items) != 2 {
		t.Fatalf("season 1 items = %d, want 2", len(org.Groups[0].Items))
	}
	if org.Groups[0].Items[0].Episode != 1 {
		t.Fatalf("first episode = %d, want 1", org.Groups[0].Items[0].Episode)
	}
}

func TestBuildMediaOrganizationMixed(t *testing.T) {
	files := []domain.FileRef{
		{Index: 0, Path: "Movie.2024.1080p.mkv", Length: 100},
		{Index: 1, Path: "Series/S01E01.mkv", Length: 100},
		{Index: 2, Path: "sample.txt", Length: 100},
	}

	org := buildMediaOrganization(files)
	if org == nil {
		t.Fatal("organization should not be nil")
	}
	if org.ContentType != "mixed" {
		t.Fatalf("contentType = %q, want mixed", org.ContentType)
	}
	if len(org.Groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(org.Groups))
	}
}

func TestBuildTorrentRecordViewIncludesOrganization(t *testing.T) {
	record := domain.TorrentRecord{
		ID: "t1",
		Files: []domain.FileRef{
			{Index: 0, Path: "Movie.2024.mkv", Length: 100},
		},
	}

	view := buildTorrentRecordView(record)
	if view.MediaOrganization == nil {
		t.Fatal("mediaOrganization should be set")
	}
	if view.MediaOrganization.ContentType != "movie" {
		t.Fatalf("contentType = %q, want movie", view.MediaOrganization.ContentType)
	}
}
