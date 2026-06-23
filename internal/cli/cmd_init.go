package cli

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// presetDefaults holds the baked-in defaults for one init preset. Flags override
// any field. Secrets are never written; only the api_key_env name.
type presetDefaults struct {
	name           string
	baseURL        string
	apiKeyEnv      string
	small          string
	large          string
	stateMode      string
	smallReasoning string
	largeReasoning string
	allowInsecure  bool // emit allow_insecure_http_localhost for http loopback URLs
}

func presetFor(name string) presetDefaults {
	switch name {
	case "sakana":
		return presetDefaults{
			name: "sakana", baseURL: "https://api.sakana.ai/v1", apiKeyEnv: "SAKANA_API_KEY",
			small: "fugu", large: "fugu-ultra", stateMode: "manual-replay",
			smallReasoning: "high", largeReasoning: "xhigh",
		}
	case "generic":
		return presetDefaults{
			name: "generic", baseURL: "https://your-openai-compatible-endpoint/v1", apiKeyEnv: "CODEEXPERT_API_KEY",
			small: "your-small-model", large: "your-large-model", stateMode: "stateless",
			smallReasoning: "medium", largeReasoning: "high",
		}
	default: // openai
		return presetDefaults{
			name: "openai", baseURL: "https://api.openai.com/v1", apiKeyEnv: "OPENAI_API_KEY",
			small: "gpt-5.4-mini", large: "gpt-5.5", stateMode: "stateless",
			smallReasoning: "medium", largeReasoning: "high",
		}
	}
}

func cmdInit(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	projectDir := fs.String("project", ".", "project directory to write .codeexpert.toml into")
	global := fs.Bool("global", false, "write to the user config directory instead of the project")
	provider := fs.String("provider", "openai", "openai | sakana | generic")
	api := fs.String("api", "responses", "responses | chat-completions")
	baseURL := fs.String("base-url", "", "override the provider base URL")
	apiKeyEnv := fs.String("api-key-env", "", "override the API-key environment variable name")
	smallModel := fs.String("small-model", "", "override the small (fast) model id")
	largeModel := fs.String("large-model", "", "override the large (strong) model id")
	force := fs.Bool("force", false, "overwrite an existing config file")
	noComments := fs.Bool("no-comments", false, "omit explanatory comments")
	if err := fs.Parse(args); err != nil {
		return exitInvalidArgs
	}
	if err := validateProviderName(*provider); err != nil {
		return fail(err)
	}
	if err := validateAPIName(*api); err != nil {
		return fail(err)
	}

	p := presetFor(*provider)
	p.baseURL = orElse(*baseURL, p.baseURL)
	p.apiKeyEnv = orElse(*apiKeyEnv, p.apiKeyEnv)
	p.small = orElse(*smallModel, p.small)
	p.large = orElse(*largeModel, p.large)
	// A loopback http base URL needs the explicit insecure opt-in or the written
	// config would fail validation.
	p.allowInsecure = isInsecureLoopback(p.baseURL)

	var target string
	if *global {
		up, err := config.UserConfigPath()
		if err != nil {
			return fail(schema.NewError(schema.CodeInternal, "cannot resolve user config dir: %v", err))
		}
		target = up
	} else {
		target = filepath.Join(*projectDir, ".codeexpert.toml")
	}

	if _, err := os.Stat(target); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "%s already exists; pass --force to overwrite\n", target)
		return exitInvalidArgs
	}

	content := renderV2Template(p, *api, !*noComments)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fail(schema.NewError(schema.CodeInternal, "cannot create directory: %v", err))
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return fail(schema.NewError(schema.CodeInternal, "cannot write %s: %v", target, err))
	}

	// Safety net: warn (don't fail) if the written config does not validate, e.g.
	// a non-loopback http base URL or another unsupported override combination.
	if cfg, lerr := config.LoadFile(target); lerr == nil {
		if verr := config.Validate(&cfg); verr != nil {
			fmt.Fprintf(os.Stderr, "warning: written config does not validate: %s\n", schema.AsToolError(verr).Error())
		}
	}

	fmt.Printf("Wrote %s (provider profile: %s)\n", target, p.name)
	fmt.Printf("\nSet your provider API key before running:\n  export %s=\"<your key>\"\n", p.apiKeyEnv)
	fmt.Printf("\nSwitch providers later by changing provider.active and adding another [providers.NAME] block.\n")
	fmt.Printf("\nRegister with Claude Code:\n  claude mcp add --transport stdio codeexpert -- codeexpert mcp --transport stdio\n")
	return exitOK
}

