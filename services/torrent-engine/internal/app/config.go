package app

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr           string
	MongoURI           string
	MongoDatabase      string
	MongoCollection    string
	LogLevel           string
	LogFormat          string
	TorrentDataDir     string
	OpenAPIPath        string
	MaxSessions        int   // 0 = unlimited
	MinDiskSpaceBytes  int64 // minimum free disk space; 0 = disabled (default 1 GB)
	FFMPEGPath         string
	FFProbePath        string
	HLSDir             string
	HLSPreset          string
	HLSCRF             int
	HLSAudioBitrate    string
	HLSSegmentDuration int
	HLSRAMBufSizeMB    int
	HLSPrebufferMB     int
	HLSWindowBeforeMB  int
	HLSWindowAfterMB   int
	CORSAllowedOrigins []string // empty = allow all (dev mode)
}

func LoadConfig() Config {
	return Config{
		HTTPAddr:          getEnv("HTTP_ADDR", ":8080"),
		MongoURI:          getEnv("MONGO_URI", "mongodb://localhost:27017"),
		MongoDatabase:     getEnv("MONGO_DB", "torrentstream"),
		MongoCollection:   getEnv("MONGO_COLLECTION", "torrents"),
		LogLevel:          strings.ToLower(getEnv("LOG_LEVEL", "info")),
		LogFormat:         strings.ToLower(getEnv("LOG_FORMAT", "text")),
		TorrentDataDir:    getEnv("TORRENT_DATA_DIR", "data"),
		OpenAPIPath:       getEnv("OPENAPI_PATH", ""),
		MaxSessions:        int(getEnvInt64("TORRENT_MAX_SESSIONS", 0)),
		MinDiskSpaceBytes:  getEnvInt64("TORRENT_MIN_DISK_SPACE_BYTES", 0),
		FFMPEGPath:        getEnv("FFMPEG_PATH", "ffmpeg"),
		FFProbePath:       getEnv("FFPROBE_PATH", "ffprobe"),
		HLSDir:            getEnv("HLS_DIR", ""),
		HLSPreset:         getEnv("HLS_PRESET", "veryfast"),
		HLSCRF:            int(getEnvInt64("HLS_CRF", 23)),
		HLSAudioBitrate:   getEnv("HLS_AUDIO_BITRATE", "128k"),
		HLSSegmentDuration: int(getEnvInt64("HLS_SEGMENT_DURATION", 2)),
		HLSRAMBufSizeMB:    int(getEnvInt64("HLS_RAMBUF_SIZE_MB", 16)),
		HLSPrebufferMB:     int(getEnvInt64("HLS_PREBUFFER_MB", 4)),
		HLSWindowBeforeMB:  int(getEnvInt64("HLS_WINDOW_BEFORE_MB", 8)),
		HLSWindowAfterMB:   int(getEnvInt64("HLS_WINDOW_AFTER_MB", 32)),
		CORSAllowedOrigins: parseCSV(getEnv("CORS_ALLOWED_ORIGINS", "")),
	}
}

func parseCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	if parsed < 0 {
		return fallback
	}
	return parsed
}
