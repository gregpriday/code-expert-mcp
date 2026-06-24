// Package config resolves CodeExpert configuration from built-in defaults, user
// and project TOML files, environment variables, and CLI/MCP arguments, in that
// precedence order. Secrets are never serialized: the config records only the
// name of the environment variable holding a key, and the resolved value lives
// in runtime-only fields (see APIKey/AuthToken).
package config

// SupportedVersion is the newest config schema version this build understands.
// Version 1 (flat [provider] + role-based [models]) is still accepted and is
// migrated in memory to the version-2 representation; see migrate.go.
const SupportedVersion = 2

// Config is the fully-resolved configuration for a process.
//
// Version 2 introduces named provider profiles ([providers.NAME] selected by
// [provider].active), two model tiers (small_model/large_model), per-stage
// [routing], and per-tier [reasoning]. These are *inputs*: Load collapses them
// into the flat Provider/Models fields, which remain the single source of truth
// the engine reads. A version-1 file leaves the v2 fields empty and is used
// directly.
type Config struct {
	Version    int              `toml:"version"`
	Server     ServerConfig     `toml:"server"`
	Provider   ProviderConfig   `toml:"provider"`
	Models     ModelsConfig     `toml:"models"`
	Repository RepositoryConfig `toml:"repository"`
	Retrieval  RetrievalConfig  `toml:"retrieval"`
	Embeddings EmbeddingsConfig `toml:"embeddings"`
	Cache      CacheConfig      `toml:"cache"`
	Plan       PlanConfig       `toml:"plan"`
	Review     ReviewConfig     `toml:"review"`
	Checks     ChecksConfig     `toml:"checks"`
	Grounding  GroundingConfig  `toml:"grounding"`

	// ProfileLimits sets the per-profile model-call ceilings the engine enforces.
	ProfileLimits ProfileLimitsConfig `toml:"profile_limits"`

	// Version-2 inputs (collapsed into Provider/Models by Load).
	Providers map[string]ProviderProfile `toml:"providers"`
	Routing   RoutingConfig              `toml:"routing"`
	Reasoning ReasoningConfig            `toml:"reasoning"`
}

// ServerConfig controls the MCP/HTTP server runtime.
type ServerConfig struct {
	Transport         string   `toml:"transport"`
	Listen            string   `toml:"listen"`
	LogLevel          string   `toml:"log_level"`
	LogJSON           bool     `toml:"log_json"`
	MaxConcurrentRuns int      `toml:"max_concurrent_runs"`
	RunRetention      Duration `toml:"run_retention"`
	MaxRequestBytes   int64    `toml:"max_request_bytes"`
	// AllowedOrigins restricts Streamable HTTP origins. Empty means loopback only.
	AllowedOrigins []string `toml:"allowed_origins"`
	AuthToken      string   `toml:"-"` // populated from env, never serialized
	AuthTokenEnv   string   `toml:"auth_token_env"`
}

// ProviderConfig configures the OpenAI-compatible model provider. In version 2
// the active profile (Active) selects one of Config.Providers and is collapsed
// into these fields at load time.
type ProviderConfig struct {
	// Active names the [providers.NAME] profile to use (version 2). Empty for
	// version-1 configs, which set the flat fields directly.
	Active                     string   `toml:"active"`
	Kind                       string   `toml:"kind"`
	API                        string   `toml:"api"` // responses | chat-completions
	BaseURL                    string   `toml:"base_url"`
	APIKeyEnv                  string   `toml:"api_key_env"`
	StateMode                  string   `toml:"state_mode"` // stateless | manual-replay | stateful
	ConnectTimeout             Duration `toml:"connect_timeout"`
	RequestTimeout             Duration `toml:"request_timeout"`
	StreamIdleTimeout          Duration `toml:"stream_idle_timeout"`
	MaxRetries                 int      `toml:"max_retries"`
	AllowInsecureHTTPLocalhost bool     `toml:"allow_insecure_http_localhost"`
	// APIKey is resolved at load time from APIKeyEnv and never serialized.
	APIKey string `toml:"-"`
}

// ProviderProfile is one named, switchable provider definition ([providers.NAME]
// in version-2 configs). The profile name is the TOML table key. Secrets are
// never stored here; only the name of the API-key environment variable.
type ProviderProfile struct {
	Preset                     string   `toml:"preset"` // openai | sakana | generic (documentation only)
	Kind                       string   `toml:"kind"`
	API                        string   `toml:"api"` // responses | chat-completions
	BaseURL                    string   `toml:"base_url"`
	APIKeyEnv                  string   `toml:"api_key_env"`
	SmallModel                 string   `toml:"small_model"`
	LargeModel                 string   `toml:"large_model"`
	StateMode                  string   `toml:"state_mode"`
	ConnectTimeout             Duration `toml:"connect_timeout"`
	RequestTimeout             Duration `toml:"request_timeout"`
	StreamIdleTimeout          Duration `toml:"stream_idle_timeout"`
	MaxRetries                 int      `toml:"max_retries"`
	AllowInsecureHTTPLocalhost bool     `toml:"allow_insecure_http_localhost"`
}

