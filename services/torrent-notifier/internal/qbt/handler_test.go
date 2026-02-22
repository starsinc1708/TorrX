package qbt_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"torrentstream/notifier/internal/qbt"
)

// fakeEngine returns a mock torrent-engine response.
func fakeEngine(t *testing.T, resp string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(resp))
	}))
}

const engineListResp = `{
  "items": [
    {
      "id": "507f1f77bcf86cd799439011",
      "name": "Test.Torrent",
      "status": "active",
      "progress": 0.5,
      "doneBytes": 512,
      "totalBytes": 1024,
      "createdAt": "2024-01-01T00:00:00Z",
      "updatedAt": "2024-01-01T00:00:00Z"
    }
  ],
  "count": 1
}`

func TestQBT_TorrentsInfo_MapsStatus(t *testing.T) {
	engine := fakeEngine(t, engineListResp)
	defer engine.Close()

	h := qbt.NewHandler(engine.URL)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v2/torrents/info", nil)
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var items []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0]["state"] != "downloading" {
		t.Errorf("expected state=downloading, got %v", items[0]["state"])
	}
	if items[0]["hash"] != "507f1f77bcf86cd799439011" {
		t.Errorf("unexpected hash: %v", items[0]["hash"])
	}
}

func TestQBT_Login_AlwaysOk(t *testing.T) {
	h := qbt.NewHandler("http://unused")
	w := httptest.NewRecorder()
	form := url.Values{"username": {"admin"}, "password": {"pass"}}
	r := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "Ok." {
		t.Errorf("expected 'Ok.', got %q", w.Body.String())
	}
}

func TestQBT_AppVersion(t *testing.T) {
	h := qbt.NewHandler("http://unused")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v2/app/version", nil)
	h.ServeHTTP(w, r)
	if w.Body.String() != "4.6.0" {
		t.Errorf("expected 4.6.0, got %q", w.Body.String())
	}
}
