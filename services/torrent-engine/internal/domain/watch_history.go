package domain

import "time"

type WatchPosition struct {
	TorrentID   TorrentID `json:"torrentId"`
	FileIndex   int       `json:"fileIndex"`
	Position    float64   `json:"position"`
	Duration    float64   `json:"duration"`
	TorrentName string    `json:"torrentName"`
	FilePath    string    `json:"filePath"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
