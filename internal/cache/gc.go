package cache

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Status summarizes cache occupancy for `cache status`.
type Status struct {
	Dir         string
	Enabled     bool
	ObjectCount int
	RunCount    int
	TotalBytes  int64
}

// Stat walks the cache and reports occupancy.
func (c *Cache) Stat() (Status, error) {
	st := Status{Dir: c.dir, Enabled: c.Enabled()}
	if !c.Enabled() {
		return st, nil
	}
	objectsDir := filepath.Join(c.dir, "objects")
	_ = filepath.WalkDir(objectsDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		st.ObjectCount++
		if info, e := d.Info(); e == nil {
			st.TotalBytes += info.Size()
		}
		return nil
	})
	runsDir := filepath.Join(c.dir, "runs")
	if entries, err := os.ReadDir(runsDir); err == nil {
		st.RunCount = len(entries)
	}
	_ = filepath.WalkDir(runsDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, e := d.Info(); e == nil {
			st.TotalBytes += info.Size()
		}
		return nil
	})
	return st, nil
}

// GC removes runs older than retention and evicts objects to satisfy maxBytes
// using least-recently-modified order. It returns the number of bytes freed.
func (c *Cache) GC(retention time.Duration, maxBytes int64) (int64, error) {
	if !c.Enabled() {
		return 0, nil
	}
	var freed int64
	cutoff := time.Now().Add(-retention)

	// Expire old runs.
	runsDir := filepath.Join(c.dir, "runs")
	if entries, err := os.ReadDir(runsDir); err == nil {
		for _, e := range entries {
			info, ierr := e.Info()
			if ierr != nil {
				continue
			}
			if retention > 0 && info.ModTime().Before(cutoff) {
				sz := dirSize(filepath.Join(runsDir, e.Name()))
				if os.RemoveAll(filepath.Join(runsDir, e.Name())) == nil {
					freed += sz
				}
			}
		}
	}

	// Evict objects if over the size budget.
	if maxBytes > 0 {
		type obj struct {
			path string
			size int64
			mod  time.Time
		}
		var objs []obj
		var total int64
		objectsDir := filepath.Join(c.dir, "objects")
		_ = filepath.WalkDir(objectsDir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if info, e := d.Info(); e == nil {
				objs = append(objs, obj{p, info.Size(), info.ModTime()})
				total += info.Size()
			}
			return nil
		})
		if total > maxBytes {
			sort.Slice(objs, func(i, j int) bool { return objs[i].mod.Before(objs[j].mod) })
			for _, o := range objs {
				if total <= maxBytes {
					break
				}
				if os.Remove(o.path) == nil {
					total -= o.size
					freed += o.size
				}
			}
		}
	}
	return freed, nil
}

// Clear removes the entire cache contents.
func (c *Cache) Clear() error {
	if !c.Enabled() {
		return nil
	}
	for _, sub := range []string{"objects", "runs", "tmp"} {
		if err := os.RemoveAll(filepath.Join(c.dir, sub)); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(c.dir, sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, e := d.Info(); e == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
