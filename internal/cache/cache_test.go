package cache

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gregpriday/codeexpert/internal/config"
)

func openTestCache(t *testing.T, compress bool) *Cache {
	t.Helper()
	c, err := Open(config.CacheConfig{Enabled: true, Dir: t.TempDir(), Compress: compress})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !c.Enabled() {
		t.Fatal("cache should be enabled")
	}
	return c
}

func TestOpenDisabled(t *testing.T) {
	c, err := Open(config.CacheConfig{Enabled: false})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if c.Enabled() {
		t.Error("cache should be disabled")
	}
	// Disabled object operations are no-ops: PutObject still returns the hash,
	// GetObject reports a miss.
	hash, err := c.PutObject([]byte("data"))
	if err != nil {
		t.Fatalf("PutObject on disabled cache: %v", err)
	}
	if hash == "" {
		t.Error("disabled PutObject should still return the content hash")
	}
	if _, ok := c.GetObject(hash); ok {
		t.Error("disabled GetObject should miss")
	}
}

func TestObjectRoundTrip(t *testing.T) {
	for _, compress := range []bool{false, true} {
		c := openTestCache(t, compress)
		data := []byte("hello content-addressed store")
		hash, err := c.PutObject(data)
		if err != nil {
			t.Fatalf("PutObject (compress=%v): %v", compress, err)
		}
		got, ok := c.GetObject(hash)
		if !ok {
			t.Fatalf("GetObject miss (compress=%v)", compress)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("roundtrip mismatch (compress=%v): got %q want %q", compress, got, data)
		}
		// Idempotent re-put: same hash, no error, content unchanged.
		hash2, err := c.PutObject(data)
		if err != nil {
			t.Fatalf("idempotent PutObject: %v", err)
		}
		if hash2 != hash {
			t.Errorf("idempotent put hash changed: %q vs %q", hash2, hash)
		}
	}
}

func TestGetObjectMiss(t *testing.T) {
	c := openTestCache(t, false)
	if _, ok := c.GetObject("deadbeef00000000"); ok {
		t.Error("expected miss for unknown hash")
	}
}

func TestKeyStability(t *testing.T) {
	base := Key{
		Kind: "plan", SnapshotID: "snap-1", RequestHash: "req", ConfigHash: "cfg",
		PromptVersion: "p1", ModelID: "m1", ProviderOrigin: "openai", IndexerVersion: "i1",
	}
	if base.String() != base.String() {
		t.Fatal("Key.String must be deterministic")
	}
	if !strings.HasPrefix(base.String(), "plan-") {
		t.Errorf("Key.String should carry the kind prefix, got %q", base.String())
	}

	// Changing any field must change the rendered key.
	mutators := []struct {
		name string
		mut  func(k *Key)
	}{
		{"snapshot", func(k *Key) { k.SnapshotID = "snap-2" }},
		{"request", func(k *Key) { k.RequestHash = "req2" }},
		{"config", func(k *Key) { k.ConfigHash = "cfg2" }},
		{"prompt", func(k *Key) { k.PromptVersion = "p2" }},
		{"model", func(k *Key) { k.ModelID = "m2" }},
		{"origin", func(k *Key) { k.ProviderOrigin = "azure" }},
		{"indexer", func(k *Key) { k.IndexerVersion = "i2" }},
		{"kind", func(k *Key) { k.Kind = "help" }},
	}
	for _, m := range mutators {
		k := base
		m.mut(&k)
		if k.String() == base.String() {
			t.Errorf("changing %s should change the key, but it stayed %q", m.name, base.String())
		}
	}
}

func TestURIRoundTrip(t *testing.T) {
	uri := URIFor("run-123", "evidence/E-1")
	runID, name, ok := ParseURI(uri)
	if !ok {
		t.Fatalf("ParseURI(%q) failed", uri)
	}
	if runID != "run-123" || name != "evidence/E-1" {
		t.Errorf("ParseURI = (%q, %q), want (run-123, evidence/E-1)", runID, name)
	}
	// Leading slash on the name is trimmed at URI construction.
	if URIFor("r", "/x") != URIScheme+"r/x" {
		t.Errorf("URIFor should trim leading slash, got %q", URIFor("r", "/x"))
	}
	// Malformed URIs are rejected.
	for _, bad := range []string{"", "http://example.com", URIScheme + "noslash"} {
		if _, _, ok := ParseURI(bad); ok {
			t.Errorf("ParseURI(%q) should fail", bad)
		}
	}
}

