package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/schema"
)

func cmdInit(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	projectDir := fs.String("project", ".", "project directory to write .codeexpert.toml into")
	global := fs.Bool("global", false, "write to the user config directory instead of the project")
	provider := fs.String("provider", "sakana", "sakana | generic")
	force := fs.Bool("force", false, "overwrite an existing config file")
	noComments := fs.Bool("no-comments", false, "omit explanatory comments")
	if err := fs.Parse(args); err != nil {
		return exitInvalidArgs
	}

	var target string
	if *global {
		p, err := config.UserConfigPath()
		if err != nil {
			return fail(schema.NewError(schema.CodeInternal, "cannot resolve user config dir: %v", err))
		}
		target = p
	} else {
		target = filepath.Join(*projectDir, ".codeexpert.toml")
	}

	if _, err := os.Stat(target); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "%s already exists; pass --force to overwrite\n", target)
		return exitInvalidArgs
	}

	apiKeyEnv := "SAKANA_API_KEY"
	baseURL := "https://api.sakana.ai/v1"
	if *provider == "generic" {
		apiKeyEnv = "CODEEXPERT_API_KEY"
		baseURL = "https://your-openai-compatible-endpoint/v1"
	}

	content := renderConfigTemplate(baseURL, apiKeyEnv, *provider, !*noComments)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fail(schema.NewError(schema.CodeInternal, "cannot create directory: %v", err))
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return fail(schema.NewError(schema.CodeInternal, "cannot write %s: %v", target, err))
	}

	fmt.Printf("Wrote %s\n", target)
	fmt.Printf("\nSet your provider API key before running:\n  export %s=\"<your key>\"\n", apiKeyEnv)
	fmt.Printf("\nRegister with Claude Code:\n  claude mcp add --transport stdio codeexpert -- codeexpert mcp --transport stdio\n")
	return exitOK
}

func renderConfigTemplate(baseURL, apiKeyEnv, provider string, comments bool) string {
	var b strings.Builder
	c := func(s string) {
		if comments {
			b.WriteString(s)
		}
	}
	b.WriteString("version = 1\n\n")
	c("# CodeExpert configuration. Secrets are NEVER stored here; only the name of\n# the environment variable holding the key is configured.\n\n")
	b.WriteString("[server]\ntransport = \"stdio\"\nlog_level = \"info\"\nmax_concurrent_runs = 2\nrun_retention = \"7d\"\n\n")
	b.WriteString("[provider]\nkind = \"openai-compatible\"\napi = \"responses\"\n")
	fmt.Fprintf(&b, "base_url = %q\napi_key_env = %q\n", baseURL, apiKeyEnv)
	b.WriteString("request_timeout = \"30m\"\nmax_retries = 3\n\n")
	c("# Model roles. fugu for ordinary work, fugu-ultra for hard synthesis/verification.\n")
	b.WriteString("[models]\n")
	if provider == "generic" {
		b.WriteString("scout = \"your-standard-model\"\nplanner = \"your-standard-model\"\nreviewer = \"your-standard-model\"\nverifier = \"your-strong-model\"\n")
	} else {
		b.WriteString("scout = \"fugu\"\nplanner = \"fugu\"\nreviewer = \"fugu\"\nverifier = \"fugu-ultra\"\n")
	}
	b.WriteString("reasoning_scout = \"high\"\nreasoning_planner = \"high\"\nreasoning_verifier = \"xhigh\"\nmax_output_tokens = 16000\n\n")
	b.WriteString("[retrieval]\nlexical = true\nsymbols = true\nsummaries = \"on-demand\"\nembeddings = false\nmax_files_per_run = 120\nmax_context_tokens = 60000\n\n")
	b.WriteString("[review]\ndefault_profile = \"balanced\"\nmax_blocking_findings = 3\nmax_total_findings = 7\nminimum_evidence = \"code-path\"\ninclude_style = false\n\n")
	c("# External checks are OFF by default. When enabled, commands run in an\n# isolated copy of the snapshot, never in your working tree.\n")
	b.WriteString("[checks]\nmode = \"off\"\nnetwork = false\n")
	return b.String()
}
