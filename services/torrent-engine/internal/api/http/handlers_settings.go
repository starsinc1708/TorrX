package apihttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"torrentstream/internal/app"
	"torrentstream/internal/domain"
)

type playerSettingsResponse struct {
	CurrentTorrentID domain.TorrentID `json:"currentTorrentId,omitempty"`
}

type updatePlayerSettingsRequest struct {
	CurrentTorrentID *string `json:"currentTorrentId"`
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
	if body.SegmentDuration == 0 {
		body.SegmentDuration = current.SegmentDuration
	}
	if body.RAMBufSizeMB == 0 {
		body.RAMBufSizeMB = current.RAMBufSizeMB
	}
	if body.PrebufferMB == 0 {
		body.PrebufferMB = current.PrebufferMB
	}
	if body.WindowBeforeMB == 0 {
		body.WindowBeforeMB = current.WindowBeforeMB
	}
	if body.WindowAfterMB == 0 {
		body.WindowAfterMB = current.WindowAfterMB
	}

	// Validation.
	if body.SegmentDuration < 2 || body.SegmentDuration > 10 {
		writeError(w, http.StatusBadRequest, "invalid_request", "segmentDuration must be 2-10")
		return
	}
	if body.RAMBufSizeMB < 4 || body.RAMBufSizeMB > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_request", "ramBufSizeMB must be 4-4096")
		return
	}
	if body.PrebufferMB < 1 || body.PrebufferMB > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_request", "prebufferMB must be 1-1024")
		return
	}
	if body.WindowBeforeMB < 1 || body.WindowBeforeMB > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_request", "windowBeforeMB must be 1-1024")
		return
	}
	if body.WindowAfterMB < 4 || body.WindowAfterMB > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_request", "windowAfterMB must be 4-4096")
		return
	}

	if err := s.hlsSettingsCtrl.Update(body); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update hls settings")
		return
	}

	writeJSON(w, http.StatusOK, s.hlsSettingsCtrl.Get())
}

