package config

import "time"

// Defaults returns the built-in configuration, matching the Sakana-oriented
// defaults from the specification. Every other configuration source overrides
// these.
func Defaults() Config {
	return Config{
		Version: SupportedVersion,
		Server: ServerConfig{
			Transport:         "stdio",
			Listen:            "127.0.0.1:7341",
			LogLevel:          "info",
			MaxConcurrentRuns: 2,
			RunRetention:      Duration(7 * 24 * time.Hour),
			MaxRequestBytes:   1 << 20, // 1 MiB
		},
		Provider: ProviderConfig{
			Kind:              "openai-compatible",
			API:               "responses",
			BaseURL:           "https://api.sakana.ai/v1",
			APIKeyEnv:         "SAKANA_API_KEY",
			ConnectTimeout:    Duration(10 * time.Second),
			RequestTimeout:    Duration(30 * time.Minute),
			StreamIdleTimeout: Duration(10 * time.Minute),
			MaxRetries:        3,
		},
		Models: ModelsConfig{
			Scout:             "fugu",
			Planner:           "fugu",
			Reviewer:          "fugu",
			Verifier:          "fugu-ultra",
			ReasoningScout:    "high",
			ReasoningPlanner:  "high",
			ReasoningVerifier: "xhigh",
			MaxOutputTokens:   16000,
		},
		Repository: RepositoryConfig{
			FollowSymlinks:        false,
			IncludeUntracked:      true,
			IncludeSubmodules:     false,
			MaxFileBytes:          1 << 20,   // 1 MiB
			MaxTotalSnapshotBytes: 512 << 20, // 512 MiB
			RespectGitignore:      true,
			RespectIgnoreFile:     true,
			IgnoreFile:            ".codeexpertignore",
			GuidelineFiles: []string{
				"AGENTS.md", "CLAUDE.md", "CONTRIBUTING.md", ".github/copilot-instructions.md",
			},
			VendorGlobs:    []string{"**/vendor/**", "**/node_modules/**", "**/.venv/**"},
			GeneratedGlobs: []string{"**/*.generated.*", "**/dist/**", "**/build/**"},
		},
		Retrieval: RetrievalConfig{
			Lexical:              true,
			Symbols:              true,
			Summaries:            "on-demand",
			Embeddings:           false,
			MaxModelToolRounds:   4,
			MaxModelToolCalls:    16,
			MaxFilesPerRun:       120,
			MaxFileReads:         80,
			MaxContextTokens:     60000,
			InitialContextTokens: 14000,
			SearchResultLimit:    100,
			UseRipgrep:           true,
			TrigramFilter:        true,
			TrigramMaxPerFile:    1 << 18, // ~262k distinct trigrams; bigger/high-entropy files degrade to always-candidate
			MaxBytesRead:         0,       // 0 = no default cap; per-request budget may set one
		},
		Embeddings: EmbeddingsConfig{
			Enabled:       false,
			ChunkTokens:   800,
			OverlapTokens: 100,
			MaxChunks:     250000,
		},
		Cache: CacheConfig{
			Enabled:             true,
			MaxSizeGB:           10,
			TTL:                 Duration(30 * 24 * time.Hour),
			StoreModelOutputs:   true,
			StoreRawToolResults: true,
			Compress:            true,
		},
		Plan: PlanConfig{
			DefaultProfile:           "balanced",
			MaxSteps:                 24,
			RequireFileEvidence:      true,
			RequireValidationPerStep: true,
			IncludeAlternatives:      true,
		},
		Review: ReviewConfig{
			DefaultProfile:       "balanced",
			Passes:               []string{"diff-local", "repository-context", "risk-specialist"},
			MaxBlockingFindings:  3,
			MaxTotalFindings:     7,
			MinimumEvidence:      "code-path",
			IncludeStyle:         false,
			IncludePraise:        false,
			IncludeGenerated:     false,
			MaxSymbolEnrichFiles: 100,
			LineOverlapTolerance: 3,
		},
		Checks: ChecksConfig{
			Mode:         "off",
			Network:      false,
			MaxParallel:  2,
			MaxTotalTime: Duration(20 * time.Minute),
		},
		ProfileLimits: ProfileLimitsConfig{
			MaxModelCallsFast:     3,
			MaxModelCallsBalanced: 7,
			MaxModelCallsDeep:     12,
		},
	}
}
