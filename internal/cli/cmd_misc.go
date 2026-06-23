package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/gregpriday/codeexpert/internal/app"
	"github.com/gregpriday/codeexpert/internal/config"
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
	if len(a.Sources) > 0 {
		fmt.Printf("  sources: %s\n", strings.Join(a.Sources, ", "))
	}
	fmt.Printf("  provider: %s (%s, dialect=%s)\n", a.Config.Provider.BaseURL, a.Config.Provider.Kind, a.Config.Provider.API)

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
		fmt.Println("\nProbing provider /models …")
		models, perr := a.Provider.ListModels(ctx)
		if perr != nil {
			fmt.Printf("✗ provider probe failed: %s\n", schema.AsToolError(perr).Error())
			return exitProviderError
		}
		fmt.Printf("✓ provider reachable; %d models available\n", len(models))
		for i, m := range models {
			if i >= 10 {
				fmt.Printf("  … and %d more\n", len(models)-10)
				break
			}
			fmt.Printf("  - %s\n", m.ID)
		}
	}
	return exitOK
}

func cmdConfig(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] != "print" {
		fmt.Fprintln(os.Stderr, "usage: codeexpert config print [--root DIR]")
		return exitInvalidArgs
	}
	fs := flag.NewFlagSet("config print", flag.ContinueOnError)
	root := fs.String("root", "", "repository root")
	if err := fs.Parse(args[1:]); err != nil {
		return exitInvalidArgs
	}
	lr, err := config.Load(*root)
	if err != nil {
		return fail(err)
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
