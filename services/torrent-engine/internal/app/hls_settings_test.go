package app

import (
	"context"
	"errors"
	"testing"
)

// ---- fakes ----

type fakeHLSEngine struct {
	segDur      int
	ramBuf      int64
	prebuffer   int64
	winBefore   int64
	winAfter    int64
	updateCalls int
}

func (f *fakeHLSEngine) SegmentDuration() int    { return f.segDur }
func (f *fakeHLSEngine) RAMBufSizeBytes() int64  { return f.ramBuf }
func (f *fakeHLSEngine) PrebufferBytes() int64   { return f.prebuffer }
func (f *fakeHLSEngine) WindowBeforeBytes() int64 { return f.winBefore }
func (f *fakeHLSEngine) WindowAfterBytes() int64  { return f.winAfter }
func (f *fakeHLSEngine) UpdateHLSSettings(s HLSSettings) {
	f.segDur = s.SegmentDuration
	f.ramBuf = int64(s.RAMBufSizeMB) * 1024 * 1024
	f.prebuffer = int64(s.PrebufferMB) * 1024 * 1024
	f.winBefore = int64(s.WindowBeforeMB) * 1024 * 1024
	f.winAfter = int64(s.WindowAfterMB) * 1024 * 1024
	f.updateCalls++
}

type fakeHLSStore struct {
	settings HLSSettings
	found    bool
	getErr   error
	setErr   error
	setCalls int
}

func (f *fakeHLSStore) GetHLSSettings(_ context.Context) (HLSSettings, bool, error) {
	return f.settings, f.found, f.getErr
}

func (f *fakeHLSStore) SetHLSSettings(_ context.Context, s HLSSettings) error {
	f.setCalls++
	if f.setErr != nil {
		return f.setErr
	}
	f.settings = s
	f.found = true
	return nil
}

// ---- tests ----

func TestHLSSettingsManager_Get(t *testing.T) {
	engine := &fakeHLSEngine{
		segDur:    2,
		ramBuf:    16 * 1024 * 1024,
		prebuffer: 4 * 1024 * 1024,
		winBefore: 8 * 1024 * 1024,
		winAfter:  64 * 1024 * 1024,
	}
	mgr := NewHLSSettingsManager(engine, nil)

	got := mgr.Get()

	if got.SegmentDuration != 2 {
		t.Errorf("expected segDur 2, got %d", got.SegmentDuration)
	}
	if got.RAMBufSizeMB != 16 {
		t.Errorf("expected ramBuf 16, got %d", got.RAMBufSizeMB)
	}
	if got.PrebufferMB != 4 {
		t.Errorf("expected prebuffer 4, got %d", got.PrebufferMB)
	}
	if got.WindowBeforeMB != 8 {
		t.Errorf("expected winBefore 8, got %d", got.WindowBeforeMB)
	}
	if got.WindowAfterMB != 64 {
		t.Errorf("expected winAfter 64, got %d", got.WindowAfterMB)
	}
}

