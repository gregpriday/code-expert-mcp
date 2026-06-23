// Package cache is the content-addressed, snapshot-safe persistent store. It
// holds reusable objects (file indexes, summaries, model outputs) and per-run
// artifacts (reports, manifests, evidence) that back MCP resource URIs. It lives
// under the OS user cache directory, never inside an analyzed repository, unless
// an administrator explicitly opts in.
//
// The default implementation uses a filesystem object store with optional gzip
// compression rather than SQLite, keeping the default binary trivially CGO-free.
package cache

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/hashutil"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// Cache is the top-level store handle.
type Cache struct {
	dir      string
	enabled  bool
	compress bool
}

// Open resolves the cache directory and ensures the layout exists. When the
// cache is disabled, a no-op Cache is returned.
func Open(cfg config.CacheConfig) (*Cache, error) {
	if !cfg.Enabled {
		return &Cache{enabled: false}, nil
	}
	dir := cfg.Dir
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return nil, schema.NewError(schema.CodeInternal, "cannot locate user cache dir: %v", err)
		}
		dir = filepath.Join(base, "CodeExpert")
	}
	for _, sub := range []string{"objects", "runs", "locks", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, schema.NewError(schema.CodeInternal, "cannot create cache dir: %v", err)
		}
	}
	return &Cache{dir: dir, enabled: true, compress: cfg.Compress}, nil
}

// Enabled reports whether the cache is active.
func (c *Cache) Enabled() bool { return c != nil && c.enabled }

// Dir returns the cache base directory.
func (c *Cache) Dir() string {
	if c == nil {
		return ""
	}
	return c.dir
}

// objectPath returns the sharded path for a content hash.
func (c *Cache) objectPath(hash string) string {
	if len(hash) < 4 {
		hash = hash + "0000"
	}
	return filepath.Join(c.dir, "objects", hash[:2], hash[2:4], hash)
}

// PutObject stores bytes content-addressed and returns the hash. It is a no-op
// returning the hash when the cache is disabled.
func (c *Cache) PutObject(data []byte) (string, error) {
	hash := hashutil.Hash(data)
	if !c.Enabled() {
		return hash, nil
	}
	p := c.objectPath(hash)
	if _, err := os.Stat(p); err == nil {
		return hash, nil // already stored (immutable)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return hash, err
	}
	return hash, c.writeFile(p, data)
}

// GetObject retrieves content by hash.
func (c *Cache) GetObject(hash string) ([]byte, bool) {
	if !c.Enabled() {
		return nil, false
	}
	return c.readFile(c.objectPath(hash))
}

func (c *Cache) writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if c.compress {
		gz := gzip.NewWriter(f)
		if _, err := gz.Write(data); err != nil {
			gz.Close()
			f.Close()
			return err
		}
		if err := gz.Close(); err != nil {
			f.Close()
			return err
		}
	} else {
		if _, err := f.Write(data); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (c *Cache) readFile(path string) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if c.compress {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, false
		}
		defer gz.Close()
		data, err := io.ReadAll(gz)
		if err != nil {
			return nil, false
		}
		return data, true
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false
	}
	return data, true
}

// IsInsideRepo reports whether the cache dir lies within root, which is
// disallowed unless explicitly permitted.
func IsInsideRepo(cacheDir, root string) bool {
	if cacheDir == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, cacheDir)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "" && !filepath.IsAbs(rel)
}
