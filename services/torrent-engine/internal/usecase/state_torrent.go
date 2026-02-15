package usecase

import (
	"context"
	"errors"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

type GetTorrentState struct {
	Engine ports.Engine
}

func (uc GetTorrentState) Execute(ctx context.Context, id domain.TorrentID) (domain.SessionState, error) {
	if uc.Engine == nil {
		return domain.SessionState{}, errors.New("engine not configured")
	}
	state, err := uc.Engine.GetSessionState(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.SessionState{}, err
		}
		return domain.SessionState{}, wrapEngine(err)
	}
	return state, nil
}

type ListActiveTorrentStates struct {
	Engine ports.Engine
}

func (uc ListActiveTorrentStates) Execute(ctx context.Context) ([]domain.SessionState, error) {
	if uc.Engine == nil {
		return nil, errors.New("engine not configured")
	}
	ids, err := uc.Engine.ListActiveSessions(ctx)
	if err != nil {
		return nil, wrapEngine(err)
	}
	states := make([]domain.SessionState, 0, len(ids))
	for _, id := range ids {
		state, err := uc.Engine.GetSessionState(ctx, id)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil, err
			}
			return nil, wrapEngine(err)
		}
		states = append(states, state)
	}
	return states, nil
}
