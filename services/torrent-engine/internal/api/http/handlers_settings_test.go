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

type fakeStorageSettingsCtrl struct {
	settings  app.StorageSettingsView
	updateErr error
}

func (f *fakeStorageSettingsCtrl) Get() app.StorageSettingsView { return f.settings }
func (f *fakeStorageSettingsCtrl) Update(s app.StorageSettings) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.settings.MaxSessions = s.MaxSessions
	f.settings.MinDiskSpaceBytes = s.MinDiskSpaceBytes
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

// ---- Storage Settings tests ----

func TestGetStorageSettings_ReturnsCurrentValues(t *testing.T) {
	ctrl := &fakeStorageSettingsCtrl{
		settings: app.StorageSettingsView{
			MaxSessions:       4,
			MinDiskSpaceBytes: 2147483648,
			Usage:             app.StorageUsage{DataDir: "data", DataDirExists: true, DataDirSizeBytes: 1024},
		},
	}
	s := makeSettingsServer(nil, nil)
	s.SetStorageSettings(ctrl)

	rec := doSettingsRequest(s, http.MethodGet, "/settings/storage", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got app.StorageSettingsView
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.MaxSessions != 4 || got.MinDiskSpaceBytes != 2147483648 {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestGetStorageSettings_NotConfigured(t *testing.T) {
	s := makeSettingsServer(nil, nil)
	rec := doSettingsRequest(s, http.MethodGet, "/settings/storage", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rec.Code)
	}
}

func TestUpdateStorageSettings_PartialUpdate(t *testing.T) {
	ctrl := &fakeStorageSettingsCtrl{
		settings: app.StorageSettingsView{
			MaxSessions:       2,
			MinDiskSpaceBytes: 1073741824,
		},
	}
	s := makeSettingsServer(nil, nil)
	s.SetStorageSettings(ctrl)

	body, _ := json.Marshal(map[string]any{"maxSessions": 8})
	rec := doSettingsRequest(s, http.MethodPatch, "/settings/storage", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ctrl.settings.MaxSessions != 8 || ctrl.settings.MinDiskSpaceBytes != 1073741824 {
		t.Fatalf("unexpected settings: %+v", ctrl.settings)
	}
}

func TestUpdateStorageSettings_InvalidValues(t *testing.T) {
	ctrl := &fakeStorageSettingsCtrl{
		settings: app.StorageSettingsView{MaxSessions: 2, MinDiskSpaceBytes: 1024},
	}
	s := makeSettingsServer(nil, nil)
	s.SetStorageSettings(ctrl)

	tests := []struct {
		name string
		body string
	}{
		{name: "negative maxSessions", body: `{"maxSessions":-1}`},
		{name: "negative minDiskSpaceBytes", body: `{"minDiskSpaceBytes":-1}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doSettingsRequest(s, http.MethodPatch, "/settings/storage", []byte(tc.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", rec.Code)
			}
		})
	}
}

func TestUpdateStorageSettings_BadJSON(t *testing.T) {
	ctrl := &fakeStorageSettingsCtrl{
		settings: app.StorageSettingsView{MaxSessions: 2, MinDiskSpaceBytes: 1024},
	}
	s := makeSettingsServer(nil, nil)
	s.SetStorageSettings(ctrl)

	rec := doSettingsRequest(s, http.MethodPatch, "/settings/storage", []byte("{bad"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestStorageSettings_MethodNotAllowed(t *testing.T) {
	ctrl := &fakeStorageSettingsCtrl{
		settings: app.StorageSettingsView{MaxSessions: 2, MinDiskSpaceBytes: 1024},
	}
	s := makeSettingsServer(nil, nil)
	s.SetStorageSettings(ctrl)

	rec := doSettingsRequest(s, http.MethodDelete, "/settings/storage", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}
