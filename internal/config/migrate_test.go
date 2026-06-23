package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTOML writes a config file into a temp dir and returns its path.
func writeTOML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestV1ConfigStillLoads proves a version-1 file loads, is migrated to a no-op,
// and validates — the backward-compatibility invariant.
func TestV1ConfigStillLoads(t *testing.T) {
	p := writeTOML(t, `
version = 1
[provider]
kind = "openai-compatible"
api = "responses"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"
[models]
scout = "fugu"
planner = "fugu"
reviewer = "fugu"
verifier = "fugu-ultra"
reasoning_scout = "high"
reasoning_planner = "high"
reasoning_verifier = "xhigh"
max_output_tokens = 16000
`)
	cfg, err := LoadFile(p)
	if err != nil {
		t.Fatalf("load v1: %v", err)
	}
	if err := Validate(&cfg); err != nil {
		t.Fatalf("v1 should validate: %v", err)
	}
	if cfg.Models.Scout != "fugu" || cfg.Models.Verifier != "fugu-ultra" {
		t.Errorf("v1 role models not preserved: %+v", cfg.Models)
	}
	if cfg.Provider.BaseURL != "https://api.sakana.ai/v1" {
		t.Errorf("v1 base_url not preserved: %q", cfg.Provider.BaseURL)
	}
}

// TestV1RoleModelsNotClobberedByDefaults guards the normalizeModels overwrite:
// a v1 file that customizes a role must not be reset by the default tiers.
func TestV1RoleModelsNotClobberedByDefaults(t *testing.T) {
	p := writeTOML(t, `
version = 1
[provider]
api = "responses"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"
[models]
scout = "custom-small"
verifier = "custom-large"
max_output_tokens = 8000
`)
	cfg, err := LoadFile(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Models.Scout != "custom-small" {
		t.Errorf("custom scout clobbered: %q", cfg.Models.Scout)
	}
	if cfg.Models.Verifier != "custom-large" {
		t.Errorf("custom verifier clobbered: %q", cfg.Models.Verifier)
	}
}

// TestV2ProfileCollapsesToProvider proves a version-2 profile selects the right
// provider + tiers, and that switching provider.active is the only change needed.
func TestV2ProfileCollapsesToProvider(t *testing.T) {
	body := `
version = 2
[provider]
active = "%s"
[providers.openai]
preset = "openai"
api = "responses"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
small_model = "small-oai"
large_model = "large-oai"
state_mode = "stateless"
[providers.sakana]
preset = "sakana"
api = "responses"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"
small_model = "fugu"
large_model = "fugu-ultra"
state_mode = "manual-replay"
[reasoning]
small = "medium"
large = "high"
`
	oai, err := LoadFile(writeTOML(t, strings.Replace(body, "%s", "openai", 1)))
	if err != nil {
		t.Fatalf("load openai: %v", err)
	}
	if err := Validate(&oai); err != nil {
		t.Fatalf("openai profile should validate: %v", err)
	}
	if oai.Provider.BaseURL != "https://api.openai.com/v1" || oai.Provider.APIKeyEnv != "OPENAI_API_KEY" {
		t.Errorf("openai provider not collapsed: %+v", oai.Provider)
	}
	if oai.Models.Scout != "small-oai" || oai.Models.Planner != "small-oai" || oai.Models.Reviewer != "small-oai" {
		t.Errorf("small tier not projected onto roles: %+v", oai.Models)
	}
	if oai.Models.Verifier != "large-oai" {
		t.Errorf("large tier not projected onto verifier: %q", oai.Models.Verifier)
	}
	if oai.Models.ReasoningScout != "medium" || oai.Models.ReasoningVerifier != "high" {
		t.Errorf("reasoning tiers not projected: scout=%q verifier=%q", oai.Models.ReasoningScout, oai.Models.ReasoningVerifier)
	}
	if oai.Provider.StateMode != "stateless" {
		t.Errorf("state_mode not collapsed: %q", oai.Provider.StateMode)
	}

	// Switching only provider.active flips everything to Sakana — config-only.
	sak, err := LoadFile(writeTOML(t, strings.Replace(body, "%s", "sakana", 1)))
	if err != nil {
		t.Fatalf("load sakana: %v", err)
	}
	if sak.Provider.BaseURL != "https://api.sakana.ai/v1" || sak.Models.Verifier != "fugu-ultra" {
		t.Errorf("active switch did not re-resolve provider/models: %+v / %+v", sak.Provider, sak.Models)
	}
}

func TestV2RoutingValidatedAndCarried(t *testing.T) {
	cfg, err := LoadFile(writeTOML(t, `
version = 2
[provider]
active = "sakana"
[providers.sakana]
api = "responses"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"
small_model = "fugu"
large_model = "fugu-ultra"
[routing]
exploration = "small"
plan_final = "large"
review_verify = "large"
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := Validate(&cfg); err != nil {
		t.Fatalf("routing should validate: %v", err)
	}
	if cfg.Routing.For("plan_final") != "large" || cfg.Routing.For("exploration") != "small" {
		t.Errorf("routing not carried: %+v", cfg.Routing)
	}
}

func TestValidateRejectsBadV2(t *testing.T) {
	cases := map[string]string{
		"unknown active profile": `
version = 2
[provider]
active = "missing"
[providers.sakana]
api = "responses"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"
small_model = "fugu"
large_model = "fugu-ultra"
`,
		"profile missing tiers": `
version = 2
[provider]
active = "x"
[providers.x]
api = "responses"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"
`,
		"profiles defined but no active": `
version = 2
[providers.openai]
api = "responses"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
small_model = "s"
large_model = "l"
`,
		"profile bad api": `
version = 2
[provider]
active = "x"
[providers.x]
api = "bogus"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"
`,
		"profile missing base_url": `
version = 2
[provider]
active = "x"
[providers.x]
api = "responses"
api_key_env = "SAKANA_API_KEY"
small_model = "m"
large_model = "n"
`,
		"bad routing tier": `
version = 2
[provider]
active = "x"
[providers.x]
api = "responses"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"
small_model = "m"
large_model = "n"
[routing]
plan_final = "huge"
`,
		"bad reasoning tier": `
version = 2
[provider]
active = "x"
[providers.x]
api = "responses"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"
small_model = "m"
large_model = "n"
[reasoning]
small = "ludicrous"
`,
		"bad state_mode": `
version = 2
[provider]
active = "x"
[providers.x]
api = "responses"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"
small_model = "m"
large_model = "n"
state_mode = "telepathic"
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, err := LoadFile(writeTOML(t, body))
			if err != nil {
				// A load error is also an acceptable rejection.
				return
			}
			if verr := Validate(&cfg); verr == nil {
				t.Errorf("expected validation rejection for %q", name)
			}
		})
	}
}

