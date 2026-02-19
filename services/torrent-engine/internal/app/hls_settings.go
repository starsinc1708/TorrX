package app

import (
	"context"
	"time"
)

type HLSSettings struct {
	SegmentDuration int `json:"segmentDuration"`
	RAMBufSizeMB    int `json:"ramBufSizeMB"`
	PrebufferMB     int `json:"prebufferMB"`
	WindowBeforeMB  int `json:"windowBeforeMB"`
	WindowAfterMB   int `json:"windowAfterMB"`
}

type HLSSettingsEngine interface {
	SegmentDuration() int
	RAMBufSizeBytes() int64
	PrebufferBytes() int64
	WindowBeforeBytes() int64
	WindowAfterBytes() int64
	UpdateHLSSettings(settings HLSSettings)
}

type HLSSettingsStore interface {
	GetHLSSettings(ctx context.Context) (HLSSettings, bool, error)
	SetHLSSettings(ctx context.Context, settings HLSSettings) error
}

type HLSSettingsManager struct {
	engine  HLSSettingsEngine
	store   HLSSettingsStore
	timeout time.Duration
}

func NewHLSSettingsManager(engine HLSSettingsEngine, store HLSSettingsStore) *HLSSettingsManager {
	return &HLSSettingsManager{
		engine:  engine,
		store:   store,
		timeout: 5 * time.Second,
	}
}

func (m *HLSSettingsManager) Get() HLSSettings {
	return HLSSettings{
		SegmentDuration: m.engine.SegmentDuration(),
		RAMBufSizeMB:    int(m.engine.RAMBufSizeBytes() / (1024 * 1024)),
		PrebufferMB:     int(m.engine.PrebufferBytes() / (1024 * 1024)),
		WindowBeforeMB:  int(m.engine.WindowBeforeBytes() / (1024 * 1024)),
		WindowAfterMB:   int(m.engine.WindowAfterBytes() / (1024 * 1024)),
	}
}

func (m *HLSSettingsManager) Update(s HLSSettings) error {
	prev := m.Get()
	m.engine.UpdateHLSSettings(s)

	if m.store == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	if err := m.store.SetHLSSettings(ctx, s); err != nil {
		m.engine.UpdateHLSSettings(prev)
		return err
	}
	return nil
}
