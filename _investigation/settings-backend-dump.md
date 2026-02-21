# Backend Settings Exploration Dump

Note: `services/torrent-engine/internal/domain` contains no `*Settings` types.

## services/torrent-engine/internal/api/http/handlers_settings.go

```go
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

```

## services/torrent-engine/internal/api/http/handlers_settings_test.go

```go
package apihttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"torrentstream/internal/app"
)

// ---- fake encoding controller ----

type fakeEncodingCtrl struct {
	settings  app.EncodingSettings
	updateErr error
}

func (f *fakeEncodingCtrl) Get() app.EncodingSettings { return f.settings }
func (f *fakeEncodingCtrl) Update(s app.EncodingSettings) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.settings = s
	return nil
}

// ---- fake HLS controller ----

type fakeHLSSettingsCtrl struct {
	settings  app.HLSSettings
	updateErr error
}

func (f *fakeHLSSettingsCtrl) Get() app.HLSSettings { return f.settings }
func (f *fakeHLSSettingsCtrl) Update(s app.HLSSettings) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.settings = s
	return nil
}

// ---- helpers ----

func makeSettingsServer(encCtrl *fakeEncodingCtrl, hlsCtrl *fakeHLSSettingsCtrl) *Server {
	var opts []ServerOption
	if encCtrl != nil {
		opts = append(opts, WithEncodingSettings(encCtrl))
	}
	s := NewServer(nil, opts...)
	if hlsCtrl != nil {
		s.SetHLSSettings(hlsCtrl)
	}
	return s
}

func doSettingsRequest(s *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

// ---- Encoding Settings tests ----

func TestGetEncodingSettings_ReturnsCurrentValues(t *testing.T) {
	ctrl := &fakeEncodingCtrl{settings: app.EncodingSettings{Preset: "veryfast", CRF: 23, AudioBitrate: "128k"}}
	s := makeSettingsServer(ctrl, nil)

	rec := doSettingsRequest(s, http.MethodGet, "/settings/encoding", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got app.EncodingSettings
	json.NewDecoder(rec.Body).Decode(&got)
	if got.Preset != "veryfast" || got.CRF != 23 || got.AudioBitrate != "128k" {
		t.Errorf("unexpected settings: %+v", got)
	}
}

func TestGetEncodingSettings_NotConfigured(t *testing.T) {
	s := makeSettingsServer(nil, nil)

	rec := doSettingsRequest(s, http.MethodGet, "/settings/encoding", nil)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", rec.Code)
	}
}

func TestUpdateEncodingSettings_ValidFullUpdate(t *testing.T) {
	ctrl := &fakeEncodingCtrl{settings: app.EncodingSettings{Preset: "veryfast", CRF: 23, AudioBitrate: "128k"}}
	s := makeSettingsServer(ctrl, nil)

	body, _ := json.Marshal(map[string]any{"preset": "medium", "crf": 28, "audioBitrate": "192k"})
	rec := doSettingsRequest(s, http.MethodPut, "/settings/encoding", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if ctrl.settings.Preset != "medium" {
		t.Errorf("expected preset medium, got %q", ctrl.settings.Preset)
	}
	if ctrl.settings.CRF != 28 {
		t.Errorf("expected crf 28, got %d", ctrl.settings.CRF)
	}
	if ctrl.settings.AudioBitrate != "192k" {
		t.Errorf("expected audioBitrate 192k, got %q", ctrl.settings.AudioBitrate)
	}
}

func TestUpdateEncodingSettings_InvalidPreset(t *testing.T) {
	ctrl := &fakeEncodingCtrl{settings: app.EncodingSettings{Preset: "veryfast", CRF: 23, AudioBitrate: "128k"}}
	s := makeSettingsServer(ctrl, nil)

	body, _ := json.Marshal(map[string]any{"preset": "slow"})
	rec := doSettingsRequest(s, http.MethodPatch, "/settings/encoding", body)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid preset, got %d", rec.Code)
	}
}

func TestUpdateEncodingSettings_CRFOutOfRange(t *testing.T) {
	tests := []struct {
		name string
		crf  int
	}{
		{"CRF too high (52)", 52},
		{"CRF negative (-1)", -1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := &fakeEncodingCtrl{settings: app.EncodingSettings{Preset: "veryfast", CRF: 23, AudioBitrate: "128k"}}
			s := makeSettingsServer(ctrl, nil)

			body, _ := json.Marshal(map[string]any{"crf": tc.crf})
			rec := doSettingsRequest(s, http.MethodPut, "/settings/encoding", body)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for crf %d, got %d", tc.crf, rec.Code)
			}
		})
	}
}

func TestUpdateEncodingSettings_InvalidAudioBitrate(t *testing.T) {
	ctrl := &fakeEncodingCtrl{settings: app.EncodingSettings{Preset: "veryfast", CRF: 23, AudioBitrate: "128k"}}
	s := makeSettingsServer(ctrl, nil)

	body, _ := json.Marshal(map[string]any{"audioBitrate": "320k"})
	rec := doSettingsRequest(s, http.MethodPatch, "/settings/encoding", body)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid audioBitrate, got %d", rec.Code)
	}
}

func TestUpdateEncodingSettings_PartialUpdate(t *testing.T) {
	ctrl := &fakeEncodingCtrl{settings: app.EncodingSettings{Preset: "veryfast", CRF: 23, AudioBitrate: "128k"}}
	s := makeSettingsServer(ctrl, nil)

	body, _ := json.Marshal(map[string]any{"preset": "fast"})
	rec := doSettingsRequest(s, http.MethodPatch, "/settings/encoding", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if ctrl.settings.Preset != "fast" {
		t.Errorf("expected preset fast, got %q", ctrl.settings.Preset)
	}
	if ctrl.settings.CRF != 23 {
		t.Errorf("expected crf preserved at 23, got %d", ctrl.settings.CRF)
	}
	if ctrl.settings.AudioBitrate != "128k" {
		t.Errorf("expected audioBitrate preserved at 128k, got %q", ctrl.settings.AudioBitrate)
	}
}

func TestUpdateEncodingSettings_BadJSON(t *testing.T) {
	ctrl := &fakeEncodingCtrl{settings: app.EncodingSettings{Preset: "veryfast", CRF: 23, AudioBitrate: "128k"}}
	s := makeSettingsServer(ctrl, nil)

	rec := doSettingsRequest(s, http.MethodPut, "/settings/encoding", []byte("{invalid"))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad JSON, got %d", rec.Code)
	}
}

func TestEncodingSettings_MethodNotAllowed(t *testing.T) {
	ctrl := &fakeEncodingCtrl{settings: app.EncodingSettings{Preset: "veryfast", CRF: 23, AudioBitrate: "128k"}}
	s := makeSettingsServer(ctrl, nil)

	rec := doSettingsRequest(s, http.MethodDelete, "/settings/encoding", nil)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for DELETE, got %d", rec.Code)
	}
}

func TestUpdateEncodingSettings_NotConfigured(t *testing.T) {
	s := makeSettingsServer(nil, nil)

	body, _ := json.Marshal(map[string]any{"preset": "fast"})
	rec := doSettingsRequest(s, http.MethodPut, "/settings/encoding", body)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", rec.Code)
	}
}

func TestUpdateEncodingSettings_AllValidPresets(t *testing.T) {
	for _, preset := range []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium"} {
		t.Run(preset, func(t *testing.T) {
			ctrl := &fakeEncodingCtrl{settings: app.EncodingSettings{Preset: "veryfast", CRF: 23, AudioBitrate: "128k"}}
			s := makeSettingsServer(ctrl, nil)

			body, _ := json.Marshal(map[string]any{"preset": preset})
			rec := doSettingsRequest(s, http.MethodPut, "/settings/encoding", body)

			if rec.Code != http.StatusOK {
				t.Errorf("expected 200 for valid preset %q, got %d", preset, rec.Code)
			}
		})
	}
}

func TestUpdateEncodingSettings_AllValidAudioBitrates(t *testing.T) {
	for _, bitrate := range []string{"96k", "128k", "192k", "256k"} {
		t.Run(bitrate, func(t *testing.T) {
			ctrl := &fakeEncodingCtrl{settings: app.EncodingSettings{Preset: "veryfast", CRF: 23, AudioBitrate: "128k"}}
			s := makeSettingsServer(ctrl, nil)

			body, _ := json.Marshal(map[string]any{"audioBitrate": bitrate})
			rec := doSettingsRequest(s, http.MethodPut, "/settings/encoding", body)

			if rec.Code != http.StatusOK {
				t.Errorf("expected 200 for valid bitrate %q, got %d", bitrate, rec.Code)
			}
		})
	}
}

func TestUpdateEncodingSettings_CRFBoundaryValues(t *testing.T) {
	// CRF 0 triggers merge (keep current), and CRF 51 is max valid.
	tests := []struct {
		name   string
		crf    int
		expect int
	}{
		{"CRF 1 (min)", 1, http.StatusOK},
		{"CRF 51 (max)", 51, http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := &fakeEncodingCtrl{settings: app.EncodingSettings{Preset: "veryfast", CRF: 23, AudioBitrate: "128k"}}
			s := makeSettingsServer(ctrl, nil)

			body, _ := json.Marshal(map[string]any{"crf": tc.crf})
			rec := doSettingsRequest(s, http.MethodPut, "/settings/encoding", body)

			if rec.Code != tc.expect {
				t.Errorf("crf=%d: expected %d, got %d", tc.crf, tc.expect, rec.Code)
			}
		})
	}
}

func TestUpdateEncodingSettings_StoreError(t *testing.T) {
	ctrl := &fakeEncodingCtrl{
		settings:  app.EncodingSettings{Preset: "veryfast", CRF: 23, AudioBitrate: "128k"},
		updateErr: errors.New("db error"),
	}
	s := makeSettingsServer(ctrl, nil)

	body, _ := json.Marshal(map[string]any{"preset": "fast", "crf": 28, "audioBitrate": "192k"})
	rec := doSettingsRequest(s, http.MethodPut, "/settings/encoding", body)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for store error, got %d", rec.Code)
	}
}

// ---- HLS Settings tests ----

func TestGetHLSSettings_ReturnsCurrentValues(t *testing.T) {
	ctrl := &fakeHLSSettingsCtrl{settings: app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 64}}
	s := makeSettingsServer(nil, ctrl)

	rec := doSettingsRequest(s, http.MethodGet, "/settings/hls", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got app.HLSSettings
	json.NewDecoder(rec.Body).Decode(&got)
	if got.SegmentDuration != 2 || got.RAMBufSizeMB != 16 {
		t.Errorf("unexpected settings: %+v", got)
	}
}

func TestGetHLSSettings_NotConfigured(t *testing.T) {
	s := makeSettingsServer(nil, nil)

	rec := doSettingsRequest(s, http.MethodGet, "/settings/hls", nil)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", rec.Code)
	}
}

func TestUpdateHLSSettings_ValidFullUpdate(t *testing.T) {
	ctrl := &fakeHLSSettingsCtrl{settings: app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 64}}
	s := makeSettingsServer(nil, ctrl)

	body, _ := json.Marshal(app.HLSSettings{SegmentDuration: 4, RAMBufSizeMB: 32, PrebufferMB: 8, WindowBeforeMB: 16, WindowAfterMB: 128})
	rec := doSettingsRequest(s, http.MethodPut, "/settings/hls", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if ctrl.settings.SegmentDuration != 4 {
		t.Errorf("expected segDur 4, got %d", ctrl.settings.SegmentDuration)
	}
}

func TestUpdateHLSSettings_ValidationRanges(t *testing.T) {
	tests := []struct {
		name string
		body app.HLSSettings
	}{
		{"segmentDuration too low (1)", app.HLSSettings{SegmentDuration: 1, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 64}},
		{"segmentDuration too high (11)", app.HLSSettings{SegmentDuration: 11, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 64}},
		{"ramBufSizeMB too low (3)", app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 3, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 64}},
		{"ramBufSizeMB too high (4097)", app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 4097, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 64}},
		// Note: PrebufferMB=0 and WindowBeforeMB=0 are treated as "not provided" by the
		// partial-update merge (Go zero-value sentinel), so they get replaced with current
		// values before validation. Use -1 to test below-minimum validation.
		{"prebufferMB too low (-1)", app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: -1, WindowBeforeMB: 8, WindowAfterMB: 64}},
		{"prebufferMB too high (1025)", app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: 1025, WindowBeforeMB: 8, WindowAfterMB: 64}},
		{"windowBeforeMB too low (-1)", app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: -1, WindowAfterMB: 64}},
		{"windowBeforeMB too high (1025)", app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: 1025, WindowAfterMB: 64}},
		{"windowAfterMB too low (3)", app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 3}},
		{"windowAfterMB too high (4097)", app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 4097}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := &fakeHLSSettingsCtrl{settings: app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 64}}
			s := makeSettingsServer(nil, ctrl)

			body, _ := json.Marshal(tc.body)
			rec := doSettingsRequest(s, http.MethodPut, "/settings/hls", body)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestUpdateHLSSettings_PartialUpdate(t *testing.T) {
	ctrl := &fakeHLSSettingsCtrl{settings: app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 64}}
	s := makeSettingsServer(nil, ctrl)

	body, _ := json.Marshal(map[string]any{"segmentDuration": 6})
	rec := doSettingsRequest(s, http.MethodPatch, "/settings/hls", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if ctrl.settings.SegmentDuration != 6 {
		t.Errorf("expected segDur 6, got %d", ctrl.settings.SegmentDuration)
	}
	if ctrl.settings.RAMBufSizeMB != 16 {
		t.Errorf("expected ramBuf preserved at 16, got %d", ctrl.settings.RAMBufSizeMB)
	}
}

func TestUpdateHLSSettings_BadJSON(t *testing.T) {
	ctrl := &fakeHLSSettingsCtrl{settings: app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 64}}
	s := makeSettingsServer(nil, ctrl)

	rec := doSettingsRequest(s, http.MethodPut, "/settings/hls", []byte("{bad"))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad JSON, got %d", rec.Code)
	}
}

func TestHLSSettings_MethodNotAllowed(t *testing.T) {
	ctrl := &fakeHLSSettingsCtrl{settings: app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 64}}
	s := makeSettingsServer(nil, ctrl)

	rec := doSettingsRequest(s, http.MethodDelete, "/settings/hls", nil)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for DELETE, got %d", rec.Code)
	}
}

func TestUpdateHLSSettings_BoundaryMinValues(t *testing.T) {
	ctrl := &fakeHLSSettingsCtrl{settings: app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 64}}
	s := makeSettingsServer(nil, ctrl)

	body, _ := json.Marshal(app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 4, PrebufferMB: 1, WindowBeforeMB: 1, WindowAfterMB: 4})
	rec := doSettingsRequest(s, http.MethodPut, "/settings/hls", body)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for min boundary values, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateHLSSettings_BoundaryMaxValues(t *testing.T) {
	ctrl := &fakeHLSSettingsCtrl{settings: app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 64}}
	s := makeSettingsServer(nil, ctrl)

	body, _ := json.Marshal(app.HLSSettings{SegmentDuration: 10, RAMBufSizeMB: 4096, PrebufferMB: 1024, WindowBeforeMB: 1024, WindowAfterMB: 4096})
	rec := doSettingsRequest(s, http.MethodPut, "/settings/hls", body)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for max boundary values, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateHLSSettings_NotConfigured(t *testing.T) {
	s := makeSettingsServer(nil, nil)

	body, _ := json.Marshal(app.HLSSettings{SegmentDuration: 4})
	rec := doSettingsRequest(s, http.MethodPut, "/settings/hls", body)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", rec.Code)
	}
}

func TestUpdateHLSSettings_StoreError(t *testing.T) {
	ctrl := &fakeHLSSettingsCtrl{
		settings:  app.HLSSettings{SegmentDuration: 2, RAMBufSizeMB: 16, PrebufferMB: 4, WindowBeforeMB: 8, WindowAfterMB: 64},
		updateErr: errors.New("db error"),
	}
	s := makeSettingsServer(nil, ctrl)

	body, _ := json.Marshal(app.HLSSettings{SegmentDuration: 4, RAMBufSizeMB: 32, PrebufferMB: 8, WindowBeforeMB: 16, WindowAfterMB: 128})
	rec := doSettingsRequest(s, http.MethodPut, "/settings/hls", body)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for store error, got %d", rec.Code)
	}
}
```

