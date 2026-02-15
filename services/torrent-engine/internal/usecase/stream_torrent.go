package usecase

import (
	"context"
	"errors"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

const (
	defaultStreamReadahead         = 16 << 20
	priorityWindowMultiplier int64 = 4
	minPriorityWindowBytes   int64 = 32 << 20
	maxPriorityWindowBytes   int64 = 256 << 20
)

func streamPriorityWindow(readahead, fileLength int64) int64 {
	if readahead <= 0 {
		readahead = defaultStreamReadahead
	}
	window := readahead * priorityWindowMultiplier
	if window < minPriorityWindowBytes {
		window = minPriorityWindowBytes
	}
	// Scale up for large files: use 1% of file size if larger than base window.
	if fileLength > 0 {
		scaled := fileLength / 100
		if scaled > window {
			window = scaled
		}
	}
	if window > maxPriorityWindowBytes {
		window = maxPriorityWindowBytes
	}
	return window
}

type StreamResult struct {
	Reader ports.StreamReader
	File   domain.FileRef
}

type StreamTorrent struct {
	Engine         ports.Engine
	Repo           ports.TorrentRepository
	ReadaheadBytes int64
}

func (uc StreamTorrent) Execute(ctx context.Context, id domain.TorrentID, fileIndex int) (StreamResult, error) {
	if uc.Engine == nil {
		return StreamResult{}, errors.New("engine not configured")
	}

	session, err := uc.Engine.GetSession(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) && uc.Repo != nil {
			record, repoErr := uc.Repo.Get(ctx, id)
			if repoErr != nil {
				if errors.Is(repoErr, domain.ErrNotFound) {
					return StreamResult{}, repoErr
				}
				return StreamResult{}, wrapRepo(repoErr)
			}

			session, err = openSessionFromRecord(ctx, uc.Engine, record)
			if err != nil {
				if errors.Is(err, errMissingSource) {
					return StreamResult{}, domain.ErrNotFound
				}
				return StreamResult{}, wrapEngine(err)
			}
			if err := session.Start(); err != nil {
				return StreamResult{}, wrapEngine(err)
			}
		} else if errors.Is(err, domain.ErrNotFound) {
			return StreamResult{}, err
		} else {
			return StreamResult{}, wrapEngine(err)
		}
	}

	// Focus the session so it gets maximum bandwidth for streaming.
	_ = uc.Engine.FocusSession(ctx, id)

	file, err := session.SelectFile(fileIndex)
	if err != nil {
		return StreamResult{}, ErrInvalidFileIndex
	}

	readahead := uc.ReadaheadBytes
	if readahead <= 0 {
		readahead = defaultStreamReadahead
	}
	priorityWindow := streamPriorityWindow(readahead, file.Length)
	session.SetPiecePriority(file, domain.Range{Off: 0, Length: priorityWindow}, domain.PriorityHigh)

	reader, err := session.NewReader(file)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return StreamResult{}, ErrInvalidFileIndex
		}
		return StreamResult{}, wrapEngine(err)
	}
	if reader == nil {
		return StreamResult{}, errors.New("stream reader not available")
	}

	reader = newSlidingPriorityReader(reader, session, file, readahead, priorityWindow)
	reader.SetContext(ctx)
	reader.SetResponsive()

	// Use the full priority window as readahead so the torrent client
	// requests pieces well ahead of the current playback position.
	reader.SetReadahead(priorityWindow)

	return StreamResult{Reader: reader, File: file}, nil
}
