package config

import "testing"

// TestDefaultsConfigurableValues locks the built-in defaults for the operational
// values that were previously hardcoded in the engine, so they keep matching the
// historical behavior (issue #8).
func TestDefaultsConfigurableValues(t *testing.T) {
	c := Defaults()
	if c.ProfileLimits.MaxModelCallsFast != 3 ||
		c.ProfileLimits.MaxModelCallsBalanced != 7 ||
		c.ProfileLimits.MaxModelCallsDeep != 12 {
		t.Errorf("profile model-call defaults = %+v, want fast=3 balanced=7 deep=12", c.ProfileLimits)
	}
	if c.Retrieval.MaxBytesRead != 0 {
		t.Errorf("retrieval.max_bytes_read default = %d, want 0", c.Retrieval.MaxBytesRead)
	}
	if c.Review.MaxSymbolEnrichFiles != 100 {
		t.Errorf("review.max_symbol_enrich_files default = %d, want 100", c.Review.MaxSymbolEnrichFiles)
	}
	if c.Review.LineOverlapTolerance != 3 {
		t.Errorf("review.line_overlap_tolerance default = %d, want 3", c.Review.LineOverlapTolerance)
	}
	if err := Validate(&c); err != nil {
		t.Fatalf("defaults should validate: %v", err)
	}
}

// TestConfigurableValuesOverride proves the new fields decode from TOML over the
// defaults, while keys omitted from the file retain their default (the
// backward-compatibility invariant of the pre-fill load pattern).
func TestConfigurableValuesOverride(t *testing.T) {
	p := writeTOML(t, `
version = 2
[provider]
kind = "openai-compatible"
api = "responses"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"

[retrieval]
max_bytes_read = 4096

[review]
max_symbol_enrich_files = 5

[profile_limits]
max_model_calls_fast = 1
max_model_calls_deep = 99
`)
	cfg, err := LoadFile(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Retrieval.MaxBytesRead != 4096 {
		t.Errorf("max_bytes_read = %d, want 4096", cfg.Retrieval.MaxBytesRead)
	}
	if cfg.Review.MaxSymbolEnrichFiles != 5 {
		t.Errorf("max_symbol_enrich_files = %d, want 5", cfg.Review.MaxSymbolEnrichFiles)
	}
	if cfg.ProfileLimits.MaxModelCallsFast != 1 || cfg.ProfileLimits.MaxModelCallsDeep != 99 {
		t.Errorf("profile overrides = %+v, want fast=1 deep=99", cfg.ProfileLimits)
	}
	// Omitted keys keep their defaults.
	if cfg.ProfileLimits.MaxModelCallsBalanced != 7 {
		t.Errorf("omitted balanced limit = %d, want default 7", cfg.ProfileLimits.MaxModelCallsBalanced)
	}
	if cfg.Review.LineOverlapTolerance != 3 {
		t.Errorf("omitted line_overlap_tolerance = %d, want default 3", cfg.Review.LineOverlapTolerance)
	}
}

func TestValidateRejectsNegativeConfigurableValues(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"negative fast profile limit", func(c *Config) { c.ProfileLimits.MaxModelCallsFast = -1 }},
		{"negative balanced profile limit", func(c *Config) { c.ProfileLimits.MaxModelCallsBalanced = -1 }},
		{"negative deep profile limit", func(c *Config) { c.ProfileLimits.MaxModelCallsDeep = -1 }},
		{"negative max_bytes_read", func(c *Config) { c.Retrieval.MaxBytesRead = -1 }},
		{"negative max_symbol_enrich_files", func(c *Config) { c.Review.MaxSymbolEnrichFiles = -1 }},
		{"negative line_overlap_tolerance", func(c *Config) { c.Review.LineOverlapTolerance = -1 }},
		{"negative request_timeout", func(c *Config) { c.Provider.RequestTimeout = Duration(-1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Defaults()
			tc.mutate(&c)
			if err := Validate(&c); err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			}
		})
	}
}
