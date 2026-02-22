package domain

// MediaServerConfig holds connection details for a single media server.
type MediaServerConfig struct {
	Enabled bool   `bson:"enabled" json:"enabled"`
	URL     string `bson:"url"     json:"url"`
	APIKey  string `bson:"apiKey"  json:"apiKey"`
}

// QBTConfig controls the qBittorrent API compatibility layer.
type QBTConfig struct {
	Enabled bool `bson:"enabled" json:"enabled"`
}

// IntegrationSettings is the single settings document stored in MongoDB.
type IntegrationSettings struct {
	Jellyfin  MediaServerConfig `bson:"jellyfin"  json:"jellyfin"`
	Emby      MediaServerConfig `bson:"emby"      json:"emby"`
	QBT       QBTConfig         `bson:"qbt"       json:"qbt"`
	UpdatedAt int64             `bson:"updatedAt" json:"updatedAt"`
}