## services/torrent-engine/internal/api/http/server.go

```go
package apihttp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"torrentstream/internal/app"
	"torrentstream/internal/domain"
	domainports "torrentstream/internal/domain/ports"
	"torrentstream/internal/usecase"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type CreateTorrentUseCase interface {
	Execute(ctx context.Context, input usecase.CreateTorrentInput) (domain.TorrentRecord, error)
}

type StartTorrentUseCase interface {
	Execute(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error)
}

type StopTorrentUseCase interface {
	Execute(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error)
}

type DeleteTorrentUseCase interface {
	Execute(ctx context.Context, id domain.TorrentID, deleteFiles bool) error
}

type StreamTorrentUseCase interface {
	Execute(ctx context.Context, id domain.TorrentID, fileIndex int) (usecase.StreamResult, error)
	ExecuteRaw(ctx context.Context, id domain.TorrentID, fileIndex int) (usecase.StreamResult, error)
}

type GetTorrentStateUseCase interface {
	Execute(ctx context.Context, id domain.TorrentID) (domain.SessionState, error)
}

type ListTorrentStatesUseCase interface {
	Execute(ctx context.Context) ([]domain.SessionState, error)
}

type WatchHistoryStore interface {
	Upsert(ctx context.Context, wp domain.WatchPosition) error
	Get(ctx context.Context, torrentID domain.TorrentID, fileIndex int) (domain.WatchPosition, error)
	ListRecent(ctx context.Context, limit int) ([]domain.WatchPosition, error)
}

type EncodingSettingsController interface {
	Get() app.EncodingSettings
	Update(settings app.EncodingSettings) error
}

type HLSSettingsController interface {
	Get() app.HLSSettings
	Update(settings app.HLSSettings) error
}

type PlayerSettingsController interface {
	CurrentTorrentID() domain.TorrentID
	SetCurrentTorrentID(id domain.TorrentID) error
}

type MediaProbe interface {
	Probe(ctx context.Context, filePath string) (domain.MediaInfo, error)
	ProbeReader(ctx context.Context, reader io.Reader) (domain.MediaInfo, error)
}

// mediaProbeCacheEntry holds a cached ffprobe result with an expiration time.
type mediaProbeCacheEntry struct {
	info      domain.MediaInfo
	expiresAt time.Time
}

// mediaProbeCacheKey uniquely identifies a media probe request.
type mediaProbeCacheKey struct {
	torrentID domain.TorrentID
	fileIndex int
}

const mediaProbeCacheTTL = 5 * time.Minute

type Server struct {
	createTorrent   CreateTorrentUseCase
	startTorrent    StartTorrentUseCase
	stopTorrent     StopTorrentUseCase
	deleteTorrent   DeleteTorrentUseCase
	streamTorrent   StreamTorrentUseCase
	getState        GetTorrentStateUseCase
	listStates      ListTorrentStatesUseCase
	repo            domainports.TorrentRepository
	openAPIPath     string
	hls             *StreamJobManager
	hlsCfg          *HLSConfig
	mediaProbe      MediaProbe
	mediaDataDir    string
	watchHistory    WatchHistoryStore
	encoding        EncodingSettingsController
	hlsSettingsCtrl HLSSettingsController
	player          PlayerSettingsController
	engine          domainports.Engine
	allowedOrigins  []string
	logger          *slog.Logger
	handler         http.Handler
	wsHub           *wsHub
	mediaCacheMu    sync.RWMutex
	mediaProbeCache map[mediaProbeCacheKey]mediaProbeCacheEntry
}

type ServerOption func(*Server)

func WithOpenAPIPath(path string) ServerOption {
	return func(s *Server) {
		s.openAPIPath = path
	}
}

func WithRepository(repo domainports.TorrentRepository) ServerOption {
	return func(s *Server) {
		s.repo = repo
	}
}

func WithStartTorrent(uc StartTorrentUseCase) ServerOption {
	return func(s *Server) {
		s.startTorrent = uc
	}
}

func WithStopTorrent(uc StopTorrentUseCase) ServerOption {
	return func(s *Server) {
		s.stopTorrent = uc
	}
}

func WithDeleteTorrent(uc DeleteTorrentUseCase) ServerOption {
	return func(s *Server) {
		s.deleteTorrent = uc
	}
}

func WithStreamTorrent(uc StreamTorrentUseCase) ServerOption {
	return func(s *Server) {
		s.streamTorrent = uc
	}
}

func WithHLS(cfg HLSConfig) ServerOption {
	return func(s *Server) {
		s.hlsCfg = &cfg
	}
}

func WithMediaProbe(probe MediaProbe, dataDir string) ServerOption {
	return func(s *Server) {
		s.mediaProbe = probe
		s.mediaDataDir = strings.TrimSpace(dataDir)
		if s.mediaDataDir != "" {
			if abs, err := filepath.Abs(s.mediaDataDir); err == nil {
				s.mediaDataDir = abs
			}
			s.mediaDataDir = filepath.Clean(s.mediaDataDir)
		}
	}
}

func WithGetTorrentState(uc GetTorrentStateUseCase) ServerOption {
	return func(s *Server) {
		s.getState = uc
	}
}

func WithListTorrentStates(uc ListTorrentStatesUseCase) ServerOption {
	return func(s *Server) {
		s.listStates = uc
	}
}

func WithWatchHistory(store WatchHistoryStore) ServerOption {
	return func(s *Server) {
		s.watchHistory = store
	}
}

func WithEncodingSettings(ctrl EncodingSettingsController) ServerOption {
	return func(s *Server) {
		s.encoding = ctrl
	}
}

func WithPlayerSettings(ctrl PlayerSettingsController) ServerOption {
	return func(s *Server) {
		s.player = ctrl
	}
}

func WithEngine(engine domainports.Engine) ServerOption {
	return func(s *Server) {
		s.engine = engine
	}
}

// WithAllowedOrigins configures the CORS allowed origins whitelist.
// When empty (default), any origin is permitted (development mode).
func WithAllowedOrigins(origins []string) ServerOption {
	return func(s *Server) {
		s.allowedOrigins = origins
	}
}

// EncodingSettingsEngine returns the internal HLS manager as an
// app.EncodingSettingsEngine. Returns nil if HLS is not configured.
func (s *Server) EncodingSettingsEngine() app.EncodingSettingsEngine {
	if s.hls == nil {
		return nil
	}
	return s.hls
}

// SetEncodingSettings sets the encoding settings controller after construction.
func (s *Server) SetEncodingSettings(ctrl EncodingSettingsController) {
	s.encoding = ctrl
}

// HLSSettingsEngine returns the internal HLS manager as an
// app.HLSSettingsEngine. Returns nil if HLS is not configured.
func (s *Server) HLSSettingsEngine() app.HLSSettingsEngine {
	if s.hls == nil {
		return nil
	}
	return s.hls
}

// SetHLSSettings sets the HLS settings controller after construction.
func (s *Server) SetHLSSettings(ctrl HLSSettingsController) {
	s.hlsSettingsCtrl = ctrl
}

// HLSCacheTotalSize returns the current total size of the HLS segment cache in bytes.
// In the FSM architecture there is no persistent segment cache, so this always returns 0.
func (s *Server) HLSCacheTotalSize() int64 {
	return 0
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if s.wsHub == nil {
		http.Error(w, "websocket not available", http.StatusServiceUnavailable)
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("ws upgrade failed", slog.String("error", err.Error()))
		return
	}
	client := &wsClient{
		hub:  s.wsHub,
		conn: conn,
		send: make(chan []byte, 256),
	}
	s.wsHub.register <- client
	go client.writePump()
	go client.readPump()
}

// BroadcastStates sends states to all WebSocket clients.
func (s *Server) BroadcastStates(states []domain.SessionState) {
	if s.wsHub != nil {
		s.wsHub.BroadcastStates(states)
	}
}

// BroadcastTorrents lists all torrents from the repository and broadcasts
// their summaries to all connected WebSocket clients.
func (s *Server) BroadcastTorrents() {
	if s.wsHub == nil || s.repo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	records, err := s.repo.List(ctx, domain.TorrentFilter{})
	if err != nil {
		s.logger.Debug("ws broadcast torrents failed", slog.String("error", err.Error()))
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
	s.wsHub.Broadcast("torrents", summaries)
}

// BroadcastPlayerSettings broadcasts the current player settings to all
// connected WebSocket clients.
func (s *Server) BroadcastPlayerSettings() {
	if s.wsHub == nil || s.player == nil {
		return
	}
	s.wsHub.Broadcast("player_settings", playerSettingsResponse{CurrentTorrentID: s.player.CurrentTorrentID()})
}

// BroadcastHealth broadcasts the current player health status to all
// connected WebSocket clients.
func (s *Server) BroadcastHealth(ctx context.Context) {
	if s.wsHub == nil {
		return
	}
	s.wsHub.Broadcast("health", s.BuildPlayerHealth(ctx))
}

func WithLogger(logger *slog.Logger) ServerOption {
	return func(s *Server) {
		s.logger = logger
	}
}

func NewServer(create CreateTorrentUseCase, opts ...ServerOption) *Server {
	s := &Server{
		createTorrent:   create,
		openAPIPath:     defaultOpenAPIPath(),
		mediaProbeCache: make(map[mediaProbeCacheKey]mediaProbeCacheEntry),
	}
	for _, opt := range opts {
		opt(s)
	}

	if s.logger == nil {
		s.logger = slog.Default()
	}

	if s.hls == nil && s.streamTorrent != nil {
		cfg := HLSConfig{}
		if s.hlsCfg != nil {
			cfg = *s.hlsCfg
		}
		s.hls = newStreamJobManager(s.streamTorrent, s.engine, cfg, s.logger)
	}

	s.wsHub = newWSHub(s.logger)
	go s.wsHub.run()

	mux := http.NewServeMux()
	mux.HandleFunc("/torrents", s.handleTorrents)
	mux.HandleFunc("/torrents/", s.handleTorrentByID)
	mux.HandleFunc("/settings/encoding", s.handleEncodingSettings)
	mux.HandleFunc("/settings/hls", s.handleHLSSettings)
	mux.HandleFunc("/settings/player", s.handlePlayerSettings)
	mux.HandleFunc("/watch-history", s.handleWatchHistory)
	mux.HandleFunc("/watch-history/", s.handleWatchHistoryByID)
	mux.HandleFunc("/internal/health/player", s.handlePlayerHealth)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/swagger", s.handleSwagger)
	mux.HandleFunc("/swagger/", s.handleSwagger)
	mux.HandleFunc("/swagger/openapi.json", s.handleOpenAPI)
	mux.HandleFunc("/swagger/openapi", s.handleOpenAPI)
	mux.HandleFunc("/ws", s.handleWS)

	traced := otelhttp.NewHandler(loggingMiddleware(s.logger, mux), "torrent-engine",
		otelhttp.WithFilter(func(r *http.Request) bool {
			p := r.URL.Path
			return p != "/metrics" && p != "/internal/health/player" && !strings.HasPrefix(p, "/swagger")
		}),
	)
	s.handler = recoveryMiddleware(s.logger, rateLimitMiddleware(100, 200, metricsMiddleware(corsMiddleware(s.allowedOrigins, traced))))
	return s
}

func defaultOpenAPIPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return filepath.Join("docs", "openapi.json")
	}

	dir := cwd
	for i := 0; i < 6; i++ {
		path := filepath.Join(dir, "docs", "openapi.json")
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return filepath.Join("docs", "openapi.json")
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// Close gracefully shuts down the HLS manager (cancelling all FFmpeg jobs)
// and the WebSocket hub, disconnecting all clients.
func (s *Server) Close() {
	if s.hls != nil {
		s.hls.shutdown()
	}
	if s.wsHub != nil {
		s.wsHub.Close()
	}
}
```

