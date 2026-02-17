package apihttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"torrentstream/internal/app"
	"torrentstream/internal/domain"
)

type storageSettingsResponse struct {
	Mode             string `json:"mode"`
	MemoryLimitBytes int64  `json:"memoryLimitBytes"`
	SpillToDisk      bool   `json:"spillToDisk"`
	DataDir          string `json:"dataDir,omitempty"`
	HLSDir           string `json:"hlsDir,omitempty"`
}

type updateStorageSettingsRequest struct {
	MemoryLimitBytes *int64 `json:"memoryLimitBytes"`
}

type playerSettingsResponse struct {
	CurrentTorrentID domain.TorrentID `json:"currentTorrentId,omitempty"`
}

type updatePlayerSettingsRequest struct {
	CurrentTorrentID *string `json:"currentTorrentId"`
}

func (s *Server) handleStorageSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetStorageSettings(w, r)
	case http.MethodPatch, http.MethodPut:
		s.handleUpdateStorageSettings(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetStorageSettings(w http.ResponseWriter, _ *http.Request) {
	if s.storage == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "storage settings are not configured")
		return
	}
	writeJSON(w, http.StatusOK, s.currentStorageSettings())
}

func (s *Server) handleUpdateStorageSettings(w http.ResponseWriter, r *http.Request) {
	if s.storage == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "storage settings are not configured")
		return
	}

	var body updateStorageSettingsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}
	if body.MemoryLimitBytes == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "memoryLimitBytes is required")
		return
	}
	if *body.MemoryLimitBytes < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "memoryLimitBytes must be >= 0")
		return
	}

	if err := s.storage.SetMemoryLimitBytes(*body.MemoryLimitBytes); err != nil {
		if errors.Is(err, domain.ErrUnsupported) {
			writeError(w, http.StatusConflict, "unsupported_operation", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update storage settings")
		return
	}

	writeJSON(w, http.StatusOK, s.currentStorageSettings())
}

func (s *Server) currentStorageSettings() storageSettingsResponse {
	mode := "disk"
	limit := int64(0)
	spill := false
	if s.storage != nil {
		mode = strings.TrimSpace(s.storage.StorageMode())
		if mode == "" {
			mode = "disk"
		}
		limit = s.storage.MemoryLimitBytes()
		spill = s.storage.SpillToDisk()
	}
	dataDir := s.mediaDataDir
	hlsDir := ""
	if s.hls != nil {
		hlsDir = s.hls.baseDir
	}
	return storageSettingsResponse{
		Mode:             mode,
		MemoryLimitBytes: limit,
		SpillToDisk:      spill,
		DataDir:          dataDir,
		HLSDir:           hlsDir,
	}
}

func (s *Server) handlePlayerSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetPlayerSettings(w, r)
	case http.MethodPatch, http.MethodPut:
		s.handleUpdatePlayerSettings(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

type playerHealthResponse struct {
	Status           string            `json:"status"`
	CheckedAt        time.Time         `json:"checkedAt"`
	CurrentTorrentID string            `json:"currentTorrentId,omitempty"`
	FocusModeEnabled bool              `json:"focusModeEnabled"`
	ActiveSessions   int               `json:"activeSessions"`
	ActiveSessionIDs []string          `json:"activeSessionIds,omitempty"`
	HLS              hlsHealthSnapshot `json:"hls"`
	Issues           []string          `json:"issues,omitempty"`
}

// BuildPlayerHealth constructs a playerHealthResponse without writing it to an HTTP response.
func (s *Server) BuildPlayerHealth(ctx context.Context) playerHealthResponse {
	resp := playerHealthResponse{
		Status:    "ok",
		CheckedAt: time.Now().UTC(),
		HLS:       hlsHealthSnapshot{},
	}

	setDegraded := func(issue string) {
		if strings.TrimSpace(issue) == "" {
			return
		}
		resp.Status = "degraded"
		resp.Issues = append(resp.Issues, issue)
	}

	if s.player != nil {
		resp.CurrentTorrentID = strings.TrimSpace(string(s.player.CurrentTorrentID()))
		resp.FocusModeEnabled = resp.CurrentTorrentID != ""
	} else {
		setDegraded("player settings are not configured")
	}

	if s.engine != nil {
		ids, err := s.engine.ListActiveSessions(ctx)
		if err != nil {
			setDegraded("failed to list active sessions")
		} else {
			resp.ActiveSessions = len(ids)
			if len(ids) > 0 {
				resp.ActiveSessionIDs = make([]string, 0, len(ids))
				for _, id := range ids {
					resp.ActiveSessionIDs = append(resp.ActiveSessionIDs, string(id))
				}
			}
		}
	} else {
		setDegraded("torrent engine is not configured")
	}

	if resp.FocusModeEnabled {
		if resp.ActiveSessions == 0 {
			setDegraded("focus mode is enabled, but there are no active sessions")
		} else if !containsString(resp.ActiveSessionIDs, resp.CurrentTorrentID) {
			setDegraded("current torrent is not present in active sessions")
		}
	}

	if s.hls != nil {
		resp.HLS = s.hls.healthSnapshot()
		if resp.HLS.LastJobError != "" && resp.HLS.LastJobErrorAt != nil {
			if resp.CheckedAt.Sub(*resp.HLS.LastJobErrorAt) <= 3*time.Minute {
				setDegraded("recent HLS failure detected")
			}
		}
		if resp.HLS.LastSeekError != "" && resp.HLS.LastSeekErrorAt != nil {
			if resp.CheckedAt.Sub(*resp.HLS.LastSeekErrorAt) <= 3*time.Minute {
				setDegraded("recent HLS seek failure detected")
			}
		}
	} else {
		setDegraded("hls manager is not configured")
	}

	return resp
}

func (s *Server) handlePlayerHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.BuildPlayerHealth(r.Context()))
}

