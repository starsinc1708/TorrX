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
	DataDir                      string    `json:"dataDir"`
	DataDirExists                bool      `json:"dataDirExists"`
	DataDirSizeBytes             int64     `json:"dataDirSizeBytes"` // Deprecated alias for dataDirLogicalBytes.
	DataDirLogicalBytes          int64     `json:"dataDirLogicalBytes"`
	DataDirAllocatedBytes        int64     `json:"dataDirAllocatedBytes"`
	TorrentClientDownloadedBytes int64     `json:"torrentClientDownloadedBytes"`
	ScannedAt                    time.Time `json:"scannedAt"`
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
	downloadedBytesFn func(ctx context.Context) (int64, error)
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
	downloadedBytesFn func(ctx context.Context) (int64, error),
) *StorageSettingsManager {
	return &StorageSettingsManager{
		runtime:           runtime,
		store:             store,
		downloadedBytesFn: downloadedBytesFn,
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

	usage := scanStorageUsage(dataDir)
	if m.downloadedBytesFn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
		defer cancel()
		if total, err := m.downloadedBytesFn(ctx); err == nil && total >= 0 {
			usage.TorrentClientDownloadedBytes = total
		}
	}

	return StorageSettingsView{
		MaxSessions:       currentMax,
		MinDiskSpaceBytes: currentMinFree,
		Usage:             usage,
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

	var logicalTotal int64
	var allocatedTotal int64
	_ = filepath.WalkDir(dataDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		fileInfo, err := d.Info()
		if err != nil {
			return nil
		}
		size := fileInfo.Size()
		if size > 0 {
			logicalTotal += size
		}
		allocated := fileAllocatedBytes(fileInfo)
		if allocated > 0 {
			allocatedTotal += allocated
		}
		return nil
	})
	usage.DataDirLogicalBytes = logicalTotal
	usage.DataDirSizeBytes = logicalTotal
	usage.DataDirAllocatedBytes = allocatedTotal
	return usage
}
