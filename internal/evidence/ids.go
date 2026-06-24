// Package evidence stores and validates the repository evidence that backs every
// material assertion. IDs are stable within a snapshot and human-debuggable.
package evidence

import (
	"strconv"
	"strings"

	"github.com/gregpriday/codeexpert/internal/hashutil"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// slugPath converts a repo-relative path into an ID-safe slug.
func slugPath(p string) string {
	r := strings.NewReplacer("/", "_", ".", "_", " ", "_", "\\", "_")
	s := r.Replace(p)
	if len(s) > 48 {
		s = s[len(s)-48:]
	}
	return s
}

func slugSymbol(name string) string {
	r := strings.NewReplacer(".", "_", "/", "_", " ", "_", "(", "", ")", "", "*", "")
	s := r.Replace(name)
	if len(s) > 48 {
		s = s[:48]
	}
	return s
}

// makeID builds a deterministic evidence ID from a record's salient fields.
func makeID(rec schema.EvidenceRecord) string {
	digest := hashutil.Short(hashutil.HashFields(map[string]string{
		"kind":     string(rec.Kind),
		"snapshot": rec.SnapshotID,
		"path":     rec.Path,
		"start":    strconv.Itoa(rec.StartLine),
		"end":      strconv.Itoa(rec.EndLine),
		"symbol":   rec.Symbol,
		"blob":     rec.BlobHash,
		"summary":  rec.Summary,
		"query":    rec.Provenance.Query,
	}), 6)

	switch rec.Kind {
	case schema.EvidenceKindFile:
		return "E-file-" + slugPath(rec.Path) + "-L" + strconv.Itoa(rec.StartLine) + "-L" + strconv.Itoa(rec.EndLine) + "-" + digest
	case schema.EvidenceKindDiff:
		return "E-diff-" + slugPath(rec.Path) + "-" + digest
	case schema.EvidenceKindSymbol:
		return "E-symbol-" + slugSymbol(rec.Symbol) + "-" + digest
	case schema.EvidenceKindCheck:
		return "E-check-" + slugSymbol(rec.Summary) + "-" + digest
	case schema.EvidenceKindSearch:
		return "E-search-" + slugPath(rec.Path) + "-" + digest
	case schema.EvidenceKindHistory:
		return "E-history-" + slugPath(rec.Path) + "-" + digest
	case schema.EvidenceKindGuidance:
		return "E-guidance-" + slugPath(rec.Path) + "-" + digest
	case schema.EvidenceKindGrounding:
		return "E-grounding-" + slugSymbol(rec.Provenance.Query) + "-" + digest
	default:
		return "E-" + string(rec.Kind) + "-" + digest
	}
}