## services/torrent-engine/internal/repository/mongo/encoding_settings.go

```go
package mongo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"torrentstream/internal/app"
)

const encodingSettingsID = "encoding"

type encodingSettingsDoc struct {
	ID           string `bson:"_id"`
	Preset       string `bson:"preset"`
	CRF          int    `bson:"crf"`
	AudioBitrate string `bson:"audioBitrate"`
	UpdatedAt    int64  `bson:"updatedAt"`
}

type EncodingSettingsRepository struct {
	collection *mongo.Collection
}

func NewEncodingSettingsRepository(client *mongo.Client, dbName string) *EncodingSettingsRepository {
	return &EncodingSettingsRepository{collection: client.Database(dbName).Collection("settings")}
}

func (r *EncodingSettingsRepository) GetEncodingSettings(ctx context.Context) (app.EncodingSettings, bool, error) {
	var doc encodingSettingsDoc
	err := r.collection.FindOne(ctx, bson.M{"_id": encodingSettingsID}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return app.EncodingSettings{}, false, nil
		}
		return app.EncodingSettings{}, false, err
	}
	return app.EncodingSettings{
		Preset:       doc.Preset,
		CRF:          doc.CRF,
		AudioBitrate: doc.AudioBitrate,
	}, true, nil
}

func (r *EncodingSettingsRepository) SetEncodingSettings(ctx context.Context, settings app.EncodingSettings) error {
	update := bson.M{
		"$set": bson.M{
			"preset":       settings.Preset,
			"crf":          settings.CRF,
			"audioBitrate": settings.AudioBitrate,
			"updatedAt":    time.Now().Unix(),
		},
	}
	_, err := r.collection.UpdateOne(
		ctx,
		bson.M{"_id": encodingSettingsID},
		update,
		options.Update().SetUpsert(true),
	)
	return err
}
```

