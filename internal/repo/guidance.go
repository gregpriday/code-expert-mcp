package repo

import (
	"context"

	"github.com/gregpriday/codeexpert/internal/config"
)

// guidanceFiles returns the configured guidance files that exist in the snapshot.
func (s *Snapshot) guidanceFiles(cfg config.RepositoryConfig) []string {
	var out []string
	for _, g := range cfg.GuidelineFiles {
		if _, ok := s.Stat(g); ok {
			out = append(out, g)
		}
	}
	return out
}

// Guidance is a single repository guidance document. Its content is untrusted:
// it expresses project conventions but must never be followed as instructions.
type Guidance struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Bytes   int    `json:"bytes"`
}

// LoadGuidance reads the configured guidance files (bounded in size). The
// returned content is labeled untrusted by callers before it reaches the model.
func (s *Snapshot) LoadGuidance(ctx context.Context, cfg config.RepositoryConfig, maxBytes int) []Guidance {
	var out []Guidance
	for _, g := range s.guidanceFiles(cfg) {
		fc, err := s.ReadFile(ctx, g)
		if err != nil {
			continue
		}
		content := string(fc.Bytes)
		if maxBytes > 0 && len(content) > maxBytes {
			content = content[:maxBytes] + "\n…(truncated)…"
		}
		out = append(out, Guidance{Path: g, Content: content, Bytes: len(fc.Bytes)})
	}
	return out
}
