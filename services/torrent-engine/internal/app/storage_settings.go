package app

import (
	"context"
	"time"
)

type StorageSettingsEngine interface {
	StorageMode() string
	MemoryLimitBytes() int64
	SpillToDisk() bool
	SetMemoryLimitBytes(limit int64) error
}

type StorageSettingsStore interface {
	GetMemoryLimitBytes(ctx context.Context) (int64, bool, error)
	SetMemoryLimitBytes(ctx context.Context, limit int64) error
}

type StorageSettingsManager struct {
	engine  StorageSettingsEngine
	store   StorageSettingsStore
	timeout time.Duration
}

func NewStorageSettingsManager(engine StorageSettingsEngine, store StorageSettingsStore) *StorageSettingsManager {
	return &StorageSettingsManager{
		engine:  engine,
		store:   store,
		timeout: 5 * time.Second,
	}
}

func (s *StorageSettingsManager) StorageMode() string {
	return s.engine.StorageMode()
}

func (s *StorageSettingsManager) MemoryLimitBytes() int64 {
	return s.engine.MemoryLimitBytes()
}

func (s *StorageSettingsManager) SpillToDisk() bool {
	return s.engine.SpillToDisk()
}

func (s *StorageSettingsManager) SetMemoryLimitBytes(limit int64) error {
	current := s.engine.MemoryLimitBytes()
	if err := s.engine.SetMemoryLimitBytes(limit); err != nil {
		return err
	}

	if s.store == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	if err := s.store.SetMemoryLimitBytes(ctx, limit); err != nil {
		_ = s.engine.SetMemoryLimitBytes(current)
		return err
	}
	return nil
}
