package apihttp

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"torrentstream/internal/app"
	"torrentstream/internal/domain"
	"torrentstream/internal/services/subtitles/opensubtitles"
)

// srtTimestampRe matches SRT-style timestamps with comma millisecond separator.
var srtTimestampRe = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)

// srtSequenceRe matches lines that are purely numeric (SRT sequence numbers).
var srtSequenceRe = regexp.MustCompile(`^\d+$`)

var subtitleProxyClient = &http.Client{Timeout: 30 * time.Second}

func (s *Server) handleSubtitleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetSubtitleSettings(w, r)
	case http.MethodPatch, http.MethodPut:
		s.handleUpdateSubtitleSettings(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetSubtitleSettings(w http.ResponseWriter, _ *http.Request) {
	if s.subtitles == nil {
		writeJSON(w, http.StatusOK, app.SubtitleSettings{})
		return
	}
	writeJSON(w, http.StatusOK, s.subtitles.Get())
}

func (s *Server) handleUpdateSubtitleSettings(w http.ResponseWriter, r *http.Request) {
	if s.subtitles == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "subtitle settings not configured")
		return
	}

	var body app.SubtitleSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}

	current := s.subtitles.Get()
	if body.APIKey == "" {
		body.APIKey = current.APIKey
	}
	if body.Languages == nil {
		body.Languages = current.Languages
	}

	if err := s.subtitles.Update(body); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update subtitle settings")
		return
	}
	writeJSON(w, http.StatusOK, s.subtitles.Get())
}

type subtitleSearchResponse struct {
	Results []opensubtitles.SubtitleResult `json:"results"`
}

func (s *Server) handleSubtitleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.subtitles == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "subtitle settings not configured")
		return
	}

	settings := s.subtitles.Get()
	if settings.APIKey == "" {
		writeError(w, http.StatusBadRequest, "no_api_key", "OpenSubtitles API key not configured")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("query"))
	hash := strings.TrimSpace(r.URL.Query().Get("hash"))
	langParam := strings.TrimSpace(r.URL.Query().Get("lang"))
	torrentID := strings.TrimSpace(r.URL.Query().Get("torrentId"))
	fileIndexParam := strings.TrimSpace(r.URL.Query().Get("fileIndex"))

	// When torrentId + fileIndex are provided and hash is empty, compute moviehash
	// from the file on disk and optionally derive a query from the filename.
	if torrentID != "" && fileIndexParam != "" && hash == "" {
		fileIndex, parseErr := strconv.Atoi(fileIndexParam)
		if parseErr == nil {
			state, stateErr := s.engine.GetSessionState(r.Context(), domain.TorrentID(torrentID))
			if stateErr == nil && fileIndex >= 0 && fileIndex < len(state.Files) {
				filePath, pathErr := resolveDataFilePath(s.mediaDataDir, state.Files[fileIndex].Path)
				if pathErr == nil {
					computed, hashErr := opensubtitles.ComputeMovieHash(filePath)
					if hashErr == nil {
						hash = computed
						slog.Debug("computed moviehash for subtitle search", "torrentId", torrentID, "fileIndex", fileIndex, "hash", hash)
					} else {
						slog.Warn("failed to compute moviehash", "error", hashErr)
					}
				}
				// If query is still empty, derive from filename.
				if query == "" {
					query = cleanFilenameForQuery(state.Files[fileIndex].Path)
				}
			}
		}
	}

	if query == "" && hash == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "query or hash required")
		return
	}

	var langs []string
	if langParam != "" {
		langs = strings.Split(langParam, ",")
	} else {
		langs = settings.Languages
	}

	client := opensubtitles.NewClient(settings.APIKey)

	var results []opensubtitles.SubtitleResult
	var err error

	if hash != "" {
		results, err = client.Search(r.Context(), opensubtitles.SearchParams{
			MovieHash: hash,
			Languages: langs,
		})
		if err != nil {
			slog.Error("subtitle search failed", "error", err)
			writeError(w, http.StatusBadGateway, "search_failed", "subtitle search failed")
			return
		}
	}

	if len(results) == 0 && query != "" {
		results, err = client.Search(r.Context(), opensubtitles.SearchParams{
			Query:     query,
			Languages: langs,
		})
		if err != nil {
			slog.Error("subtitle search failed", "error", err)
			writeError(w, http.StatusBadGateway, "search_failed", "subtitle search failed")
			return
		}
	}

	writeJSON(w, http.StatusOK, subtitleSearchResponse{Results: results})
}

// cleanFilenameForQuery extracts a search query from a file path by taking
// the base name, stripping the extension, and replacing dots and underscores
// with spaces.
func cleanFilenameForQuery(filePath string) string {
	base := filepath.Base(filePath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	name = strings.ReplaceAll(name, ".", " ")
	name = strings.ReplaceAll(name, "_", " ")
	return strings.TrimSpace(name)
}

type subtitleDownloadRequest struct {
	FileID int `json:"fileId"`
}

func (s *Server) handleSubtitleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.subtitles == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "subtitle settings not configured")
		return
	}

	settings := s.subtitles.Get()
	if settings.APIKey == "" {
		writeError(w, http.StatusBadRequest, "no_api_key", "OpenSubtitles API key not configured")
		return
	}

	var body subtitleDownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.FileID == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "fileId required")
		return
	}

	client := opensubtitles.NewClient(settings.APIKey)
	link, err := client.DownloadLink(r.Context(), body.FileID)
	if err != nil {
		slog.Error("subtitle download link failed", "error", err)
		writeError(w, http.StatusBadGateway, "download_failed", "subtitle download failed")
		return
	}

	// Fetch the actual subtitle file and proxy it as VTT.
	dlReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, link, nil)
	if err != nil {
		slog.Error("subtitle fetch request creation failed", "error", err)
		writeError(w, http.StatusBadGateway, "fetch_failed", "subtitle fetch failed")
		return
	}
	resp, err := subtitleProxyClient.Do(dlReq)
	if err != nil {
		slog.Error("subtitle fetch failed", "error", err)
		writeError(w, http.StatusBadGateway, "fetch_failed", "subtitle fetch failed")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("subtitle fetch non-200", "status", resp.StatusCode)
		writeError(w, http.StatusBadGateway, "fetch_failed", "subtitle fetch failed")
		return
	}

	// Limit body to 5MB to prevent memory issues.
	limited := io.LimitReader(resp.Body, 5<<20)

	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)

	// Convert SRT to VTT: write header, fix timestamps, strip sequence numbers.
	w.Write([]byte("WEBVTT\n\n"))
	scanner := bufio.NewScanner(limited)
	for scanner.Scan() {
		line := scanner.Text()
		// Skip SRT numeric sequence numbers.
		if srtSequenceRe.MatchString(strings.TrimSpace(line)) {
			continue
		}
		// Replace SRT timestamp commas with VTT periods.
		line = srtTimestampRe.ReplaceAllString(line, "${1}.${2}")
		w.Write([]byte(line + "\n"))
	}
	if err := scanner.Err(); err != nil {
		slog.Error("subtitle stream read error", "error", err)
	}
}
