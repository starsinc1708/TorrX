package app

import (
	"os"
	"testing"
)

func setEnvs(t *testing.T, envs map[string]string) {
	t.Helper()
	for k, v := range envs {
		t.Setenv(k, v)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	// Clear all env vars that LoadConfig reads so we get pure defaults.
	envVars := []string{
		"HTTP_ADDR", "MONGO_URI", "MONGO_DB", "MONGO_COLLECTION",
		"LOG_LEVEL", "LOG_FORMAT", "TORRENT_DATA_DIR", "OPENAPI_PATH",
		"TORRENT_MAX_SESSIONS", "TORRENT_MIN_DISK_SPACE_BYTES",
		"FFMPEG_PATH", "FFPROBE_PATH",
		"HLS_DIR", "HLS_PRESET", "HLS_CRF", "HLS_AUDIO_BITRATE",
		"HLS_SEGMENT_DURATION", "HLS_RAMBUF_SIZE_MB", "HLS_PREBUFFER_MB",
		"HLS_WINDOW_BEFORE_MB", "HLS_WINDOW_AFTER_MB",
		"CORS_ALLOWED_ORIGINS",
	}
	for _, k := range envVars {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	cfg := LoadConfig()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"HTTPAddr", cfg.HTTPAddr, ":8080"},
		{"MongoURI", cfg.MongoURI, "mongodb://localhost:27017"},
		{"MongoDatabase", cfg.MongoDatabase, "torrentstream"},
		{"MongoCollection", cfg.MongoCollection, "torrents"},
		{"LogLevel", cfg.LogLevel, "info"},
		{"LogFormat", cfg.LogFormat, "text"},
		{"TorrentDataDir", cfg.TorrentDataDir, "data"},
		{"OpenAPIPath", cfg.OpenAPIPath, ""},
		{"MaxSessions", cfg.MaxSessions, 0},
		{"MinDiskSpaceBytes", cfg.MinDiskSpaceBytes, int64(0)},
		{"FFMPEGPath", cfg.FFMPEGPath, "ffmpeg"},
		{"FFProbePath", cfg.FFProbePath, "ffprobe"},
		{"HLSDir", cfg.HLSDir, ""},
		{"HLSPreset", cfg.HLSPreset, "veryfast"},
		{"HLSCRF", cfg.HLSCRF, 23},
		{"HLSAudioBitrate", cfg.HLSAudioBitrate, "128k"},
		{"HLSSegmentDuration", cfg.HLSSegmentDuration, 2},
		{"HLSRAMBufSizeMB", cfg.HLSRAMBufSizeMB, 16},
		{"HLSPrebufferMB", cfg.HLSPrebufferMB, 4},
		{"HLSWindowBeforeMB", cfg.HLSWindowBeforeMB, 8},
		{"HLSWindowAfterMB", cfg.HLSWindowAfterMB, 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v (%T), want %v (%T)", tt.got, tt.got, tt.want, tt.want)
			}
		})
	}

	if len(cfg.CORSAllowedOrigins) != 0 {
		t.Errorf("CORSAllowedOrigins: got %v, want nil/empty", cfg.CORSAllowedOrigins)
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	setEnvs(t, map[string]string{
		"HTTP_ADDR":                  ":9090",
		"MONGO_URI":                  "mongodb://remote:27017",
		"MONGO_DB":                   "mydb",
		"MONGO_COLLECTION":           "mytorrents",
		"LOG_LEVEL":                  "DEBUG",
		"LOG_FORMAT":                 "JSON",
		"TORRENT_DATA_DIR":           "/mnt/data",
		"OPENAPI_PATH":               "/docs/openapi.json",
		"TORRENT_MAX_SESSIONS":       "10",
		"TORRENT_MIN_DISK_SPACE_BYTES": "1073741824",
		"FFMPEG_PATH":                "/usr/bin/ffmpeg",
		"FFPROBE_PATH":               "/usr/bin/ffprobe",
		"HLS_DIR":                    "/tmp/hls",
		"HLS_PRESET":                 "medium",
		"HLS_CRF":                    "18",
		"HLS_AUDIO_BITRATE":          "256k",
		"HLS_SEGMENT_DURATION":       "6",
		"HLS_RAMBUF_SIZE_MB":         "64",
		"HLS_PREBUFFER_MB":           "8",
		"HLS_WINDOW_BEFORE_MB":       "16",
		"HLS_WINDOW_AFTER_MB":        "64",
		"CORS_ALLOWED_ORIGINS":       "http://localhost:3000, https://example.com",
	})

	cfg := LoadConfig()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"HTTPAddr", cfg.HTTPAddr, ":9090"},
		{"MongoURI", cfg.MongoURI, "mongodb://remote:27017"},
		{"MongoDatabase", cfg.MongoDatabase, "mydb"},
		{"MongoCollection", cfg.MongoCollection, "mytorrents"},
		{"LogLevel", cfg.LogLevel, "debug"},
		{"LogFormat", cfg.LogFormat, "json"},
		{"TorrentDataDir", cfg.TorrentDataDir, "/mnt/data"},
		{"OpenAPIPath", cfg.OpenAPIPath, "/docs/openapi.json"},
		{"MaxSessions", cfg.MaxSessions, 10},
		{"MinDiskSpaceBytes", cfg.MinDiskSpaceBytes, int64(1073741824)},
		{"FFMPEGPath", cfg.FFMPEGPath, "/usr/bin/ffmpeg"},
		{"FFProbePath", cfg.FFProbePath, "/usr/bin/ffprobe"},
		{"HLSDir", cfg.HLSDir, "/tmp/hls"},
		{"HLSPreset", cfg.HLSPreset, "medium"},
		{"HLSCRF", cfg.HLSCRF, 18},
		{"HLSAudioBitrate", cfg.HLSAudioBitrate, "256k"},
		{"HLSSegmentDuration", cfg.HLSSegmentDuration, 6},
		{"HLSRAMBufSizeMB", cfg.HLSRAMBufSizeMB, 64},
		{"HLSPrebufferMB", cfg.HLSPrebufferMB, 8},
		{"HLSWindowBeforeMB", cfg.HLSWindowBeforeMB, 16},
		{"HLSWindowAfterMB", cfg.HLSWindowAfterMB, 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v (%T), want %v (%T)", tt.got, tt.got, tt.want, tt.want)
			}
		})
	}

	wantOrigins := []string{"http://localhost:3000", "https://example.com"}
	if len(cfg.CORSAllowedOrigins) != len(wantOrigins) {
		t.Fatalf("CORSAllowedOrigins: got %d entries, want %d", len(cfg.CORSAllowedOrigins), len(wantOrigins))
	}
	for i, got := range cfg.CORSAllowedOrigins {
		if got != wantOrigins[i] {
			t.Errorf("CORSAllowedOrigins[%d]: got %q, want %q", i, got, wantOrigins[i])
		}
	}
}

