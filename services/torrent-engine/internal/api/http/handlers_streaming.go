package apihttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"torrentstream/internal/domain"
)

func (s *Server) handleStreamTorrent(w http.ResponseWriter, r *http.Request, id string) {
	if s.streamTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "stream torrent use case not configured")
		return
	}
	if err := s.ensureStreamingAllowed(r.Context(), domain.TorrentID(id)); err != nil {
		writeDomainError(w, err)
		return
	}

	fileIndex, err := parsePositiveInt(r.URL.Query().Get("fileIndex"), false)
	if err != nil || fileIndex < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid fileIndex")
		return
	}

	ctx := r.Context()

	result, err := s.streamTorrent.Execute(ctx, domain.TorrentID(id), fileIndex)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if result.Reader == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "stream reader not available")
		return
	}
	defer result.Reader.Close()
	// HTTP streaming benefits from responsive mode: return partial data
	// immediately rather than blocking until full pieces are downloaded.
	result.Reader.SetResponsive()

	ext := strings.ToLower(path.Ext(result.File.Path))
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = fallbackContentType(ext)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Accept-Ranges", "bytes")

	size := result.File.Length

	// HEAD request: return headers only, no body.
	if r.Method == http.MethodHead {
		if size >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" {
		start, end, err := parseByteRange(rangeHeader, size)
		if errors.Is(err, errInvalidRange) {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid range")
			return
		}
		if errors.Is(err, errRangeNotSatisfiable) {
			if size >= 0 {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
			}
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}

		if _, err := result.Reader.Seek(start, io.SeekStart); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to seek stream")
			return
		}
		length := end - start + 1
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		w.WriteHeader(http.StatusPartialContent)
		if _, err := io.CopyN(w, result.Reader, length); err != nil {
			s.logger.Debug("stream range copy interrupted",
				slog.String("torrentId", id),
				slog.Int("fileIndex", fileIndex),
				slog.String("error", err.Error()),
			)
		}
		return
	}

	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, result.Reader); err != nil {
		s.logger.Debug("stream copy interrupted",
			slog.String("torrentId", id),
			slog.Int("fileIndex", fileIndex),
			slog.String("error", err.Error()),
		)
	}
}

