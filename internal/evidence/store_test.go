package evidence

import (
	"strconv"
	"sync"
	"testing"

	"github.com/gregpriday/codeexpert/internal/schema"
)

const testSnapshotID = "snap-1"

func mkRecord(i int) schema.EvidenceRecord {
	return schema.EvidenceRecord{
		Kind:      schema.EvidenceKindFile,
		Path:      "pkg/file" + strconv.Itoa(i) + ".go",
		StartLine: 1,
		EndLine:   2,
		Summary:   "record " + strconv.Itoa(i),
	}
}

// wantID computes the ID Add assigns to mkRecord(i): Add stamps the store's
// snapshot ID onto the record before hashing, so the test must do the same to
// predict the ID.
func wantID(i int) string {
	rec := mkRecord(i)
	rec.SnapshotID = testSnapshotID
	return makeID(rec)
}

// TestStoreConcurrentAccess exercises the RWMutex-guarded store from many
// goroutines doing concurrent writes and reads. Run under `go test -race` it
// asserts that promoting reads to RLock introduced no data race, and that the
// store's invariants (deterministic IDs, duplicate coalescing, insertion order)
// hold under contention.
func TestStoreConcurrentAccess(t *testing.T) {
	t.Parallel()
	store := NewStore(testSnapshotID)

	const distinct = 64
	const writers = 8

	// IDs Add will assign, precomputed so the concurrent readers below hit the
	// Get/RLock path for real instead of querying IDs that never exist.
	ids := make([]string, distinct)
	for i := 0; i < distinct; i++ {
		ids[i] = wantID(i)
	}

	// Concurrent writers each add the full set of distinct records. Because IDs
	// are deterministic, duplicates must coalesce to exactly `distinct` records.
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < distinct; i++ {
				rec := store.Add(mkRecord(i))
				if rec.ID != ids[i] {
					t.Errorf("Add assigned ID %q, want %q", rec.ID, ids[i])
				}
				// Interleave reads against the in-flight writers.
				if !store.Has(rec.ID) {
					t.Errorf("Has(%s) = false right after Add", rec.ID)
				}
				if _, ok := store.Get(rec.ID); !ok {
					t.Errorf("Get(%s) missing right after Add", rec.ID)
				}
			}
		}()
	}

	// Concurrent readers hammer the read paths (including RefsFor's per-id Get)
	// with the real IDs while writes are in flight; they only assert the store
	// stays internally consistent (no torn reads / races).
	for r := 0; r < writers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < distinct; i++ {
				_ = store.All()
				_ = store.Refs()
				_ = store.RefsFor(ids)
			}
		}()
	}

	wg.Wait()

	if got := len(store.All()); got != distinct {
		t.Fatalf("All() = %d records, want %d (duplicates must coalesce)", got, distinct)
	}
	if got := len(store.Refs()); got != distinct {
		t.Fatalf("Refs() = %d, want %d", got, distinct)
	}
	// Every record is now present, so RefsFor over all IDs resolves every one.
	if got := len(store.RefsFor(ids)); got != distinct {
		t.Fatalf("RefsFor(all) = %d, want %d", got, distinct)
	}

	// All IDs are unique (no torn or duplicated entries).
	all := store.All()
	seen := map[string]bool{}
	for _, rec := range all {
		if seen[rec.ID] {
			t.Fatalf("duplicate ID %s in All()", rec.ID)
		}
		seen[rec.ID] = true
	}
}

// TestStoreRefsFor covers RefsFor's resolution of present, missing, and
// duplicate IDs — the read path that delegates to Get under the read lock.
func TestStoreRefsFor(t *testing.T) {
	store := NewStore(testSnapshotID)
	r0 := store.Add(mkRecord(0))
	r1 := store.Add(mkRecord(1))

	refs := store.RefsFor([]string{r0.ID, "does-not-exist", r1.ID, r0.ID})
	// Two existing IDs (one repeated) and one missing: present IDs resolve in
	// request order, the missing one is dropped, the duplicate resolves twice.
	if len(refs) != 3 {
		t.Fatalf("RefsFor returned %d refs, want 3 (r0, r1, r0 again; missing dropped)", len(refs))
	}
	if refs[0].ID != r0.ID || refs[1].ID != r1.ID || refs[2].ID != r0.ID {
		t.Errorf("RefsFor order = [%s %s %s], want [%s %s %s]",
			refs[0].ID, refs[1].ID, refs[2].ID, r0.ID, r1.ID, r0.ID)
	}

	if got := store.RefsFor(nil); got != nil {
		t.Errorf("RefsFor(nil) = %v, want nil", got)
	}
}

// TestStoreInsertionOrder asserts All returns records in first-seen insertion
// order, and that re-adding an existing ID updates in place without disturbing
// the order.
func TestStoreInsertionOrder(t *testing.T) {
	store := NewStore(testSnapshotID)
	want := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		want = append(want, store.Add(mkRecord(i)).ID)
	}
	// Re-add an earlier record: it must coalesce, not reorder.
	store.Add(mkRecord(2))

	all := store.All()
	if len(all) != 5 {
		t.Fatalf("All() = %d, want 5 after re-adding a duplicate", len(all))
	}
	for i, rec := range all {
		if rec.ID != want[i] {
			t.Errorf("All()[%d] = %s, want %s (insertion order)", i, rec.ID, want[i])
		}
	}
}

