package opensubtitles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComputeMovieHash_Deterministic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "video.mkv")

	// Create a 256 KB file with deterministic content (byte = index % 256).
	const size = 256 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	hash1, err := ComputeMovieHash(path)
	if err != nil {
		t.Fatalf("first hash: %v", err)
	}
	if len(hash1) != 16 {
		t.Errorf("expected 16-char hex string, got %q (len %d)", hash1, len(hash1))
	}

	// Same file must produce the same hash.
	hash2, err := ComputeMovieHash(path)
	if err != nil {
		t.Fatalf("second hash: %v", err)
	}
	if hash1 != hash2 {
		t.Errorf("hashes differ: %q vs %q", hash1, hash2)
	}

	t.Logf("hash = %s", hash1)
}

func TestComputeMovieHash_FileTooSmall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiny.bin")

	// Create a file smaller than 128 KB.
	data := make([]byte, 100*1024) // 100 KB
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	_, err := ComputeMovieHash(path)
	if err == nil {
		t.Fatal("expected error for file smaller than 128 KB, got nil")
	}
	t.Logf("expected error: %v", err)
}

func TestComputeMovieHash_ExactMinimumSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exact.bin")

	// Create exactly 128 KB file (minimum valid size).
	const size = 128 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251) // use a prime modulus for variety
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	hash, err := ComputeMovieHash(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hash) != 16 {
		t.Errorf("expected 16-char hex string, got %q (len %d)", hash, len(hash))
	}
	t.Logf("hash = %s", hash)
}
