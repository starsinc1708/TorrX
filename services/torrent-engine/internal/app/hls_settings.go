package app

import (
	"context"
	"time"
)

type HLSSettings struct {
	MemBufSizeMB     int `json:"memBufSizeMB"`
	CacheSizeMB      int `json:"cacheSizeMB"`
	CacheMaxAgeHours int `json:"cacheMaxAgeHours"`
	SegmentDuration  int `json:"segmentDuration"`
}

type HLSSettingsEngine interface {
	MemBufSizeBytes() int64
	CacheSizeBytes() int64
	CacheMaxAge() time.Duration
	SegmentDuration() int
	UpdateHLSSettings(memBufSize, cacheSizeBytes, cacheMaxAgeHours int64, segmentDuration int)
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
		MemBufSizeMB:     int(m.engine.MemBufSizeBytes() / (1024 * 1024)),
		CacheSizeMB:      int(m.engine.CacheSizeBytes() / (1024 * 1024)),
		CacheMaxAgeHours: int(m.engine.CacheMaxAge().Hours()),
		SegmentDuration:  m.engine.SegmentDuration(),
	}
}

func (m *HLSSettingsManager) Update(s HLSSettings) error {
	prev := m.Get()
	m.engine.UpdateHLSSettings(
		int64(s.MemBufSizeMB)*1024*1024,
		int64(s.CacheSizeMB)*1024*1024,
		int64(s.CacheMaxAgeHours),
		s.SegmentDuration,
	)

	if m.store == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	if err := m.store.SetHLSSettings(ctx, s); err != nil {
		m.engine.UpdateHLSSettings(
			int64(prev.MemBufSizeMB)*1024*1024,
			int64(prev.CacheSizeMB)*1024*1024,
			int64(prev.CacheMaxAgeHours),
			prev.SegmentDuration,
		)
		return err
	}
	return nil
}