## services/torrent-engine/internal/repository/mongo/hls_settings.go

```go
package mongo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"torrentstream/internal/app"
)

const hlsSettingsID = "hls"

type hlsSettingsDoc struct {
	ID              string `bson:"_id"`
	SegmentDuration int    `bson:"segmentDuration"`
	RAMBufSizeMB    int    `bson:"ramBufSizeMB"`
	PrebufferMB     int    `bson:"prebufferMB"`
	WindowBeforeMB  int    `bson:"windowBeforeMB"`
	WindowAfterMB   int    `bson:"windowAfterMB"`
	UpdatedAt       int64  `bson:"updatedAt"`
}

type HLSSettingsRepository struct {
	collection *mongo.Collection
}

func NewHLSSettingsRepository(client *mongo.Client, dbName string) *HLSSettingsRepository {
	return &HLSSettingsRepository{collection: client.Database(dbName).Collection("settings")}
}

func (r *HLSSettingsRepository) GetHLSSettings(ctx context.Context) (app.HLSSettings, bool, error) {
	var doc hlsSettingsDoc
	err := r.collection.FindOne(ctx, bson.M{"_id": hlsSettingsID}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return app.HLSSettings{}, false, nil
		}
		return app.HLSSettings{}, false, err
	}
	return app.HLSSettings{
		SegmentDuration: doc.SegmentDuration,
		RAMBufSizeMB:    doc.RAMBufSizeMB,
		PrebufferMB:     doc.PrebufferMB,
		WindowBeforeMB:  doc.WindowBeforeMB,
		WindowAfterMB:   doc.WindowAfterMB,
	}, true, nil
}

func (r *HLSSettingsRepository) SetHLSSettings(ctx context.Context, settings app.HLSSettings) error {
	update := bson.M{
		"$set": bson.M{
			"segmentDuration": settings.SegmentDuration,
			"ramBufSizeMB":    settings.RAMBufSizeMB,
			"prebufferMB":     settings.PrebufferMB,
			"windowBeforeMB":  settings.WindowBeforeMB,
			"windowAfterMB":   settings.WindowAfterMB,
			"updatedAt":       time.Now().Unix(),
		},
	}
	_, err := r.collection.UpdateOne(
		ctx,
		bson.M{"_id": hlsSettingsID},
		update,
		options.Update().SetUpsert(true),
	)
	return err
}
```