func fileRecord(path string, start, end int) schema.EvidenceRecord {
	return schema.EvidenceRecord{
		Kind: schema.EvidenceKindFile, Path: path, StartLine: start, EndLine: end,
		Summary: "content of " + path,
	}
}

func TestAddAssignsIDAndSnapshot(t *testing.T) {
	s := NewStore("snap-1")
	if s.SnapshotID() != "snap-1" {
		t.Fatalf("SnapshotID = %q, want snap-1", s.SnapshotID())
	}
	rec := s.Add(fileRecord("main.go", 1, 5))
	if rec.ID == "" {
		t.Error("Add should assign a stable ID")
	}
	if rec.SnapshotID != "snap-1" {
		t.Errorf("Add should backfill the snapshot ID, got %q", rec.SnapshotID)
	}

	got, ok := s.Get(rec.ID)
	if !ok {
		t.Fatalf("Get(%q) miss", rec.ID)
	}
	if got.Path != "main.go" {
		t.Errorf("stored record path = %q, want main.go", got.Path)
	}
	if !s.Has(rec.ID) {
		t.Errorf("Has(%q) = false, want true", rec.ID)
	}
	if _, ok := s.Get("nope"); ok {
		t.Error("Get on unknown ID should miss")
	}
}

func TestIDDeterminismAndDistinctness(t *testing.T) {
	s := NewStore("snap-1")
	a := s.Add(fileRecord("main.go", 1, 5))
	b := s.Add(fileRecord("main.go", 1, 5))
	// Identical content coalesces to a single record.
	if a.ID != b.ID {
		t.Errorf("identical records should share an ID: %q vs %q", a.ID, b.ID)
	}
	if got := len(s.All()); got != 1 {
		t.Errorf("dedup failed: All() = %d records, want 1", got)
	}
	// Different content yields a different ID.
	c := s.Add(fileRecord("other.go", 1, 5))
	if c.ID == a.ID {
		t.Errorf("distinct records must not collide: both %q", c.ID)
	}
	if got := len(s.All()); got != 2 {
		t.Errorf("All() = %d, want 2 after a distinct add", got)
	}
}

func TestAllInsertionOrder(t *testing.T) {
	s := NewStore("snap-1")
	first := s.Add(fileRecord("a.go", 1, 2))
	second := s.Add(fileRecord("b.go", 1, 2))
	third := s.Add(fileRecord("c.go", 1, 2))
	all := s.All()
	if len(all) != 3 {
		t.Fatalf("All() = %d records, want 3", len(all))
	}
	wantOrder := []string{first.ID, second.ID, third.ID}
	for i, w := range wantOrder {
		if all[i].ID != w {
			t.Errorf("All()[%d].ID = %q, want %q (insertion order not preserved)", i, all[i].ID, w)
		}
	}
}

func TestRefsAndRefsFor(t *testing.T) {
	s := NewStore("snap-1")
	a := s.Add(fileRecord("a.go", 1, 2))
	b := s.Add(fileRecord("b.go", 3, 4))

	refs := s.Refs()
	if len(refs) != 2 {
		t.Fatalf("Refs() = %d, want 2", len(refs))
	}
	if refs[0].ID != a.ID || refs[0].Path != "a.go" {
		t.Errorf("Refs()[0] = %+v, want a.go ref", refs[0])
	}

	sub := s.RefsFor([]string{b.ID, "unknown-id"})
	if len(sub) != 1 {
		t.Fatalf("RefsFor should skip unknown IDs: got %d, want 1", len(sub))
	}
	if sub[0].ID != b.ID {
		t.Errorf("RefsFor()[0].ID = %q, want %q", sub[0].ID, b.ID)
	}
}

// TestConcurrentAdd fans out goroutines each adding a distinct record (distinct
// content => distinct deterministic ID), then asserts the store holds exactly
// one record per goroutine with unique IDs. Run under -race, this guards the
// store's mutex against concurrent-access regressions.
func TestConcurrentAdd(t *testing.T) {
	s := NewStore("snap-concurrent")
	const n = 64

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			// Distinct path per goroutine guarantees a distinct ID.
			s.Add(fileRecord("file-"+strconv.Itoa(i)+".go", i+1, i+2))
		}(i)
	}
	wg.Wait()

	all := s.All()
	if len(all) != n {
		t.Fatalf("All() = %d records, want %d", len(all), n)
	}
	seen := make(map[string]bool, n)
	for _, r := range all {
		if seen[r.ID] {
			t.Errorf("duplicate ID after concurrent adds: %q", r.ID)
		}
		seen[r.ID] = true
	}
}

// TestConcurrentReadWrite exercises concurrent readers and writers so the race
// detector covers the Add/Get/All/Has paths together.
func TestConcurrentReadWrite(t *testing.T) {
	s := NewStore("snap-rw")
	const n = 32

	var wg sync.WaitGroup
	wg.Add(n * 2)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			rec := s.Add(fileRecord("rw-"+strconv.Itoa(i)+".go", i+1, i+2))
			s.Has(rec.ID)
		}(i)
		go func() {
			defer wg.Done()
			_ = s.All()
			s.Refs()
		}()
	}
	wg.Wait()

	if got := len(s.All()); got != n {
		t.Errorf("All() = %d, want %d", got, n)
	}
}
