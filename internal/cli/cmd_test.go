package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// loadWritten loads and validates a config file written by init.
func loadWritten(t *testing.T, dir string) config.Config {
	t.Helper()
	cfg, err := config.LoadFile(filepath.Join(dir, ".codeexpert.toml"))
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if err := config.Validate(&cfg); err != nil {
		t.Fatalf("written config should validate: %v", err)
	}
	return cfg
}

func TestInitOpenAIPreset(t *testing.T) {
	dir := t.TempDir()
	if code := cmdInit(context.Background(), []string{"--provider", "openai", "--project", dir}); code != exitOK {
		t.Fatalf("init exit code %d", code)
	}
	cfg := loadWritten(t, dir)
	if cfg.Version != 2 {
		t.Errorf("written version = %d, want 2", cfg.Version)
	}
	if cfg.Provider.Active != "openai" {
		t.Errorf("active = %q, want openai", cfg.Provider.Active)
	}
	if cfg.Provider.BaseURL != "https://api.openai.com/v1" || cfg.Provider.APIKeyEnv != "OPENAI_API_KEY" {
		t.Errorf("openai provider not resolved: %+v", cfg.Provider)
	}
	if cfg.Models.Scout != "gpt-5.4-mini" || cfg.Models.Verifier != "gpt-5.5" {
		t.Errorf("openai models not resolved: small=%q large=%q", cfg.Models.Scout, cfg.Models.Verifier)
	}
}

// TestInitEmitsConfigurableValueKeys proves the template surfaces the newly
// configurable operational knobs (issue #8) as raw text. LoadFile pre-fills
// defaults, so a struct-level check would pass even if the keys were missing;
// asserting on the file text guards against the template silently dropping them.
func TestInitEmitsConfigurableValueKeys(t *testing.T) {
	dir := t.TempDir()
	if code := cmdInit(context.Background(), []string{"--provider", "openai", "--project", dir}); code != exitOK {
		t.Fatalf("init exit code %d", code)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".codeexpert.toml"))
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"max_bytes_read", "max_symbol_enrich_files", "line_overlap_tolerance",
		"[profile_limits]", "max_model_calls_fast", "max_model_calls_balanced", "max_model_calls_deep",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("init template missing %q:\n%s", want, text)
		}
	}
}

func TestInitSakanaPreset(t *testing.T) {
	dir := t.TempDir()
	if code := cmdInit(context.Background(), []string{"--provider", "sakana", "--project", dir}); code != exitOK {
		t.Fatalf("init exit code %d", code)
	}
	cfg := loadWritten(t, dir)
	if cfg.Provider.BaseURL != "https://api.sakana.ai/v1" || cfg.Provider.APIKeyEnv != "SAKANA_API_KEY" {
		t.Errorf("sakana provider not resolved: %+v", cfg.Provider)
	}
	if cfg.Models.Scout != "fugu" || cfg.Models.Verifier != "fugu-ultra" {
		t.Errorf("sakana models not resolved: small=%q large=%q", cfg.Models.Scout, cfg.Models.Verifier)
	}
}

func TestInitFlagOverrides(t *testing.T) {
	dir := t.TempDir()
	code := cmdInit(context.Background(), []string{
		"--provider", "generic", "--project", dir,
		"--base-url", "https://example.com/v1", "--api-key-env", "EXAMPLE_KEY",
		"--small-model", "small-x", "--large-model", "large-x",
	})
	if code != exitOK {
		t.Fatalf("init exit code %d", code)
	}
	cfg := loadWritten(t, dir)
	if cfg.Provider.BaseURL != "https://example.com/v1" || cfg.Provider.APIKeyEnv != "EXAMPLE_KEY" {
		t.Errorf("flag overrides not applied: %+v", cfg.Provider)
	}
	if cfg.Models.Scout != "small-x" || cfg.Models.Verifier != "large-x" {
		t.Errorf("model overrides not applied: %+v", cfg.Models)
	}
}

func TestInitLocalhostHTTPValidates(t *testing.T) {
	dir := t.TempDir()
	code := cmdInit(context.Background(), []string{
		"--provider", "generic", "--project", dir,
		"--base-url", "http://localhost:11434/v1", "--api-key-env", "LOCAL_KEY",
		"--small-model", "s", "--large-model", "l",
	})
	if code != exitOK {
		t.Fatalf("init exit code %d", code)
	}
	cfg := loadWritten(t, dir) // loadWritten also asserts Validate passes
	if !cfg.Provider.AllowInsecureHTTPLocalhost {
		t.Error("init did not emit allow_insecure_http_localhost for a loopback http URL")
	}
	if cfg.Provider.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("base_url not written: %q", cfg.Provider.BaseURL)
	}
}

func TestInitInvalidProviderExits2(t *testing.T) {
	if code := cmdInit(context.Background(), []string{"--provider", "bogus", "--project", t.TempDir()}); code != exitInvalidArgs {
		t.Errorf("invalid provider: exit %d, want %d", code, exitInvalidArgs)
	}
}

func TestCLIEnumValidationExits2(t *testing.T) {
	cases := []struct {
		name string
		run  func() int
	}{
		{"plan bad profile", func() int {
			return cmdPlan(context.Background(), []string{"--profile", "bogus", "do it"}, schema.PlanModePlan)
		}},
		{"plan bad format", func() int {
			return cmdPlan(context.Background(), []string{"--format", "bogus", "do it"}, schema.PlanModePlan)
		}},
		{"review bad format", func() int { return cmdReview(context.Background(), []string{"--format", "bogus"}) }},
		{"review bad scope", func() int { return cmdReview(context.Background(), []string{"--scope", "bogus"}) }},
		{"review bad fail-on", func() int { return cmdReview(context.Background(), []string{"--fail-on", "bogus"}) }},
		{"review bad verification", func() int { return cmdReview(context.Background(), []string{"--verification", "bogus"}) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code := c.run(); code != exitInvalidArgs {
				t.Errorf("%s: exit %d, want %d", c.name, code, exitInvalidArgs)
			}
		})
	}
}
