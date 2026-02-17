package apihttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/usecase"
)

func (s *Server) handleTorrents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateTorrent(w, r)
	case http.MethodGet:
		s.handleListTorrents(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCreateTorrent(w http.ResponseWriter, r *http.Request) {
	if s.createTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "create torrent use case not configured")
		return
	}

	contentType := r.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = ""
	}

	switch mediaType {
	case "application/json":
		s.handleCreateTorrentJSON(w, r)
	case "multipart/form-data":
		s.handleCreateTorrentMultipart(w, r)
	default:
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "unsupported content type")
	}
}

type createTorrentJSON struct {
	Magnet string `json:"magnet"`
	Name   string `json:"name,omitempty"`
}

func (s *Server) handleCreateTorrentJSON(w http.ResponseWriter, r *http.Request) {
	var body createTorrentJSON
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}

	input := usecase.CreateTorrentInput{
		Source: domain.TorrentSource{Magnet: strings.TrimSpace(body.Magnet)},
		Name:   strings.TrimSpace(body.Name),
	}

	// Cap the handler execution time so we never block indefinitely.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	record, err := s.createTorrent.Execute(ctx, input)
	if err != nil {
		writeUseCaseError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, record)
}

func (s *Server) handleCreateTorrentMultipart(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 5 << 20 // 5 MB — sufficient for .torrent files
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid multipart form")
		return
	}

	file, header, err := r.FormFile("torrent")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "missing torrent file")
		return
	}
	defer file.Close()

	path, err := saveUploadedFile(file, header.Filename, s.mediaDataDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to store torrent file")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	input := usecase.CreateTorrentInput{
		Source: domain.TorrentSource{Torrent: path},
		Name:   name,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	record, err := s.createTorrent.Execute(ctx, input)
	if err != nil {
		writeUseCaseError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, record)
}

type torrentSummary struct {
	ID         domain.TorrentID     `json:"id"`
	Name       string               `json:"name"`
	Status     domain.TorrentStatus `json:"status"`
	Progress   float64              `json:"progress"`
	DoneBytes  int64                `json:"doneBytes"`
	TotalBytes int64                `json:"totalBytes"`
	CreatedAt  time.Time            `json:"createdAt"`
	UpdatedAt  time.Time            `json:"updatedAt"`
	Tags       []string             `json:"tags,omitempty"`
}

type torrentListSummary struct {
	Items []torrentSummary `json:"items"`
	Count int              `json:"count"`
}

type torrentListFull struct {
	Items []domain.TorrentRecord `json:"items"`
	Count int                    `json:"count"`
}

func (s *Server) handleListTorrents(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "repository not configured")
		return
	}

	status, err := parseStatus(r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid status")
		return
	}

	view := strings.TrimSpace(r.URL.Query().Get("view"))
	if view == "" {
		view = "summary"
	}
	if view != "summary" && view != "full" {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid view")
		return
	}

	search := strings.TrimSpace(r.URL.Query().Get("search"))
	tags := parseCommaSeparated(r.URL.Query().Get("tags"))
	sortBy := strings.TrimSpace(r.URL.Query().Get("sortBy"))
	if sortBy == "" {
		sortBy = "updatedAt"
	}
	if !isAllowedSortBy(sortBy) {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid sortBy")
		return
	}
	sortOrder, err := parseSortOrder(r.URL.Query().Get("sortOrder"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid sortOrder")
		return
	}

	limit, err := parsePositiveInt(r.URL.Query().Get("limit"), true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid limit")
		return
	}
	offset, err := parsePositiveInt(r.URL.Query().Get("offset"), false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid offset")
		return
	}
	if offset < 0 {
		offset = 0
	}

	const maxLimit = 1000
	if limit > maxLimit {
		limit = maxLimit
	}

	filter := domain.TorrentFilter{
		Status:    status,
		Search:    search,
		Tags:      tags,
		SortBy:    sortBy,
		SortOrder: sortOrder,
	}
	if limit > 0 {
		filter.Limit = limit
	}
	if offset > 0 {
		filter.Offset = offset
	}
	records, err := s.repo.List(r.Context(), filter)
	if err != nil {
		writeRepoError(w, err)
		return
	}

	if view == "full" {
		writeJSON(w, http.StatusOK, torrentListFull{Items: records, Count: len(records)})
		return
	}

	summaries := make([]torrentSummary, 0, len(records))
	for _, record := range records {
		summaries = append(summaries, torrentSummary{
			ID:         record.ID,
			Name:       record.Name,
			Status:     record.Status,
			Progress:   progressRatio(record.DoneBytes, record.TotalBytes),
			DoneBytes:  record.DoneBytes,
			TotalBytes: record.TotalBytes,
			CreatedAt:  record.CreatedAt,
			UpdatedAt:  record.UpdatedAt,
			Tags:       record.Tags,
		})
	}

	writeJSON(w, http.StatusOK, torrentListSummary{Items: summaries, Count: len(summaries)})
}

