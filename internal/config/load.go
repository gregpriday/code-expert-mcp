package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// LoadResult carries the resolved config plus provenance for diagnostics.
type LoadResult struct {
	Config       Config
	UserFile     string // path loaded, or "" if none
	ProjectFile  string
	SourcesNoted []string
}

// Load resolves configuration using the documented precedence:
// defaults < user file < project file < environment < (caller-applied flags).
// root may be empty; when set it is used to locate the project config file.
func Load(root string) (*LoadResult, error) {
	cfg := Defaults()
	res := &LoadResult{}

	// User config.
	if ucfg, err := userConfigPath(); err == nil && ucfg != "" {
		if fileExists(ucfg) {
			if _, err := toml.DecodeFile(ucfg, &cfg); err != nil {
				return nil, schema.NewError(schema.CodeConfigInvalid, "user config %s: %v", ucfg, err)
			}
			res.UserFile = ucfg
			res.SourcesNoted = append(res.SourcesNoted, "user:"+ucfg)
		}
	}

	// Project config.
	if root != "" {
		pcfg := filepath.Join(root, ".codeexpert.toml")
		if fileExists(pcfg) {
			if _, err := toml.DecodeFile(pcfg, &cfg); err != nil {
				return nil, schema.NewError(schema.CodeConfigInvalid, "project config %s: %v", pcfg, err)
			}
			res.ProjectFile = pcfg
			res.SourcesNoted = append(res.SourcesNoted, "project:"+pcfg)
		}
	}

	// Resolve version-2 inputs (profiles, tiers, reasoning) into the flat fields
	// the engine reads — gated on the effective version. Environment overrides are
	// applied last so they win over a profile and its tiers (see migrate.go).
	applyVersionedInputs(&cfg)
	applyEnvOverrides(&cfg)
	resolveSecrets(&cfg)

	res.Config = cfg
	return res, nil
}

// LoadFile loads a single explicit config file over defaults (used by tests and
// `config print --file`).
func LoadFile(path string) (Config, error) {
	cfg := Defaults()
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, schema.NewError(schema.CodeConfigInvalid, "config %s: %v", path, err)
	}
	applyVersionedInputs(&cfg)
	applyEnvOverrides(&cfg)
	resolveSecrets(&cfg)
	return cfg, nil
}

func resolveSecrets(cfg *Config) {
	if cfg.Provider.APIKeyEnv != "" {
		cfg.Provider.APIKey = os.Getenv(cfg.Provider.APIKeyEnv)
	}
	if cfg.Server.AuthTokenEnv != "" {
		cfg.Server.AuthToken = os.Getenv(cfg.Server.AuthTokenEnv)
	}
	if cfg.Embeddings.APIKeyEnv != "" {
		cfg.Embeddings.APIKey = os.Getenv(cfg.Embeddings.APIKeyEnv)
	}
}

// applyEnvOverrides applies the CODEEXPERT_* environment overrides that are most
// useful in deployment. Unknown variables are ignored.
func applyEnvOverrides(cfg *Config) {
	set := func(env string, fn func(string)) {
		if v, ok := os.LookupEnv(env); ok && v != "" {
			fn(v)
		}
	}
	set("CODEEXPERT_LOG_LEVEL", func(v string) { cfg.Server.LogLevel = v })
	set("CODEEXPERT_TRANSPORT", func(v string) { cfg.Server.Transport = v })
	set("CODEEXPERT_LISTEN", func(v string) { cfg.Server.Listen = v })
	set("CODEEXPERT_PROVIDER_BASE_URL", func(v string) { cfg.Provider.BaseURL = v })
	set("CODEEXPERT_PROVIDER_API", func(v string) { cfg.Provider.API = v })
	set("CODEEXPERT_PROVIDER_API_KEY_ENV", func(v string) { cfg.Provider.APIKeyEnv = v })
	set("CODEEXPERT_EMBEDDINGS_API_KEY_ENV", func(v string) { cfg.Embeddings.APIKeyEnv = v })
	set("CODEEXPERT_MODELS_SCOUT", func(v string) { cfg.Models.Scout = v })
	set("CODEEXPERT_MODELS_PLANNER", func(v string) { cfg.Models.Planner = v })
	set("CODEEXPERT_MODELS_REVIEWER", func(v string) { cfg.Models.Reviewer = v })
	set("CODEEXPERT_MODELS_VERIFIER", func(v string) { cfg.Models.Verifier = v })
	set("CODEEXPERT_CACHE_DIR", func(v string) { cfg.Cache.Dir = v })
	set("CODEEXPERT_CACHE_ENABLED", func(v string) {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Cache.Enabled = b
		}
	})
	set("CODEEXPERT_AUTH_TOKEN_ENV", func(v string) { cfg.Server.AuthTokenEnv = v })
}

func userConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "CodeExpert", "config.toml"), nil
}

// UserConfigPath is the public accessor used by `init --global`.
func UserConfigPath() (string, error) { return userConfigPath() }

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// String renders a redacted, human-readable view of the config for `config
// print`. Secrets are never included; only whether they are present.
func (c Config) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "version = %d\n\n", c.Version)
	fmt.Fprintf(&b, "[server]\ntransport = %q\nlisten = %q\nlog_level = %q\nmax_concurrent_runs = %d\nrun_retention = %q\n\n",
		c.Server.Transport, c.Server.Listen, c.Server.LogLevel, c.Server.MaxConcurrentRuns, c.Server.RunRetention.Std().String())
	apiKeyPresent := c.Provider.APIKey != ""
	b.WriteString("[provider]\n")
	if c.Provider.Active != "" {
		fmt.Fprintf(&b, "active = %q  # %d profile(s) defined; fields below are the resolved values\n", c.Provider.Active, len(c.Providers))
	}
	fmt.Fprintf(&b, "kind = %q\napi = %q\nbase_url = %q\napi_key_env = %q\napi_key_present = %v\nrequest_timeout = %q\nmax_retries = %d\n\n",
		c.Provider.Kind, c.Provider.API, c.Provider.BaseURL, c.Provider.APIKeyEnv, apiKeyPresent, c.Provider.RequestTimeout.Std().String(), c.Provider.MaxRetries)
	// scout/verifier are the resolved small/large tiers the engine uses.
	fmt.Fprintf(&b, "[models]\nsmall (scout) = %q\nlarge (verifier) = %q\nplanner = %q\nreviewer = %q\nmax_output_tokens = %d\n\n",
		c.Models.Scout, c.Models.Verifier, c.Models.Planner, c.Models.Reviewer, c.Models.MaxOutputTokens)
	fmt.Fprintf(&b, "[retrieval]\nlexical = %v\nsymbols = %v\nsummaries = %q\nembeddings = %v\ntrigram_filter = %v\ntrigram_max_per_file = %d\nmax_files_per_run = %d\nmax_context_tokens = %d\n\n",
		c.Retrieval.Lexical, c.Retrieval.Symbols, c.Retrieval.Summaries, c.Retrieval.Embeddings, c.Retrieval.TrigramFilter, c.Retrieval.TrigramMaxPerFile, c.Retrieval.MaxFilesPerRun, c.Retrieval.MaxContextTokens)
	embKeyPresent := c.Embeddings.APIKey != ""
	fmt.Fprintf(&b, "[embeddings]\nenabled = %v\nprovider = %q\nmodel = %q\nbase_url = %q\napi_key_env = %q\napi_key_present = %v\nchunk_tokens = %d\noverlap_tokens = %d\nmax_chunks = %d\n\n",
		c.Embeddings.Enabled, c.Embeddings.Provider, c.Embeddings.Model, c.Embeddings.BaseURL, c.Embeddings.APIKeyEnv, embKeyPresent, c.Embeddings.ChunkTokens, c.Embeddings.OverlapTokens, c.Embeddings.MaxChunks)
	fmt.Fprintf(&b, "[cache]\nenabled = %v\nmax_size_gb = %g\nttl = %q\n\n",
		c.Cache.Enabled, c.Cache.MaxSizeGB, c.Cache.TTL.Std().String())
	fmt.Fprintf(&b, "[review]\ndefault_profile = %q\npasses = %v\nmax_blocking_findings = %d\nmax_total_findings = %d\n\n",
		c.Review.DefaultProfile, c.Review.Passes, c.Review.MaxBlockingFindings, c.Review.MaxTotalFindings)
	fmt.Fprintf(&b, "[checks]\nmode = %q\nnetwork = %v\nconfigured_commands = %d\n",
		c.Checks.Mode, c.Checks.Network, len(c.Checks.Command))
	return b.String()
}
