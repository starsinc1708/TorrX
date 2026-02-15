package domain

import "time"

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