func (s *Server) handleTorrentByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/torrents/")
	if path == "" {
		http.NotFound(w, r)
		return
	}

	if path == "state" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.handleListTorrentStates(w, r)
		return
	}

	if path == "unfocus" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.handleUnfocus(w, r)
		return
	}

	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[0] == "bulk" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch parts[1] {
		case "start":
			s.handleBulkStart(w, r)
		case "stop":
			s.handleBulkStop(w, r)
		case "delete":
			s.handleBulkDelete(w, r)
		default:
			http.NotFound(w, r)
		}
		return
	}

	if len(parts) == 1 {
		id := parts[0]
		if id == "" {
			http.NotFound(w, r)
			return
		}

		switch r.Method {
		case http.MethodGet:
			s.handleGetTorrent(w, r, id)
		case http.MethodDelete:
			s.handleDeleteTorrent(w, r, id)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	if len(parts) >= 2 {
		id := parts[0]
		action := parts[1]
		if id == "" || action == "" {
			http.NotFound(w, r)
			return
		}
		switch action {
		case "start":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleStartTorrent(w, r, id)
		case "stop":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleStopTorrent(w, r, id)
		case "stream":
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleStreamTorrent(w, r, id)
		case "state":
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleGetTorrentState(w, r, id)
		case "hls":
			if r.Method != http.MethodGet && r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleHLS(w, r, id, parts[2:])
		case "media":
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleMediaInfo(w, r, id, parts[2:])
		case "focus":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleFocus(w, r, id)
		case "tags":
			if r.Method != http.MethodPut {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.handleUpdateTags(w, r, id)
		default:
			http.NotFound(w, r)
		}
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleGetTorrent(w http.ResponseWriter, r *http.Request, id string) {
	if s.repo == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "repository not configured")
		return
	}

	record, err := s.repo.Get(r.Context(), domain.TorrentID(id))
	if err != nil {
		writeRepoError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, record)
}

func (s *Server) handleStartTorrent(w http.ResponseWriter, r *http.Request, id string) {
	if s.startTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "start torrent use case not configured")
		return
	}

	record, err := s.startTorrent.Execute(r.Context(), domain.TorrentID(id))
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, record)
}

func (s *Server) handleStopTorrent(w http.ResponseWriter, r *http.Request, id string) {
	if s.stopTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "stop torrent use case not configured")
		return
	}

	record, err := s.stopTorrent.Execute(r.Context(), domain.TorrentID(id))
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, record)
}

func (s *Server) handleDeleteTorrent(w http.ResponseWriter, r *http.Request, id string) {
	if s.deleteTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "delete torrent use case not configured")
		return
	}

	deleteFiles, err := parseBoolQuery(r.URL.Query().Get("deleteFiles"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid deleteFiles value")
		return
	}

	if err := s.deleteTorrent.Execute(r.Context(), domain.TorrentID(id), deleteFiles); err != nil {
		writeDomainError(w, err)
		return
	}

	if s.hls != nil && s.hls.cache != nil {
		s.hls.cache.PurgeTorrent(id)
	}
	if s.hls != nil && s.hls.memBuf != nil {
		s.hls.memBuf.PurgePrefix(filepath.Join(s.hls.baseDir, id))
		s.hls.memBuf.PurgePrefix(filepath.Join(s.hls.cache.BaseDir(), id))
	}

	s.invalidateMediaProbeCache(domain.TorrentID(id))

	w.WriteHeader(http.StatusNoContent)
}

type updateTagsRequest struct {
	Tags []string `json:"tags"`
}

type bulkRequest struct {
	IDs         []string `json:"ids"`
	DeleteFiles bool     `json:"deleteFiles"`
}

type bulkResultItem struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type bulkResponse struct {
	Items []bulkResultItem `json:"items"`
}

func (s *Server) handleUpdateTags(w http.ResponseWriter, r *http.Request, id string) {
	if s.repo == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "repository not configured")
		return
	}

	var body updateTagsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}

	if err := s.repo.UpdateTags(r.Context(), domain.TorrentID(id), body.Tags); err != nil {
		writeRepoError(w, err)
		return
	}
	record, err := s.repo.Get(r.Context(), domain.TorrentID(id))
	if err != nil {
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) handleBulkStart(w http.ResponseWriter, r *http.Request) {
	if s.startTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "start torrent use case not configured")
		return
	}
	req, ok := decodeBulkRequest(w, r)
	if !ok {
		return
	}

	results := make([]bulkResultItem, 0, len(req.IDs))
	for _, rawID := range req.IDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			results = append(results, bulkResultItem{ID: rawID, OK: false, Error: "empty id"})
			continue
		}
		if _, err := s.startTorrent.Execute(r.Context(), domain.TorrentID(id)); err != nil {
			results = append(results, bulkResultItem{ID: id, OK: false, Error: err.Error()})
			continue
		}
		results = append(results, bulkResultItem{ID: id, OK: true})
	}
	writeJSON(w, http.StatusOK, bulkResponse{Items: results})
}

