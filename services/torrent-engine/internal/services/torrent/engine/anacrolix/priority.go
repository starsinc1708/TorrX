package anacrolix

import (
	"github.com/anacrolix/torrent"

	"torrentstream/internal/domain"
)

type focusedPieceRange struct {
	start int
	end   int
}

func (e *Engine) applyPiecePriority(t *torrent.Torrent, id domain.TorrentID, file domain.FileRef, r domain.Range, prio domain.Priority) {
	// Safety mode: avoid File.SetPriority/Piece.SetPriority calls.
	// They can trigger a fatal panic in anacrolix under heavy seek/focus churn:
	// "piece request order has {} and pending pieces has {...}".
	//
	// Stream prefetch remains functional via reader demand + Allow/DisallowDataDownload.
	// Priority requests for stopped/paused torrents are filtered in SetPiecePriority.
	_ = t
	_ = id
	_ = file
	_ = r
	_ = prio
}

func computeFocusedPieceRange(t *torrent.Torrent, f *torrent.File, r domain.Range) (focusedPieceRange, bool) {
	if t == nil || f == nil {
		return focusedPieceRange{}, false
	}
	if r.Length <= 0 {
		return focusedPieceRange{}, false
	}
	pieceSize := int64(t.Info().PieceLength)
	if pieceSize <= 0 {
		return focusedPieceRange{}, false
	}
	fileOffset := f.Offset()
	fileLength := f.Length()
	if fileLength <= 0 {
		return focusedPieceRange{}, false
	}
	start := fileOffset + r.Off
	if start < fileOffset {
		start = fileOffset
	}
	fileEnd := fileOffset + fileLength
	if start >= fileEnd {
		return focusedPieceRange{}, false
	}
	end := start + r.Length
	if end > fileEnd || end < start {
		end = fileEnd
	}

	startPiece := int(start / pieceSize)
	endPiece := int((end + pieceSize - 1) / pieceSize)
	if endPiece <= startPiece {
		endPiece = startPiece + 1
	}

	numPieces := t.NumPieces()
	if numPieces <= 0 {
		return focusedPieceRange{}, false
	}
	if startPiece < 0 {
		startPiece = 0
	}
	if startPiece >= numPieces {
		return focusedPieceRange{}, false
	}
	if endPiece > numPieces {
		endPiece = numPieces
	}
	if endPiece <= startPiece {
		return focusedPieceRange{}, false
	}

	return focusedPieceRange{start: startPiece, end: endPiece}, true
}

func (e *Engine) storeFocusedPieces(id domain.TorrentID, r focusedPieceRange) {
	if r.end <= r.start {
		return
	}
	e.priorityMu.Lock()
	if e.focusedPieces == nil {
		e.focusedPieces = make(map[domain.TorrentID]focusedPieceRange)
	}
	e.focusedPieces[id] = r
	e.priorityMu.Unlock()
}

func (e *Engine) clearFocusedPieces(id domain.TorrentID, t *torrent.Torrent) {
	e.priorityMu.Lock()
	_, ok := e.focusedPieces[id]
	if ok {
		delete(e.focusedPieces, id)
	}
	e.priorityMu.Unlock()

	_ = t
	_ = ok
}

func (e *Engine) forgetFocusedPieces(id domain.TorrentID) {
	e.priorityMu.Lock()
	if e.focusedPieces != nil {
		delete(e.focusedPieces, id)
	}
	e.priorityMu.Unlock()
}
