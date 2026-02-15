package player

import (
	"context"
	"sync"
	"time"

	"torrentstream/internal/domain"
)

type PlayerSettingsEngine interface {
	FocusSession(ctx context.Context, id domain.TorrentID) error
	UnfocusAll(ctx context.Context) error
}

type PlayerSettingsStore interface {
	GetCurrentTorrentID(ctx context.Context) (domain.TorrentID, bool, error)
	SetCurrentTorrentID(ctx context.Context, id domain.TorrentID) error
}

type PlayerSettingsManager struct {
	engine    PlayerSettingsEngine
	store     PlayerSettingsStore
	timeout   time.Duration
	mu        sync.RWMutex
	currentID domain.TorrentID
}

func NewPlayerSettingsManager(engine PlayerSettingsEngine, store PlayerSettingsStore, currentID domain.TorrentID) *PlayerSettingsManager {
	return &PlayerSettingsManager{
		engine:    engine,
		store:     store,
		timeout:   5 * time.Second,
		currentID: currentID,
	}
}

func (m *PlayerSettingsManager) CurrentTorrentID() domain.TorrentID {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentID
}

func (m *PlayerSettingsManager) SetCurrentTorrentID(id domain.TorrentID) error {
	prev := m.CurrentTorrentID()

	if err := m.applyFocus(id); err != nil {
		return err
	}

	if m.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
		defer cancel()

		if err := m.store.SetCurrentTorrentID(ctx, id); err != nil {
			_ = m.applyFocus(prev)
			return err
		}
	}

	m.mu.Lock()
	m.currentID = id
	m.mu.Unlock()
	return nil
}

func (m *PlayerSettingsManager) applyFocus(id domain.TorrentID) error {
	if m.engine == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()

	if id == "" {
		return m.engine.UnfocusAll(ctx)
	}
	return m.engine.FocusSession(ctx, id)
}
