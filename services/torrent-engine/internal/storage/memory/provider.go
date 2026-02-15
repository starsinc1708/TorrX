package memory

import (
	"bytes"
	"container/list"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/missinggo/v2/resource"
)

type Provider struct {
	mu    sync.RWMutex
	files map[string]*fileEntry
	lru   *list.List

	maxBytes int64
	curBytes int64
	spillDir string
}

type fileEntry struct {
	data   []byte
	size   int64
	mod    time.Time
	elem   *list.Element
	onDisk bool
}

type ProviderOption func(*Provider)

func WithMaxBytes(max int64) ProviderOption {
	return func(p *Provider) {
		if max > 0 {
			p.maxBytes = max
		}
	}
}

func WithSpillDir(dir string) ProviderOption {
	return func(p *Provider) {
		trimmed := strings.TrimSpace(dir)
		if trimmed == "" {
			return
		}
		cleaned := filepath.Clean(trimmed)
		if abs, err := filepath.Abs(cleaned); err == nil {
			cleaned = abs
		}
		p.spillDir = cleaned
	}
}

func NewProvider(opts ...ProviderOption) *Provider {
	p := &Provider{
		files: make(map[string]*fileEntry),
		lru:   list.New(),
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.spillDir != "" {
		_ = os.MkdirAll(p.spillDir, 0o755)
		p.loadSpilledFiles()
	}
	return p
}

// loadSpilledFiles scans the spill directory and registers existing files
// so that anacrolix can verify and resume previously downloaded pieces.
func (p *Provider) loadSpilledFiles() {
	if p.spillDir == "" {
		return
	}
	base := filepath.Clean(p.spillDir)
	_ = filepath.Walk(base, func(fp string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(base, fp)
		if relErr != nil {
			return nil
		}
		// Convert to forward-slash path (anacrolix uses forward slashes internally).
		key := filepath.ToSlash(rel)
		p.files[key] = &fileEntry{
			size:   info.Size(),
			mod:    info.ModTime(),
			onDisk: true,
		}
		return nil
	})
}

func (p *Provider) MaxBytes() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.maxBytes
}

func (p *Provider) SetMaxBytes(max int64) {
	if max < 0 {
		max = 0
	}
	p.mu.Lock()
	p.maxBytes = max
	p.evictLocked()
	p.mu.Unlock()
}

func (p *Provider) SpillToDisk() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.spillDir != ""
}

func (p *Provider) NewInstance(name string) (resource.Instance, error) {
	clean, err := cleanPath(name)
	if err != nil {
		return nil, err
	}
	return &instance{provider: p, path: clean}, nil
}

type instance struct {
	provider *Provider
	path     string
}

