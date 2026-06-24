package workflow

import (
	"testing"
	"time"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// TestResolveLimitsFromConfig proves the per-profile model-call ceilings, the run
// timeout, and the byte-read baseline come from config (issue #8) and that a
// per-request budget still overrides them.
func TestResolveLimitsFromConfig(t *testing.T) {
	cfg := config.Defaults()

	// Defaults map onto the historical 3/7/12 ceilings.
	for _, tc := range []struct {
		profile schema.AnalysisProfile
		want    int
	}{
		{schema.ProfileFast, 3},
		{schema.ProfileBalanced, 7},
		{schema.ProfileDeep, 12},
	} {
		if got := resolveLimits(cfg, tc.profile, schema.Budget{}, schema.RetrievalOpts{}).MaxModelCalls; got != tc.want {
			t.Errorf("%s default MaxModelCalls = %d, want %d", tc.profile, got, tc.want)
		}
	}

	// Timeout is wired from the provider request timeout (default and non-default).
	if got := resolveLimits(cfg, schema.ProfileBalanced, schema.Budget{}, schema.RetrievalOpts{}).Timeout; got != 30*time.Minute {
		t.Errorf("Timeout = %v, want 30m (from provider.request_timeout)", got)
	}
	cfg.Provider.RequestTimeout = config.Duration(45 * time.Second)
	if got := resolveLimits(cfg, schema.ProfileBalanced, schema.Budget{}, schema.RetrievalOpts{}).Timeout; got != 45*time.Second {
		t.Errorf("Timeout = %v, want 45s (from non-default provider.request_timeout)", got)
	}

	// All three profile ceilings are independently configurable.
	cfg.ProfileLimits = config.ProfileLimitsConfig{MaxModelCallsFast: 4, MaxModelCallsBalanced: 8, MaxModelCallsDeep: 13}
	for _, tc := range []struct {
		profile schema.AnalysisProfile
		want    int
	}{
		{schema.ProfileFast, 4},
		{schema.ProfileBalanced, 8},
		{schema.ProfileDeep, 13},
	} {
		if got := resolveLimits(cfg, tc.profile, schema.Budget{}, schema.RetrievalOpts{}).MaxModelCalls; got != tc.want {
			t.Errorf("%s configured MaxModelCalls = %d, want %d", tc.profile, got, tc.want)
		}
	}

	// Configured byte-read baseline is honored.
	cfg.Retrieval.MaxBytesRead = 8192
	l := resolveLimits(cfg, schema.ProfileDeep, schema.Budget{}, schema.RetrievalOpts{})
	if l.MaxModelCalls != 13 {
		t.Errorf("configured deep MaxModelCalls = %d, want 13", l.MaxModelCalls)
	}
	if l.MaxBytesRead != 8192 {
		t.Errorf("MaxBytesRead baseline = %d, want 8192", l.MaxBytesRead)
	}

	// A per-request budget still wins over the config baseline.
	l = resolveLimits(cfg, schema.ProfileDeep, schema.Budget{MaxModelCalls: 2, MaxBytesRead: 4096}, schema.RetrievalOpts{})
	if l.MaxModelCalls != 2 || l.MaxBytesRead != 4096 {
		t.Errorf("request budget override = (%d,%d), want (2,4096)", l.MaxModelCalls, l.MaxBytesRead)
	}
}

// TestResolveLimitsClampsDownOnly proves a per-request budget can only LOWER a
// limit, never raise it above the configured safety maximum (the core fix for the
// router accepting limit-raising overrides).
func TestResolveLimitsClampsDownOnly(t *testing.T) {
	cfg := config.Defaults() // deep ceiling = 12 model calls, request timeout = 30m

	// Asking for MORE than the configured ceiling is clamped down.
	l := resolveLimits(cfg, schema.ProfileDeep, schema.Budget{MaxModelCalls: 9999}, schema.RetrievalOpts{})
	if l.MaxModelCalls != 12 {
		t.Errorf("MaxModelCalls = %d, want clamped to 12", l.MaxModelCalls)
	}
	// Asking for LESS is honored.
	l = resolveLimits(cfg, schema.ProfileDeep, schema.Budget{MaxModelCalls: 2}, schema.RetrievalOpts{})
	if l.MaxModelCalls != 2 {
		t.Errorf("MaxModelCalls = %d, want 2 (lowered)", l.MaxModelCalls)
	}
	// Timeout is clamped to the configured request timeout.
	l = resolveLimits(cfg, schema.ProfileBalanced, schema.Budget{TimeoutSeconds: 99 * 3600}, schema.RetrievalOpts{})
	if l.Timeout != 30*time.Minute {
		t.Errorf("Timeout = %v, want clamped to 30m", l.Timeout)
	}
	// A configured byte ceiling clamps an over-large request.
	cfg.Retrieval.MaxBytesRead = 1000
	l = resolveLimits(cfg, schema.ProfileBalanced, schema.Budget{MaxBytesRead: 5000}, schema.RetrievalOpts{})
	if l.MaxBytesRead != 1000 {
		t.Errorf("MaxBytesRead = %d, want clamped to 1000", l.MaxBytesRead)
	}
	// A retrieval override folds into the equivalent budget field and is clamped.
	cfg.Retrieval.MaxFileReads = 80
	l = resolveLimits(cfg, schema.ProfileBalanced, schema.Budget{}, schema.RetrievalOpts{MaxFileReads: 9999})
	if l.MaxFilesRead != 80 {
		t.Errorf("MaxFilesRead via retrieval = %d, want clamped to 80", l.MaxFilesRead)
	}
}

// TestRoutingSelectsTier proves [routing] drives per-stage model selection,
// overriding the profile/complexity escalation, and that an unset stage falls
// back to the legacy behavior.
func TestRoutingSelectsTier(t *testing.T) {
	cfg := config.Defaults()
	cfg.Models.SmallModel = "small-m"
	cfg.Models.LargeModel = "large-m"
	cfg.Models.Scout = "small-m"
	cfg.Models.Verifier = "large-m"
	cfg.Models.ReasoningScout = "medium"
	cfg.Models.ReasoningVerifier = "high"
	cfg.Routing = config.RoutingConfig{
		Exploration:  "small",
		PlanFinal:    "large",
		ReviewVerify: "large",
	}
	e := &Engine{Cfg: cfg}

	if m, eff := e.routedScoutModel(); m != "small-m" || eff != "medium" {
		t.Errorf("exploration routing: got (%q,%q), want (small-m,medium)", m, eff)
	}
	// plan_final routed to large regardless of low complexity / fast profile.
	if m, _ := e.routedSynthesisModel("plan_final", schema.ProfileFast, 0, false); m != "large-m" {
		t.Errorf("plan_final routing: got %q, want large-m", m)
	}
	if m, _ := e.routedSynthesisModel("review_verify", schema.ProfileBalanced, 0, false); m != "large-m" {
		t.Errorf("review_verify routing: got %q, want large-m", m)
	}
	// help_final has no routing rule -> legacy synthesisModel (balanced, simple -> planner).
	cfg2 := cfg
	cfg2.Routing = config.RoutingConfig{}
	cfg2.Models.Planner = "legacy-planner"
	e2 := &Engine{Cfg: cfg2}
	if m, _ := e2.routedSynthesisModel("help_final", schema.ProfileBalanced, 0, false); m != "legacy-planner" {
		t.Errorf("unset routing should fall back to planner, got %q", m)
	}
}
