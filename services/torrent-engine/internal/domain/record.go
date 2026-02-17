package domain

import (
	"errors"
	"time"
)

type TorrentRecord struct {
	ID         TorrentID     `json:"id"`
	Name       string        `json:"name"`
	Status     TorrentStatus `json:"status"`
	InfoHash   InfoHash      `json:"infoHash"`
	Source     TorrentSource `json:"-"`
	Files      []FileRef     `json:"files"`
	TotalBytes int64         `json:"totalBytes"`
	DoneBytes  int64         `json:"doneBytes"`
	CreatedAt  time.Time     `json:"createdAt"`
	UpdatedAt  time.Time     `json:"updatedAt"`
	Tags       []string      `json:"tags"`
}

// ProgressUpdate holds fields for an atomic progress update via $max.
type ProgressUpdate struct {
	DoneBytes  int64
	TotalBytes int64
	Status     TorrentStatus
	Files      []FileRef
	Name       string
}

// Validate checks domain invariants for TorrentRecord.
func (r TorrentRecord) Validate() error {
	if r.ID == "" {
		return errors.New("torrent id is required")
	}
	if r.TotalBytes < 0 {
		return errors.New("totalBytes must not be negative")
	}
	if r.DoneBytes < 0 {
		return errors.New("doneBytes must not be negative")
	}
	if r.TotalBytes > 0 && r.DoneBytes > r.TotalBytes {
		return errors.New("doneBytes must not exceed totalBytes")
	}
	switch r.Status {
	case TorrentPending, TorrentActive, TorrentCompleted, TorrentStopped, TorrentError:
		// valid
	case "":
		return errors.New("status is required")
	default:
		return errors.New("invalid status: " + string(r.Status))
	}
	return nil
}
