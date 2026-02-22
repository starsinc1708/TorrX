package apihttp

import (
	"io"
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

// dataSourceFilePath extracts the file path from a data source, if it's a
// local file source. Returns empty string for pipe sources.
func dataSourceFilePath(ds MediaDataSource) string {
	switch s := ds.(type) {
	case *directFileSource:
		return s.path
	default:
		return ""
	}
}

// headerPrefixReader prepends saved container header bytes to a seeked reader.
// When seeking in pipe sources, FFmpeg needs the container header (EBML/MKV,
// MP4 ftyp/moov, etc.) to identify the format and codecs. This reader returns
// the cached header first, then streams data from the seeked reader position.
type headerPrefixReader struct {
	header []byte
	pos    int
	source io.ReadCloser
}

func (r *headerPrefixReader) Read(p []byte) (int, error) {
	if r.pos < len(r.header) {
		n := copy(p, r.header[r.pos:])
		r.pos += n
		return n, nil
	}
	return r.source.Read(p)
}

func (r *headerPrefixReader) Close() error {
	return r.source.Close()
}

// Ensure all implementations satisfy the interface.
var _ MediaDataSource = (*directFileSource)(nil)