## services/torrent-engine/internal/domain/ports/storage.go

```go
package ports

import "context"

type Storage interface {
	Size() int64
	ReadAt(ctx context.Context, p []byte, off int64) (int, error)
	WriteAt(p []byte, off int64) (int, error)
	MarkPieceDone(index int)
	WaitRange(ctx context.Context, off, length int64) error
	Close() error
}
```

## services/torrent-engine/internal/app/encoding_settings.go

```go
package app

import (
	"context"
	"time"
)

type EncodingSettings struct {
	Preset       string `json:"preset"`
	CRF          int    `json:"crf"`
	AudioBitrate string `json:"audioBitrate"`
}

type EncodingSettingsEngine interface {
	EncodingPreset() string
	EncodingCRF() int
	EncodingAudioBitrate() string
	UpdateEncodingSettings(preset string, crf int, audioBitrate string)
}

type EncodingSettingsStore interface {
	GetEncodingSettings(ctx context.Context) (EncodingSettings, bool, error)
	SetEncodingSettings(ctx context.Context, settings EncodingSettings) error
}

type EncodingSettingsManager struct {
	engine  EncodingSettingsEngine
	store   EncodingSettingsStore
	timeout time.Duration
}

func NewEncodingSettingsManager(engine EncodingSettingsEngine, store EncodingSettingsStore) *EncodingSettingsManager {
	return &EncodingSettingsManager{
		engine:  engine,
		store:   store,
		timeout: 5 * time.Second,
	}
}

func (m *EncodingSettingsManager) Get() EncodingSettings {
	return EncodingSettings{
		Preset:       m.engine.EncodingPreset(),
		CRF:          m.engine.EncodingCRF(),
		AudioBitrate: m.engine.EncodingAudioBitrate(),
	}
}

func (m *EncodingSettingsManager) Update(settings EncodingSettings) error {
	prev := m.Get()
	m.engine.UpdateEncodingSettings(settings.Preset, settings.CRF, settings.AudioBitrate)

	if m.store == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	if err := m.store.SetEncodingSettings(ctx, settings); err != nil {
		m.engine.UpdateEncodingSettings(prev.Preset, prev.CRF, prev.AudioBitrate)
		return err
	}
	return nil
}
```

## services/torrent-engine/internal/app/hls_settings.go

