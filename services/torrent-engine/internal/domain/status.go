package domain

type TorrentStatus string

const (
	TorrentPending   TorrentStatus = "pending"
	TorrentActive    TorrentStatus = "active"
	TorrentCompleted TorrentStatus = "completed"
	TorrentStopped   TorrentStatus = "stopped"
	TorrentError     TorrentStatus = "error"
)
