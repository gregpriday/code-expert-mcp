package workflow

import (
	"testing"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/schema"
)

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