// TestV1IgnoresV2Keys proves that a version-1 config does not interpret v2-only
// inputs: stray small_model / [providers.*] / [routing] must be ignored, and the
// v1 role models preserved. This guards the precedence fix (a lower-precedence v2
// profile must never override a higher-precedence v1 file, since version wins).
func TestV1IgnoresV2Keys(t *testing.T) {
	cfg, err := LoadFile(writeTOML(t, `
version = 1
[provider]
api = "responses"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"
[providers.openai]
api = "responses"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
small_model = "gpt-x"
large_model = "gpt-y"
[models]
scout = "keep-small"
verifier = "keep-large"
small_model = "should-be-ignored"
[routing]
plan_final = "large"
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := Validate(&cfg); err != nil {
		t.Fatalf("v1 with stray v2 keys should validate: %v", err)
	}
	if cfg.Models.Scout != "keep-small" || cfg.Models.Verifier != "keep-large" {
		t.Errorf("v2 small_model clobbered v1 roles: scout=%q verifier=%q", cfg.Models.Scout, cfg.Models.Verifier)
	}
	if cfg.Provider.BaseURL != "https://api.sakana.ai/v1" {
		t.Errorf("v2 profile overrode v1 provider: %q", cfg.Provider.BaseURL)
	}
	if cfg.Routing.For("plan_final") != "" {
		t.Errorf("v1 config should not carry routing, got %q", cfg.Routing.For("plan_final"))
	}
	if cfg.Provider.Active != "" || cfg.Providers != nil {
		t.Errorf("v2-only inputs not cleared for v1: active=%q providers=%v", cfg.Provider.Active, cfg.Providers)
	}
}

// TestSecretsNeverSerialized proves the redacted String() view never contains an
// API key value, only the env-var name.
func TestSecretsNeverSerialized(t *testing.T) {
	t.Setenv("SAKANA_API_KEY", "sk-super-secret-value-1234")
	cfg, err := LoadFile(writeTOML(t, `
version = 2
[provider]
active = "sakana"
[providers.sakana]
api = "responses"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"
small_model = "fugu"
large_model = "fugu-ultra"
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Provider.APIKey != "sk-super-secret-value-1234" {
		t.Fatalf("expected key resolved from env for the test")
	}
	out := cfg.String()
	if strings.Contains(out, "sk-super-secret-value-1234") {
		t.Errorf("String() leaked the API key:\n%s", out)
	}
	if !strings.Contains(out, "SAKANA_API_KEY") {
		t.Errorf("String() should reference the env-var name")
	}
}

// TestMigrateRoundTripsAndValidates proves `config migrate` produces a valid,
// loadable v2 config that preserves provider details — including a loopback http
// base URL with its allow_insecure_http_localhost opt-in and custom role models.
func TestMigrateRoundTripsAndValidates(t *testing.T) {
	src, err := LoadFile(writeTOML(t, `
version = 1
[provider]
api = "responses"
base_url = "http://localhost:1234/v1"
api_key_env = "LOCAL_KEY"
allow_insecure_http_localhost = true
[models]
scout = "small-x"
verifier = "large-x"
max_output_tokens = 9000
[review]
max_total_findings = 5
`))
	if err != nil {
		t.Fatalf("load v1: %v", err)
	}
	out, err := RenderTOML(InferV2FromV1(src))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	reparsed, err := LoadFile(writeTOML(t, out))
	if err != nil {
		t.Fatalf("migrated config did not parse:\n%s\nerr: %v", out, err)
	}
	if verr := Validate(&reparsed); verr != nil {
		t.Fatalf("migrated config did not validate: %v\n%s", verr, out)
	}
	if reparsed.Version != 2 {
		t.Errorf("migrated version = %d", reparsed.Version)
	}
	if reparsed.Provider.BaseURL != "http://localhost:1234/v1" {
		t.Errorf("base_url not preserved: %q", reparsed.Provider.BaseURL)
	}
	if !reparsed.Provider.AllowInsecureHTTPLocalhost {
		t.Error("allow_insecure_http_localhost opt-in lost during migration")
	}
	if reparsed.Models.Scout != "small-x" || reparsed.Models.Verifier != "large-x" {
		t.Errorf("role models not preserved: %+v", reparsed.Models)
	}
	if reparsed.Review.MaxTotalFindings != 5 {
		t.Errorf("unrelated section dropped: review.max_total_findings = %d", reparsed.Review.MaxTotalFindings)
	}
}

// TestMigrateV2IsIdempotent proves migrating an already-version-2 config keeps
// every configured profile (not just the active one) and the active selection.
func TestMigrateV2IsIdempotent(t *testing.T) {
	src, err := LoadFile(writeTOML(t, `
version = 2
[provider]
active = "openai"
[providers.openai]
api = "responses"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
small_model = "o-small"
large_model = "o-large"
[providers.sakana]
api = "responses"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"
small_model = "fugu"
large_model = "fugu-ultra"
`))
	if err != nil {
		t.Fatalf("load v2: %v", err)
	}
	out, err := RenderTOML(InferV2FromV1(src))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	reparsed, err := LoadFile(writeTOML(t, out))
	if err != nil {
		t.Fatalf("re-migrated config did not parse:\n%s\nerr: %v", out, err)
	}
	if verr := Validate(&reparsed); verr != nil {
		t.Fatalf("re-migrated config did not validate: %v\n%s", verr, out)
	}
	if len(reparsed.Providers) != 2 {
		t.Errorf("migration dropped profiles: have %d, want 2", len(reparsed.Providers))
	}
	if reparsed.Provider.Active != "openai" || reparsed.Provider.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("active profile not preserved: active=%q base=%q", reparsed.Provider.Active, reparsed.Provider.BaseURL)
	}
}

// TestInferV2FromV1 exercises the `config migrate` projection.
func TestInferV2FromV1(t *testing.T) {
	in := Defaults()
	out := InferV2FromV1(in)
	if out.Version != 2 {
		t.Errorf("migrated version = %d, want 2", out.Version)
	}
	if out.Provider.Active != "sakana" {
		t.Errorf("active = %q, want sakana", out.Provider.Active)
	}
	p, ok := out.Providers["sakana"]
	if !ok {
		t.Fatal("expected a sakana profile")
	}
	if p.SmallModel != "fugu" || p.LargeModel != "fugu-ultra" {
		t.Errorf("inferred tiers wrong: %+v", p)
	}
	if p.APIKeyEnv != "SAKANA_API_KEY" {
		t.Errorf("api_key_env not carried: %q", p.APIKeyEnv)
	}
}
