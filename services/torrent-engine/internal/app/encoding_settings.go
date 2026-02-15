package app

import (
	"context"
	"time"
)

type EncodingSettings struct {
	Preset       string `json:"preset"`
	CRF          int    `json:"crf"`
	AudioBitrate string `json:"audioBitrate"`
}

type EncodingSettingsEngine interface {
	EncodingPreset() string
	EncodingCRF() int
	EncodingAudioBitrate() string
	UpdateEncodingSettings(preset string, crf int, audioBitrate string)
}

type EncodingSettingsStore interface {
	GetEncodingSettings(ctx context.Context) (EncodingSettings, bool, error)
	SetEncodingSettings(ctx context.Context, settings EncodingSettings) error
}

type EncodingSettingsManager struct {
	engine  EncodingSettingsEngine
	store   EncodingSettingsStore
	timeout time.Duration
}

func NewEncodingSettingsManager(engine EncodingSettingsEngine, store EncodingSettingsStore) *EncodingSettingsManager {
	return &EncodingSettingsManager{
		engine:  engine,
		store:   store,
		timeout: 5 * time.Second,
	}
}

func (m *EncodingSettingsManager) Get() EncodingSettings {
	return EncodingSettings{
		Preset:       m.engine.EncodingPreset(),
		CRF:          m.engine.EncodingCRF(),
		AudioBitrate: m.engine.EncodingAudioBitrate(),
	}
}

func (m *EncodingSettingsManager) Update(settings EncodingSettings) error {
	prev := m.Get()
	m.engine.UpdateEncodingSettings(settings.Preset, settings.CRF, settings.AudioBitrate)

	if m.store == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	if err := m.store.SetEncodingSettings(ctx, settings); err != nil {
		m.engine.UpdateEncodingSettings(prev.Preset, prev.CRF, prev.AudioBitrate)
		return err
	}
	return nil
}
