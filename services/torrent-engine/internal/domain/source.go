package domain

type TorrentSource struct {
	Magnet  string `json:"magnet,omitempty"`
	Torrent string `json:"torrent,omitempty"`
}
