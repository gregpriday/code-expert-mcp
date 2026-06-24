package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/gregpriday/codeexpert/internal/app"
	"github.com/gregpriday/codeexpert/internal/eval"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// cmdEval runs the quality-evaluation harness over a directory of case files and
// prints a scorecard. It uses the real engine, so a quality run needs an API key;
// the deterministic graders (schema, no-write, status, evidence, coverage,
// approval language) run regardless, and the model-judge layer is added when
// --judge is set.
func cmdEval(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	casesDir := fs.String("cases", "", "directory of *.json case files (required)")
	root := fs.String("root", "", "override every case's repository root with this path")
	output := fs.String("output", "", "write the full JSON report to this file")
	minPass := fs.Float64("min-pass", 0, "exit 7 if the case pass-rate is below this fraction (0..1)")
	judge := fs.Bool("judge", false, "add the model-judge grader (uses the configured large model)")
	quiet := fs.Bool("quiet", false, "suppress progress output")
	if err := fs.Parse(args); err != nil {
		return exitInvalidArgs
	}
	if *casesDir == "" {
		return fail(schema.NewError(schema.CodeInvalidArgument, "eval requires --cases <dir>"))
	}

	cases, err := eval.LoadCases(*casesDir)
	if err != nil {
		return fail(schema.NewError(schema.CodeInvalidArgument, "loading cases: %v", err))
	}
	if len(cases) == 0 {
		return fail(schema.NewError(schema.CodeInvalidArgument, "no *.json cases found in %s", *casesDir))
	}
	if *root != "" {
		for i := range cases {
			overrideRoot(&cases[i], *root)
		}
	}

	a, err := app.Build(app.BuildOptions{Root: *root, Quiet: *quiet})
	if err != nil {
		return fail(err)
	}
	if err := a.RequireAPIKey(); err != nil {
		return fail(err)
	}

	graders := eval.CodeGraders()
	if *judge {
		graders = append(graders, eval.ModelGrader{
			Provider: a.Engine.Provider,
			Model:    a.Engine.Cfg.Models.Verifier,
			Effort:   a.Engine.Cfg.Models.ReasoningVerifier,
		})
	}

	rep := eval.RunCases(ctx, a.Engine, cases, graders)
	printScorecard(os.Stdout, rep)

	if *output != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		if werr := os.WriteFile(*output, b, 0o644); werr != nil {
			return fail(schema.NewError(schema.CodeInternal, "writing report: %v", werr))
		}
	}
	if *minPass > 0 && rep.Total > 0 {
		rate := float64(rep.Passed) / float64(rep.Total)
		if rate < *minPass {
			fmt.Fprintf(os.Stderr, "eval: pass-rate %.2f is below the --min-pass gate %.2f\n", rate, *minPass)
			return exitThreshold
		}
	}
	return exitOK
}

func overrideRoot(c *eval.Case, root string) {
	switch {
	case c.Plan != nil:
		c.Plan.Root = root
	case c.Help != nil:
		c.Help.Root = root
	case c.Review != nil:
		c.Review.Root = root
	}
}

func printScorecard(w *os.File, rep eval.Report) {
	fmt.Fprintf(w, "# Eval scorecard\n\n")
	fmt.Fprintf(w, "Cases: %d   Passed: %d   Mean score: %.2f\n\n", rep.Total, rep.Passed, rep.MeanScore)

	var names []string
	for name := range rep.ByGrader {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprintf(w, "Pass-rate by grader:\n")
	for _, name := range names {
		fmt.Fprintf(w, "  %-20s %.2f\n", name, rep.ByGrader[name])
	}
	fmt.Fprintf(w, "\nFailures:\n")
	any := false
	for _, c := range rep.Cases {
		if c.Passed {
			continue
		}
		any = true
		fmt.Fprintf(w, "  ✗ %s (%s)\n", c.CaseID, c.Capability)
		for _, g := range c.Grades {
			if !g.Pass {
				fmt.Fprintf(w, "      %s: %s\n", g.Grader, g.Detail)
			}
		}
	}
	if !any {
		fmt.Fprintf(w, "  none\n")
	}
}
