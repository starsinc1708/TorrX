package usecase

import (
	"context"
	"errors"
	"strings"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

var ErrInvalidSource = errors.New("invalid torrent source")

type CreateTorrent struct {
	Engine ports.Engine
	Repo   ports.TorrentRepository
	Now    func() time.Time
}

type CreateTorrentInput struct {
	Source domain.TorrentSource
	Name   string
}

func (uc CreateTorrent) Execute(ctx context.Context, input CreateTorrentInput) (domain.TorrentRecord, error) {
	if err := validateSource(input.Source); err != nil {
		return domain.TorrentRecord{}, err
	}

	now := time.Now
	if uc.Now != nil {
		now = uc.Now
	}

	session, err := uc.Engine.Open(ctx, input.Source)
	if err != nil {
		return domain.TorrentRecord{}, wrapEngine(err)
	}

	// If the torrent already exists in the repository, return the existing
	// record instead of failing with a duplicate key error.
	existing, getErr := uc.Repo.Get(ctx, session.ID())
	if getErr == nil {
		return existing, nil
	}

	files := session.Files()
	status := domain.TorrentActive

	if len(files) == 0 {
		// Metadata not yet available — torrent is pending
		status = domain.TorrentPending
	} else {
		if err := session.Start(); err != nil {
			return domain.TorrentRecord{}, wrapEngine(err)
		}
	}

	name := input.Name
	if name == "" {
		name = deriveName(files)
	}

	infoHash := parseInfoHash(input.Source.Magnet)
	if infoHash == "" {
		infoHash = domain.InfoHash(session.ID())
	}

	record := domain.TorrentRecord{
		ID:         session.ID(),
		Name:       name,
		Status:     status,
		InfoHash:   infoHash,
		Source:     input.Source,
		Files:      files,
		TotalBytes: sumFileLengths(files),
		DoneBytes:  0,
		CreatedAt:  now(),
		UpdatedAt:  now(),
	}

	if err := uc.Repo.Create(ctx, record); err != nil {
		_ = session.Stop()
		return domain.TorrentRecord{}, wrapRepo(err)
	}

	return record, nil
}

func validateSource(src domain.TorrentSource) error {
	hasMagnet := strings.TrimSpace(src.Magnet) != ""
	hasTorrent := strings.TrimSpace(src.Torrent) != ""
	if hasMagnet == hasTorrent {
		return ErrInvalidSource
	}
	return nil
}

func sumFileLengths(files []domain.FileRef) int64 {
	var total int64
	for _, f := range files {
		total += f.Length
	}
	return total
}

func deriveName(files []domain.FileRef) string {
	if len(files) == 0 {
		return ""
	}
	parts := splitPathParts(files[0].Path)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func splitPathParts(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	})
}

func parseInfoHash(magnet string) domain.InfoHash {
	magnet = strings.TrimSpace(magnet)
	if magnet == "" {
		return ""
	}

	lower := strings.ToLower(magnet)
	idx := strings.Index(lower, "xt=urn:btih:")
	if idx == -1 {
		return ""
	}

	start := idx + len("xt=urn:btih:")
	rest := magnet[start:]
	if rest == "" {
		return ""
	}

	end := strings.Index(rest, "&")
	if end == -1 {
		return domain.InfoHash(rest)
	}
	return domain.InfoHash(rest[:end])
}
