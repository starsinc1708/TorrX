package opensubtitles

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

const hashBlockSize = 65536 // 64 KB

// ComputeMovieHash computes the OpenSubtitles moviehash for a file on disk.
//
// Algorithm:
//  1. Take file size as uint64.
//  2. Read first 64 KB, interpret as 8192 little-endian uint64 values, sum them.
//  3. Read last 64 KB, interpret as 8192 little-endian uint64 values, sum them.
//  4. Add file size to the running sum.
//  5. Return as 16-char zero-padded lowercase hex string.
//
// Returns an error if the file is smaller than 128 KB (2 * hashBlockSize).
func ComputeMovieHash(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("moviehash: open: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("moviehash: stat: %w", err)
	}

	size := fi.Size()
	if size < 2*hashBlockSize {
		return "", fmt.Errorf("moviehash: file too small (%d bytes, need at least %d)", size, 2*hashBlockSize)
	}

	hash := uint64(size)

	// Sum first 64 KB.
	hash, err = sumBlock(f, hash)
	if err != nil {
		return "", fmt.Errorf("moviehash: head block: %w", err)
	}

	// Seek to last 64 KB.
	if _, err := f.Seek(-hashBlockSize, io.SeekEnd); err != nil {
		return "", fmt.Errorf("moviehash: seek tail: %w", err)
	}

	// Sum last 64 KB.
	hash, err = sumBlock(f, hash)
	if err != nil {
		return "", fmt.Errorf("moviehash: tail block: %w", err)
	}

	return fmt.Sprintf("%016x", hash), nil
}

// sumBlock reads hashBlockSize bytes from r, interprets them as little-endian
// uint64 values, and adds each to the running hash. Returns the updated hash.
func sumBlock(r io.Reader, hash uint64) (uint64, error) {
	buf := make([]byte, hashBlockSize)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, err
	}
	for i := 0; i < hashBlockSize; i += 8 {
		hash += binary.LittleEndian.Uint64(buf[i : i+8])
	}
	return hash, nil
}
