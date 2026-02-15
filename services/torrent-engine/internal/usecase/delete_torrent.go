package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

type DeleteTorrent struct {
	Engine  ports.Engine
	Repo    ports.TorrentRepository
	DataDir string
}

func (uc DeleteTorrent) Execute(ctx context.Context, id domain.TorrentID, deleteFiles bool) error {
	record, err := uc.Repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return err
		}
		return wrapRepo(err)
	}

	if uc.Engine != nil {
		if err := uc.Engine.RemoveSession(ctx, id); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return wrapEngine(err)
		}
	}

	if deleteFiles {
		if err := removeTorrentFiles(uc.DataDir, record.Files); err != nil {
			return err
		}
	}

	if err := uc.Repo.Delete(ctx, id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return err
		}
		return wrapRepo(err)
	}
	return nil
}

func removeTorrentFiles(baseDir string, files []domain.FileRef) error {
	if strings.TrimSpace(baseDir) == "" {
		return errors.New("data dir not configured")
	}

	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return err
	}
	baseAbs = filepath.Clean(baseAbs)

	for _, file := range files {
		if strings.TrimSpace(file.Path) == "" {
			return errors.New("invalid file path")
		}
		if filepath.IsAbs(file.Path) {
			return errors.New("invalid file path")
		}
		relPath := filepath.FromSlash(file.Path)
		fullPath := filepath.Join(baseAbs, relPath)
		fullPath = filepath.Clean(fullPath)

		if !strings.HasPrefix(fullPath, baseAbs+string(os.PathSeparator)) && fullPath != baseAbs {
			return errors.New("invalid file path")
		}

		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}
