package ports

import "torrentstream/internal/domain"

type Session interface {
	ID() domain.TorrentID
	Files() []domain.FileRef
	SelectFile(index int) (domain.FileRef, error)
	SetPiecePriority(file domain.FileRef, r domain.Range, prio domain.Priority)
	Start() error
	Stop() error
	NewReader(file domain.FileRef) (StreamReader, error)
}