// RoutingConfig maps each pipeline stage to a model tier ("small" | "large").
// Empty stages fall back to the engine's profile/complexity escalation.
type RoutingConfig struct {
	Exploration      string `toml:"exploration"`
	QueryExpansion   string `toml:"query_expansion"`
	Summary          string `toml:"summary"`
	ReviewCandidates string `toml:"review_candidates"`
	PlanFinal        string `toml:"plan_final"`
	HelpFinal        string `toml:"help_final"`
	ReviewVerify     string `toml:"review_verify"`
	ReviewFinal      string `toml:"review_final"`
}

// For returns the configured tier for a pipeline stage, or "" if unset.
func (r RoutingConfig) For(stage string) string {
	switch stage {
	case "exploration":
		return r.Exploration
	case "query_expansion":
		return r.QueryExpansion
	case "summary":
		return r.Summary
	case "review_candidates":
		return r.ReviewCandidates
	case "plan_final":
		return r.PlanFinal
	case "help_final":
		return r.HelpFinal
	case "review_verify":
		return r.ReviewVerify
	case "review_final":
		return r.ReviewFinal
	}
	return ""
}

// ReasoningConfig sets the reasoning effort for each model tier (version 2).
type ReasoningConfig struct {
	Small string `toml:"small"`
	Large string `toml:"large"`
}

// ModelsConfig maps roles to model IDs and reasoning effort levels. Version 2
// configs typically set only SmallModel/LargeModel (via a provider profile) plus
// [reasoning]; Load fills the role fields from those tiers when they are empty.
type ModelsConfig struct {
	// SmallModel and LargeModel are the two version-2 model tiers. They seed the
	// role fields below when those are unset.
	SmallModel        string `toml:"small_model"`
	LargeModel        string `toml:"large_model"`
	Scout             string `toml:"scout"`
	Planner           string `toml:"planner"`
	Reviewer          string `toml:"reviewer"`
	Verifier          string `toml:"verifier"`
	ReasoningScout    string `toml:"reasoning_scout"`
	ReasoningPlanner  string `toml:"reasoning_planner"`
	ReasoningVerifier string `toml:"reasoning_verifier"`
	MaxOutputTokens   int    `toml:"max_output_tokens"`
}

// RepositoryConfig controls inventory, ignore handling, and snapshot limits.
type RepositoryConfig struct {
	FollowSymlinks        bool     `toml:"follow_symlinks"`
	IncludeUntracked      bool     `toml:"include_untracked"`
	IncludeSubmodules     bool     `toml:"include_submodules"`
	MaxFileBytes          int64    `toml:"max_file_bytes"`
	MaxTotalSnapshotBytes int64    `toml:"max_total_snapshot_bytes"`
	RespectGitignore      bool     `toml:"respect_gitignore"`
	RespectIgnoreFile     bool     `toml:"respect_ignore_file"`
	IgnoreFile            string   `toml:"ignore_file"`
	GuidelineFiles        []string `toml:"guideline_files"`
	VendorGlobs           []string `toml:"vendor_globs"`
	GeneratedGlobs        []string `toml:"generated_globs"`
}

// RetrievalConfig controls the retrieval ladder and per-run ceilings.
type RetrievalConfig struct {
	Lexical              bool   `toml:"lexical"`
	Symbols              bool   `toml:"symbols"`
	Summaries            string `toml:"summaries"` // off | on-demand | eager
	Embeddings           bool   `toml:"embeddings"`
	MaxModelToolRounds   int    `toml:"max_model_tool_rounds"`
	MaxModelToolCalls    int    `toml:"max_model_tool_calls"`
	MaxFilesPerRun       int    `toml:"max_files_per_run"`
	MaxFileReads         int    `toml:"max_file_reads"`
	MaxContextTokens     int    `toml:"max_context_tokens"`
	InitialContextTokens int    `toml:"initial_context_tokens"`
	SearchResultLimit    int    `toml:"search_result_limit"`
	UseRipgrep           bool   `toml:"use_ripgrep"`
	// TrigramFilter enables the in-memory trigram candidate pre-filter, which
	// narrows the files an exact search scans without changing results.
	TrigramFilter bool `toml:"trigram_filter"`
	// TrigramMaxPerFile bounds the distinct trigrams indexed per file before that
	// file becomes an unconditional candidate; 0 uses the engine default.
	TrigramMaxPerFile int `toml:"trigram_max_per_file"`
	// MaxBytesRead is the default per-run cap on bytes read by the repo_read tool;
	// 0 means no limit. The review's frozen diff and head-read tools serve
	// already-bounded snapshot content and are not charged against it. A per-request
	// budget (schema.Budget.MaxBytesRead) overrides this default.
	MaxBytesRead int64 `toml:"max_bytes_read"`
}