func TestHLSSettingsManager_Update_NoStore(t *testing.T) {
	engine := &fakeHLSEngine{
		segDur:    2,
		ramBuf:    16 * 1024 * 1024,
		prebuffer: 4 * 1024 * 1024,
		winBefore: 8 * 1024 * 1024,
		winAfter:  64 * 1024 * 1024,
	}
	mgr := NewHLSSettingsManager(engine, nil)

	err := mgr.Update(HLSSettings{
		SegmentDuration: 4,
		RAMBufSizeMB:    32,
		PrebufferMB:     8,
		WindowBeforeMB:  16,
		WindowAfterMB:   128,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if engine.segDur != 4 {
		t.Errorf("expected segDur 4, got %d", engine.segDur)
	}
	if engine.ramBuf != 32*1024*1024 {
		t.Errorf("expected ramBuf 32MB, got %d", engine.ramBuf)
	}
}

func TestHLSSettingsManager_Update_WithStore(t *testing.T) {
	engine := &fakeHLSEngine{
		segDur:    2,
		ramBuf:    16 * 1024 * 1024,
		prebuffer: 4 * 1024 * 1024,
		winBefore: 8 * 1024 * 1024,
		winAfter:  64 * 1024 * 1024,
	}
	store := &fakeHLSStore{}
	mgr := NewHLSSettingsManager(engine, store)

	settings := HLSSettings{
		SegmentDuration: 6,
		RAMBufSizeMB:    64,
		PrebufferMB:     16,
		WindowBeforeMB:  32,
		WindowAfterMB:   256,
	}
	err := mgr.Update(settings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store.setCalls != 1 {
		t.Errorf("expected 1 store call, got %d", store.setCalls)
	}
	if store.settings.SegmentDuration != 6 {
		t.Errorf("expected stored segDur 6, got %d", store.settings.SegmentDuration)
	}
}

func TestHLSSettingsManager_Update_StoreError_Rollback(t *testing.T) {
	engine := &fakeHLSEngine{
		segDur:    2,
		ramBuf:    16 * 1024 * 1024,
		prebuffer: 4 * 1024 * 1024,
		winBefore: 8 * 1024 * 1024,
		winAfter:  64 * 1024 * 1024,
	}
	store := &fakeHLSStore{setErr: errors.New("db error")}
	mgr := NewHLSSettingsManager(engine, store)

	err := mgr.Update(HLSSettings{
		SegmentDuration: 8,
		RAMBufSizeMB:    128,
		PrebufferMB:     32,
		WindowBeforeMB:  64,
		WindowAfterMB:   512,
	})
	if err == nil {
		t.Fatal("expected error from store")
	}

	// Engine should be rolled back.
	if engine.segDur != 2 {
		t.Errorf("expected rollback segDur 2, got %d", engine.segDur)
	}
	if engine.ramBuf != 16*1024*1024 {
		t.Errorf("expected rollback ramBuf 16MB, got %d", engine.ramBuf)
	}
	if engine.updateCalls != 2 {
		t.Errorf("expected 2 update calls (set + rollback), got %d", engine.updateCalls)
	}
}

func TestHLSSettingsManager_GetReflectsEngineState(t *testing.T) {
	engine := &fakeHLSEngine{
		segDur:    2,
		ramBuf:    16 * 1024 * 1024,
		prebuffer: 4 * 1024 * 1024,
		winBefore: 8 * 1024 * 1024,
		winAfter:  64 * 1024 * 1024,
	}
	mgr := NewHLSSettingsManager(engine, nil)

	// Modify engine directly.
	engine.segDur = 10
	engine.ramBuf = 256 * 1024 * 1024

	got := mgr.Get()
	if got.SegmentDuration != 10 || got.RAMBufSizeMB != 256 {
		t.Errorf("Get should reflect engine state, got segDur=%d ramBuf=%d", got.SegmentDuration, got.RAMBufSizeMB)
	}
}

func TestHLSSettingsManager_GetConvertsToMB(t *testing.T) {
	engine := &fakeHLSEngine{
		segDur:    2,
		ramBuf:    100 * 1024 * 1024, // 100MB
		prebuffer: 50 * 1024 * 1024,  // 50MB
		winBefore: 25 * 1024 * 1024,  // 25MB
		winAfter:  200 * 1024 * 1024, // 200MB
	}
	mgr := NewHLSSettingsManager(engine, nil)

	got := mgr.Get()
	if got.RAMBufSizeMB != 100 {
		t.Errorf("expected 100MB, got %d", got.RAMBufSizeMB)
	}
	if got.PrebufferMB != 50 {
		t.Errorf("expected 50MB, got %d", got.PrebufferMB)
	}
	if got.WindowBeforeMB != 25 {
		t.Errorf("expected 25MB, got %d", got.WindowBeforeMB)
	}
	if got.WindowAfterMB != 200 {
		t.Errorf("expected 200MB, got %d", got.WindowAfterMB)
	}
}
