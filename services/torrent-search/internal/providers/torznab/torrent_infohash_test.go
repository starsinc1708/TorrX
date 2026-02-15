package torznab

import (
	"crypto/sha1"
	"encoding/hex"
	"testing"
)

func TestExtractInfoHashFromTorrent(t *testing.T) {
	// Minimal valid torrent: top-level dict containing "info" dict.
	info := []byte("d4:name4:test12:piece lengthi16384ee")
	payload := append([]byte("d4:info"), info...)
	payload = append(payload, 'e')

	wantBytes := sha1.Sum(info)
	want := hex.EncodeToString(wantBytes[:])

	got, err := ExtractInfoHashFromTorrent(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
