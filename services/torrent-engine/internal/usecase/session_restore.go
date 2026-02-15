package usecase

import (
	"context"
	"errors"
	"strings"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

var errMissingSource = errors.New("torrent source not available")

func openSessionFromRecord(ctx context.Context, engine ports.Engine, record domain.TorrentRecord) (ports.Session, error) {
	if !hasSource(record.Source) {
		return nil, errMissingSource
	}
	return engine.Open(ctx, record.Source)
}

func hasSource(src domain.TorrentSource) bool {
	return strings.TrimSpace(src.Magnet) != "" || strings.TrimSpace(src.Torrent) != ""
}
