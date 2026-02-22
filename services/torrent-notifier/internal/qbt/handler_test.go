package qbt_test

import (
	"encoding/json"
	"fmt"
	"io"
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

func TestQBT_Login_SetsSIDCookie(t *testing.T) {
	h := qbt.NewHandler("http://unused")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", nil)
	h.ServeHTTP(w, r)
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "SID" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected SID cookie to be set")
	}
}

func TestQBT_WebapiVersion(t *testing.T) {
	h := qbt.NewHandler("http://unused")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v2/app/webapiVersion", nil)
	h.ServeHTTP(w, r)
	if w.Body.String() != "2.8.3" {
		t.Errorf("expected 2.8.3, got %q", w.Body.String())
	}
}

func TestQBT_TorrentsAdd_ForwardsMagnet(t *testing.T) {
	var gotBody []byte
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/torrents" {
			t.Errorf("expected /torrents, got %s", r.URL.Path)
		}
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer engine.Close()

	h := qbt.NewHandler(engine.URL)
	form := url.Values{"urls": {"magnet:?xt=urn:btih:abc123"}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v2/torrents/add",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "Ok." {
		t.Errorf("expected Ok., got %q", w.Body.String())
	}
	if !strings.Contains(string(gotBody), "abc123") {
		t.Errorf("engine body should contain magnet hash, got: %s", gotBody)
	}
}

func TestQBT_TorrentsDelete_ForwardsDELETE(t *testing.T) {
	var gotMethod, gotPath string
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer engine.Close()

	h := qbt.NewHandler(engine.URL)
	form := url.Values{"hashes": {"abc123"}, "deleteFiles": {"false"}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v2/torrents/delete",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(w, r)

	if gotMethod != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
	if gotPath != "/torrents/abc123" {
		t.Errorf("expected /torrents/abc123, got %s", gotPath)
	}
}

func TestQBT_TorrentsInfo_AllStatusMappings(t *testing.T) {
	cases := []struct {
		engineStatus string
		wantState    string
	}{
		{"active", "downloading"},
		{"completed", "uploading"},
		{"stopped", "pausedDL"},
		{"pending", "checkingDL"},
		{"error", "error"},
		{"unknown_x", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.engineStatus, func(t *testing.T) {
			resp := fmt.Sprintf(`{"items":[{"id":"abc","name":"T","status":%q,"progress":0,"doneBytes":0,"totalBytes":0,"createdAt":"2024-01-01T00:00:00Z","updatedAt":"2024-01-01T00:00:00Z"}],"count":1}`, tc.engineStatus)
			engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(resp))
			}))
			defer engine.Close()

			h := qbt.NewHandler(engine.URL)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/api/v2/torrents/info", nil)
			h.ServeHTTP(w, r)

			var items []map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(items) != 1 {
				t.Fatalf("expected 1 item, got %d", len(items))
			}
			if items[0]["state"] != tc.wantState {
				t.Errorf("status %q: expected state=%q, got %v", tc.engineStatus, tc.wantState, items[0]["state"])
			}
		})
	}
}
