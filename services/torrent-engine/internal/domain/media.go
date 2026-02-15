package domain

type MediaTrack struct {
	Index    int    `json:"index"`
	Type     string `json:"type"`
	Codec    string `json:"codec"`
	Language string `json:"language"`
	Title    string `json:"title"`
	Default  bool   `json:"default"`
}

type MediaInfo struct {
	Tracks         []MediaTrack `json:"tracks"`
	Duration       float64      `json:"duration"`
	SubtitlesReady bool         `json:"subtitlesReady"`
}
