package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type StorageSettings struct {
	MaxSessions       int   `json:"maxSessions"`
	MinDiskSpaceBytes int64 `json:"minDiskSpaceBytes"`
}

type StorageUsage struct {
	DataDir          string    `json:"dataDir"`
	DataDirExists    bool      `json:"dataDirExists"`
	DataDirSizeBytes int64     `json:"dataDirSizeBytes"`
	ScannedAt        time.Time `json:"scannedAt"`
}

type StorageSettingsView struct {
	MaxSessions       int          `json:"maxSessions"`
	MinDiskSpaceBytes int64        `json:"minDiskSpaceBytes"`
	Usage             StorageUsage `json:"usage"`
}

type StorageSettingsRuntime interface {
	MaxSessions() int
	SetMaxSessions(limit int)
}

type StorageSettingsStore interface {
	GetStorageSettings(ctx context.Context) (StorageSettings, bool, error)
	SetStorageSettings(ctx context.Context, settings StorageSettings) error
}

type StorageSettingsManager struct {
	mu                sync.RWMutex
	runtime           StorageSettingsRuntime
	store             StorageSettingsStore
	dataDir           string
	maxSessions       int
	minDiskSpaceBytes int64
	timeout           time.Duration
}

func NewStorageSettingsManager(
	dataDir string,
	initial StorageSettings,
	runtime StorageSettingsRuntime,
	store StorageSettingsStore,
) *StorageSettingsManager {
	return &StorageSettingsManager{
		runtime:           runtime,
		store:             store,
		dataDir:           filepath.Clean(dataDir),
		maxSessions:       initial.MaxSessions,
		minDiskSpaceBytes: initial.MinDiskSpaceBytes,
		timeout:           5 * time.Second,
	}
}

func (m *StorageSettingsManager) Get() StorageSettingsView {
	m.mu.RLock()
	currentMax := m.maxSessions
	currentMinFree := m.minDiskSpaceBytes
	dataDir := m.dataDir
	m.mu.RUnlock()

	if m.runtime != nil {
		currentMax = m.runtime.MaxSessions()
	}

	return StorageSettingsView{
		MaxSessions:       currentMax,
		MinDiskSpaceBytes: currentMinFree,
		Usage:             scanStorageUsage(dataDir),
	}
}

func (m *StorageSettingsManager) Update(next StorageSettings) error {
	prev := m.Get()

	if m.runtime != nil && next.MaxSessions != prev.MaxSessions {
		m.runtime.SetMaxSessions(next.MaxSessions)
	}

	m.mu.Lock()
	m.maxSessions = next.MaxSessions
	m.minDiskSpaceBytes = next.MinDiskSpaceBytes
	m.mu.Unlock()

	if m.store == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	if err := m.store.SetStorageSettings(ctx, next); err != nil {
		if m.runtime != nil && next.MaxSessions != prev.MaxSessions {
			m.runtime.SetMaxSessions(prev.MaxSessions)
		}
		m.mu.Lock()
		m.maxSessions = prev.MaxSessions
		m.minDiskSpaceBytes = prev.MinDiskSpaceBytes
		m.mu.Unlock()
		return err
	}

	return nil
}

func scanStorageUsage(dataDir string) StorageUsage {
	usage := StorageUsage{
		DataDir:   dataDir,
		ScannedAt: time.Now().UTC(),
	}
	if dataDir == "" {
		return usage
	}

	info, err := os.Stat(dataDir)
	if err != nil || !info.IsDir() {
		return usage
	}
	usage.DataDirExists = true

	var total int64
	_ = filepath.WalkDir(dataDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		fileInfo, err := d.Info()
		if err != nil {
			return nil
		}
		total += fileInfo.Size()
		return nil
	})
	usage.DataDirSizeBytes = total
	return usage
}