```go
package app

import (
	"context"
	"time"
)

type HLSSettings struct {
	SegmentDuration int `json:"segmentDuration"`
	RAMBufSizeMB    int `json:"ramBufSizeMB"`
	PrebufferMB     int `json:"prebufferMB"`
	WindowBeforeMB  int `json:"windowBeforeMB"`
	WindowAfterMB   int `json:"windowAfterMB"`
}

type HLSSettingsEngine interface {
	SegmentDuration() int
	RAMBufSizeBytes() int64
	PrebufferBytes() int64
	WindowBeforeBytes() int64
	WindowAfterBytes() int64
	UpdateHLSSettings(settings HLSSettings)
}

type HLSSettingsStore interface {
	GetHLSSettings(ctx context.Context) (HLSSettings, bool, error)
	SetHLSSettings(ctx context.Context, settings HLSSettings) error
}

type HLSSettingsManager struct {
	engine  HLSSettingsEngine
	store   HLSSettingsStore
	timeout time.Duration
}

func NewHLSSettingsManager(engine HLSSettingsEngine, store HLSSettingsStore) *HLSSettingsManager {
	return &HLSSettingsManager{
		engine:  engine,
		store:   store,
		timeout: 5 * time.Second,
	}
}

func (m *HLSSettingsManager) Get() HLSSettings {
	return HLSSettings{
		SegmentDuration: m.engine.SegmentDuration(),
		RAMBufSizeMB:    int(m.engine.RAMBufSizeBytes() / (1024 * 1024)),
		PrebufferMB:     int(m.engine.PrebufferBytes() / (1024 * 1024)),
		WindowBeforeMB:  int(m.engine.WindowBeforeBytes() / (1024 * 1024)),
		WindowAfterMB:   int(m.engine.WindowAfterBytes() / (1024 * 1024)),
	}
}

func (m *HLSSettingsManager) Update(s HLSSettings) error {
	prev := m.Get()
	m.engine.UpdateHLSSettings(s)

	if m.store == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	if err := m.store.SetHLSSettings(ctx, s); err != nil {
		m.engine.UpdateHLSSettings(prev)
		return err
	}
	return nil
}
```

## services/torrent-engine/internal/app/config.go

```go
package app

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr           string
	MongoURI           string
	MongoDatabase      string
	MongoCollection    string
	LogLevel           string
	LogFormat          string
	TorrentDataDir     string
	OpenAPIPath        string
	MaxSessions        int   // 0 = unlimited
	MinDiskSpaceBytes  int64 // minimum free disk space; 0 = disabled (default 1 GB)
	FFMPEGPath         string
	FFProbePath        string
	HLSDir             string
	HLSPreset          string
	HLSCRF             int
	HLSAudioBitrate    string
	HLSSegmentDuration int
	HLSRAMBufSizeMB    int
	HLSPrebufferMB     int
	HLSWindowBeforeMB  int
	HLSWindowAfterMB   int
	CORSAllowedOrigins []string // empty = allow all (dev mode)
}

func LoadConfig() Config {
	return Config{
		HTTPAddr:          getEnv("HTTP_ADDR", ":8080"),
		MongoURI:          getEnv("MONGO_URI", "mongodb://localhost:27017"),
		MongoDatabase:     getEnv("MONGO_DB", "torrentstream"),
		MongoCollection:   getEnv("MONGO_COLLECTION", "torrents"),
		LogLevel:          strings.ToLower(getEnv("LOG_LEVEL", "info")),
		LogFormat:         strings.ToLower(getEnv("LOG_FORMAT", "text")),
		TorrentDataDir:    getEnv("TORRENT_DATA_DIR", "data"),
		OpenAPIPath:       getEnv("OPENAPI_PATH", ""),
		MaxSessions:        int(getEnvInt64("TORRENT_MAX_SESSIONS", 0)),
		MinDiskSpaceBytes:  getEnvInt64("TORRENT_MIN_DISK_SPACE_BYTES", 0),
		FFMPEGPath:        getEnv("FFMPEG_PATH", "ffmpeg"),
		FFProbePath:       getEnv("FFPROBE_PATH", "ffprobe"),
		HLSDir:            getEnv("HLS_DIR", ""),
		HLSPreset:         getEnv("HLS_PRESET", "veryfast"),
		HLSCRF:            int(getEnvInt64("HLS_CRF", 23)),
		HLSAudioBitrate:   getEnv("HLS_AUDIO_BITRATE", "128k"),
		HLSSegmentDuration: int(getEnvInt64("HLS_SEGMENT_DURATION", 2)),
		HLSRAMBufSizeMB:    int(getEnvInt64("HLS_RAMBUF_SIZE_MB", 16)),
		HLSPrebufferMB:     int(getEnvInt64("HLS_PREBUFFER_MB", 4)),
		HLSWindowBeforeMB:  int(getEnvInt64("HLS_WINDOW_BEFORE_MB", 8)),
		HLSWindowAfterMB:   int(getEnvInt64("HLS_WINDOW_AFTER_MB", 32)),
		CORSAllowedOrigins: parseCSV(getEnv("CORS_ALLOWED_ORIGINS", "")),
	}
}

func parseCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	if parsed < 0 {
		return fallback
	}
	return parsed
}
```

## services/torrent-engine/cmd/server/main.go

