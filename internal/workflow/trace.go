package workflow

import (
	"encoding/json"

	"github.com/gregpriday/codeexpert/internal/prompts"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// promptVersions returns the content-hash version of each prompt id, so a trace
// records exactly which prompt revisions produced a run (a one-mechanism-at-a-time
// change shows up as a changed hash).
func promptVersions(ids ...string) map[string]string {
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		out[id] = prompts.Hash(id)
	}
	return out
}

// stagesFromLimitations turns a fixed pipeline order plus the recorded
// limitations into per-stage terminal states, so coverage of the run itself is
// reported rather than assumed. A stage named by a limitation is "partial".
func stagesFromLimitations(order []string, lims []schema.Limitation, overall schema.RunStatus) []schema.StageTrace {
	byStage := map[string]string{}
	for _, l := range lims {
		if l.Stage != "" {
			byStage[l.Stage] = l.Message
		}
	}
	var out []schema.StageTrace
	for _, name := range order {
		st := schema.StageTrace{Name: name, Status: "complete"}
		if detail, ok := byStage[name]; ok {
			st.Status = "partial"
			st.Detail = detail
		}
		out = append(out, st)
	}
	return out
}

// attachPlanTrace builds the run trace, persists it as a trace.json artifact
// (always, for the improvement loop), and attaches it to the result only when the
// caller asked for it via output.include_trace.
func (e *Engine) attachPlanTrace(res *schema.PlanResult, profile schema.AnalysisProfile, promptIDs []string, includeTrace bool) {
	tr := buildPlanTrace(*res, profile, promptIDs)
	if b, err := json.MarshalIndent(tr, "", "  "); err == nil {
		e.persistArtifact(res.RunID, "trace.json", b)
	}
	if includeTrace {
		res.Trace = tr
	}
}

// attachReviewTrace mirrors attachPlanTrace for a review result.
func (e *Engine) attachReviewTrace(res *schema.ReviewResult, profile schema.AnalysisProfile, evidenceCount int, promptIDs []string, includeTrace bool) {
	tr := buildReviewTrace(*res, profile, evidenceCount, promptIDs)
	if b, err := json.MarshalIndent(tr, "", "  "); err == nil {
		e.persistArtifact(res.RunID, "trace.json", b)
	}
	if includeTrace {
		res.Trace = tr
	}
}

// buildPlanTrace assembles the run trace for a plan/help result.
func buildPlanTrace(res schema.PlanResult, profile schema.AnalysisProfile, promptIDs []string) *schema.RunTrace {
	t := &schema.RunTrace{
		RunID:          res.RunID,
		Capability:     string(res.Kind),
		AnswerType:     res.Request.AnswerType,
		Profile:        string(profile),
		Status:         res.Status,
		ModelsUsed:     res.Usage.ModelsUsed,
		PromptVersions: promptVersions(promptIDs...),
		Stages:         stagesFromLimitations([]string{"snapshot", "exploration", "synthesis", "validation"}, res.Limitations, res.Status),
		EvidenceCount:  len(res.Evidence),
		Repaired:       hasStageLimitation(res.Limitations, "validation"),
		LimitationN:    len(res.Limitations),
		Usage:          res.Usage,
	}
	return t
}

// buildReviewTrace assembles the run trace for a review result.
func buildReviewTrace(res schema.ReviewResult, profile schema.AnalysisProfile, evidenceCount int, promptIDs []string) *schema.RunTrace {
	return &schema.RunTrace{
		RunID:          res.RunID,
		Capability:     "review",
		Profile:        string(profile),
		Status:         res.Status,
		ModelsUsed:     res.Usage.ModelsUsed,
		PromptVersions: promptVersions(promptIDs...),
		Stages:         stagesFromLimitations([]string{"snapshot", "risk-map", "candidates", "verification", "synthesis"}, res.Limitations, res.Status),
		EvidenceCount:  evidenceCount,
		LimitationN:    len(res.Limitations),
		Usage:          res.Usage,
	}
}
