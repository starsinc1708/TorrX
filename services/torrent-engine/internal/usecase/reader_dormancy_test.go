package usecase

import (
	"context"
	"io"
	"testing"
	"time"

	"torrentstream/internal/domain"
	"torrentstream/internal/domain/ports"
)

// fakeDataReader is a StreamReader that returns data from a buffer.
type fakeDataReader struct {
	ctx       context.Context
	readahead int64
	pos       int64
	data      []byte
}

func (f *fakeDataReader) SetContext(ctx context.Context) { f.ctx = ctx }
func (f *fakeDataReader) SetReadahead(n int64)           { f.readahead = n }
func (f *fakeDataReader) SetResponsive()                 {}
func (f *fakeDataReader) Read(p []byte) (int, error) {
	if f.pos >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.pos:])
	f.pos += int64(n)
	return n, nil
}
func (f *fakeDataReader) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.pos = off
	case io.SeekCurrent:
		f.pos += off
	case io.SeekEnd:
		f.pos = int64(len(f.data)) + off
	}
	if f.pos < 0 {
		f.pos = 0
	}
	return f.pos, nil
}
func (f *fakeDataReader) Close() error { return nil }

// multiReaderSession returns a new fakeDataReader for each NewReader call.
type multiReaderSession struct {
	files   []domain.FileRef
	readers []*fakeDataReader
	idx     int
}

func (s *multiReaderSession) ID() domain.TorrentID { return "t1" }
func (s *multiReaderSession) Files() []domain.FileRef {
	return append([]domain.FileRef(nil), s.files...)
}
func (s *multiReaderSession) SelectFile(index int) (domain.FileRef, error) {
	if index < 0 || index >= len(s.files) {
		return domain.FileRef{}, domain.ErrNotFound
	}
	return s.files[index], nil
}
func (s *multiReaderSession) SetPiecePriority(domain.FileRef, domain.Range, domain.Priority) {}
func (s *multiReaderSession) Start() error                                                   { return nil }
func (s *multiReaderSession) Stop() error                                                    { return nil }
func (s *multiReaderSession) NewReader(file domain.FileRef) (ports.StreamReader, error) {
	if s.idx >= len(s.readers) {
		return nil, io.ErrUnexpectedEOF
	}
	r := s.readers[s.idx]
	s.idx++
	return r, nil
}

func TestReaderDormancyIdleReaderSleeps(t *testing.T) {
	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte(i % 256)
	}
	fileLen := int64(1 << 30) // 1 GB
	session := &multiReaderSession{
		files: []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: fileLen}},
		readers: []*fakeDataReader{
			{data: data},
			{data: data},
		},
	}
	engine := &fakeStreamEngine{session: session}
	uc := &StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}

	// Create reader 1.
	result1, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute 1: %v", err)
	}
	defer result1.Reader.Close()

	// Create reader 2.
	result2, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute 2: %v", err)
	}
	defer result2.Reader.Close()

	reg := uc.readers
	if reg == nil {
		t.Fatal("registry not initialized")
	}

	// Both readers should be registered.
	reg.mu.Lock()
	count := len(reg.readers["t1"])
	reg.mu.Unlock()
	if count != 2 {
		t.Fatalf("expected 2 registered readers, got %d", count)
	}

	// Make reader 1 appear idle.
	spr1 := result1.Reader.(*slidingPriorityReader)
	spr1.mu.Lock()
	spr1.lastAccess = time.Now().Add(-2 * readerDormancyTimeout)
	spr1.mu.Unlock()

	// Read from reader 2 — force dormancy check by clearing throttle.
	spr2 := result2.Reader.(*slidingPriorityReader)
	spr2.mu.Lock()
	spr2.lastDormancyCheck = time.Time{}
	spr2.mu.Unlock()

	buf := make([]byte, 256)
	n, err := result2.Reader.Read(buf)
	if n == 0 {
		t.Fatalf("Read returned 0 bytes, err: %v", err)
	}

	// Reader 1 should now be dormant.
	spr1.mu.Lock()
	isDormant := spr1.dormant
	spr1.mu.Unlock()
	if !isDormant {
		t.Fatal("expected reader 1 to be dormant after reader 2 read")
	}

	// Reader 1's underlying readahead should be 0.
	dr1 := session.readers[0]
	if dr1.readahead != 0 {
		t.Fatalf("dormant reader readahead: got %d, want 0", dr1.readahead)
	}
}

