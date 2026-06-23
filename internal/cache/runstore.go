package cache

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gregpriday/codeexpert/internal/schema"
	"github.com/gregpriday/codeexpert/internal/security"
)

// URIScheme is the resource URI scheme for run artifacts.
const URIScheme = "codeexpert://runs/"

// RunStore persists artifacts for a single run and maps them to resource URIs.
type RunStore struct {
	cache *Cache
	runID string
	dir   string
}

// RunStore returns a per-run artifact store. When the cache is disabled, the
// store still functions in-memory-less mode by returning URIs without persisting
// (resources will be unavailable, which callers tolerate).
func (c *Cache) RunStore(runID string) *RunStore {
	rs := &RunStore{cache: c, runID: runID}
	if c.Enabled() {
		rs.dir = filepath.Join(c.dir, "runs", runID)
	}
	return rs
}

// Put stores an artifact under a logical name (which may contain slashes, e.g.
// "evidence/E-123") and returns its resource URI.
func (rs *RunStore) Put(name string, data []byte) (string, error) {
	uri := URIFor(rs.runID, name)
	if rs.dir == "" {
		return uri, nil
	}
	clean, ok := security.CleanRelative(name)
	if !ok {
		return uri, schema.NewError(schema.CodeInvalidArgument, "invalid artifact name %q", name)
	}
	p := filepath.Join(rs.dir, filepath.FromSlash(clean))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return uri, err
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return uri, err
	}
	return uri, nil
}

// URIFor builds a resource URI for a run artifact.
func URIFor(runID, name string) string {
	return URIScheme + runID + "/" + strings.TrimLeft(name, "/")
}

// ParseURI extracts the run ID and artifact name from a resource URI.
func ParseURI(uri string) (runID, name string, ok bool) {
	if !strings.HasPrefix(uri, URIScheme) {
		return "", "", false
	}
	rest := strings.TrimPrefix(uri, URIScheme)
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "", "", false
	}
	return rest[:slash], rest[slash+1:], true
}

// ReadResource reads a run artifact identified by run ID and name. It enforces
// that the resolved path stays within the run directory.
func (c *Cache) ReadResource(runID, name string) ([]byte, bool) {
	if !c.Enabled() {
		return nil, false
	}
	clean, ok := security.CleanRelative(name)
	if !ok {
		return nil, false
	}
	runDir := filepath.Join(c.dir, "runs", runID)
	p := filepath.Join(runDir, filepath.FromSlash(clean))
	root := security.Root{Canonical: runDir}
	if !root.ContainsPath(p) {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	return data, true
}
