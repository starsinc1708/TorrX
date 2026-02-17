package apihttp

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"torrentstream/internal/domain"
	"torrentstream/internal/usecase"
)

func TestDirectFileSourceInputSpec(t *testing.T) {
	ds := &directFileSource{path: "/data/movie.mkv"}
	input, reader := ds.InputSpec()
	if input != "/data/movie.mkv" {
		t.Fatalf("expected path /data/movie.mkv, got %q", input)
	}
	if reader != nil {
		t.Fatalf("expected nil reader for directFileSource, got non-nil")
	}
}

func TestHttpStreamSourceInputSpec(t *testing.T) {
	ds := &httpStreamSource{url: "http://127.0.0.1:8080/torrents/abc/stream?fileIndex=0"}
	input, reader := ds.InputSpec()
	if input != "http://127.0.0.1:8080/torrents/abc/stream?fileIndex=0" {
		t.Fatalf("expected URL, got %q", input)
	}
	if reader != nil {
		t.Fatalf("expected nil reader for httpStreamSource, got non-nil")
	}
}

func TestPipeSourceInputSpec(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	source := io.NopCloser(bytes.NewReader([]byte("test data")))
	buffered := newBufferedStreamReader(source, 1024, logger)
	defer buffered.Close()

	ds := &pipeSource{buffered: buffered}
	input, reader := ds.InputSpec()
	if input != "pipe:0" {
		t.Fatalf("expected input 'pipe:0', got %q", input)
	}
	if reader == nil {
		t.Fatalf("expected non-nil reader for pipeSource, got nil")
	}
}

func TestPartialDirectSourceInputSpec(t *testing.T) {
	ds := &partialDirectSource{path: "/data/partial.mkv"}
	input, reader := ds.InputSpec()
	if input != "/data/partial.mkv" {
		t.Fatalf("expected path /data/partial.mkv, got %q", input)
	}
	if reader != nil {
		t.Fatalf("expected nil reader for partialDirectSource, got non-nil")
	}
}

func TestDataSourceFilePath(t *testing.T) {
	tests := []struct {
		name     string
		ds       MediaDataSource
		expected string
	}{
		{
			name:     "directFileSource",
			ds:       &directFileSource{path: "/data/movie.mkv"},
			expected: "/data/movie.mkv",
		},
		{
			name:     "partialDirectSource",
			ds:       &partialDirectSource{path: "/data/partial.mkv"},
			expected: "/data/partial.mkv",
		},
		{
			name:     "httpStreamSource",
			ds:       &httpStreamSource{url: "http://localhost/stream"},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := dataSourceFilePath(tc.ds)
			if result != tc.expected {
				t.Fatalf("expected dataSourceFilePath=%q, got %q", tc.expected, result)
			}
		})
	}

	// Test pipeSource separately since it needs cleanup.
	t.Run("pipeSource", func(t *testing.T) {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		source := io.NopCloser(bytes.NewReader([]byte("data")))
		buffered := newBufferedStreamReader(source, 1024, logger)
		defer buffered.Close()

		ds := &pipeSource{buffered: buffered}
		result := dataSourceFilePath(ds)
		if result != "" {
			t.Fatalf("expected empty path for pipeSource, got %q", result)
		}
	})
}

func TestDataSourceIsPartialDirect(t *testing.T) {
	tests := []struct {
		name     string
		ds       MediaDataSource
		expected bool
	}{
		{"directFileSource", &directFileSource{path: "/a"}, false},
		{"httpStreamSource", &httpStreamSource{url: "http://x"}, false},
		{"partialDirectSource", &partialDirectSource{path: "/a"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if dataSourceIsPartialDirect(tc.ds) != tc.expected {
				t.Fatalf("expected dataSourceIsPartialDirect=%v for %s", tc.expected, tc.name)
			}
		})
	}

	// Test pipeSource separately since it needs cleanup.
	t.Run("pipeSource", func(t *testing.T) {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		source := io.NopCloser(bytes.NewReader([]byte("data")))
		buffered := newBufferedStreamReader(source, 1024, logger)
		defer buffered.Close()

		ds := &pipeSource{buffered: buffered}
		if dataSourceIsPartialDirect(ds) {
			t.Fatalf("expected dataSourceIsPartialDirect=false for pipeSource")
		}
	})
}

func TestDataSourceIsPipe(t *testing.T) {
	tests := []struct {
		name     string
		ds       MediaDataSource
		expected bool
	}{
		{"directFileSource", &directFileSource{path: "/a"}, false},
		{"httpStreamSource", &httpStreamSource{url: "http://x"}, false},
		{"partialDirectSource", &partialDirectSource{path: "/a"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if dataSourceIsPipe(tc.ds) != tc.expected {
				t.Fatalf("expected dataSourceIsPipe=%v for %s", tc.expected, tc.name)
			}
		})
	}

	// Test pipeSource separately since it needs cleanup.
	t.Run("pipeSource", func(t *testing.T) {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		source := io.NopCloser(bytes.NewReader([]byte("data")))
		buffered := newBufferedStreamReader(source, 1024, logger)
		defer buffered.Close()

		ds := &pipeSource{buffered: buffered}
		if !dataSourceIsPipe(ds) {
			t.Fatalf("expected dataSourceIsPipe=true for pipeSource")
		}
	})
}

func TestNewDataSourceQuasiComplete(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "movie.mkv")
	data := make([]byte, 11*1024*1024)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := &hlsManager{
		dataDir: dir,
		logger:  logger,
	}

	result := usecase.StreamResult{
		Reader: &testStreamReader{Reader: bytes.NewReader(nil)},
		File: domain.FileRef{
			Path:           "movie.mkv",
			Length:         12 * 1024 * 1024,
			BytesCompleted: 12 * 1024 * 1024 * 96 / 100,
		},
	}
	job := &hlsJob{seekSeconds: 300}

	ds, _ := mgr.newDataSource(result, job, hlsKey{})
	defer ds.Close()

	if _, ok := ds.(*directFileSource); !ok {
		t.Fatalf("expected directFileSource for quasi-complete file with seek, got %T", ds)
	}
}

func TestNewDataSourceQuasiCompleteThreshold(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "movie.mkv")
	data := make([]byte, 11*1024*1024)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := &hlsManager{
		dataDir: dir,
		logger:  logger,
	}

	result := usecase.StreamResult{
		Reader: &testStreamReader{Reader: bytes.NewReader(nil)},
		File: domain.FileRef{
			Path:           "movie.mkv",
			Length:         12 * 1024 * 1024,
			BytesCompleted: 12 * 1024 * 1024 * 80 / 100,
		},
	}
	job := &hlsJob{seekSeconds: 0}

	ds, _ := mgr.newDataSource(result, job, hlsKey{})
	defer ds.Close()

	if _, ok := ds.(*partialDirectSource); !ok {
		t.Fatalf("expected partialDirectSource for 80%% complete file at seek=0, got %T", ds)
	}
}
