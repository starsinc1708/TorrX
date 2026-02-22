package app

import (
	"os"
	"strings"
)

type Config struct {
	HTTPAddr         string
	MongoURI         string
	MongoDatabase    string
	TorrentEngineURL string
	LogLevel         string
	LogFormat        string
}

func LoadConfig() Config {
	return Config{
		HTTPAddr:         getEnv("HTTP_ADDR", ":8070"),
		MongoURI:         getEnv("MONGO_URI", "mongodb://localhost:27017"),
		MongoDatabase:    getEnv("MONGO_DB", "torrentstream"),
		TorrentEngineURL: getEnv("TORRENT_ENGINE_URL", "http://localhost:8080"),
		LogLevel:         strings.ToLower(getEnv("LOG_LEVEL", "info")),
		LogFormat:        strings.ToLower(getEnv("LOG_FORMAT", "text")),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