func (s *Server) handleGetPlayerSettings(w http.ResponseWriter, _ *http.Request) {
	if s.player == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "player settings are not configured")
		return
	}
	writeJSON(w, http.StatusOK, playerSettingsResponse{CurrentTorrentID: s.player.CurrentTorrentID()})
}

func (s *Server) handleUpdatePlayerSettings(w http.ResponseWriter, r *http.Request) {
	if s.player == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "player settings are not configured")
		return
	}

	var body updatePlayerSettingsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}
	if body.CurrentTorrentID == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "currentTorrentId is required")
		return
	}

	id := domain.TorrentID(strings.TrimSpace(*body.CurrentTorrentID))
	if err := s.setCurrentTorrentID(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, playerSettingsResponse{CurrentTorrentID: s.player.CurrentTorrentID()})
	s.BroadcastPlayerSettings()
}

// Encoding settings handlers.

var validPresets = map[string]bool{
	"ultrafast": true,
	"superfast": true,
	"veryfast":  true,
	"faster":    true,
	"fast":      true,
	"medium":    true,
}

var validAudioBitrates = map[string]bool{
	"96k":  true,
	"128k": true,
	"192k": true,
	"256k": true,
}

func (s *Server) handleEncodingSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetEncodingSettings(w, r)
	case http.MethodPatch, http.MethodPut:
		s.handleUpdateEncodingSettings(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetEncodingSettings(w http.ResponseWriter, _ *http.Request) {
	if s.encoding == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "encoding settings not configured")
		return
	}
	writeJSON(w, http.StatusOK, s.encoding.Get())
}

func (s *Server) handleUpdateEncodingSettings(w http.ResponseWriter, r *http.Request) {
	if s.encoding == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "encoding settings not configured")
		return
	}

	var body app.EncodingSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}

	if body.Preset != "" && !validPresets[body.Preset] {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid preset")
		return
	}
	if body.CRF < 0 || body.CRF > 51 {
		writeError(w, http.StatusBadRequest, "invalid_request", "crf must be 0-51")
		return
	}
	if body.AudioBitrate != "" && !validAudioBitrates[body.AudioBitrate] {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid audioBitrate")
		return
	}

	// Merge with current values for partial updates.
	current := s.encoding.Get()
	if body.Preset == "" {
		body.Preset = current.Preset
	}
	if body.CRF == 0 {
		body.CRF = current.CRF
	}
	if body.AudioBitrate == "" {
		body.AudioBitrate = current.AudioBitrate
	}

	if err := s.encoding.Update(body); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update encoding settings")
		return
	}

	writeJSON(w, http.StatusOK, s.encoding.Get())
}

// HLS settings handlers.

func (s *Server) handleHLSSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetHLSSettings(w, r)
	case http.MethodPatch, http.MethodPut:
		s.handleUpdateHLSSettings(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetHLSSettings(w http.ResponseWriter, _ *http.Request) {
	if s.hlsSettingsCtrl == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "hls settings not configured")
		return
	}
	writeJSON(w, http.StatusOK, s.hlsSettingsCtrl.Get())
}

func (s *Server) handleUpdateHLSSettings(w http.ResponseWriter, r *http.Request) {
	if s.hlsSettingsCtrl == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "hls settings not configured")
		return
	}

	var body app.HLSSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid json")
		return
	}

	// Merge with current values for partial updates.
	current := s.hlsSettingsCtrl.Get()
	if body.MemBufSizeMB == 0 {
		body.MemBufSizeMB = current.MemBufSizeMB
	}
	if body.CacheSizeMB == 0 {
		body.CacheSizeMB = current.CacheSizeMB
	}
	if body.CacheMaxAgeHours == 0 {
		body.CacheMaxAgeHours = current.CacheMaxAgeHours
	}
	if body.SegmentDuration == 0 {
		body.SegmentDuration = current.SegmentDuration
	}

	// Validation.
	if body.MemBufSizeMB < 0 || body.MemBufSizeMB > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_request", "memBufSizeMB must be 0-4096")
		return
	}
	if body.CacheSizeMB < 100 || body.CacheSizeMB > 102400 {
		writeError(w, http.StatusBadRequest, "invalid_request", "cacheSizeMB must be 100-102400")
		return
	}
	if body.CacheMaxAgeHours < 1 || body.CacheMaxAgeHours > 8760 {
		writeError(w, http.StatusBadRequest, "invalid_request", "cacheMaxAgeHours must be 1-8760")
		return
	}
	if body.SegmentDuration < 2 || body.SegmentDuration > 10 {
		writeError(w, http.StatusBadRequest, "invalid_request", "segmentDuration must be 2-10")
		return
	}

	if err := s.hlsSettingsCtrl.Update(body); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update hls settings")
		return
	}

	writeJSON(w, http.StatusOK, s.hlsSettingsCtrl.Get())
}