func (i *instance) Get() (io.ReadCloser, error) {
	data, ok := i.provider.get(i.path)
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (i *instance) Put(r io.Reader) error {
	if r == nil {
		return errors.New("nil reader")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	i.provider.set(i.path, data)
	return nil
}

func (i *instance) PutSized(r io.Reader, size int64) error {
	if r == nil {
		return errors.New("nil reader")
	}
	if size < 0 {
		return errors.New("invalid size")
	}
	if size == 0 {
		i.provider.set(i.path, nil)
		return nil
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	i.provider.set(i.path, buf)
	return nil
}

func (i *instance) Stat() (os.FileInfo, error) {
	return i.provider.stat(i.path)
}

func (i *instance) ReadAt(b []byte, off int64) (int, error) {
	return i.provider.readAt(i.path, b, off)
}

func (i *instance) WriteAt(b []byte, off int64) (int, error) {
	return i.provider.writeAt(i.path, b, off)
}

func (i *instance) Delete() error {
	i.provider.delete(i.path)
	return nil
}

func (i *instance) Readdirnames() ([]string, error) {
	return i.provider.readdir(i.path)
}

func (p *Provider) get(name string) ([]byte, bool) {
	p.mu.Lock()
	item, ok := p.files[name]
	if !ok {
		p.mu.Unlock()
		return nil, false
	}
	if !item.onDisk {
		p.touchLocked(name, item)
		data := make([]byte, len(item.data))
		copy(data, item.data)
		p.mu.Unlock()
		return data, true
	}
	p.mu.Unlock()

	data, err := p.readDiskAll(name)
	if err != nil {
		return nil, false
	}
	return data, true
}

func (p *Provider) set(name string, data []byte) {
	copied := make([]byte, len(data))
	copy(copied, data)
	p.mu.Lock()
	p.setLocked(name, copied)
	p.mu.Unlock()
}

func (p *Provider) readAt(name string, b []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	p.mu.Lock()
	item, ok := p.files[name]
	if !ok {
		p.mu.Unlock()
		return 0, os.ErrNotExist
	}
	if !item.onDisk {
		if off >= int64(len(item.data)) {
			p.mu.Unlock()
			return 0, io.EOF
		}
		n := copy(b, item.data[off:])
		p.touchLocked(name, item)
		p.mu.Unlock()
		if n < len(b) {
			return n, io.EOF
		}
		return n, nil
	}
	size := item.size
	p.mu.Unlock()

	if off >= size {
		return 0, io.EOF
	}
	fp, err := p.diskPath(name)
	if err != nil {
		return 0, err
	}
	f, err := os.Open(fp)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, os.ErrNotExist
		}
		return 0, err
	}
	defer f.Close()
	return f.ReadAt(b, off)
}

func (p *Provider) writeAt(name string, b []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	maxInt := int64(^uint(0) >> 1)
	if off > maxInt-int64(len(b)) {
		return 0, errors.New("offset too large")
	}
	end := int(off) + len(b)

	p.mu.Lock()
	item := p.files[name]
	if item == nil {
		item = &fileEntry{}
		p.files[name] = item
	}

	if item.onDisk {
		n, err := p.writeDiskAtLocked(name, item, b, off)
		p.mu.Unlock()
		return n, err
	}

	p.touchLocked(name, item)
	p.curBytes -= int64(len(item.data))
	if end > len(item.data) {
		next := make([]byte, end)
		copy(next, item.data)
		item.data = next
	}
	copy(item.data[off:], b)
	item.size = int64(len(item.data))
	item.mod = time.Now().UTC()
	item.onDisk = false
	p.curBytes += int64(len(item.data))
	p.evictLocked()
	p.mu.Unlock()
	return len(b), nil
}

func (p *Provider) delete(name string) {
	p.mu.Lock()
	if item, ok := p.files[name]; ok {
		if item.onDisk {
			p.deleteDiskFileLocked(name)
		} else {
			p.curBytes -= int64(len(item.data))
		}
		if item.elem != nil {
			p.lru.Remove(item.elem)
		}
		delete(p.files, name)
	}
	p.mu.Unlock()
}

func (p *Provider) stat(name string) (os.FileInfo, error) {
	p.mu.Lock()
	item, ok := p.files[name]
	if ok {
		if !item.onDisk {
			p.touchLocked(name, item)
		}
		size := item.size
		if !item.onDisk {
			size = int64(len(item.data))
		}
		fi := memFileInfo{
			name: path.Base(name),
			size: size,
			mod:  item.mod,
		}
		p.mu.Unlock()
		return fi, nil
	}
	if p.hasChildrenLocked(name) {
		fi := memFileInfo{
			name: path.Base(name),
			dir:  true,
			mod:  time.Now().UTC(),
		}
		p.mu.Unlock()
		return fi, nil
	}
	p.mu.Unlock()
	return nil, os.ErrNotExist
}

func (p *Provider) readdir(name string) ([]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if _, ok := p.files[name]; ok {
		return nil, errors.New("not a directory")
	}

	prefix := name
	if prefix != "" {
		prefix += "/"
	}
	seen := map[string]struct{}{}
	for key := range p.files {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := strings.TrimPrefix(key, prefix)
		if rest == "" {
			continue
		}
		part := strings.SplitN(rest, "/", 2)[0]
		seen[part] = struct{}{}
	}

	if len(seen) == 0 {
		return nil, os.ErrNotExist
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (p *Provider) hasChildrenLocked(name string) bool {
	prefix := name
	if prefix != "" {
		prefix += "/"
	}
	for key := range p.files {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func (p *Provider) setLocked(name string, data []byte) {
	now := time.Now().UTC()
	if item, ok := p.files[name]; ok {
		if item.onDisk {
			p.deleteDiskFileLocked(name)
		} else {
			p.curBytes -= int64(len(item.data))
		}
		item.data = data
		item.size = int64(len(data))
		item.mod = now
		item.onDisk = false
		p.curBytes += item.size
		p.touchLocked(name, item)
		p.evictLocked()
		return
	}

	item := &fileEntry{data: data, size: int64(len(data)), mod: now}
	item.elem = p.lru.PushFront(name)
	p.files[name] = item
	p.curBytes += item.size
	p.evictLocked()
}

func (p *Provider) touchLocked(name string, item *fileEntry) {
	if item.onDisk {
		if item.elem != nil {
			p.lru.Remove(item.elem)
			item.elem = nil
		}
		return
	}
	if item.elem == nil {
		item.elem = p.lru.PushFront(name)
		return
	}
	p.lru.MoveToFront(item.elem)
}

func (p *Provider) evictLocked() {
	if p.maxBytes <= 0 {
		return
	}
	for p.curBytes > p.maxBytes {
		back := p.lru.Back()
		if back == nil {
			break
		}
		key, _ := back.Value.(string)
		p.lru.Remove(back)

		item := p.files[key]
		if item == nil {
			continue
		}
		item.elem = nil
		if item.onDisk {
			continue
		}

		if p.spillDir != "" {
			if err := p.persistToDiskLocked(key, item); err == nil {
				continue
			}
		}

		p.curBytes -= int64(len(item.data))
		delete(p.files, key)
	}
}

func (p *Provider) persistToDiskLocked(name string, item *fileEntry) error {
	if item == nil || item.onDisk {
		return nil
	}
	fp, err := p.diskPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(fp, item.data, 0o644); err != nil {
		return err
	}
	p.curBytes -= int64(len(item.data))
	item.size = int64(len(item.data))
	item.data = nil
	item.onDisk = true
	item.mod = time.Now().UTC()
	return nil
}

func (p *Provider) writeDiskAtLocked(name string, item *fileEntry, b []byte, off int64) (int, error) {
	fp, err := p.diskPath(name)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(fp, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	n, err := f.WriteAt(b, off)
	if err != nil {
		return n, err
	}
	end := off + int64(n)
	if end > item.size {
		item.size = end
	}
	item.mod = time.Now().UTC()
	item.onDisk = true
	item.data = nil
	item.elem = nil
	return n, nil
}

func (p *Provider) readDiskAll(name string) ([]byte, error) {
	fp, err := p.diskPath(name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(fp)
}

func (p *Provider) deleteDiskFileLocked(name string) {
	fp, err := p.diskPath(name)
	if err != nil {
		return
	}
	_ = os.Remove(fp)
}

func (p *Provider) diskPath(name string) (string, error) {
	if p.spillDir == "" {
		return "", errors.New("spill directory is not configured")
	}
	base := filepath.Clean(p.spillDir)
	candidate := filepath.Join(base, filepath.FromSlash(name))
	candidate = filepath.Clean(candidate)
	if candidate != base && !strings.HasPrefix(candidate, base+string(os.PathSeparator)) {
		return "", errors.New("invalid spill path")
	}
	return candidate, nil
}

type memFileInfo struct {
	name string
	size int64
	mod  time.Time
	dir  bool
}

func (m memFileInfo) Name() string { return m.name }
func (m memFileInfo) Size() int64  { return m.size }
func (m memFileInfo) Mode() os.FileMode {
	if m.dir {
		return os.ModeDir | 0o755
	}
	return 0o644
}
func (m memFileInfo) ModTime() time.Time { return m.mod }
func (m memFileInfo) IsDir() bool        { return m.dir }
func (m memFileInfo) Sys() interface{}   { return nil }

func cleanPath(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.New("empty path")
	}
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	if strings.HasPrefix(trimmed, "/") {
		return "", errors.New("absolute path not allowed")
	}
	if strings.Contains(trimmed, "\x00") {
		return "", errors.New("invalid path")
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("invalid path")
	}
	return cleaned, nil
}