func (s *Server) handleHLS(w http.ResponseWriter, r *http.Request, id string, tail []string) {
	if s.hls == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "hls not configured")
		return
	}
	if err := s.ensureStreamingAllowed(r.Context(), domain.TorrentID(id)); err != nil {
		writeDomainError(w, err)
		return
	}

	if len(tail) < 2 {
		http.NotFound(w, r)
		return
	}

	fileIndex, err := strconv.Atoi(tail[0])
	if err != nil || fileIndex < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid fileIndex")
		return
	}

	audioTrack, err := parseOptionalIntQuery(r.URL.Query().Get("audioTrack"), 0)
	if err != nil || audioTrack < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid audioTrack")
		return
	}
	subtitleTrack, err := parseOptionalIntQuery(r.URL.Query().Get("subtitleTrack"), -1)
	if err != nil || subtitleTrack < -1 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid subtitleTrack")
		return
	}

	segmentName := path.Join(tail[1:]...)
	key := hlsKey{
		id:            domain.TorrentID(id),
		fileIndex:     fileIndex,
		audioTrack:    audioTrack,
		subtitleTrack: subtitleTrack,
	}

	// Handle seek request: POST /torrents/{id}/hls/{fileIndex}/seek
	if segmentName == "seek" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.handleHLSSeek(w, r, domain.TorrentID(id), fileIndex, audioTrack, subtitleTrack)
		return
	}

	job, err := s.hls.ensureJob(domain.TorrentID(id), fileIndex, audioTrack, subtitleTrack)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to start hls")
		return
	}

	if segmentName == "index.m3u8" {
		select {
		case <-job.ready:
		case <-time.After(90 * time.Second):
			writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "hls playlist not ready")
			return
		}

		if job.err != nil {
			// The job may have been replaced by a seek — re-fetch the
			// current job before attempting an auto-restart.
			if current, _ := s.hls.ensureJob(domain.TorrentID(id), fileIndex, audioTrack, subtitleTrack); current != nil && current != job {
				job = current
				select {
				case <-job.ready:
				case <-time.After(90 * time.Second):
					writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "hls playlist not ready after seek")
					return
				}
			} else if restarted, ok := s.hls.tryAutoRestart(key, job, "request_error"); ok && restarted != nil {
				job = restarted
				select {
				case <-job.ready:
				case <-time.After(90 * time.Second):
					writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "hls playlist not ready after auto-restart")
					return
				}
			}
		}

		// When subtitle source is unavailable, fall back to transcoding
		// without subtitles instead of failing the entire request.
		if job.err != nil && errors.Is(job.err, errSubtitleSourceUnavailable) && subtitleTrack >= 0 {
			s.logger.Warn("hls subtitle source unavailable, falling back to no subtitles",
				slog.String("torrentId", id),
				slog.Int("fileIndex", fileIndex),
				slog.Int("requestedSubtitleTrack", subtitleTrack),
			)
			fallbackKey := hlsKey{
				id:            domain.TorrentID(id),
				fileIndex:     fileIndex,
				audioTrack:    audioTrack,
				subtitleTrack: -1,
			}
			key = fallbackKey
			subtitleTrack = -1
			job, err = s.hls.ensureJob(domain.TorrentID(id), fileIndex, audioTrack, -1)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "failed to start hls without subtitles")
				return
			}
			select {
			case <-job.ready:
			case <-time.After(90 * time.Second):
				writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "hls playlist not ready (subtitle fallback)")
				return
			}
		}

		if job.err != nil {
			// Return 503 for transient errors (context cancelled by seek, etc.)
			// so the player retries instead of treating it as a permanent failure.
			writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "hls stream error: "+job.err.Error())
			return
		}

		// For multi-variant jobs, job.playlist points to master.m3u8;
		// for single-variant it points to index.m3u8. Both are rewritten
		// with query params so the client forwards track selection.
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		if cached := cachedRewrittenPlaylist(job, job.playlist, audioTrack, subtitleTrack); cached != nil {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached)
			return
		}

		playlistBytes, err := os.ReadFile(job.playlist)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "playlist unavailable")
			return
		}
		playlistBytes = rewritePlaylistSegmentURLs(playlistBytes, audioTrack, subtitleTrack)
		storeRewrittenPlaylist(job, job.playlist, audioTrack, subtitleTrack, playlistBytes)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(playlistBytes)
		return
	}

	// Variant playlist request (e.g. v0/index.m3u8, v1/index.m3u8).
	if strings.HasSuffix(segmentName, ".m3u8") {
		variantPath, pathErr := safeSegmentPath(job.dir, segmentName)
		if pathErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid segment path")
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		if cached := cachedRewrittenPlaylist(job, variantPath, audioTrack, subtitleTrack); cached != nil {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached)
			return
		}

		playlistBytes, readErr := os.ReadFile(variantPath)
		if readErr != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "variant playlist unavailable")
			return
		}
		playlistBytes = rewritePlaylistSegmentURLs(playlistBytes, audioTrack, subtitleTrack)
		storeRewrittenPlaylist(job, variantPath, audioTrack, subtitleTrack, playlistBytes)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(playlistBytes)
		return
	}

	// Extract variant prefix for cache lookups (e.g. "v0" from "v0/seg-00001.ts").
	variant := ""
	if job.multiVariant {
		if idx := strings.IndexByte(segmentName, '/'); idx > 0 && segmentName[0] == 'v' {
			variant = segmentName[:idx]
		}
	}

	segmentPath, err := safeSegmentPath(job.dir, segmentName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid segment path")
		return
	}

	// 1. Try in-memory buffer (zero disk I/O).
	if s.hls.memBuf != nil {
		if data, ok := s.hls.memBuf.Get(segmentPath); ok {
			w.Header().Set("Content-Type", "video/MP2T")
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			http.ServeContent(w, r, segmentName, time.Time{}, bytes.NewReader(data))
			return
		}
	}

	// 2. Try serving from job working directory.
	if _, err := os.Stat(segmentPath); err != nil {
		// Segment not in job dir — try the HLS cache.
		if os.IsNotExist(err) && s.hls != nil && s.hls.cache != nil {
			if timeSec, ok := segmentTimeOffset(job, segmentName); ok {
				if cached, found := s.hls.cache.Lookup(string(id), fileIndex, audioTrack, subtitleTrack, variant, timeSec); found {
					// 3a. Check memBuf under cache path.
					if s.hls.memBuf != nil {
						if data, memOk := s.hls.memBuf.Get(cached.Path); memOk {
							w.Header().Set("Content-Type", "video/MP2T")
							w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
							http.ServeContent(w, r, segmentName, time.Time{}, bytes.NewReader(data))
							return
						}
					}
					// 3b. Serve from disk cache, async promote to memBuf.
					w.Header().Set("Content-Type", "video/MP2T")
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					http.ServeFile(w, r, cached.Path)
					if s.hls.memBuf != nil {
						go func(p string) {
							if raw, readErr := os.ReadFile(p); readErr == nil {
								s.hls.memBuf.Put(p, raw)
							}
						}(cached.Path)
					}
					return
				}
			}
			// Segment not yet produced by FFmpeg and not in cache.
			// Return 503 (not 404) so HLS.js treats this as a retryable
			// transient error rather than a permanent "not found".
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "segment not yet available")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "segment unavailable")
		return
	}

	w.Header().Set("Content-Type", "video/MP2T")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	http.ServeFile(w, r, segmentPath)
	// Async promote to memBuf so subsequent requests (re-watch, multi-client) hit RAM.
	if s.hls.memBuf != nil {
		go func(p string) {
			if raw, readErr := os.ReadFile(p); readErr == nil {
				s.hls.memBuf.Put(p, raw)
			}
		}(segmentPath)
	}
}

