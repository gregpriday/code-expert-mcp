package config

import (
	"net"
	"net/url"
	"strings"

	"github.com/gregpriday/codeexpert/internal/schema"
)

// shellMetaChars are rejected in check command argv elements to prevent shell
// injection via configuration.
const shellMetaChars = "|&;<>$`(){}*?!#\n\r"

// Validate enforces the startup invariants from the specification. It returns a
// typed CE_CONFIG_* error on the first problem.
func Validate(c *Config) error {
	if c.Version != 1 && c.Version != 2 {
		return schema.NewError(schema.CodeConfigUnsupportedVersion,
			"config version %d is not supported (expected 1 or 2)", c.Version)
	}

	// Provider profiles (version 2). These are validated before the resolved
	// flat provider so a bad profile reports a precise [providers.NAME] message.
	if err := validateProfiles(c); err != nil {
		return err
	}

	// Resolved provider (collapsed from the active profile, or set directly in a
	// version-1 config).
	if err := validateBaseURL("provider", c.Provider.BaseURL, c.Provider.AllowInsecureHTTPLocalhost); err != nil {
		return err
	}
	if err := validateAPIDialect("provider.api", c.Provider.API); err != nil {
		return err
	}
	if c.Provider.APIKeyEnv == "" {
		return schema.NewError(schema.CodeConfigInvalid, "provider.api_key_env is required")
	}
	if err := validateStateMode("provider.state_mode", c.Provider.StateMode); err != nil {
		return err
	}

	// Models.
	for role, m := range map[string]string{
		"scout": c.Models.Scout, "planner": c.Models.Planner,
		"reviewer": c.Models.Reviewer, "verifier": c.Models.Verifier,
	} {
		if strings.TrimSpace(m) == "" {
			return schema.NewError(schema.CodeConfigInvalid, "models.%s must name a model", role)
		}
	}
	if c.Models.MaxOutputTokens <= 0 {
		return schema.NewError(schema.CodeConfigInvalid, "models.max_output_tokens must be positive")
	}

	// Reasoning effort (role fields and version-2 tiers).
	for field, v := range map[string]string{
		"models.reasoning_scout":    c.Models.ReasoningScout,
		"models.reasoning_planner":  c.Models.ReasoningPlanner,
		"models.reasoning_verifier": c.Models.ReasoningVerifier,
		"reasoning.small":           c.Reasoning.Small,
		"reasoning.large":           c.Reasoning.Large,
	} {
		if err := validateReasoning(field, v); err != nil {
			return err
		}
	}

	// Routing tiers (version 2): each configured stage must name a tier.
	for stage, tier := range map[string]string{
		"routing.exploration":       c.Routing.Exploration,
		"routing.query_expansion":   c.Routing.QueryExpansion,
		"routing.summary":           c.Routing.Summary,
		"routing.review_candidates": c.Routing.ReviewCandidates,
		"routing.plan_final":        c.Routing.PlanFinal,
		"routing.help_final":        c.Routing.HelpFinal,
		"routing.review_verify":     c.Routing.ReviewVerify,
		"routing.review_final":      c.Routing.ReviewFinal,
	} {
		if err := validateTier(stage, tier); err != nil {
			return err
		}
	}

	// Budgets / retrieval sanity.
	if c.Retrieval.MaxFilesPerRun < 0 || c.Retrieval.MaxFileReads < 0 ||
		c.Retrieval.MaxContextTokens < 0 || c.Retrieval.MaxModelToolRounds < 0 ||
		c.Retrieval.MaxBytesRead < 0 {
		return schema.NewError(schema.CodeConfigInvalid, "retrieval limits must not be negative")
	}
	if c.Server.MaxConcurrentRuns < 1 {
		return schema.NewError(schema.CodeConfigInvalid, "server.max_concurrent_runs must be at least 1")
	}

	// Per-profile model-call limits and review operational caps.
	if c.ProfileLimits.MaxModelCallsFast < 0 || c.ProfileLimits.MaxModelCallsBalanced < 0 ||
		c.ProfileLimits.MaxModelCallsDeep < 0 {
		return schema.NewError(schema.CodeConfigInvalid, "profile_limits model-call limits must not be negative")
	}
	if c.Review.MaxSymbolEnrichFiles < 0 || c.Review.LineOverlapTolerance < 0 {
		return schema.NewError(schema.CodeConfigInvalid, "review limits must not be negative")
	}

	// Profiles.
	for name, p := range map[string]string{"plan.default_profile": c.Plan.DefaultProfile, "review.default_profile": c.Review.DefaultProfile} {
		switch p {
		case "fast", "balanced", "deep", "":
		default:
			return schema.NewError(schema.CodeConfigInvalid, "%s %q must be fast, balanced, or deep", name, p)
		}
	}

	// Checks mode.
	switch c.Checks.Mode {
	case "off", "safe", "configured", "deep", "":
	default:
		return schema.NewError(schema.CodeConfigInvalid, "checks.mode %q must be off, safe, configured, or deep", c.Checks.Mode)
	}

	// Each check command must be an executable + argument array with no shell
	// metacharacters.
	names := map[string]bool{}
	for i, cmd := range c.Checks.Command {
		if cmd.Name == "" {
			return schema.NewError(schema.CodeConfigInvalid, "checks.command[%d].name is required", i)
		}
		if names[cmd.Name] {
			return schema.NewError(schema.CodeConfigInvalid, "duplicate check name %q", cmd.Name)
		}
		names[cmd.Name] = true
		if len(cmd.Argv) == 0 {
			return schema.NewError(schema.CodeConfigInvalid, "checks.command %q must define a non-empty argv array", cmd.Name)
		}
		for _, arg := range cmd.Argv {
			if strings.ContainsAny(arg, shellMetaChars) {
				return schema.NewError(schema.CodeConfigInvalid,
					"checks.command %q argument %q contains shell metacharacters; argv must be a literal argument array", cmd.Name, arg)
			}
		}
	}

	// Embeddings: if enabled, require a model.
	if c.Embeddings.Enabled && strings.TrimSpace(c.Embeddings.Model) == "" {
		return schema.NewError(schema.CodeConfigInvalid, "embeddings.enabled is true but embeddings.model is empty")
	}

	// Summaries mode.
	switch c.Retrieval.Summaries {
	case "off", "on-demand", "eager", "":
	default:
		return schema.NewError(schema.CodeConfigInvalid, "retrieval.summaries %q must be off, on-demand, or eager", c.Retrieval.Summaries)
	}

	return nil
}