```go
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	apihttp "torrentstream/internal/api/http"
	"torrentstream/internal/app"
	"torrentstream/internal/domain"
	"torrentstream/internal/metrics"
	mongorepo "torrentstream/internal/repository/mongo"
	"torrentstream/internal/services/session/player"
	sessionmongo "torrentstream/internal/services/session/repository/mongo"
	"torrentstream/internal/services/torrent/engine/anacrolix"
	"torrentstream/internal/services/torrent/engine/ffprobe"
	"torrentstream/internal/telemetry"
	"torrentstream/internal/usecase"

	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.opentelemetry.io/contrib/instrumentation/go.mongodb.org/mongo-driver/mongo/otelmongo"
)

func main() {
	cfg := app.LoadConfig()
	logger := newLogger(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)
	metrics.Register(prometheus.DefaultRegisterer)

	shutdownTracer, err := telemetry.Init(context.Background(), "torrent-engine")
	if err != nil {
		logger.Warn("otel init failed", slog.String("error", err.Error()))
	}
	defer func() {
		if shutdownTracer != nil {
			_ = shutdownTracer(context.Background())
		}
	}()

	logger.Info("configuration loaded",
		slog.String("service", "torrent-engine"),
		slog.String("httpAddr", cfg.HTTPAddr),
		slog.String("logLevel", cfg.LogLevel),
		slog.String("logFormat", cfg.LogFormat),
		slog.String("hlsDir", cfg.HLSDir),
		slog.String("dataDir", cfg.TorrentDataDir),
	)

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctx, cancel := context.WithTimeout(rootCtx, 10*time.Second)
	defer cancel()

	mongoOpts := otelmongo.NewMonitor()
	mongoClient, err := mongorepo.Connect(ctx, cfg.MongoURI, options.Client().SetMonitor(mongoOpts))
	if err != nil {
		logger.Error("mongo connect failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if err := mongoClient.Ping(ctx, readpref.Primary()); err != nil {
		logger.Error("mongo ping failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	repo := mongorepo.NewRepository(mongoClient, cfg.MongoDatabase, cfg.MongoCollection)
	watchHistoryRepo := sessionmongo.NewWatchHistoryRepository(mongoClient, cfg.MongoDatabase)
	encodingSettingsRepo := mongorepo.NewEncodingSettingsRepository(mongoClient, cfg.MongoDatabase)
	hlsSettingsRepo := mongorepo.NewHLSSettingsRepository(mongoClient, cfg.MongoDatabase)
	playerSettingsRepo := sessionmongo.NewPlayerSettingsRepository(mongoClient, cfg.MongoDatabase)

	if err := repo.EnsureIndexes(ctx); err != nil {
		logger.Warn("mongo ensure indexes failed", slog.String("error", err.Error()))
	}

	if enc, ok, err := encodingSettingsRepo.GetEncodingSettings(ctx); err != nil {
		logger.Warn("encoding settings load failed", slog.String("error", err.Error()))
	} else if ok {
		cfg.HLSPreset = enc.Preset
		cfg.HLSCRF = enc.CRF
		cfg.HLSAudioBitrate = enc.AudioBitrate
	}

	if hls, ok, err := hlsSettingsRepo.GetHLSSettings(ctx); err != nil {
		logger.Warn("hls settings load failed", slog.String("error", err.Error()))
	} else if ok {
		if hls.SegmentDuration > 0 {
			cfg.HLSSegmentDuration = hls.SegmentDuration
		}
		if hls.RAMBufSizeMB > 0 {
			cfg.HLSRAMBufSizeMB = hls.RAMBufSizeMB
		}
		if hls.PrebufferMB > 0 {
			cfg.HLSPrebufferMB = hls.PrebufferMB
		}
		if hls.WindowBeforeMB > 0 {
			cfg.HLSWindowBeforeMB = hls.WindowBeforeMB
		}
		if hls.WindowAfterMB > 0 {
			cfg.HLSWindowAfterMB = hls.WindowAfterMB
		}
	}

	currentTorrentID := domain.TorrentID("")
	if id, ok, err := playerSettingsRepo.GetCurrentTorrentID(ctx); err != nil {
		logger.Warn("player settings load failed", slog.String("error", err.Error()))
	} else if ok {
		currentTorrentID = id
	}

	engine, err := anacrolix.New(anacrolix.Config{
		DataDir:     cfg.TorrentDataDir,
		MaxSessions: cfg.MaxSessions,
	})
	if err != nil {
		logger.Error("torrent engine init failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Restore previously active torrents from DB (in background so HTTP server starts immediately).
	// The focused torrent is opened and focused first so playback resumes with minimum delay.
	go func() {
		restoreTorrents(rootCtx, engine, repo, logger, currentTorrentID)
	}()

	// Start background state sync.
	syncUC := usecase.SyncState{Engine: engine, Repo: repo, Logger: logger}
	go syncUC.Run(rootCtx)

	// Start disk pressure monitor.
	if cfg.MinDiskSpaceBytes > 0 {
		diskUC := usecase.DiskPressure{
			Engine:        engine,
			Logger:        logger,
			DataDir:       cfg.TorrentDataDir,
			MinFreeBytes:  cfg.MinDiskSpaceBytes,
			ResumeBytes:   cfg.MinDiskSpaceBytes * 2,
		}
		go diskUC.Run(rootCtx)
	}

	createUC := usecase.CreateTorrent{Engine: engine, Repo: repo, Now: time.Now}
	startUC := usecase.StartTorrent{Engine: engine, Repo: repo, Now: time.Now}
	stopUC := usecase.StopTorrent{Engine: engine, Repo: repo, Now: time.Now}
	deleteUC := usecase.DeleteTorrent{Engine: engine, Repo: repo, DataDir: cfg.TorrentDataDir}
	streamUC := &usecase.StreamTorrent{Engine: engine, Repo: repo, ReadaheadBytes: 2 << 20}
	stateUC := usecase.GetTorrentState{Engine: engine}
	listStateUC := usecase.ListActiveTorrentStates{Engine: engine}
	mediaProbe := ffprobe.New(cfg.FFProbePath)
	playerSettings := player.NewPlayerSettingsManager(engine, playerSettingsRepo, currentTorrentID)

	hlsCfg := apihttp.HLSConfig{
		FFMPEGPath:      cfg.FFMPEGPath,
		FFProbePath:     cfg.FFProbePath,
		BaseDir:         cfg.HLSDir,
		DataDir:         cfg.TorrentDataDir,
		Preset:          cfg.HLSPreset,
		CRF:             cfg.HLSCRF,
		AudioBitrate:    cfg.HLSAudioBitrate,
		SegmentDuration: cfg.HLSSegmentDuration,
		RAMBufSizeMB:    cfg.HLSRAMBufSizeMB,
		PrebufferMB:     cfg.HLSPrebufferMB,
		WindowBeforeMB:  cfg.HLSWindowBeforeMB,
		WindowAfterMB:   cfg.HLSWindowAfterMB,
	}

	options := []apihttp.ServerOption{
		apihttp.WithRepository(repo),
		apihttp.WithLogger(logger),
		apihttp.WithStartTorrent(startUC),
		apihttp.WithStopTorrent(stopUC),
		apihttp.WithDeleteTorrent(deleteUC),
		apihttp.WithStreamTorrent(streamUC),
		apihttp.WithGetTorrentState(stateUC),
		apihttp.WithListTorrentStates(listStateUC),
		apihttp.WithHLS(hlsCfg),
		apihttp.WithMediaProbe(mediaProbe, cfg.TorrentDataDir),
		apihttp.WithWatchHistory(watchHistoryRepo),
		apihttp.WithEngine(engine),
		apihttp.WithPlayerSettings(playerSettings),
		apihttp.WithAllowedOrigins(cfg.CORSAllowedOrigins),
	}
	if cfg.OpenAPIPath != "" {
		options = append(options, apihttp.WithOpenAPIPath(cfg.OpenAPIPath))
	}

	handler := apihttp.NewServer(createUC, options...)

	// Wire encoding settings manager after server creation (needs HLS engine).
	if hlsEngine := handler.EncodingSettingsEngine(); hlsEngine != nil {
		encodingMgr := app.NewEncodingSettingsManager(hlsEngine, encodingSettingsRepo)
		handler.SetEncodingSettings(encodingMgr)
	}

	// Wire HLS settings manager after server creation (needs HLS engine).
	if hlsEngine := handler.HLSSettingsEngine(); hlsEngine != nil {
		hlsMgr := app.NewHLSSettingsManager(hlsEngine, hlsSettingsRepo)
		handler.SetHLSSettings(hlsMgr)
	}

	// Periodically update Prometheus gauges from engine state.
	go updateEngineMetrics(rootCtx, engine, handler.HLSCacheTotalSize, handler)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	logger.Info("server started", slog.String("addr", cfg.HTTPAddr))

	select {
	case <-rootCtx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	handler.Close()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown error", slog.String("error", err.Error()))
	}
	if err := engine.Close(); err != nil {
		logger.Warn("engine close error", slog.String("error", err.Error()))
	}
	if err := mongoClient.Disconnect(context.Background()); err != nil {
		logger.Warn("mongo disconnect error", slog.String("error", err.Error()))
	}

	logger.Info("server stopped")
}

func updateEngineMetrics(ctx context.Context, engine *anacrolix.Engine, cacheSize func() int64, handler *apihttp.Server) {
	stateTicker := time.NewTicker(5 * time.Second)
	torrentTicker := time.NewTicker(15 * time.Second)
	healthTicker := time.NewTicker(30 * time.Second)
	defer stateTicker.Stop()
	defer torrentTicker.Stop()
	defer healthTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-stateTicker.C:
			ids, err := engine.ListActiveSessions(ctx)
			if err != nil {
				continue
			}
			metrics.ActiveSessions.Set(float64(len(ids)))
			var dlTotal, ulTotal int64
			var peersTotal int64
			var states []domain.SessionState
			for _, id := range ids {
				state, err := engine.GetSessionState(ctx, id)
				if err != nil {
					continue
				}
				dlTotal += state.DownloadSpeed
				ulTotal += state.UploadSpeed
				peersTotal += int64(state.Peers)
				states = append(states, state)
			}
			metrics.DownloadSpeedBytes.Set(float64(dlTotal))
			metrics.UploadSpeedBytes.Set(float64(ulTotal))
			metrics.PeersConnected.Set(float64(peersTotal))
			if cacheSize != nil {
				metrics.HLSCacheSizeBytes.Set(float64(cacheSize()))
			}
			handler.BroadcastStates(states)
		case <-torrentTicker.C:
			handler.BroadcastTorrents()
		case <-healthTicker.C:
			handler.BroadcastHealth(ctx)
		}
	}
}

func restoreTorrents(ctx context.Context, engine *anacrolix.Engine, repo *mongorepo.Repository, logger *slog.Logger, priorityID domain.TorrentID) {
	active := domain.TorrentActive
	pending := domain.TorrentPending

	var records []domain.TorrentRecord
	for _, status := range []*domain.TorrentStatus{&active, &pending} {
		recs, err := repo.List(ctx, domain.TorrentFilter{Status: status})
		if err != nil {
			logger.Warn("restore: list failed", slog.String("status", string(*status)), slog.String("error", err.Error()))
			continue
		}
		records = append(records, recs...)
	}

	if len(records) == 0 {
		return
	}

	logger.Info("restoring torrents", slog.Int("count", len(records)))

	restoreOne := func(rec domain.TorrentRecord) {
		src := rec.Source
		if strings.TrimSpace(src.Magnet) == "" && strings.TrimSpace(src.Torrent) == "" {
			logger.Warn("restore: no source", slog.String("id", string(rec.ID)))
			return
		}
		session, err := engine.Open(ctx, src)
		if err != nil {
			logger.Warn("restore: open failed", slog.String("id", string(rec.ID)), slog.String("error", err.Error()))
			return
		}
		if rec.Status == domain.TorrentActive {
			if err := session.Start(); err != nil {
				logger.Warn("restore: start failed", slog.String("id", string(rec.ID)), slog.String("error", err.Error()))
			}
		}
		logger.Info("restored torrent", slog.String("id", string(rec.ID)), slog.String("name", rec.Name))
	}

	// Open and focus the priority torrent first so playback resumes immediately.
	for _, rec := range records {
		if rec.ID != priorityID {
			continue
		}
		restoreOne(rec)
		if err := engine.FocusSession(ctx, priorityID); err != nil {
			logger.Warn("restore focused torrent failed",
				slog.String("torrentId", string(priorityID)),
				slog.String("error", err.Error()),
			)
		}
		break
	}

	// Restore remaining torrents in parallel.
	var wg sync.WaitGroup
	for _, rec := range records {
		if rec.ID == priorityID {
			continue
		}
		wg.Add(1)
		go func(r domain.TorrentRecord) {
			defer wg.Done()
			restoreOne(r)
		}(rec)
	}
	wg.Wait()
}

func newLogger(levelRaw, formatRaw string) *slog.Logger {
	level := parseLogLevel(levelRaw)
	options := &slog.HandlerOptions{Level: level}
	format := strings.ToLower(strings.TrimSpace(formatRaw))
	if format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, options))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, options))
}

func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
```

