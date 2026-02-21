package domain

type FileRef struct {
	Index          int     `json:"index"`
	Path           string  `json:"path"`
	Length         int64   `json:"length"`
	BytesCompleted int64   `json:"bytesCompleted"`
	Progress       float64 `json:"progress"`
	Priority       string  `json:"priority,omitempty"`
	PieceStart     int     `json:"pieceStart,omitempty"` // inclusive
	PieceEnd       int     `json:"pieceEnd,omitempty"`   // exclusive
}
