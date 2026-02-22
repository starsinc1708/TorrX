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

func TestBitfieldHasPiece(t *testing.T) {
	mask := []byte{0b1010_0000} // piece 0 and 2 are complete
	if !bitfieldHasPiece(mask, 0) {
		t.Fatalf("piece 0 should be complete")
	}
	if bitfieldHasPiece(mask, 1) {
		t.Fatalf("piece 1 should be incomplete")
	}
	if !bitfieldHasPiece(mask, 2) {
		t.Fatalf("piece 2 should be complete")
	}
	if bitfieldHasPiece(mask, 9) {
		t.Fatalf("piece 9 should be out of range/incomplete")
	}
}
