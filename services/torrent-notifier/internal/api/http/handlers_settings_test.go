package apihttp_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apihttp "torrentstream/notifier/internal/api/http"
)

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
