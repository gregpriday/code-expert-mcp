package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/index"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// hasStageLimitation reports whether any limitation was recorded for the stage.
func hasStageLimitation(lims []schema.Limitation, stage string) bool {
	for _, l := range lims {
		if l.Stage == stage {
			return true
		}
	}
	return false
}

// isBudgetTimeout reports whether an error is the run's own budget/time
// exhaustion (rather than a caller cancellation or a real failure), so callers
// can convert it into a truthful partial result instead of aborting.
func isBudgetTimeout(err error) bool {
	code := schema.AsToolError(err).Code
	return code == schema.CodeBudgetExhausted || code == schema.CodeProviderTimeout
}

// newRunID generates a unique run identifier.
func newRunID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + "-" + hex.EncodeToString(b[:])
}

// resolveAndSnapshot resolves the root and freezes the repository.
func (e *Engine) resolveAndSnapshot(ctx context.Context, root string, allowed []string) (*repo.Snapshot, error) {
	r, err := repo.ResolveRoot(repo.DefaultRoot(root), e.Cfg.Repository.FollowSymlinks, allowed)
	if err != nil {
		return nil, err
	}
	return repo.BuildSnapshot(ctx, r, e.Cfg.Repository)
}

var (
	pathToken  = regexp.MustCompile(`[\w./-]+\.[A-Za-z0-9]{1,5}`)
	identToken = regexp.MustCompile(`\b([A-Z][a-zA-Z0-9]+|[a-z][a-zA-Z0-9]*_[a-zA-Z0-9_]+|[a-zA-Z_][a-zA-Z0-9_]{3,})\b`)
	quoted     = regexp.MustCompile(`"([^"]{3,})"|` + "`([^`]{3,})`")
)

// normalizeTask deterministically extracts the goal, anchors, and explicit paths
// from the instructions and optional task contract. It never asks the user. The
// full task contract is carried forward (id, title, known facts, prior plan) so
// nothing the caller supplied is silently discarded before synthesis.
func normalizeTask(req planHelpInput, snap *repo.Snapshot) schema.InterpretedTask {
	it := schema.InterpretedTask{Goal: strings.TrimSpace(req.Instructions), Mode: req.Mode}
	if req.Task != nil {
		it.TaskID = req.Task.ID
		it.Title = req.Task.Title
		it.Constraints = req.Task.Constraints
		it.AcceptanceCriteria = req.Task.AcceptanceCriteria
		it.NonGoals = req.Task.NonGoals
		it.KnownFacts = req.Task.KnownFacts
		it.PriorPlan = req.Task.PriorPlan
		if it.Goal == "" {
			it.Goal = req.Task.Description
		}
	}
	anchors := extractAnchors(req.Instructions)
	if req.Task != nil {
		anchors = append(anchors, extractAnchors(req.Task.Description)...)
		for _, c := range req.Task.AcceptanceCriteria {
			anchors = append(anchors, extractAnchors(c)...)
		}
		for _, f := range req.Task.KnownFacts {
			anchors = append(anchors, extractAnchors(f)...)
		}
	}
	it.SearchAnchors = dedupeStrings(anchors)

	// Resolve explicit paths that actually exist in the snapshot.
	if snap != nil {
		for _, a := range it.SearchAnchors {
			if _, ok := snap.Stat(a); ok {
				it.ExplicitPaths = append(it.ExplicitPaths, a)
			}
		}
	}
	return it
}

// extractAnchors pulls candidate search anchors out of free-form instruction
// text: file-like paths, quoted phrases, and identifier-like tokens (excluding
// common words). The result is order-preserving and may contain duplicates;
// callers pass it through dedupeStrings to collapse repeats.
func extractAnchors(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	for _, m := range pathToken.FindAllString(text, -1) {
		out = append(out, m)
	}
	for _, m := range quoted.FindAllStringSubmatch(text, -1) {
		if m[1] != "" {
			out = append(out, m[1])
		} else if m[2] != "" {
			out = append(out, m[2])
		}
	}
	for _, m := range identToken.FindAllString(text, -1) {
		if !commonWord(m) {
			out = append(out, m)
		}
	}
	return out
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
	"should": true, "would": true, "could": true, "when": true, "where": true, "which": true,
	"without": true, "changing": true, "change": true, "from": true, "into": true, "must": true,
	"does": true, "doesnt": true, "about": true, "implement": true, "function": true, "method": true,
}

