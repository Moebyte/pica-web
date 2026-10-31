package imgproxy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type meta struct {
	Mime      string `json:"mime"`
	Size      int64  `json:"size"`
	FetchedAt int64  `json:"fetchedAt"`
}

// Entry is an open cached image.
type Entry struct {
	File *os.File
	Mime string
	Size int64
}

// Cache is a content-addressed, TTL+LRU disk cache.
type Cache struct {
	dir      string
	ttl      time.Duration
	maxBytes int64

	mu    sync.Mutex
	sizes map[string]int64
	total int64
}

// New creates the cache directory and loads its size index. maxBytes <= 0
// disables caching entirely.
func New(dir string, ttl time.Duration, maxBytes int64) (*Cache, error) {
	c := &Cache{dir: dir, ttl: ttl, maxBytes: maxBytes, sizes: make(map[string]int64)}
	if maxBytes <= 0 {
		return c, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := c.load(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.evictLocked()
	c.mu.Unlock()
	return c, nil
}

// Enabled reports whether caching is on.
func (c *Cache) Enabled() bool {
	return c != nil && c.maxBytes > 0
}

func (c *Cache) keyPath(key string) (data, metaPath string) {
	sub := key
	if len(sub) > 2 {
		sub = sub[:2]
	}
	dir := filepath.Join(c.dir, sub)
	return filepath.Join(dir, key), filepath.Join(dir, key+".json")
}

func (c *Cache) load() error {
	return filepath.WalkDir(c.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if filepath.Ext(name) == ".json" {
			return nil
		}
		if len(name) > 0 && name[0] == '.' {
			_ = os.Remove(path)
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		c.sizes[name] = info.Size()
		c.total += info.Size()
		return nil
	})
}

// Get returns an open file for key on a cache hit. The file's mtime is touched
// for LRU ordering.
func (c *Cache) Get(key string) (*Entry, bool) {
	if !c.Enabled() {
		return nil, false
	}
	dataPath, metaPath := c.keyPath(key)
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, false
	}
	var m meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	if c.ttl > 0 && time.Since(time.Unix(m.FetchedAt, 0)) > c.ttl {
		c.remove(key)
		return nil, false
	}

	c.mu.Lock()
	info, err := os.Stat(dataPath)
	if err != nil || info.Size() != m.Size {
		c.mu.Unlock()
		c.remove(key)
		return nil, false
	}
	c.mu.Unlock()

	f, err := os.Open(dataPath)
	if err != nil {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(dataPath, now, now)
	return &Entry{File: f, Mime: m.Mime, Size: m.Size}, true
}

// Writer streams a cache fill to a temporary file and atomically commits it.
type Writer struct {
	cache    *Cache
	key      string
	file     *os.File
	tempPath string
	size     int64
	tooLarge bool
	failed   bool
	closed   bool
}

// Begin starts a cache fill for key.
func (c *Cache) Begin(key string) (*Writer, error) {
	if !c.Enabled() {
		return nil, errors.New("cache disabled")
	}
	dataPath, _ := c.keyPath(key)
	if err := os.MkdirAll(filepath.Dir(dataPath), 0o700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(filepath.Dir(dataPath), ".tmp-"+key+"-*")
	if err != nil {
		return nil, err
	}
	return &Writer{cache: c, key: key, file: f, tempPath: f.Name()}, nil
}

// Write never fails the client stream for cache-side problems: oversized files
// and disk errors are recorded and dropped at Commit time.
func (w *Writer) Write(p []byte) (int, error) {
	if w == nil || w.closed {
		return len(p), nil
	}
	if w.tooLarge || w.failed {
		return len(p), nil
	}
	if w.size+int64(len(p)) > w.cache.maxBytes {
		w.tooLarge = true
		return len(p), nil
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	if err != nil {
		w.failed = true
	}
	return len(p), nil
}

// Commit finalizes the cache entry. It is a no-op for oversized/failed writes.
func (w *Writer) Commit(mime string) error {
	if w == nil || w.closed {
		return nil
	}
	w.closed = true
	defer func() {
		_ = w.file.Close()
		_ = os.Remove(w.tempPath)
	}()
	if w.tooLarge || w.failed || w.size == 0 {
		return nil
	}
	dataPath, metaPath := w.cache.keyPath(w.key)
	if err := w.file.Sync(); err != nil {
		return err
	}
	if err := w.file.Close(); err != nil {
		return err
	}
	if err := os.Rename(w.tempPath, dataPath); err != nil {
		return err
	}
	m := meta{Mime: mime, Size: w.size, FetchedAt: time.Now().Unix()}
	raw, _ := json.Marshal(m)
	tmpMeta := metaPath + ".tmp"
	if err := os.WriteFile(tmpMeta, raw, 0o600); err != nil {
		_ = os.Remove(dataPath)
		return err
	}
	if err := os.Rename(tmpMeta, metaPath); err != nil {
		_ = os.Remove(tmpMeta)
		_ = os.Remove(dataPath)
		return err
	}

	w.cache.mu.Lock()
	w.cache.sizes[w.key] = w.size
	w.cache.total += w.size
	w.cache.evictLocked()
	w.cache.mu.Unlock()
	return nil
}

// Abort discards the fill.
func (w *Writer) Abort() {
	if w == nil || w.closed {
		return
	}
	w.closed = true
	_ = w.file.Close()
	_ = os.Remove(w.tempPath)
}

func (c *Cache) remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	dataPath, metaPath := c.keyPath(key)
	if size, ok := c.sizes[key]; ok {
		c.total -= size
		delete(c.sizes, key)
	}
	_ = os.Remove(dataPath)
	_ = os.Remove(metaPath)
}

// evictLocked deletes oldest files until total <= maxBytes. The caller must
// hold c.mu.
func (c *Cache) evictLocked() {
	if c.total <= c.maxBytes {
		return
	}
	type candidate struct {
		key   string
		size  int64
		mtime time.Time
	}
	var files []candidate
	_ = filepath.WalkDir(c.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(d.Name()) == ".json" || d.Name()[0] == '.' {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, candidate{key: d.Name(), size: info.Size(), mtime: info.ModTime()})
		return nil
	})
	for while := 0; while < len(files) && c.total > c.maxBytes; while++ {
		oldest := 0
		for i := 1; i < len(files); i++ {
			if files[i].mtime.Before(files[oldest].mtime) {
				oldest = i
			}
		}
		victim := files[oldest]
		files = append(files[:oldest], files[oldest+1:]...)
		dataPath, metaPath := c.keyPath(victim.key)
		_ = os.Remove(dataPath)
		_ = os.Remove(metaPath)
		if size, ok := c.sizes[victim.key]; ok {
			c.total -= size
			delete(c.sizes, victim.key)
		} else {
			c.total -= victim.size
		}
	}
}
