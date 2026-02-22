package apihttp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apihttp "torrentstream/notifier/internal/api/http"
)

const engineResp = `{"items":[
  {"id":"1","name":"A","status":"active","progress":0.5,"doneBytes":512,"totalBytes":1024,"createdAt":"2024-01-01T00:00:00Z","updatedAt":"2024-01-01T00:00:00Z"},
  {"id":"2","name":"B","status":"completed","progress":1.0,"doneBytes":1024,"totalBytes":1024,"createdAt":"2024-01-01T00:00:00Z","updatedAt":"2024-01-01T00:00:00Z"},
  {"id":"3","name":"C","status":"stopped","progress":0.1,"doneBytes":100,"totalBytes":1024,"createdAt":"2024-01-01T00:00:00Z","updatedAt":"2024-01-01T00:00:00Z"}
],"count":3}`

func TestWidget_CountsByStatus(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(engineResp))
	}))
	defer engine.Close()

	srv := apihttp.NewServer(engine.URL, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/widget", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["active"].(float64) != 1 {
		t.Errorf("expected active=1, got %v", resp["active"])
	}
	if resp["completed"].(float64) != 1 {
		t.Errorf("expected completed=1, got %v", resp["completed"])
	}
	if resp["stopped"].(float64) != 1 {
		t.Errorf("expected stopped=1, got %v", resp["stopped"])
	}
	if resp["total"].(float64) != 3 {
		t.Errorf("expected total=3, got %v", resp["total"])
	}
}

func TestHealth_ReturnsOK(t *testing.T) {
	srv := apihttp.NewServer("http://unused", nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp["status"])
	}
}
