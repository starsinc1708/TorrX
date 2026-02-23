package apihttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"torrentstream/internal/domain"
)

var seriesHintPattern = regexp.MustCompile(`(?i)(S\d{1,3}E\d{1,3}|season\s*\d|episode\s*\d|\b\d{1,2}x\d{2}\b)`)

func detectContentType(filePath string) string {
	if seriesHintPattern.MatchString(filePath) {
		return "series"
	}
	return "movie"
}

func (s *Server) handleWatchHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.watchHistory == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "watch history not configured")
		return
	}

	limit, err := parsePositiveInt(r.URL.Query().Get("limit"), true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid limit")
		return
	}
	if limit <= 0 {
		limit = 20
	}

	status := strings.TrimSpace(r.URL.Query().Get("status"))

	var positions []domain.WatchPosition
	if status == "incomplete" {
		positions, err = s.watchHistory.ListIncomplete(r.Context(), limit)
	} else {
		positions, err = s.watchHistory.ListRecent(r.Context(), limit)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list watch history")
		return
	}

	writeJSON(w, http.StatusOK, positions)
}

func (s *Server) handleWatchHistoryByID(w http.ResponseWriter, r *http.Request) {
	if s.watchHistory == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "watch history not configured")
		return
	}

	tail := strings.TrimPrefix(r.URL.Path, "/watch-history/")
	parts := strings.SplitN(tail, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.NotFound(w, r)
		return
	}

	torrentID := domain.TorrentID(parts[0])
	fileIndex, err := strconv.Atoi(parts[1])
	if err != nil || fileIndex < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid fileIndex")
		return
	}

	switch r.Method {
	case http.MethodGet:
		pos, err := s.watchHistory.Get(r.Context(), torrentID, fileIndex)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "no watch position found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to get watch position")
			return
		}
		writeJSON(w, http.StatusOK, pos)

	case http.MethodPut:
		var body struct {
			Position    float64 `json:"position"`
			Duration    float64 `json:"duration"`
			TorrentName string  `json:"torrentName"`
			FilePath    string  `json:"filePath"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
			return
		}

		wp := domain.WatchPosition{
			TorrentID:   torrentID,
			FileIndex:   fileIndex,
			Position:    body.Position,
			Duration:    body.Duration,
			ContentType: detectContentType(body.FilePath),
			TorrentName: body.TorrentName,
			FilePath:    body.FilePath,
		}
		if err := s.watchHistory.Upsert(r.Context(), wp); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to save watch position")
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