func (s *Server) handleBulkStop(w http.ResponseWriter, r *http.Request) {
	if s.stopTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "stop torrent use case not configured")
		return
	}
	req, ok := decodeBulkRequest(w, r)
	if !ok {
		return
	}

	results := make([]bulkResultItem, 0, len(req.IDs))
	for _, rawID := range req.IDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			results = append(results, bulkResultItem{ID: rawID, OK: false, Error: "empty id"})
			continue
		}
		if _, err := s.stopTorrent.Execute(r.Context(), domain.TorrentID(id)); err != nil {
			results = append(results, bulkResultItem{ID: id, OK: false, Error: err.Error()})
			continue
		}
		results = append(results, bulkResultItem{ID: id, OK: true})
	}
	writeJSON(w, http.StatusOK, bulkResponse{Items: results})
}

func (s *Server) handleBulkDelete(w http.ResponseWriter, r *http.Request) {
	if s.deleteTorrent == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "delete torrent use case not configured")
		return
	}
	req, ok := decodeBulkRequest(w, r)
	if !ok {
		return
	}

	results := make([]bulkResultItem, 0, len(req.IDs))
	for _, rawID := range req.IDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			results = append(results, bulkResultItem{ID: rawID, OK: false, Error: "empty id"})
			continue
		}
		if err := s.deleteTorrent.Execute(r.Context(), domain.TorrentID(id), req.DeleteFiles); err != nil {
			results = append(results, bulkResultItem{ID: id, OK: false, Error: err.Error()})
			continue
		}
		if s.hls != nil && s.hls.cache != nil {
			s.hls.cache.PurgeTorrent(id)
		}
		if s.hls != nil && s.hls.memBuf != nil {
			s.hls.memBuf.PurgePrefix(filepath.Join(s.hls.baseDir, id))
			s.hls.memBuf.PurgePrefix(filepath.Join(s.hls.cache.BaseDir(), id))
		}
		s.invalidateMediaProbeCache(domain.TorrentID(id))
		results = append(results, bulkResultItem{ID: id, OK: true})
	}
	writeJSON(w, http.StatusOK, bulkResponse{Items: results})
}

const maxBulkIDs = 100

func decodeBulkRequest(w http.ResponseWriter, r *http.Request) (bulkRequest, bool) {
	var req bulkRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return bulkRequest{}, false
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "ids is required")
		return bulkRequest{}, false
	}
	if len(req.IDs) > maxBulkIDs {
		writeError(w, http.StatusBadRequest, "invalid_request",
			fmt.Sprintf("too many ids (max %d)", maxBulkIDs))
		return bulkRequest{}, false
	}
	return req, true
}

type torrentStateList struct {
	Items []domain.SessionState `json:"items"`
	Count int                   `json:"count"`
}

func (s *Server) handleGetTorrentState(w http.ResponseWriter, r *http.Request, id string) {
	if s.getState == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "torrent state use case not configured")
		return
	}

	state, err := s.getState.Execute(r.Context(), domain.TorrentID(id))
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleListTorrentStates(w http.ResponseWriter, r *http.Request) {
	if s.listStates == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "torrent state list use case not configured")
		return
	}

	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" || status != string(domain.TorrentActive) {
		writeError(w, http.StatusBadRequest, "invalid_request", "status must be active")
		return
	}

	states, err := s.listStates.Execute(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, torrentStateList{Items: states, Count: len(states)})
}

// Focus/unfocus handlers.

func (s *Server) handleFocus(w http.ResponseWriter, r *http.Request, id string) {
	if s.player != nil {
		if err := s.setCurrentTorrentID(r.Context(), domain.TorrentID(id)); err != nil {
			writeDomainError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.engine == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "engine not configured")
		return
	}
	if err := s.engine.FocusSession(r.Context(), domain.TorrentID(id)); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnfocus(w http.ResponseWriter, r *http.Request) {
	if s.player != nil {
		if err := s.setCurrentTorrentID(r.Context(), ""); err != nil {
			writeDomainError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.engine == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "engine not configured")
		return
	}
	if err := s.engine.UnfocusAll(r.Context()); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setCurrentTorrentID(ctx context.Context, id domain.TorrentID) error {
	if s.player == nil {
		return domain.ErrUnsupported
	}
	if err := s.player.SetCurrentTorrentID(id); err != nil {
		if id == "" || !errors.Is(err, domain.ErrNotFound) || s.startTorrent == nil {
			return err
		}
		if _, startErr := s.startTorrent.Execute(ctx, id); startErr != nil {
			return startErr
		}
		return s.player.SetCurrentTorrentID(id)
	}
	return nil
}

func (s *Server) ensureStreamingAllowed(ctx context.Context, id domain.TorrentID) error {
	if id == "" {
		return domain.ErrNotFound
	}
	return nil
}
