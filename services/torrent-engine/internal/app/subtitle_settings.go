package app

import (
	"context"
	"sync"
	"time"
)

type SubtitleSettings struct {
	Enabled    bool     `json:"enabled"`
	APIKey     string   `json:"apiKey"`
	Languages  []string `json:"languages"`
	AutoSearch bool     `json:"autoSearch"`
}

type SubtitleSettingsStore interface {
	GetSubtitleSettings(ctx context.Context) (SubtitleSettings, bool, error)
	SetSubtitleSettings(ctx context.Context, settings SubtitleSettings) error
}

type SubtitleSettingsManager struct {
	mu      sync.RWMutex
	current SubtitleSettings
	store   SubtitleSettingsStore
	timeout time.Duration
}

func NewSubtitleSettingsManager(store SubtitleSettingsStore) *SubtitleSettingsManager {
	mgr := &SubtitleSettingsManager{
		store:   store,
		timeout: 5 * time.Second,
	}
	if store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), mgr.timeout)
		defer cancel()
		if saved, ok, err := store.GetSubtitleSettings(ctx); err == nil && ok {
			mgr.current = saved
		}
	}
	return mgr
}

func (m *SubtitleSettingsManager) Get() SubtitleSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.current
	langs := make([]string, len(s.Languages))
	copy(langs, s.Languages)
	s.Languages = langs
	return s
}

func (m *SubtitleSettingsManager) Update(settings SubtitleSettings) error {
	if m.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
		defer cancel()
		if err := m.store.SetSubtitleSettings(ctx, settings); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.current = settings
	m.mu.Unlock()
	return nil
}