func TestGetEnvInt64InvalidFallsBack(t *testing.T) {
	tests := []struct {
		name     string
		envVal   string
		fallback int64
		want     int64
	}{
		{"empty string", "", 42, 42},
		{"not a number", "abc", 42, 42},
		{"negative number", "-5", 42, 42},
		{"zero", "0", 42, 0},
		{"valid positive", "100", 42, 100},
		{"whitespace around number", "  50  ", 42, 50},
		{"float", "3.14", 42, 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TEST_INT_VAR", tt.envVal)
			got := getEnvInt64("TEST_INT_VAR", tt.fallback)
			if got != tt.want {
				t.Errorf("getEnvInt64(%q, %d) = %d, want %d", tt.envVal, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestParseCSV(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty string", "", nil},
		{"whitespace only", "   ", nil},
		{"single value", "http://localhost:3000", []string{"http://localhost:3000"}},
		{"multiple values", "a,b,c", []string{"a", "b", "c"}},
		{"values with spaces", " a , b , c ", []string{"a", "b", "c"}},
		{"trailing comma", "a,b,", []string{"a", "b"}},
		{"empty entries filtered", "a,,b,,c", []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCSV(tt.input)
			if tt.want == nil {
				if got != nil {
					t.Errorf("parseCSV(%q) = %v, want nil", tt.input, got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseCSV(%q) returned %d elements, want %d", tt.input, len(got), len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("parseCSV(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestGetEnvFallback(t *testing.T) {
	t.Setenv("TEST_EXISTING", "hello")

	if got := getEnv("TEST_EXISTING", "default"); got != "hello" {
		t.Errorf("getEnv(existing) = %q, want %q", got, "hello")
	}

	// Unset to test fallback
	t.Setenv("TEST_MISSING_XYZ", "")
	os.Unsetenv("TEST_MISSING_XYZ")
	if got := getEnv("TEST_MISSING_XYZ", "default"); got != "default" {
		t.Errorf("getEnv(missing) = %q, want %q", got, "default")
	}
}

func TestLogLevelCaseInsensitive(t *testing.T) {
	// LoadConfig lowercases LOG_LEVEL, so "DEBUG" -> "debug"
	t.Setenv("LOG_LEVEL", "DEBUG")
	cfg := LoadConfig()
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel: got %q, want %q", cfg.LogLevel, "debug")
	}

	t.Setenv("LOG_LEVEL", "Warn")
	cfg = LoadConfig()
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel: got %q, want %q", cfg.LogLevel, "warn")
	}
}
