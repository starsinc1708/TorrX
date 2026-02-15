package usecase

import (
	"context"
	"io"
	"sync"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

const (
	minSlidingPriorityStep = 1 << 20
)

type slidingPriorityReader struct {
	reader    ports.StreamReader
	session   ports.Session
	file      domain.FileRef
	window    int64
	backtrack int64
	step      int64

	mu      sync.Mutex
	pos     int64
	lastOff int64
}

func newSlidingPriorityReader(
	reader ports.StreamReader,
	session ports.Session,
	file domain.FileRef,
	readahead int64,
	window int64,
) ports.StreamReader {
	backtrack := readahead
	if backtrack < 0 {
		backtrack = 0
	}
	if backtrack > window/2 {
		backtrack = window / 2
	}

	step := window / 4
	if step < minSlidingPriorityStep {
		step = minSlidingPriorityStep
	}

	return &slidingPriorityReader{
		reader:    reader,
		session:   session,
		file:      file,
		window:    window,
		backtrack: backtrack,
		step:      step,
		lastOff:   0,
	}
}

func (r *slidingPriorityReader) SetContext(ctx context.Context) {
	r.reader.SetContext(ctx)
}

func (r *slidingPriorityReader) SetReadahead(n int64) {
	r.reader.SetReadahead(n)
}

func (r *slidingPriorityReader) SetResponsive() {
	r.reader.SetResponsive()
}

func (r *slidingPriorityReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.mu.Lock()
		r.pos += int64(n)
		r.updatePriorityWindowLocked(false)
		r.mu.Unlock()
	}
	if err != nil {
		return n, err
	}
	return n, nil
}

func (r *slidingPriorityReader) Seek(offset int64, whence int) (int64, error) {
	pos, err := r.reader.Seek(offset, whence)
	if err != nil {
		return pos, err
	}
	r.mu.Lock()
	r.pos = pos
	r.updatePriorityWindowLocked(true)
	r.mu.Unlock()
	return pos, nil
}

func (r *slidingPriorityReader) Close() error {
	return r.reader.Close()
}

func (r *slidingPriorityReader) updatePriorityWindowLocked(force bool) {
	off := r.pos - r.backtrack
	if off < 0 {
		off = 0
	}

	if !force {
		delta := off - r.lastOff
		if delta < 0 {
			delta = -delta
		}
		if delta < r.step {
			return
		}
	}

	r.session.SetPiecePriority(
		r.file,
		domain.Range{Off: off, Length: r.window},
		domain.PriorityHigh,
	)
	r.lastOff = off
}

var _ ports.StreamReader = (*slidingPriorityReader)(nil)
var _ io.ReadSeekCloser = (*slidingPriorityReader)(nil)