func TestReaderDormancyWakeOnRead(t *testing.T) {
	data := make([]byte, 4096)
	fileLen := int64(1 << 30)
	session := &multiReaderSession{
		files: []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: fileLen}},
		readers: []*fakeDataReader{
			{data: data},
			{data: data},
		},
	}
	engine := &fakeStreamEngine{session: session}
	uc := &StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}

	result1, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute 1: %v", err)
	}
	defer result1.Reader.Close()

	result2, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute 2: %v", err)
	}
	defer result2.Reader.Close()

	// Put reader 1 to sleep manually.
	spr1 := result1.Reader.(*slidingPriorityReader)
	spr1.mu.Lock()
	spr1.enterDormancyLocked()
	spr1.mu.Unlock()

	spr1.mu.Lock()
	if !spr1.dormant {
		t.Fatal("expected reader 1 to be dormant")
	}
	spr1.mu.Unlock()

	// Reading from reader 1 should wake it up.
	buf := make([]byte, 64)
	n, _ := result1.Reader.Read(buf)
	if n == 0 {
		t.Fatal("Read returned 0 bytes")
	}

	spr1.mu.Lock()
	isDormant := spr1.dormant
	spr1.mu.Unlock()
	if isDormant {
		t.Fatal("expected reader 1 to wake up after Read")
	}

	// Readahead should be restored.
	dr1 := session.readers[0]
	if dr1.readahead == 0 {
		t.Fatal("expected readahead to be restored after wake-up")
	}
}

func TestReaderDormancyNoDormancySingleReader(t *testing.T) {
	data := make([]byte, 4096)
	fileLen := int64(1 << 30)
	session := &multiReaderSession{
		files: []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: fileLen}},
		readers: []*fakeDataReader{
			{data: data},
		},
	}
	engine := &fakeStreamEngine{session: session}
	uc := &StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}

	result, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	defer result.Reader.Close()

	// Even with stale lastAccess, a single reader should never go dormant.
	spr := result.Reader.(*slidingPriorityReader)
	spr.mu.Lock()
	spr.lastAccess = time.Now().Add(-5 * readerDormancyTimeout)
	spr.lastDormancyCheck = time.Time{}
	spr.mu.Unlock()

	buf := make([]byte, 64)
	spr.Read(buf)

	spr.mu.Lock()
	isDormant := spr.dormant
	spr.mu.Unlock()
	if isDormant {
		t.Fatal("single reader should never go dormant")
	}
}

func TestReaderRegistryUnregisterOnClose(t *testing.T) {
	data := make([]byte, 512)
	fileLen := int64(1 << 30)
	session := &multiReaderSession{
		files: []domain.FileRef{{Index: 0, Path: "movie.mkv", Length: fileLen}},
		readers: []*fakeDataReader{
			{data: data},
		},
	}
	engine := &fakeStreamEngine{session: session}
	uc := &StreamTorrent{Engine: engine, ReadaheadBytes: 2 << 20}

	result, err := uc.Execute(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	reg := uc.readers
	reg.mu.Lock()
	before := len(reg.readers["t1"])
	reg.mu.Unlock()
	if before != 1 {
		t.Fatalf("expected 1 reader before close, got %d", before)
	}

	result.Reader.Close()

	reg.mu.Lock()
	after := len(reg.readers["t1"])
	reg.mu.Unlock()
	if after != 0 {
		t.Fatalf("expected 0 readers after close, got %d", after)
	}
}
