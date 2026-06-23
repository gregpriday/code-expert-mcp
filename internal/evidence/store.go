package evidence

import (
	"sync"

	"github.com/gregpriday/codeexpert/internal/schema"
)

// Store is the per-run evidence collection. It is safe for concurrent use by the
// candidate passes. Reads (Get, Has, All) take a read lock so the concurrent
// passes don't serialize on each other; only Add, the sole writer, takes the
// exclusive lock.
type Store struct {
	snapshotID string
	mu         sync.RWMutex
	records    map[string]schema.EvidenceRecord
	order      []string
}

// NewStore creates an evidence store bound to a snapshot.
func NewStore(snapshotID string) *Store {
	return &Store{snapshotID: snapshotID, records: map[string]schema.EvidenceRecord{}}
}

// Add stores a record, assigning a stable ID (and snapshot ID) if absent, and
// returns the stored record. Duplicate IDs are coalesced.
func (s *Store) Add(rec schema.EvidenceRecord) schema.EvidenceRecord {
	if rec.SnapshotID == "" {
		rec.SnapshotID = s.snapshotID
	}
	if rec.ID == "" {
		rec.ID = makeID(rec)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[rec.ID]; !exists {
		s.order = append(s.order, rec.ID)
	}
	s.records[rec.ID] = rec
	return rec
}

// Get returns a stored record by ID.
func (s *Store) Get(id string) (schema.EvidenceRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.records[id]
	return r, ok
}

// Has reports whether an ID is known.
func (s *Store) Has(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.records[id]
	return ok
}

// All returns records in insertion order.
func (s *Store) All() []schema.EvidenceRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]schema.EvidenceRecord, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.records[id])
	}
	return out
}

// Refs returns the compact references for all records.
func (s *Store) Refs() []schema.EvidenceRef {
	all := s.All()
	out := make([]schema.EvidenceRef, 0, len(all))
	for _, r := range all {
		out = append(out, r.Ref())
	}
	return out
}

// RefsFor returns compact references for the given IDs that exist.
func (s *Store) RefsFor(ids []string) []schema.EvidenceRef {
	var out []schema.EvidenceRef
	for _, id := range ids {
		if r, ok := s.Get(id); ok {
			out = append(out, r.Ref())
		}
	}
	return out
}

// SnapshotID returns the bound snapshot ID.
func (s *Store) SnapshotID() string { return s.snapshotID }
