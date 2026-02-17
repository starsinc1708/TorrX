package apihttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"torrentstream/internal/domain"
	"torrentstream/internal/usecase"
)

type errorEnvelope struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeUseCaseError(w http.ResponseWriter, err error) {
	if errors.Is(err, usecase.ErrInvalidSource) {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid torrent source")
		return
	}
	if errors.Is(err, usecase.ErrInvalidFileIndex) {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid fileIndex")
		return
	}
	if errors.Is(err, usecase.ErrRepository) {
		writeError(w, http.StatusInternalServerError, "repository_error", err.Error())
		return
	}
	if errors.Is(err, usecase.ErrEngine) {
		writeError(w, http.StatusInternalServerError, "engine_error", err.Error())
		return
	}

	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeRepoError(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "torrent not found")
		return
	}

	writeError(w, http.StatusInternalServerError, "repository_error", err.Error())
}

func writeDomainError(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "torrent not found")
		return
	}
	if errors.Is(err, usecase.ErrInvalidFileIndex) {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid fileIndex")
		return
	}
	if errors.Is(err, usecase.ErrRepository) {
		writeError(w, http.StatusInternalServerError, "repository_error", err.Error())
		return
	}
	if errors.Is(err, usecase.ErrEngine) {
		writeError(w, http.StatusInternalServerError, "engine_error", err.Error())
		return
	}

	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorPayload{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func saveUploadedFile(src io.Reader, filename string) (string, error) {
	base := strings.TrimSpace(filename)
	if base == "" {
		base = "torrent"
	}
	base = strings.ReplaceAll(base, string(os.PathSeparator), "_")
	ext := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, ext)
	pattern := prefix + "-*" + ext

	out, err := os.CreateTemp(os.TempDir(), pattern)
	if err != nil {
		return "", err
	}
	defer out.Close()

	if _, err := io.Copy(out, src); err != nil {
		return "", err
	}

	return out.Name(), nil
}

func resolveDataFilePath(dataDir, filePath string) (string, error) {
	base := strings.TrimSpace(dataDir)
	if base == "" {
		return "", errors.New("data dir is required")
	}
	base = filepath.Clean(base)
	if abs, err := filepath.Abs(base); err == nil {
		base = abs
	}

	joined := filepath.Join(base, filepath.FromSlash(filePath))
	joined = filepath.Clean(joined)
	if abs, err := filepath.Abs(joined); err == nil {
		joined = abs
	}

	if joined != base && !strings.HasPrefix(joined, base+string(filepath.Separator)) {
		return "", errors.New("path escapes data dir")
	}
	return joined, nil
}

func parseStatus(value string) (*domain.TorrentStatus, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "all" {
		return nil, nil
	}
	switch value {
	case string(domain.TorrentActive), string(domain.TorrentCompleted), string(domain.TorrentStopped):
		status := domain.TorrentStatus(value)
		return &status, nil
	default:
		return nil, errors.New("invalid status")
	}
}

func parsePositiveInt(value string, requirePositive bool) (int, error) {
	if strings.TrimSpace(value) == "" {
		return -1, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	if requirePositive && parsed <= 0 {
		return 0, errors.New("must be > 0")
	}
	if !requirePositive && parsed < 0 {
		return 0, errors.New("must be >= 0")
	}
	return parsed, nil
}

func parseSortOrder(value string) (domain.SortOrder, error) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return domain.SortDesc, nil
	}
	switch domain.SortOrder(trimmed) {
	case domain.SortAsc:
		return domain.SortAsc, nil
	case domain.SortDesc:
		return domain.SortDesc, nil
	default:
		return "", errors.New("invalid sort order")
	}
}

func isAllowedSortBy(value string) bool {
	switch value {
	case "name", "createdAt", "updatedAt", "totalBytes", "progress":
		return true
	default:
		return false
	}
}

func parseCommaSeparated(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func applyLimitOffset(records []domain.TorrentRecord, limit, offset int) []domain.TorrentRecord {
	if offset > 0 {
		if offset >= len(records) {
			return []domain.TorrentRecord{}
		}
		records = records[offset:]
	}
	if limit > 0 && limit < len(records) {
		records = records[:limit]
	}
	return records
}

func progressRatio(done, total int64) float64 {
	if total <= 0 {
		return 0
	}
	progress := float64(done) / float64(total)
	if progress < 0 {
		return 0
	}
	if progress > 1 {
		return 1
	}
	return progress
}

func parseBoolQuery(value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, nil
	}
	switch strings.ToLower(value) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("invalid bool")
	}
}

func parseOptionalIntQuery(value string, defaultValue int) (int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

var (
	errInvalidRange        = errors.New("invalid range")
	errRangeNotSatisfiable = errors.New("range not satisfiable")
)

func parseByteRange(value string, size int64) (int64, int64, error) {
	if size <= 0 {
		return 0, 0, errRangeNotSatisfiable
	}

	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if !strings.HasPrefix(lower, "bytes=") {
		return 0, 0, errInvalidRange
	}

	spec := strings.TrimSpace(value[len("bytes="):])
	if spec == "" || strings.Contains(spec, ",") {
		return 0, 0, errInvalidRange
	}

	parts := strings.SplitN(spec, "-", 2)
	if len(parts) == 1 {
		parts = append(parts, "")
	}
	if len(parts) != 2 {
		return 0, 0, errInvalidRange
	}

	startStr := strings.TrimSpace(parts[0])
	endStr := strings.TrimSpace(parts[1])

	if startStr == "" {
		if endStr == "" {
			return 0, 0, errInvalidRange
		}
		suffix, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, errInvalidRange
		}
		if suffix > size {
			suffix = size
		}
		start := size - suffix
		end := size - 1
		return start, end, nil
	}

	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return 0, 0, errInvalidRange
	}

	if start >= size {
		return 0, 0, errRangeNotSatisfiable
	}

	if endStr == "" {
		return start, size - 1, nil
	}

	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < 0 {
		return 0, 0, errInvalidRange
	}
	if end < start {
		return 0, 0, errInvalidRange
	}
	if end >= size {
		end = size - 1
	}
	return start, end, nil
}

func fallbackContentType(ext string) string {
	switch ext {
	case ".mp4":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	case ".m4v":
		return "video/x-m4v"
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".ogg":
		return "audio/ogg"
	default:
		return "application/octet-stream"
	}
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(s.openAPIPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "openapi not available")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleSwagger(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, swaggerHTML)
}
