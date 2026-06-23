package schema

// EvidenceRecord is the full, stored evidence object. EvidenceRef is the
// compact form embedded in outputs.
type EvidenceRecord struct {
	ID         string       `json:"id"`
	Kind       EvidenceKind `json:"kind"`
	SnapshotID string       `json:"snapshot_id"`
	Path       string       `json:"path,omitempty"`
	StartLine  int          `json:"start_line,omitempty"`
	EndLine    int          `json:"end_line,omitempty"`
	Symbol     string       `json:"symbol,omitempty"`
	BlobHash   string       `json:"blob_hash,omitempty"`
	Summary    string       `json:"summary"`
	RawObject  string       `json:"raw_object_uri,omitempty"`
	Provenance Provenance   `json:"provenance"`
}

// Provenance describes how an evidence record was produced.
type Provenance struct {
	Tool      string `json:"tool,omitempty"`
	Stage     string `json:"stage,omitempty"`
	ModelID   string `json:"model_id,omitempty"`
	Query     string `json:"query,omitempty"`
	Untrusted bool   `json:"untrusted,omitempty"`
}

// Ref returns the compact EvidenceRef projection of a record.
func (r EvidenceRecord) Ref() EvidenceRef {
	return EvidenceRef{
		ID:        r.ID,
		Kind:      r.Kind,
		Path:      r.Path,
		StartLine: r.StartLine,
		EndLine:   r.EndLine,
		Symbol:    r.Symbol,
		Summary:   r.Summary,
		URI:       r.RawObject,
	}
}