type seekResponse struct {
	SeekTime float64 `json:"seekTime"`
	SeekMode string  `json:"seekMode"`
}

func (s *Server) handleHLSSeek(w http.ResponseWriter, r *http.Request, id domain.TorrentID, fileIndex, audioTrack, subtitleTrack int) {
	timeStr := r.URL.Query().Get("time")
	if timeStr == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "time parameter is required")
		return
	}
	seekTime, err := strconv.ParseFloat(timeStr, 64)
	if err != nil || seekTime < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid time parameter")
		return
	}

	job, seekMode, err := s.hls.seekJob(id, fileIndex, audioTrack, subtitleTrack, seekTime)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to start hls seek")
		return
	}

	// Soft seek: no FFmpeg restart needed, return immediately.
	if seekMode == SeekModeSoft {
		writeJSON(w, http.StatusOK, seekResponse{
			SeekTime: seekTime,
			SeekMode: seekMode.String(),
		})
		return
	}

	// Wait briefly for the job to become ready so we can detect early
	// errors (e.g. subtitle source unavailable). If FFmpeg is still
	// starting after the short wait, return success anyway — HLS.js on
	// the client side will poll the manifest with its built-in retry
	// logic.  This avoids the previous 30s blocking wait which caused
	// the frontend to retry the seek endpoint, killing the in-progress
	// FFmpeg job and restarting it from scratch each time.
	select {
	case <-job.ready:
	case <-time.After(5 * time.Second):
		// Job still starting — return success; client will poll manifest.
		writeJSON(w, http.StatusOK, seekResponse{
			SeekTime: seekTime,
			SeekMode: seekMode.String(),
		})
		return
	}

	// When subtitle source is unavailable during seek, fall back to
	// transcoding without subtitles instead of failing entirely.
	if job.err != nil && errors.Is(job.err, errSubtitleSourceUnavailable) && subtitleTrack >= 0 {
		s.logger.Warn("hls seek subtitle source unavailable, falling back to no subtitles",
			slog.String("torrentId", string(id)),
			slog.Int("fileIndex", fileIndex),
			slog.Int("requestedSubtitleTrack", subtitleTrack),
			slog.Float64("seekTime", seekTime),
		)
		job, seekMode, err = s.hls.seekJob(id, fileIndex, audioTrack, -1, seekTime)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to start hls seek without subtitles")
			return
		}
		select {
		case <-job.ready:
		case <-time.After(5 * time.Second):
			writeJSON(w, http.StatusOK, seekResponse{
				SeekTime: seekTime,
				SeekMode: seekMode.String(),
			})
			return
		}
	}

	if job.err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", job.err.Error())
		return
	}

	writeJSON(w, http.StatusOK, seekResponse{
		SeekTime: seekTime,
		SeekMode: seekMode.String(),
	})
}

