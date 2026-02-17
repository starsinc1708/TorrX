package apihttp

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"torrentstream/internal/usecase"
)

// MediaDataSource abstracts how media data is provided to FFmpeg.
type MediaDataSource interface {
	// InputSpec returns the FFmpeg input argument and an optional pipe reader.
	// When pipeReader is non-nil, it should be connected to cmd.Stdin.
	InputSpec() (input string, pipeReader io.ReadCloser)
	// SupportsSeek reports whether byte-level seeking is available.
	SupportsSeek() bool
	// SeekTo positions the reader to approximately the given byte offset.
	SeekTo(offset int64) error
	// Close releases resources.
	Close() error
}

// directFileSource serves a fully downloaded file from disk.
type directFileSource struct {
	path   string
	reader io.ReadCloser // torrent reader to close
}

func (s *directFileSource) InputSpec() (string, io.ReadCloser) { return s.path, nil }
func (s *directFileSource) SupportsSeek() bool                 { return false }
func (s *directFileSource) SeekTo(int64) error                 { return nil }
func (s *directFileSource) Close() error {
	if s.reader != nil {
		return s.reader.Close()
	}
	return nil
}

// httpStreamSource serves via an internal HTTP range endpoint.
type httpStreamSource struct {
	url    string
	reader io.ReadCloser
}

func (s *httpStreamSource) InputSpec() (string, io.ReadCloser) { return s.url, nil }
func (s *httpStreamSource) SupportsSeek() bool                 { return false }
func (s *httpStreamSource) SeekTo(int64) error                 { return nil }
func (s *httpStreamSource) Close() error {
	if s.reader != nil {
		return s.reader.Close()
	}
	return nil
}

// pipeSource streams partial downloads through a buffered reader pipe.
type pipeSource struct {
	buffered *bufferedStreamReader
}

func (s *pipeSource) InputSpec() (string, io.ReadCloser) {
	return "pipe:0", s.buffered
}
func (s *pipeSource) SupportsSeek() bool { return true }
func (s *pipeSource) SeekTo(offset int64) error {
	// The buffered reader wraps a seekable torrent reader.
	// For seek, we'd need to close and reopen — not supported
	// through the buffered reader. Return nil (no-op) and rely
	// on FFmpeg's -ss flag for software seek.
	return nil
}
func (s *pipeSource) Close() error {
	return s.buffered.Close()
}

// partialDirectSource reads a partially downloaded file from disk while
// keeping the torrent reader open to continue downloading.
type partialDirectSource struct {
	path   string
	reader io.ReadCloser
}

func (s *partialDirectSource) InputSpec() (string, io.ReadCloser) { return s.path, nil }
func (s *partialDirectSource) SupportsSeek() bool                 { return false }
func (s *partialDirectSource) SeekTo(int64) error                 { return nil }
func (s *partialDirectSource) Close() error {
	if s.reader != nil {
		return s.reader.Close()
	}
	return nil
}

// newDataSource determines the best data source for the given stream result
// and creates the appropriate MediaDataSource implementation.
// Returns the data source and the subtitle source path (if any).
func (m *hlsManager) newDataSource(result usecase.StreamResult, job *hlsJob, key hlsKey) (MediaDataSource, string) {
	fileComplete := result.File.Length <= 0 ||
		(result.File.BytesCompleted > 0 && result.File.BytesCompleted >= result.File.Length)

	subtitleSourcePath := ""

	if m.dataDir != "" {
		candidatePath, pathErr := resolveDataFilePath(m.dataDir, result.File.Path)
		if pathErr == nil {
			if info, statErr := os.Stat(candidatePath); statErr == nil && !info.IsDir() {
				subtitleSourcePath = candidatePath
				if fileComplete {
					// Fully downloaded — direct file read.
					m.logger.Info("hls using directFileSource",
						slog.String("path", candidatePath),
					)
					return &directFileSource{path: candidatePath, reader: result.Reader}, subtitleSourcePath
				}
				if info.Size() >= 10*1024*1024 && info.Size() < result.File.Length && job.seekSeconds == 0 {
					// Partial download AND the physical file is shorter than declared
					// (non-preallocating storage). Header region is available and we are
					// playing from the start: FFmpeg can read directly.
					m.logger.Info("hls using partialDirectSource",
						slog.String("path", candidatePath),
						slog.Int64("available", info.Size()),
						slog.Int64("total", result.File.Length),
					)
					return &partialDirectSource{path: candidatePath, reader: result.Reader}, subtitleSourcePath
				}
			}
		}
	}

	// When seeking a fully-downloaded file that is not on disk (e.g. memory
	// storage), use the internal HTTP stream endpoint.
	if job.seekSeconds > 0 && fileComplete && m.listenAddr != "" {
		host := m.listenAddr
		if strings.HasPrefix(host, ":") {
			host = "127.0.0.1" + host
		}
		url := fmt.Sprintf("http://%s/torrents/%s/stream?fileIndex=%d",
			host, string(key.id), key.fileIndex)
		m.logger.Info("hls using httpStreamSource",
			slog.String("url", url),
		)
		return &httpStreamSource{url: url, reader: result.Reader}, subtitleSourcePath
	}

	// Default: pipe through buffered stream reader.
	m.logger.Info("hls using pipeSource")
	buffered := newBufferedStreamReader(result.Reader, defaultStreamBufSize, m.logger)
	return &pipeSource{buffered: buffered}, subtitleSourcePath
}

// dataSourceFilePath extracts the file path from a data source, if it's a
// local file source. Returns empty string for pipe and HTTP sources.
func dataSourceFilePath(ds MediaDataSource) string {
	switch s := ds.(type) {
	case *directFileSource:
		return s.path
	case *partialDirectSource:
		return s.path
	default:
		return ""
	}
}

// dataSourceIsPartialDirect returns true if the data source is a partial direct read.
func dataSourceIsPartialDirect(ds MediaDataSource) bool {
	_, ok := ds.(*partialDirectSource)
	return ok
}

// dataSourceIsPipe returns true if the data source is a pipe.
func dataSourceIsPipe(ds MediaDataSource) bool {
	_, ok := ds.(*pipeSource)
	return ok
}

// Ensure all implementations satisfy the interface.
var (
	_ MediaDataSource = (*directFileSource)(nil)
	_ MediaDataSource = (*httpStreamSource)(nil)
	_ MediaDataSource = (*pipeSource)(nil)
	_ MediaDataSource = (*partialDirectSource)(nil)
)
