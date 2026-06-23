package config

import (
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// applyVersionedInputs resolves the version-2 inputs (provider profiles, model
// tiers, per-tier reasoning) into the flat Provider/Models fields the engine
// reads — but only when the effective config is version 2. Because `version` is a
// scalar that the highest-precedence file sets, a version-1 file layered over a
// version-2 file (or vice versa) is interpreted by the *winning* file's version:
// a version-1 result ignores all v2-only inputs entirely, so a v1 project config
// cannot be silently overridden by a v2 user profile, and stray v2 keys in a v1
// file are not interpreted.
//
// For version 2 the resolution order is:
//
//	selectActiveProfile(cfg)   // copy the active [providers.NAME] profile in
//	normalizeModels(cfg)        // project tiers + [reasoning] onto role fields
//
// The caller applies environment overrides (CODEEXPERT_*) AFTER this so they win
// over a profile and its tiers. Secrets are never touched here — only the
// api_key_env *name* is propagated.
func applyVersionedInputs(cfg *Config) {
	if cfg.Version < 2 {
		// Pure version-1: ignore every v2-only input so behavior matches the
		// original v1 semantics regardless of inherited lower-precedence v2 data.
		cfg.Provider.Active = ""
		cfg.Providers = nil
		cfg.Routing = RoutingConfig{}
		cfg.Reasoning = ReasoningConfig{}
		cfg.Models.SmallModel = ""
		cfg.Models.LargeModel = ""
		return
	}
	selectActiveProfile(cfg)
	normalizeModels(cfg)
}

// selectActiveProfile copies the active [providers.NAME] profile into the flat
// Provider/Models fields. The active profile may be overridden by the
// CODEEXPERT_PROVIDER_PROFILE environment variable.
func selectActiveProfile(cfg *Config) {
	if v := strings.TrimSpace(os.Getenv("CODEEXPERT_PROVIDER_PROFILE")); v != "" {
		cfg.Provider.Active = v
	}
	if cfg.Provider.Active == "" {
		return
	}
	p, ok := cfg.Providers[cfg.Provider.Active]
	if !ok {
		// Unknown active profile: leave the flat fields as-is. Validate reports
		// the error with a precise message.
		return
	}
	if p.Kind != "" {
		cfg.Provider.Kind = p.Kind
	} else if cfg.Provider.Kind == "" {
		cfg.Provider.Kind = "openai-compatible"
	}
	if p.API != "" {
		cfg.Provider.API = p.API
	}
	if p.BaseURL != "" {
		cfg.Provider.BaseURL = p.BaseURL
	}
	if p.APIKeyEnv != "" {
		cfg.Provider.APIKeyEnv = p.APIKeyEnv
	}
	if p.StateMode != "" {
		cfg.Provider.StateMode = p.StateMode
	}
	if p.ConnectTimeout != 0 {
		cfg.Provider.ConnectTimeout = p.ConnectTimeout
	}
	if p.RequestTimeout != 0 {
		cfg.Provider.RequestTimeout = p.RequestTimeout
	}
	if p.StreamIdleTimeout != 0 {
		cfg.Provider.StreamIdleTimeout = p.StreamIdleTimeout
	}
	if p.MaxRetries != 0 {
		cfg.Provider.MaxRetries = p.MaxRetries
	}
	if p.AllowInsecureHTTPLocalhost {
		cfg.Provider.AllowInsecureHTTPLocalhost = true
	}
	if p.SmallModel != "" {
		cfg.Models.SmallModel = p.SmallModel
	}
	if p.LargeModel != "" {
		cfg.Models.LargeModel = p.LargeModel
	}
}

// normalizeModels projects the version-2 model tiers and [reasoning] onto the
// role-based fields the engine reads. The projection is authoritative: when a
// tier is set it overwrites the corresponding role fields (the small tier feeds
// scout/planner/reviewer, the large tier feeds verifier). This must run after
// selectActiveProfile and before role-level environment overrides
// (CODEEXPERT_MODELS_*), which are applied last and therefore win.
//
// A version-1 config sets no tiers and no [reasoning], so this is a no-op and the
// role/reasoning values decoded from the file are preserved.
func normalizeModels(cfg *Config) {
	if cfg.Models.SmallModel != "" {
		cfg.Models.Scout = cfg.Models.SmallModel
		cfg.Models.Planner = cfg.Models.SmallModel
		cfg.Models.Reviewer = cfg.Models.SmallModel
	}
	if cfg.Models.LargeModel != "" {
		cfg.Models.Verifier = cfg.Models.LargeModel
	}
	if eff := strings.TrimSpace(cfg.Reasoning.Small); eff != "" {
		cfg.Models.ReasoningScout = eff
		cfg.Models.ReasoningPlanner = eff
	}
	if eff := strings.TrimSpace(cfg.Reasoning.Large); eff != "" {
		cfg.Models.ReasoningVerifier = eff
	}
}

// InferV2FromV1 renders a version-2 view of a (possibly version-1) config for the
// `config migrate` command. It maps small<-scout, large<-verifier, builds a
// single provider profile from the flat provider fields, and derives [routing]
// and [reasoning] from the role fields. It never reads or writes API keys; only
// the api_key_env name is carried. The returned config is suitable for
// re-serialization but its flat fields are left intact so it still validates.
func InferV2FromV1(in Config) Config {
	out := in
	out.Version = 2

	name := profileNameFor(in.Provider.BaseURL)
	out.Provider.Active = name
	out.Providers = map[string]ProviderProfile{
		name: {
			Preset:                     name,
			Kind:                       firstNonEmpty(in.Provider.Kind, "openai-compatible"),
			API:                        in.Provider.API,
			BaseURL:                    in.Provider.BaseURL,
			APIKeyEnv:                  in.Provider.APIKeyEnv,
			SmallModel:                 firstNonEmpty(in.Models.SmallModel, in.Models.Scout),
			LargeModel:                 firstNonEmpty(in.Models.LargeModel, in.Models.Verifier),
			StateMode:                  in.Provider.StateMode,
			ConnectTimeout:             in.Provider.ConnectTimeout,
			RequestTimeout:             in.Provider.RequestTimeout,
			StreamIdleTimeout:          in.Provider.StreamIdleTimeout,
			MaxRetries:                 in.Provider.MaxRetries,
			AllowInsecureHTTPLocalhost: in.Provider.AllowInsecureHTTPLocalhost,
		},
	}
	out.Models.SmallModel = firstNonEmpty(in.Models.SmallModel, in.Models.Scout)
	out.Models.LargeModel = firstNonEmpty(in.Models.LargeModel, in.Models.Verifier)
	out.Reasoning = ReasoningConfig{
		Small: firstNonEmpty(in.Reasoning.Small, in.Models.ReasoningScout),
		Large: firstNonEmpty(in.Reasoning.Large, in.Models.ReasoningVerifier),
	}

	// Make the profile authoritative by clearing the now-redundant flat provider
	// and role-model fields. Reloading re-derives them from the profile + tiers, so
	// the migrated file stays valid (including any allow_insecure_http_localhost or
	// custom timeouts, which are carried into the profile above) while avoiding a
	// confusing dual representation. Every other section (server, cache, review,
	// checks, …) is preserved verbatim from the input.
	out.Provider.Kind = ""
	out.Provider.API = ""
	out.Provider.BaseURL = ""
	out.Provider.APIKeyEnv = ""
	out.Provider.StateMode = ""
	out.Provider.ConnectTimeout = 0
	out.Provider.RequestTimeout = 0
	out.Provider.StreamIdleTimeout = 0
	out.Provider.MaxRetries = 0
	out.Provider.AllowInsecureHTTPLocalhost = false
	out.Models.Scout = ""
	out.Models.Planner = ""
	out.Models.Reviewer = ""
	out.Models.Verifier = ""
	out.Models.ReasoningScout = ""
	out.Models.ReasoningPlanner = ""
	out.Models.ReasoningVerifier = ""
	return out
}

// RenderTOML serializes a config to TOML for `config migrate`. Secrets are never
// emitted: APIKey and AuthToken carry the `toml:"-"` tag and are skipped.
func RenderTOML(cfg Config) (string, error) {
	var b strings.Builder
	if err := toml.NewEncoder(&b).Encode(cfg); err != nil {
		return "", schema.NewError(schema.CodeInternal, "encode config: %v", err)
	}
	return b.String(), nil
}

// profileNameFor derives a stable profile name from a base URL host.
func profileNameFor(baseURL string) string {
	switch {
	case strings.Contains(baseURL, "openai.com"):
		return "openai"
	case strings.Contains(baseURL, "sakana"):
		return "sakana"
	default:
		return "generic"
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
