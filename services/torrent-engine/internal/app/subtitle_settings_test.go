package app

import (
	"context"
	"testing"
)

type fakeSubtitleStore struct {
	settings SubtitleSettings
	exists   bool
	getErr   error
	setErr   error
	setCalls int
}

func (f *fakeSubtitleStore) GetSubtitleSettings(ctx context.Context) (SubtitleSettings, bool, error) {
	return f.settings, f.exists, f.getErr
}

func (f *fakeSubtitleStore) SetSubtitleSettings(ctx context.Context, s SubtitleSettings) error {
	f.setCalls++
	f.settings = s
	f.exists = true
	return f.setErr
}

func TestSubtitleSettingsManager_GetDefault(t *testing.T) {
	mgr := NewSubtitleSettingsManager(nil)
	got := mgr.Get()
	if got.Enabled {
		t.Fatal("expected disabled by default")
	}
	if len(got.Languages) != 0 {
		t.Fatalf("expected no languages, got %v", got.Languages)
	}
}

func TestSubtitleSettingsManager_UpdateAndGet(t *testing.T) {
	store := &fakeSubtitleStore{}
	mgr := NewSubtitleSettingsManager(store)
	err := mgr.Update(SubtitleSettings{
		Enabled:    true,
		APIKey:     "test-key",
		Languages:  []string{"en", "ru"},
		AutoSearch: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mgr.Get()
	if !got.Enabled || got.APIKey != "test-key" || len(got.Languages) != 2 || !got.AutoSearch {
		t.Fatalf("unexpected settings: %+v", got)
	}
	if store.setCalls != 1 {
		t.Fatalf("expected 1 store call, got %d", store.setCalls)
	}
}
