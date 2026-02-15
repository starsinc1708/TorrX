package ports

import "context"

type Storage interface {
	Size() int64
	ReadAt(ctx context.Context, p []byte, off int64) (int, error)
	WriteAt(p []byte, off int64) (int, error)
	MarkPieceDone(index int)
	WaitRange(ctx context.Context, off, length int64) error
	Close() error
}
