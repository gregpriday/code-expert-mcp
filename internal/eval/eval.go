// Package eval is the quality-evaluation harness for CodeExpert. It runs cases
// (a request plus expectations) through the engine and grades the structured
// result, so model routing, retrieval, prompts, and validators can be measured
// rather than judged by feel. It records the request, the result, the grader
// outcomes, and a score for every case — the artifact the improvement loop turns
// failures into regression cases from.
//
// Three grader layers are supported:
//   - Code graders (this file): deterministic checks on the structured output —
//     schema validity, the no-write invariant, evidence resolution, status,
//     coverage, and forbidden approval language. These never need a model.
//   - Model graders (model_grader.go): an LLM-as-judge layer for correctness and
//     completeness, used when a provider is available.
//   - Human calibration: the JSON report is the input to periodic human review.
package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gregpriday/codeexpert/internal/schema"
	"github.com/gregpriday/codeexpert/internal/workflow"
)

// Capability is the tool a case exercises.
type Capability string

const (
	CapPlan   Capability = "plan"
	CapHelp   Capability = "help"
	CapReview Capability = "review"
)

// Case is one evaluation unit: a request for one capability plus the expectations
// its result must satisfy.
type Case struct {
	ID         string                `json:"id"`
	Capability Capability            `json:"capability"`
	Plan       *schema.PlanRequest   `json:"plan,omitempty"`
	Help       *schema.HelpRequest   `json:"help,omitempty"`
	Review     *schema.ReviewRequest `json:"review,omitempty"`
	Expect     Expectation           `json:"expect"`
}

// Expectation is the deterministic, code-checkable contract a case's result must
// meet. Zero-valued fields are not checked.
type Expectation struct {
	// MustComplete requires a terminal status of "complete".
	MustComplete bool `json:"must_complete,omitempty"`
	// AllowPartial permits "partial" (used for cases that are expected to hit a
	// limit); without it, a partial result fails the status grader.
	AllowPartial bool `json:"allow_partial,omitempty"`
	// MustError requires the run to return an error with this code (e.g. an
	// invalid request or a non-git review).
	MustErrorCode string `json:"must_error_code,omitempty"`

	// Plan/help expectations.
	MinSteps            int  `json:"min_steps,omitempty"`
	RequireTraceability bool `json:"require_traceability,omitempty"`
	RequireDirectAnswer bool `json:"require_direct_answer,omitempty"`

	// Review expectations.
	MinFindings        int      `json:"min_findings,omitempty"`
	MaxFindings        int      `json:"max_findings,omitempty"` // -1 disables; 0 means exactly-zero is allowed
	ExpectFindingPaths []string `json:"expect_finding_paths,omitempty"`
	ForbidApprovalLang bool     `json:"forbid_approval_language,omitempty"`
}

// Outcome captures everything a grader needs about a single run.
type Outcome struct {
	Case       Case
	Plan       *schema.PlanResult
	Review     *schema.ReviewResult
	Err        error
	RepoBefore string
	RepoAfter  string
}

// GradeResult is one grader's verdict on one outcome.
type GradeResult struct {
	Grader string  `json:"grader"`
	Pass   bool    `json:"pass"`
	Score  float64 `json:"score"` // 0..1
	Detail string  `json:"detail,omitempty"`
}

// CaseResult bundles a case's outcome with its grades.
type CaseResult struct {
	CaseID     string        `json:"case_id"`
	Capability Capability    `json:"capability"`
	Grades     []GradeResult `json:"grades"`
	Passed     bool          `json:"passed"`
	Score      float64       `json:"score"`
}

// Report is the aggregate over a run of cases.
type Report struct {
	Cases     []CaseResult       `json:"cases"`
	Total     int                `json:"total"`
	Passed    int                `json:"passed"`
	MeanScore float64            `json:"mean_score"`
	ByGrader  map[string]float64 `json:"by_grader_pass_rate"`
}

