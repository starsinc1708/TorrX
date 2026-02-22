package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
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

	// Delete DB record first, then files. This ensures that if file deletion fails,
	// the record is already gone and cleanup can be retried safely.
	if err := uc.Repo.Delete(ctx, id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return err
		}
		return wrapRepo(err)
	}

	if deleteFiles {
		if err := removeTorrentFiles(uc.DataDir, record.Files); err != nil {
			return err
		}
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

	// Accumulate errors instead of aborting on first failure
	var errs []error
	dirsToCleanup := make(map[string]struct{})

	for _, file := range files {
		if strings.TrimSpace(file.Path) == "" {
			errs = append(errs, errors.New("invalid file path: empty"))
			continue
		}
		if filepath.IsAbs(file.Path) {
			errs = append(errs, errors.New("invalid file path: absolute path"))
			continue
		}
		relPath := filepath.FromSlash(file.Path)
		if cleaned := filepath.Clean(relPath); cleaned == "." {
			errs = append(errs, errors.New("invalid file path: empty"))
			continue
		}
		fullPath := filepath.Join(baseAbs, relPath)
		fullPath = filepath.Clean(fullPath)

		if !strings.HasPrefix(fullPath, baseAbs+string(os.PathSeparator)) && fullPath != baseAbs {
			errs = append(errs, errors.New("invalid file path: outside base dir"))
			continue
		}

		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}

		// Remove now-empty parent directories up to baseAbs (exclusive).
		parent := filepath.Dir(fullPath)
		for parent != baseAbs {
			if !strings.HasPrefix(parent, baseAbs+string(os.PathSeparator)) {
				break
			}
			dirsToCleanup[parent] = struct{}{}
			next := filepath.Dir(parent)
			if next == parent {
				break
			}
			parent = next
		}
	}

	if len(dirsToCleanup) > 0 {
		dirs := make([]string, 0, len(dirsToCleanup))
		for dir := range dirsToCleanup {
			dirs = append(dirs, dir)
		}
		sort.Slice(dirs, func(i, j int) bool {
			return strings.Count(dirs[i], string(os.PathSeparator)) > strings.Count(dirs[j], string(os.PathSeparator))
		})

		for _, dir := range dirs {
			if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
				// Ignore non-empty directories; they may contain data from other torrents.
				if entries, readErr := os.ReadDir(dir); readErr == nil && len(entries) > 0 {
					continue
				}
				errs = append(errs, err)
			}
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
