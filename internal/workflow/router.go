package workflow

import (
	"strings"
	"time"

	"github.com/gregpriday/codeexpert/internal/budget"
	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// resolveProfile picks the effective profile, falling back to the configured
// default then "balanced".
func resolveProfile(requested schema.AnalysisProfile, configured string) schema.AnalysisProfile {
	if requested != "" {
		return requested
	}
	switch configured {
	case "fast", "balanced", "deep":
		return schema.AnalysisProfile(configured)
	}
	return schema.ProfileBalanced
}

// resolveLimits combines configuration, profile, and per-request budget into the
// enforced ceilings. Per-request overrides may only LOWER a limit, never raise it
// above the configured safety maximum: every override is clamped with clampDown.
// A retrieval override (MaxFileReads/MaxContextTokens) is treated identically to
// the equivalent budget field.
func resolveLimits(cfg config.Config, profile schema.AnalysisProfile, b schema.Budget, ret schema.RetrievalOpts) budget.Limits {
	l := budget.Limits{
		Timeout:            cfg.Provider.RequestTimeout.Std(),
		MaxModelToolRounds: cfg.Retrieval.MaxModelToolRounds,
		MaxInternalTools:   cfg.Retrieval.MaxModelToolCalls,
		MaxFilesRead:       cfg.Retrieval.MaxFileReads,
		MaxBytesRead:       cfg.Retrieval.MaxBytesRead,
		MaxContextTokens:   cfg.Retrieval.MaxContextTokens,
		MaxOutputTokens:    cfg.Models.MaxOutputTokens,
		MaxCheckSeconds:    checkSecondsCeiling(cfg.Checks.MaxTotalTime.Std()),
	}
	switch profile {
	case schema.ProfileFast:
		l.MaxModelCalls = cfg.ProfileLimits.MaxModelCallsFast
		l.MaxModelToolRounds = min(l.MaxModelToolRounds, 2)
	case schema.ProfileDeep:
		l.MaxModelCalls = cfg.ProfileLimits.MaxModelCallsDeep
	default: // balanced
		l.MaxModelCalls = cfg.ProfileLimits.MaxModelCallsBalanced
	}

	// A retrieval override folds into the equivalent budget field before clamping,
	// so the two never disagree; the budget field wins when both are set.
	if ret.MaxFileReads > 0 && b.MaxFilesRead == 0 {
		b.MaxFilesRead = ret.MaxFileReads
	}
	if ret.MaxContextTokens > 0 && b.MaxContextTokens == 0 {
		b.MaxContextTokens = ret.MaxContextTokens
	}

	if b.TimeoutSeconds > 0 {
		l.Timeout = clampDownDuration(time.Duration(b.TimeoutSeconds)*time.Second, l.Timeout)
	}
	if b.MaxModelCalls > 0 {
		l.MaxModelCalls = clampDown(b.MaxModelCalls, l.MaxModelCalls)
	}
	if b.MaxModelToolRounds > 0 {
		l.MaxModelToolRounds = clampDown(b.MaxModelToolRounds, l.MaxModelToolRounds)
	}
	if b.MaxInternalToolCalls > 0 {
		l.MaxInternalTools = clampDown(b.MaxInternalToolCalls, l.MaxInternalTools)
	}
	if b.MaxFilesRead > 0 {
		l.MaxFilesRead = clampDown(b.MaxFilesRead, l.MaxFilesRead)
	}
	if b.MaxBytesRead > 0 {
		l.MaxBytesRead = clampDown64(int64(b.MaxBytesRead), l.MaxBytesRead)
	}
	if b.MaxContextTokens > 0 {
		l.MaxContextTokens = clampDown(b.MaxContextTokens, l.MaxContextTokens)
	}
	if b.MaxOutputTokens > 0 {
		l.MaxOutputTokens = clampDown(b.MaxOutputTokens, l.MaxOutputTokens)
	}
	if b.MaxCheckSeconds > 0 {
		l.MaxCheckSeconds = clampDown(b.MaxCheckSeconds, l.MaxCheckSeconds)
	}
	return l
}

// checkSecondsCeiling converts a configured check time budget to whole seconds
// without truncating a positive sub-second budget to 0 (which clampDown would
// then read as "no ceiling" and let a per-request value exceed it).
func checkSecondsCeiling(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	secs := int(d / time.Second)
	if secs == 0 {
		secs = 1
	}
	return secs
}

// clampDown returns the requested value bounded by a configured maximum. A
// non-positive max means "no configured ceiling", so the request is honored as-is
// (callers without a configured limit get exactly what they ask for). A
// non-positive request is treated as "unset" and returns the max unchanged.
func clampDown(requested, max int) int {
	if requested <= 0 {
		return max
	}
	if max > 0 && requested > max {
		return max
	}
	return requested
}

func clampDown64(requested, max int64) int64 {
	if requested <= 0 {
		return max
	}
	if max > 0 && requested > max {
		return max
	}
	return requested
}

func clampDownDuration(requested, max time.Duration) time.Duration {
	if requested <= 0 {
		return max
	}
	if max > 0 && requested > max {
		return max
	}
	return requested
}

// synthesisModel selects the model and reasoning effort for final synthesis
// based on profile and a deterministic complexity score (0-100).
func (e *Engine) synthesisModel(profile schema.AnalysisProfile, complexity int, highRisk bool) (string, string) {
	m := e.Cfg.Models
	switch profile {
	case schema.ProfileFast:
		return m.Planner, m.ReasoningPlanner
	case schema.ProfileDeep:
		return m.Verifier, m.ReasoningVerifier
	default: // balanced: escalate to verifier for complex/high-risk work
		if complexity >= 60 || highRisk {
			return m.Verifier, m.ReasoningVerifier
		}
		return m.Planner, m.ReasoningPlanner
	}
}

// scoutModel returns the model/effort for exploration.
func (e *Engine) scoutModel() (string, string) {
	return e.Cfg.Models.Scout, e.Cfg.Models.ReasoningScout
}

// tierModel resolves a model tier ("small" | "large") to a concrete model and
// reasoning effort. It reads the resolved role fields (scout for small, verifier
// for large) rather than the raw small_model/large_model tier inputs, because the
// role fields are the post-everything values: config migration projects the tiers
// onto them and environment overrides (CODEEXPERT_MODELS_*) are applied last, so
// reading them here keeps routing consistent with those overrides.
func (e *Engine) tierModel(tier string) (string, string) {
	m := e.Cfg.Models
	if tier == "large" {
		return m.Verifier, m.ReasoningVerifier
	}
	return m.Scout, m.ReasoningScout
}

// routedScoutModel honors [routing].exploration when set, otherwise falls back
// to the role-based scout model.
func (e *Engine) routedScoutModel() (string, string) {
	if t := e.Cfg.Routing.Exploration; t != "" {
		return e.tierModel(t)
	}
	return e.scoutModel()
}

// routedSynthesisModel honors the [routing] tier for the named stage when set,
// otherwise falls back to the profile/complexity escalation in synthesisModel.
func (e *Engine) routedSynthesisModel(stage string, profile schema.AnalysisProfile, complexity int, highRisk bool) (string, string) {
	if t := e.Cfg.Routing.For(stage); t != "" {
		return e.tierModel(t)
	}
	return e.synthesisModel(profile, complexity, highRisk)
}

// reviewComplexity computes a deterministic 0-100 score and high-risk flag from
// a change manifest.
func reviewComplexity(m *repo.ChangeManifest) (int, bool) {
	if m == nil {
		return 0, false
	}
	score := 0
	score += min(len(m.Files)*6, 40)
	score += min((m.TotalAdded+m.TotalDeleted)/40, 25)

	langs := map[string]bool{}
	highRisk := false
	for _, f := range m.Files {
		if f.Language != "" {
			langs[f.Language] = true
		}
		if isHighRiskPath(f.Path) {
			highRisk = true
			score += 8
		}
		if f.Generated {
			score -= 2
		}
	}
	score += min(len(langs)*5, 15)
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score, highRisk
}

func isHighRiskPath(p string) bool {
	lower := strings.ToLower(p)
	for _, sig := range []string{"auth", "login", "password", "token", "crypto", "secret",
		"migrat", "schema", "payment", "billing", "session", "permission", "acl",
		"sql", "query", "deserial", "unmarshal", "exec", "concurren", "goroutine", "mutex", "lock"} {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}