// isLoopbackHost reports whether host is the loopback. It parses IPs so a remote
// hostname that merely starts with "127." (e.g. "127.example.com") is NOT treated
// as loopback.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// validateProfiles checks the version-2 [providers.NAME] table and that the
// active profile names one of them.
func validateProfiles(c *Config) error {
	for name, p := range c.Providers {
		if strings.TrimSpace(name) == "" {
			return schema.NewError(schema.CodeConfigInvalid, "a provider profile has an empty name")
		}
		label := "providers." + name
		if err := validateBaseURL(label, p.BaseURL, p.AllowInsecureHTTPLocalhost); err != nil {
			return err
		}
		if err := validateAPIDialect(label+".api", p.API); err != nil {
			return err
		}
		if p.APIKeyEnv == "" {
			return schema.NewError(schema.CodeConfigInvalid, "%s.api_key_env is required", label)
		}
		if strings.TrimSpace(p.SmallModel) == "" {
			return schema.NewError(schema.CodeConfigInvalid, "%s.small_model is required", label)
		}
		if strings.TrimSpace(p.LargeModel) == "" {
			return schema.NewError(schema.CodeConfigInvalid, "%s.large_model is required", label)
		}
		if err := validateStateMode(label+".state_mode", p.StateMode); err != nil {
			return err
		}
	}
	if len(c.Providers) > 0 && c.Provider.Active == "" {
		return schema.NewError(schema.CodeConfigInvalid,
			"provider.active is required when [providers.NAME] profiles are defined (or set CODEEXPERT_PROVIDER_PROFILE)")
	}
	if c.Provider.Active != "" {
		if _, ok := c.Providers[c.Provider.Active]; !ok {
			return schema.NewError(schema.CodeConfigInvalid,
				"provider.active %q does not match any [providers.NAME] profile", c.Provider.Active)
		}
	}
	return nil
}

// validateBaseURL enforces a well-formed URL and rejects insecure http unless
// explicitly permitted for a loopback host. label is the config path for errors.
func validateBaseURL(label, baseURL string, allowInsecure bool) error {
	if baseURL == "" {
		return schema.NewError(schema.CodeConfigInvalid, "%s.base_url is required", label)
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return schema.NewError(schema.CodeConfigInvalid, "%s.base_url %q is not a valid URL", label, baseURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return schema.NewError(schema.CodeConfigInvalid, "%s.base_url scheme %q must be http or https", label, u.Scheme)
	}
	if u.Scheme == "http" {
		// Insecure http is permitted only for a loopback host, and only with the
		// explicit opt-in. A remote http endpoint is rejected regardless of the
		// flag, so allow_insecure_http_localhost cannot widen the trust boundary.
		if !isLoopbackHost(u.Hostname()) {
			return schema.NewError(schema.CodeConfigInvalid,
				"%s.base_url uses insecure http for a non-loopback host; use https", label)
		}
		if !allowInsecure {
			return schema.NewError(schema.CodeConfigInvalid,
				"%s.base_url uses http on a loopback host; set allow_insecure_http_localhost = true to permit it", label)
		}
	}
	return nil
}

func validateAPIDialect(label, api string) error {
	switch api {
	case "responses", "chat-completions":
		return nil
	}
	return schema.NewError(schema.CodeConfigInvalid, "%s %q must be responses or chat-completions", label, api)
}

func validateStateMode(label, mode string) error {
	switch mode {
	case "", "stateless", "manual-replay", "stateful":
		return nil
	}
	return schema.NewError(schema.CodeConfigInvalid, "%s %q must be stateless, manual-replay, or stateful", label, mode)
}

func validateReasoning(label, effort string) error {
	switch effort {
	case "", "low", "medium", "high", "xhigh":
		return nil
	}
	return schema.NewError(schema.CodeConfigInvalid, "%s %q must be low, medium, high, or xhigh", label, effort)
}

func validateTier(label, tier string) error {
	switch tier {
	case "", "small", "large":
		return nil
	}
	return schema.NewError(schema.CodeConfigInvalid, "%s %q must be small or large", label, tier)
}