## services/torrent-engine/docs/api.md

```md
# API Contracts

Base URL: `http://localhost:8080`  
Swagger UI: `/swagger`  
OpenAPI JSON: `/swagger/openapi.json` (source: `docs/openapi.json`)

All non-stream responses use `application/json`.

## Error Envelope
```json
{
  "error": {
    "code": "invalid_request",
    "message": "details"
  }
}
```

Common `error.code` values:
- `invalid_request`
- `not_found`
- `engine_error`
- `repository_error`
- `internal_error`
- `stream_unavailable`

## Torrent Control
- `POST /torrents`
- `GET /torrents`
- `GET /torrents/{id}`
- `POST /torrents/{id}/start`
- `POST /torrents/{id}/stop`
- `DELETE /torrents/{id}?deleteFiles=true|false`

## Session State
- `GET /torrents/{id}/state`
- `GET /torrents/state?status=active`

## Storage Settings
- `GET /settings/storage`
  - returns current storage mode and memory-spill settings.
- `PATCH /settings/storage`
  - body:
```json
{
  "memoryLimitBytes": 536870912
}
```
  - updates RAM limit at runtime (`0` = unlimited).
  - for `disk` mode returns `409 unsupported_operation`.

## Media Streaming
- `GET /torrents/{id}/stream?fileIndex={n}`
  - supports `Range: bytes=start-end`
  - returns `200` or `206`
- `GET /torrents/{id}/hls/{fileIndex}/index.m3u8`
  - optional query:
  - `audioTrack` (int, default `0`)
  - `subtitleTrack` (int, optional, burn-in)
  - returns playlist and triggers/reuses transcoding job
- `GET /torrents/{id}/hls/{fileIndex}/{segment}`
  - returns `.ts` segment

## Media Metadata
- `GET /torrents/{id}/media/{fileIndex}`
  - probes file with ffprobe
  - returns audio/subtitle track metadata
  - response:
```json
{
  "tracks": [
    {
      "index": 0,
      "type": "audio",
      "codec": "aac",
      "language": "eng",
      "title": "English",
      "default": true
    }
  ]
}
```

If probing fails (e.g. metadata not available yet), API returns `200` with empty `tracks`.

## Notes
- `TorrentRecord.Source` is persisted internally for session restore and not exposed in API JSON.
- `subtitleTrack` is implemented as subtitle burn-in in ffmpeg HLS pipeline.
- Complete schema reference: `docs/openapi.json`.
```


