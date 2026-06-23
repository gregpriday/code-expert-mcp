package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gregpriday/codeexpert/internal/app"
	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/repo/git"
	"github.com/gregpriday/codeexpert/internal/schema"
)

func cmdDoctor(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	root := fs.String("root", "", "repository root to check")
	probe := fs.Bool("probe", false, "probe the provider /models endpoint")
	if err := fs.Parse(args); err != nil {
		return exitInvalidArgs
	}

	fmt.Printf("CodeExpert %s\n\n", app.Version)

	// Git availability.
	if git.Available() {
		fmt.Println("✓ git found")
	} else {
		fmt.Println("✗ git not found (planning/help still work in filesystem mode; review requires git)")
	}

	a, err := app.Build(app.BuildOptions{Root: *root})
	if err != nil {
		fmt.Printf("✗ configuration: %s\n", schema.AsToolError(err).Error())
		return exitInvalidArgs
	}
	fmt.Println("✓ configuration valid")
	fmt.Printf("  root: %s\n", a.Root)
	if a.ProjectFile != "" {
		fmt.Printf("  project config: %s\n", a.ProjectFile)
	} else {
		fmt.Println("  project config: none (using defaults + user config + env)")
	}
	if len(a.Sources) > 0 {
		fmt.Printf("  sources: %s\n", strings.Join(a.Sources, ", "))
	}
	if a.Config.Version < 2 {
		fmt.Println("  • config is version 1; run `codeexpert config migrate` to print a version-2 equivalent")
	}
	if a.Config.Provider.Active != "" {
		fmt.Printf("  provider profile: %s\n", a.Config.Provider.Active)
	}
	fmt.Printf("  provider: %s (%s, dialect=%s)\n", a.Config.Provider.BaseURL, a.Config.Provider.Kind, a.Config.Provider.API)
	fmt.Printf("  models: small=%s large=%s\n", a.Config.Models.Scout, a.Config.Models.Verifier)

	if a.Config.Provider.APIKey != "" {
		fmt.Printf("✓ API key present (from %s)\n", a.Config.Provider.APIKeyEnv)
	} else {
		fmt.Printf("✗ API key missing; set %s\n", a.Config.Provider.APIKeyEnv)
	}

	if a.Cache.Enabled() {
		fmt.Printf("✓ cache at %s\n", a.Cache.Dir())
	} else {
		fmt.Println("• cache disabled")
	}

	fmt.Println("\nData boundary: selected repository content is sent to the configured model endpoint.")
	fmt.Println("Exclude paths and cap source bytes via the [repository] and [retrieval] config sections.")

	if *probe {
		if a.Config.Provider.APIKey == "" {
			fmt.Println("\n✗ cannot probe without an API key")
			return exitProviderError
		}
		return runProbe(ctx, a.Provider, a.Config, os.Stdout)
	}
	return exitOK
}

// runProbe exercises the configured provider's capabilities and reports each as a
// separate line: reachability, declared capabilities, small/large text
// generation, a tool-call + tool-result continuation, and strict structured
// output. It hard-fails only when the provider is unreachable or rejects the key;
// individual unsupported capabilities are reported but not fatal.
func runProbe(ctx context.Context, p provider.Provider, cfg config.Config, w io.Writer) int {
	fmt.Fprintln(w, "\nProbing provider capabilities …")
	caps := p.Capabilities(ctx)
	fmt.Fprintf(w, "  dialect: %s (tools=%v, structured=%v, streaming=%v, reasoning=%v)\n",
		caps.Dialect, caps.SupportsTools, caps.SupportsStructured, caps.SupportsStreaming, caps.SupportsReasoning)

	models, perr := p.ListModels(ctx)
	if perr != nil {
		fmt.Fprintf(w, "  ✗ provider unreachable: %s\n", schema.AsToolError(perr).Error())
		return exitProviderError
	}
	fmt.Fprintf(w, "  ✓ reachable (%d models listed)\n", len(models))

	// Probe the same resolved role models the engine actually runs (scout = small
	// tier, verifier = large tier) so the probe reflects real behavior, including
	// any CODEEXPERT_MODELS_* env overrides.
	small := cfg.Models.Scout
	large := cfg.Models.Verifier
	authFailed := false
	note := func(err error) {
		if err != nil && schema.AsToolError(err).Code == schema.CodeProviderAuth {
			authFailed = true
		}
	}
	note(probeText(ctx, p, w, "small-text", small))
	if large != small {
		note(probeText(ctx, p, w, "large-text", large))
	}
	if caps.SupportsTools {
		note(probeToolCall(ctx, p, w, small))
	}
	if caps.SupportsStructured {
		note(probeStructured(ctx, p, w, small))
	}
	if authFailed {
		fmt.Fprintln(w, "  ✗ provider rejected the API key")
		return exitProviderError
	}
	return exitOK
}

