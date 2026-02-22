package apihttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apihttp "torrentstream/notifier/internal/api/http"
	"torrentstream/notifier/internal/domain"
)

// inMemoryRepo is a test double for settingsRepository.
type inMemoryRepo struct {
	stored domain.IntegrationSettings
}

func (r *inMemoryRepo) Get(_ context.Context) (domain.IntegrationSettings, error) {
	return r.stored, nil
}

func (r *inMemoryRepo) Upsert(_ context.Context, s domain.IntegrationSettings) error {
	r.stored = s
	return nil
}

func TestSettings_GetReturnsDefaults(t *testing.T) {
	srv := apihttp.NewServer("http://unused", nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/settings/integrations", nil)
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["jellyfin"]; !ok {
		t.Error("response missing jellyfin field")
	}
	if _, ok := body["emby"]; !ok {
		t.Error("response missing emby field")
	}
	if _, ok := body["qbt"]; !ok {
		t.Error("response missing qbt field")
	}
}

func TestSettings_MethodNotAllowed(t *testing.T) {
	srv := apihttp.NewServer("http://unused", nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/settings/integrations", nil)
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestTestJellyfin_InvalidURL_ReturnsError(t *testing.T) {
	srv := apihttp.NewServer("http://unused", nil)
	body := `{"jellyfin":{"enabled":true,"url":"http://127.0.0.1:1","apiKey":"key"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/settings/integrations/test-jellyfin",
		bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["ok"] == true {
		t.Error("connection to port 1 should fail")
	}
}

func TestSettings_PatchSavesAndReturns(t *testing.T) {
	repo := &inMemoryRepo{}
	srv := apihttp.NewServer("http://unused", repo)

	body := `{"jellyfin":{"enabled":true,"url":"http://jellyfin:8096","apiKey":"testkey"},"emby":{"enabled":false,"url":"","apiKey":""},"qbt":{"enabled":false}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/settings/integrations",
		bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	jellyfin, ok := result["jellyfin"].(map[string]interface{})
	if !ok {
		t.Fatal("jellyfin field missing or wrong type")
	}
	if jellyfin["enabled"] != true {
		t.Errorf("expected jellyfin.enabled=true, got %v", jellyfin["enabled"])
	}
	if jellyfin["url"] != "http://jellyfin:8096" {
		t.Errorf("expected jellyfin.url=http://jellyfin:8096, got %v", jellyfin["url"])
	}
}

func TestSettings_PatchBadJSON_Returns400(t *testing.T) {
	repo := &inMemoryRepo{}
	srv := apihttp.NewServer("http://unused", repo)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/settings/integrations",
		bytes.NewBufferString("not json"))
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
