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
// enforced ceilings. Request values win but are clamped to safety maxima.
func resolveLimits(cfg config.Config, profile schema.AnalysisProfile, b schema.Budget) budget.Limits {
	l := budget.Limits{
		Timeout:            cfg.Provider.RequestTimeout.Std(),
		MaxModelToolRounds: cfg.Retrieval.MaxModelToolRounds,
		MaxInternalTools:   cfg.Retrieval.MaxModelToolCalls,
		MaxFilesRead:       cfg.Retrieval.MaxFileReads,
		MaxBytesRead:       cfg.Retrieval.MaxBytesRead,
		MaxContextTokens:   cfg.Retrieval.MaxContextTokens,
		MaxOutputTokens:    cfg.Models.MaxOutputTokens,
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

	if b.TimeoutSeconds > 0 {
		l.Timeout = time.Duration(b.TimeoutSeconds) * time.Second
	}
	if b.MaxModelCalls > 0 {
		l.MaxModelCalls = b.MaxModelCalls
	}
	if b.MaxModelToolRounds > 0 {
		l.MaxModelToolRounds = b.MaxModelToolRounds
	}
	if b.MaxInternalToolCalls > 0 {
		l.MaxInternalTools = b.MaxInternalToolCalls
	}
	if b.MaxFilesRead > 0 {
		l.MaxFilesRead = b.MaxFilesRead
	}
	if b.MaxBytesRead > 0 {
		l.MaxBytesRead = int64(b.MaxBytesRead)
	}
	if b.MaxContextTokens > 0 {
		l.MaxContextTokens = b.MaxContextTokens
	}
	if b.MaxOutputTokens > 0 {
		l.MaxOutputTokens = b.MaxOutputTokens
	}
	return l
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
