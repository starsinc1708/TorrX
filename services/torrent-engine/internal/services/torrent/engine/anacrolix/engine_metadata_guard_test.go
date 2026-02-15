package anacrolix

import "testing"

func TestTorrentInfoReadyNil(t *testing.T) {
	if torrentInfoReady(nil) {
		t.Fatalf("expected false for nil torrent")
	}
}

func TestMapFilesNil(t *testing.T) {
	files := mapFiles(nil)
	if len(files) != 0 {
		t.Fatalf("expected no files for nil torrent, got %d", len(files))
	}
}