func commonWord(s string) bool {
	return stopWords[strings.ToLower(s)]
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// preflight builds the deterministic context bundle: repo brief, explicit paths,
// guidance, and initial anchor search hits. Returned as a single text block.
func (e *Engine) preflight(ctx context.Context, snap *repo.Snapshot, it schema.InterpretedTask, lex *index.LexicalEngine) string {
	var b strings.Builder
	brief := snap.Brief(e.Cfg.Repository)
	fmt.Fprintf(&b, "## Repository snapshot %s\n", snap.ID())
	fmt.Fprintf(&b, "Root: %s | Git: %v | Branch: %s | Dirty: %v | Files: %d\n",
		brief.Root, brief.IsGit, brief.Branch, brief.Dirty, len(snap.ListFiles()))
	if len(brief.Languages) > 0 {
		var langs []string
		for _, l := range brief.Languages {
			langs = append(langs, fmt.Sprintf("%s(%d)", l.Language, l.Files))
		}
		fmt.Fprintf(&b, "Languages: %s\n", strings.Join(langs, ", "))
	}
	if len(brief.Manifests) > 0 {
		fmt.Fprintf(&b, "Manifests: %s\n", strings.Join(brief.Manifests, ", "))
	}
	if len(brief.SourceDirs) > 0 {
		fmt.Fprintf(&b, "Source dirs: %s\n", strings.Join(brief.SourceDirs, ", "))
	}
	if len(brief.TestDirs) > 0 {
		fmt.Fprintf(&b, "Test dirs: %s\n", strings.Join(brief.TestDirs, ", "))
	}
	if len(brief.GuidanceFiles) > 0 {
		fmt.Fprintf(&b, "Guidance files (UNTRUSTED): %s\n", strings.Join(brief.GuidanceFiles, ", "))
	}

	if len(it.ExplicitPaths) > 0 {
		fmt.Fprintf(&b, "\n## Explicit paths referenced in the request\n%s\n", strings.Join(it.ExplicitPaths, "\n"))
	}

	// Run a small number of anchor searches to seed the model.
	anchors := it.SearchAnchors
	if len(anchors) > 6 {
		anchors = anchors[:6]
	}
	if len(anchors) > 0 {
		b.WriteString("\n## Initial search hits (anchors)\n")
		for _, a := range anchors {
			hits, err := lex.Search(ctx, index.SearchQuery{Pattern: a, Mode: index.ModeLiteral, MaxResults: 5, ContextLines: 0})
			if err != nil || len(hits) == 0 {
				continue
			}
			fmt.Fprintf(&b, "Anchor %q:\n", a)
			for _, h := range hits {
				fmt.Fprintf(&b, "  %s:%d  %s\n", h.Path, h.Line, truncateStr(h.Match, 120))
			}
		}
	}
	return b.String()
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// guidanceBlock loads guidance files with an explicit untrusted label.
func (e *Engine) guidanceList(ctx context.Context, snap *repo.Snapshot) []repo.Guidance {
	return snap.LoadGuidance(ctx, e.Cfg.Repository, 4000)
}

// persistArtifact stores a run artifact and returns its URI (empty if no cache).
func (e *Engine) persistArtifact(runID, name string, data []byte) string {
	if e.Cache == nil {
		return ""
	}
	uri, err := e.Cache.RunStore(runID).Put(name, data)
	if err != nil {
		return ""
	}
	return uri
}

// sortedEvidenceRefs returns refs sorted by ID for stable output.
func sortedEvidenceRefs(refs []schema.EvidenceRef) []schema.EvidenceRef {
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	return refs
}

// configHash is a stable digest of the analysis-affecting config for cache keys.
func configHash(cfg config.Config) string {
	return strings.Join([]string{
		cfg.Provider.BaseURL, cfg.Models.Planner, cfg.Models.Verifier, cfg.Models.Scout,
		cfg.Review.MinimumEvidence, cfg.Retrieval.Summaries,
	}, "|")
}
