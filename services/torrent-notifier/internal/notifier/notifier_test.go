package notifier_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"torrentstream/notifier/internal/domain"
	"torrentstream/notifier/internal/notifier"
)

func TestNotifier_Jellyfin_CallsLibraryRefresh(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/Library/Refresh" {
			t.Errorf("expected /Library/Refresh, got %s", r.URL.Path)
		}
		if r.Header.Get("X-Emby-Token") != "testkey" {
			t.Errorf("expected X-Emby-Token: testkey, got %s", r.Header.Get("X-Emby-Token"))
		}
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	cfg := domain.MediaServerConfig{
		Enabled: true,
		URL:     ts.URL,
		APIKey:  "testkey",
	}
	n := notifier.New()
	err := n.NotifyMediaServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("server was not called")
	}
}

func TestNotifier_Disabled_DoesNotCall(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer ts.Close()

	cfg := domain.MediaServerConfig{Enabled: false, URL: ts.URL, APIKey: "key"}
	n := notifier.New()
	err := n.NotifyMediaServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("server should not have been called when disabled")
	}
}

func TestNotifier_EmptyURL_ReturnsNil(t *testing.T) {
	cfg := domain.MediaServerConfig{Enabled: true, URL: "", APIKey: "key"}
	n := notifier.New()
	err := n.NotifyMediaServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("expected nil for empty URL, got %v", err)
	}
}
