package domain

import "time"

type SessionState struct {
	ID            TorrentID     `json:"id"`
	Status        TorrentStatus `json:"status"`
	Mode          SessionMode   `json:"mode,omitempty"`
	Progress      float64       `json:"progress"`
	Peers         int           `json:"peers"`
	DownloadSpeed int64         `json:"downloadSpeed"`
	UploadSpeed   int64         `json:"uploadSpeed"`
	Files         []FileRef     `json:"files,omitempty"`
	NumPieces     int           `json:"numPieces,omitempty"`
	PieceBitfield string        `json:"pieceBitfield,omitempty"`
	UpdatedAt     time.Time     `json:"updatedAt"`
}
