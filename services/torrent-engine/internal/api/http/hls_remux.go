package apihttp

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"torrentstream/internal/domain"
)

// remuxEntry tracks a background FFmpeg remux (MKV → MP4 codec copy).
type remuxEntry struct {
	path    string       // absolute path to the output .mp4
	ready   chan struct{} // closed when remux is complete (check err)
	err     error
	started time.Time
}

// remuxCacheKey returns a unique key for the remux cache.
func remuxCacheKey(id domain.TorrentID, fileIndex int) string {
	return string(id) + "/" + strconv.Itoa(fileIndex)
}

// getRemuxPath returns the on-disk path for a cached remux MP4.
func (m *hlsManager) getRemuxPath(id domain.TorrentID, fileIndex int) string {
	return filepath.Join(m.baseDir, "remux", string(id), strconv.Itoa(fileIndex)+".mp4")
}

// checkRemux returns the remux MP4 path and whether it is ready to serve.
func (m *hlsManager) checkRemux(id domain.TorrentID, fileIndex int) (path string, ready bool) {
	key := remuxCacheKey(id, fileIndex)

	m.remuxCacheMu.Lock()
	entry, ok := m.remuxCache[key]
	m.remuxCacheMu.Unlock()

	if !ok {
		// No in-memory entry — check if a previous run left a completed file on disk.
		p := m.getRemuxPath(id, fileIndex)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Size() > 0 {
			// Populate cache so subsequent checks are fast.
			e := &remuxEntry{path: p, ready: make(chan struct{}), started: info.ModTime()}
			close(e.ready)
			m.remuxCacheMu.Lock()
			m.remuxCache[key] = e
			m.remuxCacheMu.Unlock()
			return p, true
		}
		return "", false
	}

	select {
	case <-entry.ready:
		if entry.err != nil {
			return "", false
		}
		return entry.path, true
	default:
		// Still in progress.
		return entry.path, false
	}
}

// triggerRemux starts a background FFmpeg codec-copy remux from inputPath (MKV)
// to an MP4 file. If a remux is already running or complete for this key, it
// returns immediately.
func (m *hlsManager) triggerRemux(id domain.TorrentID, fileIndex int, inputPath string) {
	key := remuxCacheKey(id, fileIndex)

	m.remuxCacheMu.Lock()
	if _, ok := m.remuxCache[key]; ok {
		m.remuxCacheMu.Unlock()
		return // already running or complete
	}

	outPath := m.getRemuxPath(id, fileIndex)
	entry := &remuxEntry{
		path:    outPath,
		ready:   make(chan struct{}),
		started: time.Now(),
	}
	m.remuxCache[key] = entry
	m.remuxCacheMu.Unlock()

	go m.runRemux(entry, inputPath, key)
}

// runRemux executes the FFmpeg remux and closes entry.ready when done.
func (m *hlsManager) runRemux(entry *remuxEntry, inputPath, cacheKey string) {
	defer close(entry.ready)

	outDir := filepath.Dir(entry.path)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		entry.err = fmt.Errorf("remux mkdir: %w", err)
		m.logger.Warn("remux mkdir failed", slog.String("error", err.Error()))
		return
	}

	// Write to a temp file first, then rename for atomicity.
	tmpPath := entry.path + ".tmp"

	// Determine audio codec of source to decide copy vs re-encode.
	audioArgs := []string{"-c:a", "aac", "-b:a", "128k"}
	if m.isAACAudioWithCache(inputPath) {
		audioArgs = []string{"-c:a", "copy"}
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-i", inputPath,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-c:v", "copy",
	}
	args = append(args, audioArgs...)
	args = append(args,
		"-movflags", "+faststart",
		"-y",
		tmpPath,
	)

	m.logger.Info("remux starting",
		slog.String("input", inputPath),
		slog.String("output", entry.path),
	)

	start := time.Now()
	cmd := exec.Command(m.ffmpegPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpPath)
		stderrMsg := strings.TrimSpace(stderr.String())
		entry.err = fmt.Errorf("remux ffmpeg: %w: %s", err, stderrMsg)
		m.logger.Warn("remux failed",
			slog.String("input", inputPath),
			slog.String("error", err.Error()),
			slog.String("stderr", stderrMsg),
		)
		// Remove failed entry so a retry is possible.
		m.remuxCacheMu.Lock()
		if current, ok := m.remuxCache[cacheKey]; ok && current == entry {
			delete(m.remuxCache, cacheKey)
		}
		m.remuxCacheMu.Unlock()
		return
	}

	// Atomic rename.
	if err := os.Rename(tmpPath, entry.path); err != nil {
		_ = os.Remove(tmpPath)
		entry.err = fmt.Errorf("remux rename: %w", err)
		m.logger.Warn("remux rename failed", slog.String("error", err.Error()))
		return
	}

	m.logger.Info("remux complete",
		slog.String("output", entry.path),
		slog.Duration("elapsed", time.Since(start)),
	)
}
