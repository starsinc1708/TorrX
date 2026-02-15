package parser

import (
	"context"
	"errors"
	"strings"
)

var ErrSearchNotImplemented = errors.New("torrent search service is not implemented yet")

type Result struct {
	Name      string `json:"name"`
	Magnet    string `json:"magnet"`
	SizeBytes int64  `json:"sizeBytes"`
	Seeds     int    `json:"seeds"`
	Peers     int    `json:"peers"`
	SourceURL string `json:"sourceUrl"`
	Tracker   string `json:"tracker"`
}

type TrackerClient interface {
	Search(ctx context.Context, query string, limit int) ([]Result, error)
}

type Service struct {
	clients []TrackerClient
}

func NewService(clients ...TrackerClient) *Service {
	filtered := make([]TrackerClient, 0, len(clients))
	for _, client := range clients {
		if client != nil {
			filtered = append(filtered, client)
		}
	}
	return &Service{clients: filtered}
}

func (s *Service) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("query is required")
	}
	if limit <= 0 {
		limit = 20
	}

	_ = ctx
	_ = limit

	// Placeholder: tracker parsing/integration will be implemented next.
	return nil, ErrSearchNotImplemented
}
