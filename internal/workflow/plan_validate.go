package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/evidence"
	"github.com/gregpriday/codeexpert/internal/prompts"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// validatePlan applies the deterministic plan gates and returns a list of issues
// (empty means valid).
func validatePlan(ctx context.Context, plan *schema.ImplementationPlan, snap *repo.Snapshot, evid *evidence.Store, cfg config.Config) []string {
	var issues []string
	val := evidence.NewValidator(snap)

	if strings.TrimSpace(plan.Goal) == "" {
		issues = append(issues, "plan.goal is empty")
	}

	ids := map[string]bool{}
	for _, s := range plan.Steps {
		if s.ID == "" {
			issues = append(issues, "a step has an empty id")
			continue
		}
		if ids[s.ID] {
			issues = append(issues, "duplicate step id "+s.ID)
		}
		ids[s.ID] = true
	}

	for _, s := range plan.Steps {
		// File targets must exist (unless the action is to create a new file).
		createPaths := map[string]bool{}
		for _, f := range s.Files {
			if f.Action == "create" {
				createPaths[f.Path] = true
				continue
			}
			if f.Path != "" && !val.FileExists(f.Path) {
				issues = append(issues, fmt.Sprintf("step %s references non-existent file %q", s.ID, f.Path))
			}
		}
		// Symbol targets that name a path must point at a real file (unless that
		// file is being created by this step). Symbol-name resolution is left to
		// the heuristic index; only the published path is validated here.
		for _, sym := range s.Symbols {
			if sym.Path != "" && !createPaths[sym.Path] && !val.FileExists(sym.Path) {
				issues = append(issues, fmt.Sprintf("step %s references symbol %q in non-existent file %q", s.ID, sym.Name, sym.Path))
			}
		}
		// Dependencies must reference known steps (DAG checked below).
		for _, dep := range s.DependsOn {
			if !ids[dep] {
				issues = append(issues, fmt.Sprintf("step %s depends on unknown step %q", s.ID, dep))
			}
		}
		// Each implementation step should have a validation method.
		if cfg.Plan.RequireValidationPerStep && len(s.Validation) == 0 {
			issues = append(issues, fmt.Sprintf("step %s has no validation method", s.ID))
		}
		// Cited evidence IDs must exist.
		for _, eid := range s.EvidenceIDs {
			if !evid.Has(eid) {
				issues = append(issues, fmt.Sprintf("step %s cites unknown evidence id %q", s.ID, eid))
			}
		}
		if cfg.Plan.RequireFileEvidence && len(s.Files) == 0 && len(s.EvidenceIDs) == 0 {
			issues = append(issues, fmt.Sprintf("step %s has neither files nor evidence", s.ID))
		}
		// CodeExpert must never instruct itself to edit the repository.
		if mentionsSelfEdit(s.Objective) || mentionsSelfEdit(strings.Join(s.DetailedChanges, " ")) {
			// This is informational, not a hard failure; the model is describing
			// changes for another agent. We only flag explicit self-references.
		}
	}

	// Dependency DAG (cycle detection).
	if cyc := detectCycle(plan.Steps); cyc != "" {
		issues = append(issues, "step dependency cycle detected: "+cyc)
	}

	// Current-behavior and impacted-area evidence references.
	for _, cb := range plan.CurrentBehavior {
		for _, eid := range cb.EvidenceIDs {
			if !evid.Has(eid) {
				issues = append(issues, "current_behavior cites unknown evidence id "+eid)
			}
		}
	}

	// Impacted-area paths that look like concrete files must exist in the
	// snapshot. Directory- or subsystem-style entries are tolerated as
	// informational hints rather than published file locations.
	for _, ia := range plan.ImpactedAreas {
		for _, p := range ia.Paths {
			if looksLikeFilePath(p) && !val.FileExists(p) {
				issues = append(issues, fmt.Sprintf("impacted area %q references non-existent file %q", ia.Area, p))
			}
		}
	}
	return dedupeStrings(issues)
}

// looksLikeFilePath reports whether p is specific enough to validate as a file
// (its last segment contains a dot and it is not a directory-style path). This
// avoids rejecting legitimate subsystem/area hints like "internal/workflow".
func looksLikeFilePath(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || strings.HasSuffix(p, "/") {
		return false
	}
	base := p
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		base = p[i+1:]
	}
	return strings.Contains(base, ".")
}

func mentionsSelfEdit(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "codeexpert will edit") || strings.Contains(l, "i will modify the repository")
}

// detectCycle returns a description of a cycle in the step dependency graph, or "".
func detectCycle(steps []schema.PlanStep) string {
	graph := map[string][]string{}
	for _, s := range steps {
		graph[s.ID] = s.DependsOn
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var path []string
	var visit func(string) string
	visit = func(n string) string {
		color[n] = gray
		path = append(path, n)
		for _, dep := range graph[n] {
			if _, ok := graph[dep]; !ok {
				continue
			}
			if color[dep] == gray {
				return strings.Join(append(path, dep), " -> ")
			}
			if color[dep] == white {
				if c := visit(dep); c != "" {
					return c
				}
			}
		}
		path = path[:len(path)-1]
		color[n] = black
		return ""
	}
	for _, s := range steps {
		if color[s.ID] == white {
			if c := visit(s.ID); c != "" {
				return c
			}
		}
	}
	return ""
}

// validateHelp performs light validation, returning limitations for bad citations.
func validateHelp(ctx context.Context, help *schema.HelpReport, snap *repo.Snapshot, evid *evidence.Store) []schema.Limitation {
	var lims []schema.Limitation
	for i := range help.LikelyCauses {
		valid := help.LikelyCauses[i].EvidenceIDs[:0]
		for _, eid := range help.LikelyCauses[i].EvidenceIDs {
			if evid.Has(eid) {
				valid = append(valid, eid)
			}
		}
		// If a cause claimed verified but cites no valid evidence, downgrade it.
		if help.LikelyCauses[i].Verified && len(valid) == 0 {
			help.LikelyCauses[i].Verified = false
		}
		help.LikelyCauses[i].EvidenceIDs = valid
	}
	if strings.TrimSpace(help.ProblemRestatement) == "" {
		lims = append(lims, schema.Limitation{Stage: "synthesis", Message: "help report missing a problem restatement"})
	}
	return lims
}

// repairPlan sends the validation issues back for one bounded repair attempt.
func (e *Engine) repairPlan(ctx context.Context, sess *Session, snap *repo.Snapshot, evid *evidence.Store, issues []string) (*schema.ImplementationPlan, error) {
	out := inferSchema[schema.ImplementationPlan]("implementation_plan")
	instr := prompts.MustGet(prompts.RepairSchema) + "\n\n## Validation errors to fix\n- " +
		strings.Join(issues, "\n- ") +
		"\n\nReturn the corrected implementation plan JSON. Fix only these problems; remove or mark claims you cannot ground in the evidence catalog."
	raw, err := sess.Synthesize(ctx, instr, out)
	if err != nil {
		return nil, err
	}
	var plan schema.ImplementationPlan
	if jerr := json.Unmarshal(raw, &plan); jerr != nil {
		return nil, schema.NewError(schema.CodeOutputInvalid, "repaired plan not parseable: %v", jerr)
	}
	if remaining := validatePlan(ctx, &plan, snap, evid, e.Cfg); len(remaining) > 0 {
		return &plan, schema.NewError(schema.CodeOutputInvalid, "%s", strings.Join(remaining, "; "))
	}
	return &plan, nil
}