// Grader scores one outcome on one dimension.
type Grader interface {
	Name() string
	Grade(ctx context.Context, out Outcome) GradeResult
}

// CodeGraders returns the deterministic grader set every run applies.
func CodeGraders() []Grader {
	return []Grader{
		schemaGrader{}, noWriteGrader{}, statusGrader{}, evidenceGrader{},
		planGrader{}, helpGrader{}, reviewGrader{},
	}
}

// RunCases executes every case through the engine, applies the graders, and
// aggregates a report. The engine is the real one — pass an engine built with a
// fake provider for deterministic CI, or a live provider for a quality run.
func RunCases(ctx context.Context, eng *workflow.Engine, cases []Case, graders []Grader) Report {
	rep := Report{ByGrader: map[string]float64{}}
	graderPass := map[string]int{}
	graderTotal := map[string]int{}

	for _, c := range cases {
		out := runCase(ctx, eng, c)
		cr := CaseResult{CaseID: c.ID, Capability: c.Capability, Passed: true}
		var sum float64
		for _, g := range graders {
			gr := g.Grade(ctx, out)
			cr.Grades = append(cr.Grades, gr)
			sum += gr.Score
			graderTotal[gr.Grader]++
			if gr.Pass {
				graderPass[gr.Grader]++
			} else {
				cr.Passed = false
			}
		}
		if len(graders) > 0 {
			cr.Score = sum / float64(len(graders))
		}
		rep.Cases = append(rep.Cases, cr)
		rep.Total++
		if cr.Passed {
			rep.Passed++
		}
		rep.MeanScore += cr.Score
	}
	if rep.Total > 0 {
		rep.MeanScore /= float64(rep.Total)
	}
	for name, total := range graderTotal {
		if total > 0 {
			rep.ByGrader[name] = float64(graderPass[name]) / float64(total)
		}
	}
	return rep
}

// runCase runs a single case, fingerprinting the repo around the run to support
// the no-write grader.
func runCase(ctx context.Context, eng *workflow.Engine, c Case) Outcome {
	out := Outcome{Case: c}
	root := caseRoot(c)
	if root != "" {
		out.RepoBefore = fingerprintTree(root)
	}
	switch c.Capability {
	case CapPlan:
		if c.Plan != nil {
			res, err := eng.Plan(ctx, *c.Plan, workflow.RunOptions{})
			out.Plan, out.Err = &res, err
		}
	case CapHelp:
		if c.Help != nil {
			res, err := eng.Help(ctx, *c.Help, workflow.RunOptions{})
			out.Plan, out.Err = &res, err
		}
	case CapReview:
		if c.Review != nil {
			res, err := eng.Review(ctx, *c.Review, workflow.RunOptions{})
			out.Review, out.Err = &res, err
		}
	}
	if out.Err != nil {
		out.Plan, out.Review = nil, nil
	}
	if root != "" {
		out.RepoAfter = fingerprintTree(root)
	}
	return out
}

func caseRoot(c Case) string {
	switch {
	case c.Plan != nil:
		return c.Plan.Root
	case c.Help != nil:
		return c.Help.Root
	case c.Review != nil:
		return c.Review.Root
	}
	return ""
}

// fingerprintTree hashes every file's name and contents under root, INCLUDING
// .git, so the no-write grader catches any mutation to the working tree or Git
// state — the read-only invariant covers both. (The engine runs only read-only
// git commands, so .git stays byte-stable across a run, as the release-blocking
// TestNoWriteInvariant also asserts.)
func fingerprintTree(root string) string {
	h := sha256.New()
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		fmt.Fprintf(h, "%s\n", rel)
		if b, rerr := os.ReadFile(p); rerr == nil {
			h.Write(b)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

// LoadCases reads every *.json case file in a directory.
func LoadCases(dir string) ([]Case, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Case
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		c, err := decodeCase(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