func (s *Server) handleMediaInfo(w http.ResponseWriter, r *http.Request, id string, tail []string) {
	const mediaProbeTimeout = 5 * time.Second

	if len(tail) != 1 {
		http.NotFound(w, r)
		return
	}
	if s.repo == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "repository not configured")
		return
	}

	fileIndex, err := strconv.Atoi(tail[0])
	if err != nil || fileIndex < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid fileIndex")
		return
	}

	record, err := s.repo.Get(r.Context(), domain.TorrentID(id))
	if err != nil {
		writeRepoError(w, err)
		return
	}

	// Do not fail the request when probing is unavailable or file is incomplete.
	// UI can still proceed with default playback mode.
	if s.mediaProbe == nil || s.mediaDataDir == "" {
		writeJSON(w, http.StatusOK, domain.MediaInfo{Tracks: []domain.MediaTrack{}})
		return
	}

	// Check in-memory probe cache first.
	cacheKey := mediaProbeCacheKey{torrentID: domain.TorrentID(id), fileIndex: fileIndex}
	if cached, ok := s.lookupMediaProbeCache(cacheKey); ok {
		// SubtitlesReady is dynamic (depends on file existence on disk), so
		// recompute it even on cache hit.
		if fileIndex < len(record.Files) && record.Files[fileIndex].Path != "" && s.mediaDataDir != "" {
			if resolved, resolveErr := resolveDataFilePath(s.mediaDataDir, record.Files[fileIndex].Path); resolveErr == nil {
				if info, statErr := os.Stat(resolved); statErr == nil && !info.IsDir() {
					cached.SubtitlesReady = true
				}
			}
		}
		writeJSON(w, http.StatusOK, cached)
		return
	}

	filePathRel := ""
	if fileIndex < len(record.Files) {
		filePathRel = record.Files[fileIndex].Path
	}

	// Records created before metadata availability can have empty Files.
	// Fallback to active stream session to resolve selected file path.
	if filePathRel == "" && s.streamTorrent != nil {
		result, streamErr := s.streamTorrent.Execute(r.Context(), domain.TorrentID(id), fileIndex)
		if streamErr != nil {
			writeDomainError(w, streamErr)
			return
		}
		if result.Reader != nil {
			_ = result.Reader.Close()
		}
		filePathRel = result.File.Path
	}

	if filePathRel == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid fileIndex")
		return
	}

	bestInfo := domain.MediaInfo{Tracks: []domain.MediaTrack{}}

	if filePathRel != "" {
		filePath, pathErr := resolveDataFilePath(s.mediaDataDir, filePathRel)
		if pathErr == nil {
			probeCtx, probeCancel := context.WithTimeout(r.Context(), mediaProbeTimeout)
			info, probeErr := s.mediaProbe.Probe(probeCtx, filePath)
			probeCancel()
			if probeErr != nil {
				if s.logger != nil {
					s.logger.Warn("media probe by path failed",
						slog.String("torrentId", id),
						slog.Int("fileIndex", fileIndex),
						slog.String("filePath", filePath),
						slog.String("error", probeErr.Error()),
					)
				}
			} else {
				bestInfo = info
			}
		}
	}

	// For partially downloaded MKV files, path-based ffprobe may expose only
	// a subset of streams. Probe from stream reader and keep richer result.
	if s.streamTorrent != nil && len(bestInfo.Tracks) <= 1 {
		streamCtx, streamCancel := context.WithTimeout(r.Context(), mediaProbeTimeout)
		result, streamErr := s.streamTorrent.Execute(streamCtx, domain.TorrentID(id), fileIndex)
		streamCancel()
		if streamErr == nil {
			if result.Reader != nil {
				probeReaderCtx, probeReaderCancel := context.WithTimeout(r.Context(), mediaProbeTimeout)
				streamInfo, probeReaderErr := s.mediaProbe.ProbeReader(probeReaderCtx, result.Reader)
				probeReaderCancel()
				_ = result.Reader.Close()
				if probeReaderErr != nil {
					if s.logger != nil {
						s.logger.Warn("media probe by stream failed",
							slog.String("torrentId", id),
							slog.Int("fileIndex", fileIndex),
							slog.String("error", probeReaderErr.Error()),
						)
					}
				} else if len(streamInfo.Tracks) > len(bestInfo.Tracks) {
					bestInfo = streamInfo
				}
			}
		} else if s.logger != nil {
			s.logger.Warn("media stream fallback failed",
				slog.String("torrentId", id),
				slog.Int("fileIndex", fileIndex),
				slog.String("error", streamErr.Error()),
			)
		}
	}

	// Subtitles require the file to exist on disk for ffmpeg -vf subtitles.
	if filePathRel != "" && s.mediaDataDir != "" {
		if resolved, err := resolveDataFilePath(s.mediaDataDir, filePathRel); err == nil {
			if info, statErr := os.Stat(resolved); statErr == nil && !info.IsDir() {
				bestInfo.SubtitlesReady = true
			}
		}
	}

	// Cache the probe result (without SubtitlesReady, which is recomputed on hit).
	// Only cache results with more than 1 track — partially downloaded files may
	// expose incomplete track lists that would become stale once more data arrives.
	if len(bestInfo.Tracks) > 1 {
		cachedInfo := bestInfo
		cachedInfo.SubtitlesReady = false
		s.storeMediaProbeCache(cacheKey, cachedInfo)
	}

	writeJSON(w, http.StatusOK, bestInfo)
}

