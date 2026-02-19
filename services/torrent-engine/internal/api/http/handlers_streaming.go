package apihttp

import (
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
	"path/filepath"
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

	// Fast path: serve complete files directly from disk, bypassing the
	// torrent reader. This gives better Range handling, ETag support, and
	// kernel-level sendfile. Works for both active and stopped/completed
	// torrents by falling back to the repository when the live session is
	// unavailable (see resolveFileRef).
	if s.mediaDataDir != "" {
		if file, ok := s.resolveFileRef(r.Context(), domain.TorrentID(id), fileIndex); ok {
			if file.Length > 0 && file.BytesCompleted >= file.Length {
				filePath, pathErr := resolveDataFilePath(s.mediaDataDir, file.Path)
				if pathErr == nil {
					if info, statErr := os.Stat(filePath); statErr == nil && !info.IsDir() {
						http.ServeFile(w, r, filePath)
						return
					}
				}
			}
		}
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
	// Do NOT use SetResponsive() for direct HTTP streaming: the responsive
	// reader returns EOF when piece data isn't available, which causes
	// io.Copy to terminate prematurely and silently truncate the stream.
	// Instead, let the reader block until pieces arrive (like TorrServer).
	// The HLS path uses SetResponsive() + bufferedStreamReader which has
	// retry logic for transient EOFs.

	ext := strings.ToLower(path.Ext(result.File.Path))
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = fallbackContentType(ext)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Accept-Ranges", "bytes")
	// Close the connection after streaming to prevent keep-alive from holding
	// the reader open after the player stops playback.
	w.Header().Set("Connection", "close")

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

	// Handle seek request: POST /torrents/{id}/hls/{fileIndex}/seek
	if segmentName == "seek" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.handleHLSSeek(w, r, domain.TorrentID(id), fileIndex, audioTrack, subtitleTrack)
		return
	}

	job, err := s.hls.EnsureJob(domain.TorrentID(id), fileIndex, audioTrack, subtitleTrack)
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
			// The job may have been replaced by a seek — re-fetch.
			if current, _ := s.hls.EnsureJob(domain.TorrentID(id), fileIndex, audioTrack, subtitleTrack); current != nil && current != job {
				job = current
				select {
				case <-job.ready:
				case <-time.After(90 * time.Second):
					writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "hls playlist not ready after seek")
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
			subtitleTrack = -1
			job, err = s.hls.EnsureJob(domain.TorrentID(id), fileIndex, audioTrack, -1)
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
			writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "hls stream error: "+job.err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		if cached := cachedRewrittenPlaylistStream(job, job.playlist, audioTrack, subtitleTrack); cached != nil {
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
		storeRewrittenPlaylistStream(job, job.playlist, audioTrack, subtitleTrack, playlistBytes)
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

		if cached := cachedRewrittenPlaylistStream(job, variantPath, audioTrack, subtitleTrack); cached != nil {
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
		storeRewrittenPlaylistStream(job, variantPath, audioTrack, subtitleTrack, playlistBytes)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(playlistBytes)
		return
	}

	// Serve segment from job working directory.
	segmentPath, err := safeSegmentPath(job.dir, segmentName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid segment path")
		return
	}

	if _, err := os.Stat(segmentPath); err != nil {
		// Segment not yet produced by FFmpeg.
		// Return 503 so HLS.js treats this as a retryable transient error.
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "segment not yet available")
		return
	}

	w.Header().Set("Content-Type", "video/MP2T")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	http.ServeFile(w, r, segmentPath)
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

	job, seekMode, err := s.hls.SeekJob(id, fileIndex, audioTrack, subtitleTrack, seekTime)
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
		job, seekMode, err = s.hls.SeekJob(id, fileIndex, audioTrack, -1, seekTime)
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

// cachedRewrittenPlaylistStream returns the cached rewritten playlist if the
func cachedRewrittenPlaylistStream(job *StreamJob, playlistPath string, audioTrack, subtitleTrack int) []byte {
	job.rewrittenMu.RLock()
	defer job.rewrittenMu.RUnlock()

	if job.rewrittenPlaylist == nil ||
		job.rewrittenPlaylistPath != playlistPath ||
		job.rewrittenAudioTrack != audioTrack ||
		job.rewrittenSubTrack != subtitleTrack {
		return nil
	}
	if time.Since(job.rewrittenCacheTime) < playlistCacheTTL {
		return job.rewrittenPlaylist
	}
	info, err := os.Stat(playlistPath)
	if err != nil || info.ModTime().After(job.rewrittenPlaylistMod) {
		return nil
	}
	return job.rewrittenPlaylist
}

// storeRewrittenPlaylistStream is the StreamJob variant of storeRewrittenPlaylist.
func storeRewrittenPlaylistStream(job *StreamJob, playlistPath string, audioTrack, subtitleTrack int, data []byte) {
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

// resolveFileRef returns the FileRef for the given fileIndex from the live
// engine session (most accurate BytesCompleted) or, if the torrent is not
// active, from the persisted repository record. This allows the fast path
// and direct-playback handler to work for stopped/completed torrents.
func (s *Server) resolveFileRef(ctx context.Context, id domain.TorrentID, fileIndex int) (domain.FileRef, bool) {
	if s.getState != nil {
		if state, err := s.getState.Execute(ctx, id); err == nil {
			if fileIndex < len(state.Files) {
				return state.Files[fileIndex], true
			}
		}
	}
	if s.repo != nil {
		if record, err := s.repo.Get(ctx, id); err == nil {
			if fileIndex < len(record.Files) {
				return record.Files[fileIndex], true
			}
		}
	}
	return domain.FileRef{}, false
}

// handleDirectPlayback serves a browser-ready file for direct playback.
// For .mp4/.m4v files it serves the original. For .mkv files with H.264+AAC
// it serves a cached remux (codec copy to MP4). Returns:
//   - 200: file ready to serve
//   - 202: remux in progress (retry later)
//   - 404: not available (incomplete file or unsupported format)
func (s *Server) handleDirectPlayback(w http.ResponseWriter, r *http.Request, id string, fileIndex int) {
	if s.mediaDataDir == "" {
		http.NotFound(w, r)
		return
	}

	file, ok := s.resolveFileRef(r.Context(), domain.TorrentID(id), fileIndex)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if file.Length <= 0 || file.BytesCompleted < file.Length {
		http.NotFound(w, r)
		return
	}

	filePath, pathErr := resolveDataFilePath(s.mediaDataDir, file.Path)
	if pathErr != nil {
		http.NotFound(w, r)
		return
	}
	if info, statErr := os.Stat(filePath); statErr != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	ext := strings.ToLower(filepath.Ext(filePath))

	// MP4/M4V: serve directly — already browser-compatible.
	if ext == ".mp4" || ext == ".m4v" {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.ServeFile(w, r, filePath)
		return
	}

	// MKV: check if H.264 video — required for browser playback.
	if ext != ".mkv" {
		http.NotFound(w, r)
		return
	}

	if s.hls == nil {
		http.NotFound(w, r)
		return
	}

	if !s.hls.isH264FileWithCache(filePath) {
		http.NotFound(w, r)
		return
	}

	// Check if a remuxed MP4 is ready.
	remuxPath, ready := s.hls.checkRemux(domain.TorrentID(id), fileIndex)
	if ready {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.ServeFile(w, r, remuxPath)
		return
	}

	// Trigger remux if not started.
	if remuxPath == "" {
		s.hls.triggerRemux(domain.TorrentID(id), fileIndex, filePath)
	}

	// 202 Accepted — remux in progress.
	w.Header().Set("Retry-After", "3")
	w.WriteHeader(http.StatusAccepted)
}
