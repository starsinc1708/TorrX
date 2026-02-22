package usecase

import (
	"context"
	"errors"
	"sync"

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
	Reader          ports.StreamReader
	File            domain.FileRef
	Generation      uint64         // HLS generation counter; used for stale reader detection
	ConsumptionRate func() float64 // returns EMA consumer read rate in bytes/sec; nil if unavailable
}

type StreamPrioritySettings interface {
	PrioritizeActiveFileOnly() bool
}

type StreamTorrent struct {
	Engine         ports.Engine
	Repo           ports.TorrentRepository
	ReadaheadBytes int64
	PlayerSettings StreamPrioritySettings

	readersOnce sync.Once
	readers     *readerRegistry
}

func (uc *StreamTorrent) getRegistry() *readerRegistry {
	uc.readersOnce.Do(func() {
		uc.readers = newReaderRegistry()
	})
	return uc.readers
}

func (uc *StreamTorrent) Execute(ctx context.Context, id domain.TorrentID, fileIndex int) (StreamResult, error) {
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
				_ = session.Stop() // Clean up session on Start failure
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

	applyFilePriorityPolicy(session, file, uc.prioritizeActiveFileOnly())

	readahead := uc.ReadaheadBytes
	if readahead <= 0 {
		readahead = defaultStreamReadahead
	}
	priorityWindow := streamPriorityWindow(readahead, file.Length)
	applyStartupGradient(session, file, priorityWindow)

	// Preload file tail for container headers (MP4 moov atoms, MKV SeekHead/Cues).
	// Players commonly seek to the file end first to read container metadata.
	const tailPreloadSize int64 = 16 << 20
	if file.Length > tailPreloadSize*2 {
		session.SetPiecePriority(file,
			domain.Range{Off: file.Length - tailPreloadSize, Length: tailPreloadSize},
			domain.PriorityReadahead)
	}

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

	reg := uc.getRegistry()
	spr := newSlidingPriorityReader(reader, session, file, readahead, priorityWindow, reg, id)
	reg.register(id, spr)
	spr.SetContext(ctx)

	// Use the full priority window as readahead so the torrent client
	// requests pieces well ahead of the current playback position.
	spr.SetReadahead(priorityWindow)

	return StreamResult{
		Reader:          spr,
		File:            file,
		ConsumptionRate: spr.EffectiveBytesPerSec,
	}, nil
}

// ExecuteRaw is the same as Execute but returns the raw ports.StreamReader
// without wrapping it in a slidingPriorityReader. Use this when the caller
// manages download priorities externally (e.g. FSM-based streaming with
// PriorityManager).
func (uc *StreamTorrent) ExecuteRaw(ctx context.Context, id domain.TorrentID, fileIndex int) (StreamResult, error) {
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
				_ = session.Stop()
				return StreamResult{}, wrapEngine(err)
			}
		} else if errors.Is(err, domain.ErrNotFound) {
			return StreamResult{}, err
		} else {
			return StreamResult{}, wrapEngine(err)
		}
	}

	_ = uc.Engine.FocusSession(ctx, id)

	file, err := session.SelectFile(fileIndex)
	if err != nil {
		return StreamResult{}, ErrInvalidFileIndex
	}

	applyFilePriorityPolicy(session, file, uc.prioritizeActiveFileOnly())

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

	return StreamResult{
		Reader: reader,
		File:   file,
	}, nil
}

func (uc *StreamTorrent) prioritizeActiveFileOnly() bool {
	if uc.PlayerSettings == nil {
		return true
	}
	return uc.PlayerSettings.PrioritizeActiveFileOnly()
}

// applyFilePriorityPolicy adjusts priorities for non-selected files in the
// torrent while ensuring the selected file stays at least PriorityNormal.
func applyFilePriorityPolicy(session ports.Session, activeFile domain.FileRef, activeFileOnly bool) {
	files := session.Files()
	if len(files) <= 1 {
		return
	}

	nonActivePriority := domain.PriorityLow
	if activeFileOnly {
		nonActivePriority = domain.PriorityNone
	}

	for _, file := range files {
		if file.Length <= 0 {
			continue
		}
		if file.Index == activeFile.Index {
			continue
		}
		session.SetPiecePriority(file, domain.Range{Off: 0, Length: file.Length}, nonActivePriority)
	}

	if activeFile.Length > 0 {
		session.SetPiecePriority(activeFile, domain.Range{Off: 0, Length: activeFile.Length}, domain.PriorityNormal)
	}
}

// applyStartupGradient sets a graduated priority on the initial window instead
// of a flat PriorityHigh. The first 4 MB gets PriorityHigh so those pieces
// arrive fastest, then graduated bands so the torrent client focuses on the
// most urgent bytes first.
func applyStartupGradient(session ports.Session, file domain.FileRef, window int64) {
	const (
		startupHighBand int64 = 4 << 20 // 4 MB
		startupNextBand int64 = 4 << 20 // 4 MB
	)
	remaining := window

	h := startupHighBand
	if h > remaining {
		h = remaining
	}
	session.SetPiecePriority(file, domain.Range{Off: 0, Length: h}, domain.PriorityHigh)
	remaining -= h

	if remaining > 0 {
		n := startupNextBand
		if n > remaining {
			n = remaining
		}
		session.SetPiecePriority(file, domain.Range{Off: h, Length: n}, domain.PriorityNext)
		remaining -= n
	}
	if remaining > 0 {
		ra := remaining / 4
		if ra < startupHighBand {
			ra = remaining
		}
		if ra > remaining {
			ra = remaining
		}
		off := h + startupNextBand
		session.SetPiecePriority(file, domain.Range{Off: off, Length: ra}, domain.PriorityReadahead)
		remaining -= ra
	}
	if remaining > 0 {
		off := window - remaining
		session.SetPiecePriority(file, domain.Range{Off: off, Length: remaining}, domain.PriorityNormal)
	}
}