// lookupMediaProbeCache returns a cached MediaInfo if present and not expired.
func (s *Server) lookupMediaProbeCache(key mediaProbeCacheKey) (domain.MediaInfo, bool) {
	s.mediaCacheMu.RLock()
	entry, ok := s.mediaProbeCache[key]
	s.mediaCacheMu.RUnlock()
	if !ok {
		return domain.MediaInfo{}, false
	}
	if time.Now().After(entry.expiresAt) {
		// Expired — remove lazily.
		s.mediaCacheMu.Lock()
		if existing, stillThere := s.mediaProbeCache[key]; stillThere && time.Now().After(existing.expiresAt) {
			delete(s.mediaProbeCache, key)
		}
		s.mediaCacheMu.Unlock()
		return domain.MediaInfo{}, false
	}
	return entry.info, true
}

// storeMediaProbeCache stores a MediaInfo result in the cache with TTL.
func (s *Server) storeMediaProbeCache(key mediaProbeCacheKey, info domain.MediaInfo) {
	s.mediaCacheMu.Lock()
	s.mediaProbeCache[key] = mediaProbeCacheEntry{
		info:      info,
		expiresAt: time.Now().Add(mediaProbeCacheTTL),
	}
	s.mediaCacheMu.Unlock()
}

// invalidateMediaProbeCache removes all cached probe entries for the given torrent ID.
func (s *Server) invalidateMediaProbeCache(id domain.TorrentID) {
	s.mediaCacheMu.Lock()
	for key := range s.mediaProbeCache {
		if key.torrentID == id {
			delete(s.mediaProbeCache, key)
		}
	}
	s.mediaCacheMu.Unlock()
}

func rewritePlaylistSegmentURLs(playlist []byte, audioTrack, subtitleTrack int) []byte {
	values := url.Values{}
	values.Set("audioTrack", strconv.Itoa(audioTrack))
	if subtitleTrack >= 0 {
		values.Set("subtitleTrack", strconv.Itoa(subtitleTrack))
	}
	query := values.Encode()
	if query == "" {
		return playlist
	}

	lines := strings.Split(string(playlist), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, "?") {
			lines[i] = line + "&" + query
		} else {
			lines[i] = line + "?" + query
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

// playlistCacheTTL is the duration for which a cached rewritten playlist is
// served without re-checking the source file mtime. This eliminates os.Stat
// calls in the hot path when HLS.js polls the manifest rapidly.
const playlistCacheTTL = 500 * time.Millisecond

// cachedRewrittenPlaylist returns the cached rewritten playlist if the
// source file hasn't changed and track parameters match. Returns nil on miss.
func cachedRewrittenPlaylist(job *hlsJob, playlistPath string, audioTrack, subtitleTrack int) []byte {
	job.rewrittenMu.RLock()
	defer job.rewrittenMu.RUnlock()

	if job.rewrittenPlaylist == nil ||
		job.rewrittenPlaylistPath != playlistPath ||
		job.rewrittenAudioTrack != audioTrack ||
		job.rewrittenSubTrack != subtitleTrack {
		return nil
	}
	// Within TTL, serve from cache without any disk I/O.
	if time.Since(job.rewrittenCacheTime) < playlistCacheTTL {
		return job.rewrittenPlaylist
	}
	// TTL expired — verify the underlying playlist file hasn't changed.
	info, err := os.Stat(playlistPath)
	if err != nil || info.ModTime().After(job.rewrittenPlaylistMod) {
		return nil
	}
	return job.rewrittenPlaylist
}

// storeRewrittenPlaylist caches the rewritten playlist bytes for future requests.
func storeRewrittenPlaylist(job *hlsJob, playlistPath string, audioTrack, subtitleTrack int, data []byte) {
	mod := time.Now()
	if info, err := os.Stat(playlistPath); err == nil {
		mod = info.ModTime()
	}

	job.rewrittenMu.Lock()
	job.rewrittenPlaylist = data
	job.rewrittenPlaylistPath = playlistPath
	job.rewrittenPlaylistMod = mod
	job.rewrittenAudioTrack = audioTrack
	job.rewrittenSubTrack = subtitleTrack
	job.rewrittenCacheTime = time.Now()
	job.rewrittenMu.Unlock()
}