func probeText(ctx context.Context, p provider.Provider, w io.Writer, label, model string) error {
	if model == "" {
		fmt.Fprintf(w, "  • %s: no model configured\n", label)
		return nil
	}
	resp, err := p.Generate(ctx, provider.GenerationRequest{
		Model:           model,
		Input:           []provider.Message{{Role: provider.RoleUser, Content: "Reply with the single word: ok"}},
		MaxOutputTokens: 16,
	})
	if err != nil {
		fmt.Fprintf(w, "  ✗ %s (%s): %s\n", label, model, schema.AsToolError(err).Error())
		return err
	}
	fmt.Fprintf(w, "  ✓ %s (%s, %d in / %d out)\n", label, model, resp.Usage.InputTokens, resp.Usage.OutputTokens)
	return nil
}

func probeToolCall(ctx context.Context, p provider.Provider, w io.Writer, model string) error {
	if model == "" {
		fmt.Fprintln(w, "  • tool-call: no model configured")
		return nil
	}
	tool := provider.FunctionTool{
		Name:        "ping",
		Description: "Returns pong. Call this tool.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}
	resp, err := p.Generate(ctx, provider.GenerationRequest{
		Model:           model,
		Input:           []provider.Message{{Role: provider.RoleUser, Content: "Call the ping tool now."}},
		Tools:           []provider.FunctionTool{tool},
		ToolChoice:      provider.ToolChoice{Mode: provider.ToolChoiceRequired},
		MaxOutputTokens: 64,
	})
	if err != nil {
		fmt.Fprintf(w, "  ✗ tool-call (%s): %s\n", model, schema.AsToolError(err).Error())
		return err
	}
	if len(resp.ToolCalls) == 0 {
		fmt.Fprintf(w, "  • tool-call (%s): model did not request the tool\n", model)
		return nil
	}
	// Continuation: feed the tool result back, replaying provider items.
	call := resp.ToolCalls[0]
	_, cerr := p.Generate(ctx, provider.GenerationRequest{
		Model: model,
		Input: []provider.Message{
			{Role: provider.RoleUser, Content: "Call the ping tool now."},
			{Role: provider.RoleAssistant, Content: resp.Text, ToolCalls: resp.ToolCalls, ProviderItems: resp.ProviderItems},
			{Role: provider.RoleTool, ToolCallID: call.ID, Name: call.Name, Content: "pong"},
		},
		MaxOutputTokens: 16,
	})
	if cerr != nil {
		fmt.Fprintf(w, "  ✗ tool-call continuation (%s): %s\n", model, schema.AsToolError(cerr).Error())
		return cerr
	}
	fmt.Fprintf(w, "  ✓ tool-call + continuation (%s)\n", model)
	return nil
}

func probeStructured(ctx context.Context, p provider.Provider, w io.Writer, model string) error {
	if model == "" {
		fmt.Fprintln(w, "  • structured: no model configured")
		return nil
	}
	schemaJSON := json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`)
	resp, err := p.Generate(ctx, provider.GenerationRequest{
		Model:           model,
		Input:           []provider.Message{{Role: provider.RoleUser, Content: `Return {"ok": true} as JSON.`}},
		OutputSchema:    &provider.JSONSchema{Name: "probe", Schema: schemaJSON, Strict: true},
		MaxOutputTokens: 64,
	})
	if err != nil {
		fmt.Fprintf(w, "  ✗ structured (%s): %s\n", model, schema.AsToolError(err).Error())
		return err
	}
	body := resp.StructuredJSON
	if len(body) == 0 {
		body = []byte(resp.Text)
	}
	var probe struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal(body, &probe) != nil {
		fmt.Fprintf(w, "  • structured (%s): response was not valid JSON for the schema\n", model)
		return nil
	}
	fmt.Fprintf(w, "  ✓ structured (%s)\n", model)
	return nil
}

func cmdConfig(ctx context.Context, args []string) int {
	if len(args) == 0 || (args[0] != "print" && args[0] != "migrate") {
		fmt.Fprintln(os.Stderr, "usage: codeexpert config print|migrate [--root DIR]")
		return exitInvalidArgs
	}
	sub := args[0]
	fs := flag.NewFlagSet("config "+sub, flag.ContinueOnError)
	root := fs.String("root", "", "repository root")
	if err := fs.Parse(args[1:]); err != nil {
		return exitInvalidArgs
	}
	lr, err := config.Load(repo.DefaultRoot(*root))
	if err != nil {
		return fail(err)
	}

	if sub == "migrate" {
		return printMigratedConfig(lr.Config)
	}

	if verr := config.Validate(&lr.Config); verr != nil {
		fmt.Fprintf(os.Stderr, "warning: %s\n", schema.AsToolError(verr).Error())
	}
	if len(lr.SourcesNoted) > 0 {
		fmt.Printf("# sources: %s\n\n", strings.Join(lr.SourcesNoted, ", "))
	}
	fmt.Print(lr.Config.String())
	return exitOK
}

// printMigratedConfig renders a faithful version-2 .codeexpert.toml derived from
// the loaded config, preserving every section (timeouts, cache, review, checks,
// localhost opt-ins, …). Secrets are never emitted — only api_key_env names.
// It is idempotent: an already-version-2 config re-renders to an equivalent file.
func printMigratedConfig(src config.Config) int {
	v2 := config.InferV2FromV1(src)
	out, err := config.RenderTOML(v2)
	if err != nil {
		return fail(err)
	}
	fmt.Println("# Migrated to config version 2. Review, then save as .codeexpert.toml.")
	fmt.Println("# Secrets are not included; only api_key_env names. Set keys via the environment.")
	fmt.Println()
	fmt.Print(out)
	return exitOK
}

func cmdCache(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: codeexpert cache status|gc|clear")
		return exitInvalidArgs
	}
	a, err := app.Build(app.BuildOptions{})
	if err != nil {
		return fail(err)
	}
	switch args[0] {
	case "status":
		st, _ := a.Cache.Stat()
		fmt.Printf("enabled: %v\n", st.Enabled)
		fmt.Printf("dir: %s\n", st.Dir)
		fmt.Printf("objects: %d\n", st.ObjectCount)
		fmt.Printf("runs: %d\n", st.RunCount)
		fmt.Printf("size: %.2f MiB\n", float64(st.TotalBytes)/(1<<20))
	case "gc":
		freed, gerr := a.Cache.GC(a.Config.Cache.TTL.Std(), int64(a.Config.Cache.MaxSizeGB*float64(1<<30)))
		if gerr != nil {
			return fail(gerr)
		}
		fmt.Printf("freed %.2f MiB\n", float64(freed)/(1<<20))
	case "clear":
		if cerr := a.Cache.Clear(); cerr != nil {
			return fail(cerr)
		}
		fmt.Println("cache cleared")
	default:
		fmt.Fprintln(os.Stderr, "usage: codeexpert cache status|gc|clear")
		return exitInvalidArgs
	}
	return exitOK
}

func cmdIndex(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	root := fs.String("root", "", "repository root")
	if err := fs.Parse(args); err != nil {
		return exitInvalidArgs
	}
	a, err := app.Build(app.BuildOptions{Root: *root})
	if err != nil {
		return fail(err)
	}
	r, err := repo.ResolveRoot(repo.DefaultRoot(*root), a.Config.Repository.FollowSymlinks, nil)
	if err != nil {
		return fail(err)
	}
	snap, err := repo.BuildSnapshot(ctx, r, a.Config.Repository)
	if err != nil {
		return fail(err)
	}
	brief := snap.Brief(a.Config.Repository)
	fmt.Printf("Snapshot: %s\n", snap.ID())
	fmt.Printf("Root: %s\n", brief.Root)
	fmt.Printf("Git: %v", brief.IsGit)
	if brief.IsGit {
		fmt.Printf(" (branch %s, dirty=%v)", brief.Branch, brief.Dirty)
	}
	fmt.Printf("\nFiles: %d (tracked %d, untracked %d)\n", len(snap.ListFiles()), brief.TrackedFiles, brief.UntrackedFiles)
	if snap.Truncated() {
		fmt.Println("⚠ snapshot truncated at the configured byte limit")
	}
	fmt.Println("\nLanguages:")
	for _, l := range brief.Languages {
		fmt.Printf("  %-14s %4d files  %6.1f%%  [%s]\n", l.Language, l.Files, l.Share*100, l.Indexer)
	}
	if len(brief.Manifests) > 0 {
		fmt.Printf("\nManifests: %s\n", strings.Join(brief.Manifests, ", "))
	}
	if len(brief.GuidanceFiles) > 0 {
		fmt.Printf("Guidance:  %s\n", strings.Join(brief.GuidanceFiles, ", "))
	}
	return exitOK
}