func TestRunStorePutAndReadResource(t *testing.T) {
	c := openTestCache(t, false)
	rs := c.RunStore("run-abc")
	uri, err := rs.Put("evidence/E-1.json", []byte(`{"ok":true}`))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	runID, name, ok := ParseURI(uri)
	if !ok {
		t.Fatalf("returned URI not parseable: %q", uri)
	}
	got, ok := c.ReadResource(runID, name)
	if !ok {
		t.Fatalf("ReadResource miss for %q/%q", runID, name)
	}
	if string(got) != `{"ok":true}` {
		t.Errorf("ReadResource = %q, want the stored JSON", got)
	}
	// Unknown artifact reads miss.
	if _, ok := c.ReadResource("run-abc", "missing.txt"); ok {
		t.Error("expected miss for missing artifact")
	}
}

func TestRunStoreRejectsTraversal(t *testing.T) {
	c := openTestCache(t, false)
	rs := c.RunStore("run-trav")
	if _, err := rs.Put("../escape", []byte("nope")); err == nil {
		t.Error("Put with a traversal name must be rejected")
	}
	// ReadResource must also refuse traversal names.
	if _, ok := c.ReadResource("run-trav", "../../etc/passwd"); ok {
		t.Error("ReadResource must refuse traversal names")
	}
}

func TestStatAndClear(t *testing.T) {
	c := openTestCache(t, false)
	if _, err := c.PutObject([]byte("obj-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutObject([]byte("obj-2")); err != nil {
		t.Fatal(err)
	}
	rs := c.RunStore("run-1")
	if _, err := rs.Put("a.txt", []byte("artifact")); err != nil {
		t.Fatal(err)
	}

	st, err := c.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.ObjectCount != 2 {
		t.Errorf("ObjectCount = %d, want 2", st.ObjectCount)
	}
	if st.RunCount != 1 {
		t.Errorf("RunCount = %d, want 1", st.RunCount)
	}
	if st.TotalBytes <= 0 {
		t.Errorf("TotalBytes = %d, want > 0", st.TotalBytes)
	}

	if err := c.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	st2, err := c.Stat()
	if err != nil {
		t.Fatalf("Stat after clear: %v", err)
	}
	if st2.ObjectCount != 0 || st2.RunCount != 0 {
		t.Errorf("after Clear: objects=%d runs=%d, want 0/0", st2.ObjectCount, st2.RunCount)
	}
}

// TestGCExpiresOldRuns backdates a run directory's mtime with os.Chtimes (no
// real sleeping) and asserts GC removes it and reports the freed bytes.
func TestGCExpiresOldRuns(t *testing.T) {
	c := openTestCache(t, false)
	rs := c.RunStore("old-run")
	if _, err := rs.Put("artifact.txt", []byte("some bytes here")); err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(c.Dir(), "runs", "old-run")
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(runDir, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	freed, err := c.GC(24*time.Hour, 0)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if freed <= 0 {
		t.Errorf("GC freed = %d, want > 0", freed)
	}
	if _, statErr := os.Stat(runDir); !os.IsNotExist(statErr) {
		t.Errorf("expired run dir should be removed, stat err = %v", statErr)
	}
}

// TestGCEvictsObjectsOverBudget asserts the size-budget eviction path frees
// bytes when the object store exceeds maxBytes.
func TestGCEvictsObjectsOverBudget(t *testing.T) {
	c := openTestCache(t, false)
	for _, s := range []string{"alpha-object", "bravo-object", "charlie-object"} {
		if _, err := c.PutObject([]byte(s + "-padding-padding-padding")); err != nil {
			t.Fatal(err)
		}
	}
	st, err := c.Stat()
	if err != nil {
		t.Fatal(err)
	}
	// Set a budget below current occupancy so eviction must run.
	freed, err := c.GC(0, st.TotalBytes/2)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if freed <= 0 {
		t.Errorf("size-budget GC freed = %d, want > 0", freed)
	}
}

func TestIsInsideRepo(t *testing.T) {
	cases := []struct {
		name       string
		cacheDir   string
		root       string
		wantInside bool
	}{
		{"nested", "/repo/.cache", "/repo", true},
		{"same dir is inside", "/repo", "/repo", true},
		{"sibling", "/other", "/repo", false},
		{"parent", "/", "/repo", false},
		{"empty cache", "", "/repo", false},
		{"empty root", "/repo/.cache", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsInsideRepo(tc.cacheDir, tc.root); got != tc.wantInside {
				t.Errorf("IsInsideRepo(%q, %q) = %v, want %v", tc.cacheDir, tc.root, got, tc.wantInside)
			}
		})
	}
}