func renderV2Template(p presetDefaults, api string, comments bool) string {
	var b strings.Builder
	c := func(s string) {
		if comments {
			b.WriteString(s)
		}
	}
	b.WriteString("version = 2\n\n")
	c("# CodeExpert configuration. Secrets are NEVER stored here; only the name of\n# the environment variable holding the key is configured. Switch providers by\n# changing provider.active to another [providers.NAME] block.\n\n")
	b.WriteString("[server]\ntransport = \"stdio\"\nlog_level = \"info\"\nmax_concurrent_runs = 2\nrun_retention = \"7d\"\n\n")

	fmt.Fprintf(&b, "[provider]\nactive = %q\n\n", p.name)
	fmt.Fprintf(&b, "[providers.%s]\n", p.name)
	fmt.Fprintf(&b, "preset = %q\nkind = \"openai-compatible\"\napi = %q\n", p.name, api)
	fmt.Fprintf(&b, "base_url = %q\napi_key_env = %q\n", p.baseURL, p.apiKeyEnv)
	c("# small_model handles exploration and recall-heavy passes; large_model handles\n# final synthesis and verification.\n")
	fmt.Fprintf(&b, "small_model = %q\nlarge_model = %q\n", p.small, p.large)
	c("# state_mode: stateless (manual replay; private) | manual-replay | stateful (OpenAI previous_response_id).\n")
	fmt.Fprintf(&b, "state_mode = %q\n", p.stateMode)
	if p.allowInsecure {
		c("# Required because base_url uses http on a loopback host.\n")
		b.WriteString("allow_insecure_http_localhost = true\n")
	}
	b.WriteString("request_timeout = \"30m\"\nmax_retries = 3\n\n")

	c("# Per-stage model tier: \"small\" (fast/cheap) or \"large\" (strong synthesis).\n")
	b.WriteString("[routing]\nexploration = \"small\"\nreview_candidates = \"small\"\nplan_final = \"large\"\nhelp_final = \"large\"\nreview_verify = \"large\"\nreview_final = \"large\"\n\n")

	c("# Reasoning effort per tier (low | medium | high | xhigh).\n")
	fmt.Fprintf(&b, "[reasoning]\nsmall = %q\nlarge = %q\n\n", p.smallReasoning, p.largeReasoning)

	b.WriteString("[retrieval]\nlexical = true\nsymbols = true\nsummaries = \"on-demand\"\nembeddings = false\nmax_files_per_run = 120\nmax_context_tokens = 60000\n\n")
	b.WriteString("[review]\ndefault_profile = \"balanced\"\nmax_blocking_findings = 3\nmax_total_findings = 7\nminimum_evidence = \"code-path\"\ninclude_style = false\n\n")
	c("# External checks are OFF by default. When enabled, commands run in an\n# isolated copy of the snapshot, never in your working tree.\n")
	b.WriteString("[checks]\nmode = \"off\"\nnetwork = false\n")
	return b.String()
}

func orElse(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

// isInsecureLoopback reports whether a base URL uses http on a loopback host,
// which requires the explicit allow_insecure_http_localhost opt-in to validate.
func isInsecureLoopback(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "http" {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || h == "::1" || strings.HasPrefix(h, "127.")
}