// EmbeddingsConfig configures the optional, provider-neutral embedding index.
type EmbeddingsConfig struct {
	Enabled       bool   `toml:"enabled"`
	Provider      string `toml:"provider"`
	Model         string `toml:"model"`
	BaseURL       string `toml:"base_url"`
	APIKeyEnv     string `toml:"api_key_env"`
	ChunkTokens   int    `toml:"chunk_tokens"`
	OverlapTokens int    `toml:"overlap_tokens"`
	MaxChunks     int    `toml:"max_chunks"`
	// APIKey is resolved at load time from APIKeyEnv and never serialized.
	APIKey string `toml:"-"`
}

// CacheConfig controls the content-addressed cache.
type CacheConfig struct {
	Enabled             bool     `toml:"enabled"`
	Dir                 string   `toml:"dir"`
	MaxSizeGB           float64  `toml:"max_size_gb"`
	TTL                 Duration `toml:"ttl"`
	StoreModelOutputs   bool     `toml:"store_model_outputs"`
	StoreRawToolResults bool     `toml:"store_raw_tool_results"`
	Compress            bool     `toml:"compress"`
	AllowInRepo         bool     `toml:"allow_in_repo"`
}

// PlanConfig controls plan/help synthesis policy.
type PlanConfig struct {
	DefaultProfile           string `toml:"default_profile"`
	MaxSteps                 int    `toml:"max_steps"`
	RequireFileEvidence      bool   `toml:"require_file_evidence"`
	RequireValidationPerStep bool   `toml:"require_validation_per_step"`
	IncludeAlternatives      bool   `toml:"include_alternatives"`
}

// ReviewConfig controls review passes and publication thresholds.
type ReviewConfig struct {
	DefaultProfile      string   `toml:"default_profile"`
	Passes              []string `toml:"passes"`
	MaxBlockingFindings int      `toml:"max_blocking_findings"`
	MaxTotalFindings    int      `toml:"max_total_findings"`
	MinimumEvidence     string   `toml:"minimum_evidence"`
	IncludeStyle        bool     `toml:"include_style"`
	IncludePraise       bool     `toml:"include_praise"`
	IncludeGenerated    bool     `toml:"include_generated"`
	// MaxSymbolEnrichFiles caps how many changed files have their enclosing changed
	// symbols resolved for the diff-local pass. Zero disables symbol enrichment.
	MaxSymbolEnrichFiles int `toml:"max_symbol_enrich_files"`
	// LineOverlapTolerance is the adjacent-context tolerance (in lines) when
	// attributing a finding to a changed hunk. Zero requires exact overlap.
	LineOverlapTolerance int `toml:"line_overlap_tolerance"`
}

// ProfileLimitsConfig sets the maximum number of model calls each analysis
// profile may make. Zero means no explicit limit. A per-request budget
// (schema.Budget.MaxModelCalls) overrides these.
type ProfileLimitsConfig struct {
	MaxModelCallsFast     int `toml:"max_model_calls_fast"`
	MaxModelCallsBalanced int `toml:"max_model_calls_balanced"`
	MaxModelCallsDeep     int `toml:"max_model_calls_deep"`
}

// GroundingConfig gates the optional web-search grounding tool. When enabled the
// model may call a read-only tool that performs a provider-side web search (the
// OpenAI Responses built-in web_search) and folds the result back as UNTRUSTED
// external content. Disabled by default; requires the responses dialect.
type GroundingConfig struct {
	Enabled bool `toml:"enabled"`
	// SearchContextSize tunes retrieval depth: low | medium | high. Empty leaves
	// the provider default.
	SearchContextSize string `toml:"search_context_size"`
	// AllowedDomains and BlockedDomains optionally constrain the search. Bare
	// hostnames only (no scheme, no path).
	AllowedDomains []string `toml:"allowed_domains"`
	BlockedDomains []string `toml:"blocked_domains"`
}

// ChecksConfig configures the external check runner.
type ChecksConfig struct {
	Mode         string         `toml:"mode"` // off | safe | configured | deep
	Network      bool           `toml:"network"`
	MaxParallel  int            `toml:"max_parallel"`
	MaxTotalTime Duration       `toml:"max_total_time"`
	Command      []CheckCommand `toml:"command"`
}

// CheckCommand is one pre-approved command. Argv is an executable plus argument
// array; shell strings are never accepted.
type CheckCommand struct {
	Name      string   `toml:"name"`
	Argv      []string `toml:"argv"`
	Cwd       string   `toml:"cwd"`
	Timeout   Duration `toml:"timeout"`
	Languages []string `toml:"languages"`
	Enabled   bool     `toml:"enabled"`
	// Safe marks a read-only analyzer (formatter check, linter, type-checker) that
	// the "safe" verification mode may run. Leave false for builds and test suites,
	// which only run under "configured" or "deep".
	Safe bool `toml:"safe"`
}
