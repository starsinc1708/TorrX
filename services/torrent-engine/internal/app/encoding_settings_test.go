package app

import (
	"context"
	"errors"
	"testing"
)

// ---- fakes ----

type fakeEncodingEngine struct {
	preset       string
	crf          int
	audioBitrate string
	updateCalls  int
}

func (f *fakeEncodingEngine) EncodingPreset() string       { return f.preset }
func (f *fakeEncodingEngine) EncodingCRF() int             { return f.crf }
func (f *fakeEncodingEngine) EncodingAudioBitrate() string { return f.audioBitrate }
func (f *fakeEncodingEngine) UpdateEncodingSettings(preset string, crf int, audioBitrate string) {
	f.preset = preset
	f.crf = crf
	f.audioBitrate = audioBitrate
	f.updateCalls++
}

type fakeEncodingStore struct {
	settings EncodingSettings
	found    bool
	getErr   error
	setErr   error
	setCalls int
}

func (f *fakeEncodingStore) GetEncodingSettings(_ context.Context) (EncodingSettings, bool, error) {
	return f.settings, f.found, f.getErr
}

func (f *fakeEncodingStore) SetEncodingSettings(_ context.Context, s EncodingSettings) error {
	f.setCalls++
	if f.setErr != nil {
		return f.setErr
	}
	f.settings = s
	f.found = true
	return nil
}

// ---- tests ----

func TestEncodingSettingsManager_Get(t *testing.T) {
	engine := &fakeEncodingEngine{preset: "veryfast", crf: 23, audioBitrate: "128k"}
	mgr := NewEncodingSettingsManager(engine, nil)

	got := mgr.Get()

	if got.Preset != "veryfast" {
		t.Errorf("expected preset veryfast, got %q", got.Preset)
	}
	if got.CRF != 23 {
		t.Errorf("expected crf 23, got %d", got.CRF)
	}
	if got.AudioBitrate != "128k" {
		t.Errorf("expected audioBitrate 128k, got %q", got.AudioBitrate)
	}
}

func TestEncodingSettingsManager_Update_NoStore(t *testing.T) {
	engine := &fakeEncodingEngine{preset: "veryfast", crf: 23, audioBitrate: "128k"}
	mgr := NewEncodingSettingsManager(engine, nil)

	err := mgr.Update(EncodingSettings{Preset: "fast", CRF: 28, AudioBitrate: "192k"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if engine.preset != "fast" {
		t.Errorf("expected engine preset fast, got %q", engine.preset)
	}
	if engine.crf != 28 {
		t.Errorf("expected engine crf 28, got %d", engine.crf)
	}
	if engine.audioBitrate != "192k" {
		t.Errorf("expected engine audioBitrate 192k, got %q", engine.audioBitrate)
	}
}

func TestEncodingSettingsManager_Update_WithStore(t *testing.T) {
	engine := &fakeEncodingEngine{preset: "veryfast", crf: 23, audioBitrate: "128k"}
	store := &fakeEncodingStore{}
	mgr := NewEncodingSettingsManager(engine, store)

	settings := EncodingSettings{Preset: "medium", CRF: 20, AudioBitrate: "256k"}
	err := mgr.Update(settings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store.setCalls != 1 {
		t.Errorf("expected 1 store set call, got %d", store.setCalls)
	}
	if store.settings.Preset != "medium" {
		t.Errorf("expected store preset medium, got %q", store.settings.Preset)
	}
	if engine.preset != "medium" {
		t.Errorf("expected engine preset medium, got %q", engine.preset)
	}
}

func TestEncodingSettingsManager_Update_StoreError_Rollback(t *testing.T) {
	engine := &fakeEncodingEngine{preset: "veryfast", crf: 23, audioBitrate: "128k"}
	store := &fakeEncodingStore{setErr: errors.New("db error")}
	mgr := NewEncodingSettingsManager(engine, store)

	err := mgr.Update(EncodingSettings{Preset: "fast", CRF: 28, AudioBitrate: "192k"})
	if err == nil {
		t.Fatal("expected error from store")
	}

	// Engine should be rolled back to original values.
	if engine.preset != "veryfast" {
		t.Errorf("expected rollback to veryfast, got %q", engine.preset)
	}
	if engine.crf != 23 {
		t.Errorf("expected rollback to crf 23, got %d", engine.crf)
	}
	if engine.audioBitrate != "128k" {
		t.Errorf("expected rollback to 128k, got %q", engine.audioBitrate)
	}
	// Update was called twice: once for new values, once for rollback.
	if engine.updateCalls != 2 {
		t.Errorf("expected 2 update calls (set + rollback), got %d", engine.updateCalls)
	}
}

func TestEncodingSettingsManager_GetReflectsEngineState(t *testing.T) {
	engine := &fakeEncodingEngine{preset: "veryfast", crf: 23, audioBitrate: "128k"}
	mgr := NewEncodingSettingsManager(engine, nil)

	// Modify engine directly.
	engine.preset = "fast"
	engine.crf = 30

	got := mgr.Get()
	if got.Preset != "fast" || got.CRF != 30 {
		t.Errorf("Get should reflect engine state, got %+v", got)
	}
}
