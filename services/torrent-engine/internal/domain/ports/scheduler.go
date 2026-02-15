package ports

import "torrentstream/internal/domain"

type Scheduler interface {
	OnRangeRequest(file domain.FileRef, r domain.Range)
	Prefetch(file domain.FileRef, from int64, bytes int64)
}
