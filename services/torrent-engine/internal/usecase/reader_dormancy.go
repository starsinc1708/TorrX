package usecase

import (
	"sync"
	"time"

	"torrentstream/internal/domain"
)

const readerDormancyTimeout = 60 * time.Second

// readerRegistry tracks active slidingPriorityReader instances per torrent.
// When multiple readers exist for the same torrent, idle readers (no Read/Seek
// for 60s) are put to sleep: readahead set to 0 and priority window
// deprioritized. This prevents idle readers from consuming bandwidth that
// active readers need (matches TorrServer's reader dormancy pattern).
type readerRegistry struct {
	mu      sync.Mutex
	readers map[domain.TorrentID][]*slidingPriorityReader
}

func newReaderRegistry() *readerRegistry {
	return &readerRegistry{
		readers: make(map[domain.TorrentID][]*slidingPriorityReader),
	}
}

func (reg *readerRegistry) register(id domain.TorrentID, r *slidingPriorityReader) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.readers[id] = append(reg.readers[id], r)
}

func (reg *readerRegistry) unregister(id domain.TorrentID, r *slidingPriorityReader) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	list := reg.readers[id]
	for i, rr := range list {
		if rr == r {
			reg.readers[id] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(reg.readers[id]) == 0 {
		delete(reg.readers, id)
	}
}

// enforceDormancy checks all readers for the given torrent and puts idle ones
// to sleep when multiple readers exist. Called by active readers periodically.
// The caller's lock must NOT be held when calling this method.
func (reg *readerRegistry) enforceDormancy(id domain.TorrentID, caller *slidingPriorityReader) {
	reg.mu.Lock()
	readers := make([]*slidingPriorityReader, len(reg.readers[id]))
	copy(readers, reg.readers[id])
	reg.mu.Unlock()

	if len(readers) < 2 {
		return // no dormancy with a single reader
	}

	now := time.Now()
	for _, r := range readers {
		if r == caller {
			continue
		}
		r.mu.Lock()
		if !r.dormant && now.Sub(r.lastAccess) > readerDormancyTimeout {
			r.enterDormancyLocked()
		}
		r.mu.Unlock()
	}
}
