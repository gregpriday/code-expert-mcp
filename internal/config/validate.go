package config

import (
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
	if c.Version != SupportedVersion {
		return schema.NewError(schema.CodeConfigUnsupportedVersion,
			"config version %d is not supported (expected %d)", c.Version, SupportedVersion)
	}

	// Provider.
	if c.Provider.BaseURL == "" {
		return schema.NewError(schema.CodeConfigInvalid, "provider.base_url is required")
	}
	u, err := url.Parse(c.Provider.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return schema.NewError(schema.CodeConfigInvalid, "provider.base_url %q is not a valid URL", c.Provider.BaseURL)
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) && !c.Provider.AllowInsecureHTTPLocalhost {
		return schema.NewError(schema.CodeConfigInvalid,
			"provider.base_url uses insecure http for a non-local host; set allow_insecure_http_localhost only for localhost")
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) && !c.Provider.AllowInsecureHTTPLocalhost {
		return schema.NewError(schema.CodeConfigInvalid,
			"provider.base_url uses http on localhost; set provider.allow_insecure_http_localhost = true to permit it")
	}
	switch c.Provider.API {
	case "responses", "chat-completions":
	default:
		return schema.NewError(schema.CodeConfigInvalid, "provider.api %q must be responses or chat-completions", c.Provider.API)
	}
	if c.Provider.APIKeyEnv == "" {
		return schema.NewError(schema.CodeConfigInvalid, "provider.api_key_env is required")
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

	// Budgets / retrieval sanity.
	if c.Retrieval.MaxFilesPerRun < 0 || c.Retrieval.MaxFileReads < 0 ||
		c.Retrieval.MaxContextTokens < 0 || c.Retrieval.MaxModelToolRounds < 0 {
		return schema.NewError(schema.CodeConfigInvalid, "retrieval limits must not be negative")
	}
	if c.Server.MaxConcurrentRuns < 1 {
		return schema.NewError(schema.CodeConfigInvalid, "server.max_concurrent_runs must be at least 1")
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

func isLoopbackHost(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return strings.HasPrefix(host, "127.")
}
