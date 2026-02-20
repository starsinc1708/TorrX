package domain

type MediaTrack struct {
	Index    int    `json:"index"`
	Type     string `json:"type"`
	Codec    string `json:"codec"`
	Language string `json:"language"`
	Title    string `json:"title"`
	Default  bool   `json:"default"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	FPS      float64 `json:"fps,omitempty"`
	Channels int    `json:"channels,omitempty"`
}

type MediaInfo struct {
	Tracks                   []MediaTrack `json:"tracks"`
	Duration                 float64      `json:"duration"`
	StartTime                float64      `json:"startTime"`
	SubtitlesReady           bool         `json:"subtitlesReady"`
	DirectPlaybackCompatible bool         `json:"directPlaybackCompatible"`
}
