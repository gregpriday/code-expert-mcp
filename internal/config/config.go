// Package config resolves CodeExpert configuration from built-in defaults, user
// and project TOML files, environment variables, and CLI/MCP arguments, in that
// precedence order. Secrets are never stored in the config struct; only the name
// of the environment variable holding a key is configured.
package config

// SupportedVersion is the only config schema version this build understands.
const SupportedVersion = 1

// Config is the fully-resolved configuration for a process.
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

// ProviderConfig configures the OpenAI-compatible model provider.
type ProviderConfig struct {
	Kind                       string   `toml:"kind"`
	API                        string   `toml:"api"` // responses | chat-completions
	BaseURL                    string   `toml:"base_url"`
	APIKeyEnv                  string   `toml:"api_key_env"`
	ConnectTimeout             Duration `toml:"connect_timeout"`
	RequestTimeout             Duration `toml:"request_timeout"`
	StreamIdleTimeout          Duration `toml:"stream_idle_timeout"`
	MaxRetries                 int      `toml:"max_retries"`
	AllowInsecureHTTPLocalhost bool     `toml:"allow_insecure_http_localhost"`
	// APIKey is resolved at load time from APIKeyEnv and never serialized.
	APIKey string `toml:"-"`
}

// ModelsConfig maps roles to model IDs and reasoning effort levels.
type ModelsConfig struct {
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
}
