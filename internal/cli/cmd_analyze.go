package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/gregpriday/codeexpert/internal/app"
	"github.com/gregpriday/codeexpert/internal/report"
	"github.com/gregpriday/codeexpert/internal/schema"
	"github.com/gregpriday/codeexpert/internal/workflow"
)

// stderrProgress prints stage transitions to stderr (keeping stdout for output).
func stderrProgress(quiet bool) func(stage, detail string) {
	if quiet {
		return nil
	}
	return func(stage, detail string) {
		if detail != "" {
			fmt.Fprintf(os.Stderr, "• %s: %s\n", stage, detail)
		} else {
			fmt.Fprintf(os.Stderr, "• %s\n", stage)
		}
	}
}

func cmdPlan(ctx context.Context, args []string, mode schema.PlanMode) int {
	fs := flag.NewFlagSet(string(mode), flag.ContinueOnError)
	root := fs.String("root", "", "repository root (default: CLAUDE_PROJECT_DIR or cwd)")
	profile := fs.String("profile", "", "fast | balanced | deep")
	format := fs.String("format", "markdown", "markdown | json | both")
	output := fs.String("output", "", "write the report to this file")
	instrFile := fs.String("instructions-file", "", "read instructions from a file")
	quiet := fs.Bool("quiet", false, "suppress progress output")
	noCache := fs.Bool("no-cache", false, "disable the cache for this run")
	if err := fs.Parse(args); err != nil {
		return exitInvalidArgs
	}
	if err := validateProfile(*profile); err != nil {
		return fail(err)
	}
	if err := validateFormat(*format); err != nil {
		return fail(err)
	}

	instructions, err := gatherInstructions(fs.Args(), *instrFile)
	if err != nil {
		return fail(err)
	}
	if instructions == "" {
		return fail(schema.NewError(schema.CodeInvalidArgument, "provide instructions as an argument or via --instructions-file"))
	}

	a, err := app.Build(app.BuildOptions{Root: *root, Quiet: *quiet, DisableCache: *noCache})
	if err != nil {
		return fail(err)
	}
	if err := a.RequireAPIKey(); err != nil {
		return fail(err)
	}

	req := schema.PlanRequest{
		Root:         *root,
		Instructions: instructions,
		Mode:         mode,
		Profile:      schema.AnalysisProfile(*profile),
	}
	res, err := a.Engine.Plan(ctx, req, workflow.RunOptions{Progress: stderrProgress(*quiet)})
	if err != nil {
		return fail(err)
	}

	jsonOut, _ := report.PlanJSON(res)
	if err := emit(res.Markdown, jsonOut, *format, *output); err != nil {
		return fail(err)
	}
	return exitOK
}

func cmdReview(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	root := fs.String("root", "", "repository root")
	scope := fs.String("scope", "working-tree", "working-tree | staged | unstaged | range | commit | merge-base")
	base := fs.String("base", "", "base ref (range scope)")
	head := fs.String("head", "", "head ref (range/merge-base scope)")
	commit := fs.String("commit", "", "commit (commit scope)")
	upstream := fs.String("upstream", "", "upstream ref (merge-base scope)")
	includeUntracked := fs.Bool("include-untracked", true, "include untracked files in working-tree review")
	profile := fs.String("profile", "", "fast | balanced | deep")
	verification := fs.String("verification", "", "off | safe | configured | deep")
	taskFile := fs.String("task-file", "", "JSON task contract (intent/acceptance criteria) to compare the change against")
	format := fs.String("format", "markdown", "markdown | json | both")
	output := fs.String("output", "", "write the report to this file")
	failOn := fs.String("fail-on", "", "exit 7 if a finding at/above this severity is published: critical|high|medium")
	quiet := fs.Bool("quiet", false, "suppress progress output")
	noCache := fs.Bool("no-cache", false, "disable the cache for this run")
	if err := fs.Parse(args); err != nil {
		return exitInvalidArgs
	}
	for _, v := range []error{
		validateScope(*scope), validateProfile(*profile),
		validateVerification(*verification), validateFormat(*format), validateFailOn(*failOn),
	} {
		if v != nil {
			return fail(v)
		}
	}

	instructions := strings.TrimSpace(strings.Join(fs.Args(), " "))

	a, err := app.Build(app.BuildOptions{Root: *root, Quiet: *quiet, DisableCache: *noCache})
	if err != nil {
		return fail(err)
	}
	if err := a.RequireAPIKey(); err != nil {
		return fail(err)
	}

	iu := *includeUntracked
	req := schema.ReviewRequest{
		Root:         *root,
		Instructions: instructions,
		Profile:      schema.AnalysisProfile(*profile),
		Verification: schema.VerificationMode(*verification),
		Target: schema.ReviewTarget{
			Type:             schema.ReviewTargetType(strings.ReplaceAll(*scope, "-", "_")),
			BaseRef:          *base,
			HeadRef:          *head,
			Commit:           *commit,
			UpstreamRef:      *upstream,
			IncludeUntracked: &iu,
		},
	}
	if *taskFile != "" {
		task, terr := loadTaskContract(*taskFile)
		if terr != nil {
			return fail(terr)
		}
		req.Task = task
	}
	res, err := a.Engine.Review(ctx, req, workflow.RunOptions{Progress: stderrProgress(*quiet)})
	if err != nil {
		return fail(err)
	}

	jsonOut, _ := report.ReviewJSON(res)
	if err := emit(res.Markdown, jsonOut, *format, *output); err != nil {
		return fail(err)
	}
	if code := failOnThreshold(res, *failOn); code != 0 {
		return code
	}
	return exitOK
}

func failOnThreshold(res schema.ReviewResult, failOn string) int {
	if failOn == "" {
		return 0
	}
	want := severityThreshold(failOn)
	if want == 0 {
		return 0
	}
	for _, f := range res.Findings {
		if severityValue(f.Severity) >= want {
			fmt.Fprintf(os.Stderr, "fail-on: published finding at severity %q meets threshold %q\n", f.Severity, failOn)
			return exitThreshold
		}
	}
	return 0
}

func severityThreshold(s string) int {
	switch strings.ToLower(s) {
	case "critical":
		return 5
	case "high":
		return 4
	case "medium":
		return 3
	}
	return 0
}

func severityValue(s schema.Severity) int {
	switch s {
	case schema.SeverityCritical:
		return 5
	case schema.SeverityHigh:
		return 4
	case schema.SeverityMedium:
		return 3
	case schema.SeverityLow:
		return 2
	case schema.SeverityInfo:
		return 1
	}
	return 0
}

// loadTaskContract reads a JSON task contract from a file. Its contents are
// treated downstream as an untrusted statement of intent, never ground truth.
func loadTaskContract(path string) (*schema.TaskContract, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, schema.NewError(schema.CodeInvalidArgument, "cannot read task file: %v", err)
	}
	var tc schema.TaskContract
	if err := json.Unmarshal(b, &tc); err != nil {
		return nil, schema.NewError(schema.CodeInvalidArgument, "task file is not valid JSON: %v", err)
	}
	return &tc, nil
}

// gatherInstructions resolves instructions from positional args or a file (these
// are mutually exclusive).
func gatherInstructions(positional []string, file string) (string, error) {
	text := strings.TrimSpace(strings.Join(positional, " "))
	if file != "" {
		if text != "" {
			return "", schema.NewError(schema.CodeInvalidArgument, "positional instructions and --instructions-file are mutually exclusive")
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return "", schema.NewError(schema.CodeInvalidArgument, "cannot read instructions file: %v", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return text, nil
}
