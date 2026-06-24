// Package cli implements the command-line interface. Commands map to the same
// engine the MCP server uses; `help` is codeexpert_plan with mode="help".
package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/gregpriday/codeexpert/internal/app"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// Exit codes per the specification.
const (
	exitOK            = 0
	exitInvalidArgs   = 2
	exitRootError     = 3
	exitProviderError = 4
	exitBudget        = 5
	exitInternal      = 6
	exitThreshold     = 7
)

// Run dispatches a subcommand and returns a process exit code. stdout is used
// for results; diagnostics go to stderr.
func Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return exitInvalidArgs
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println("codeexpert " + app.Version)
		return exitOK
	case "help", "--help", "-h":
		// Bare `help` with no further args prints usage; otherwise it is the
		// help workflow (codeexpert_help).
		if len(rest) == 0 {
			printUsage(os.Stdout)
			return exitOK
		}
		return cmdHelp(ctx, rest)
	case "init":
		return cmdInit(ctx, rest)
	case "mcp":
		return cmdMCP(ctx, rest)
	case "serve":
		return cmdServe(ctx, rest)
	case "plan":
		return cmdPlan(ctx, rest)
	case "review":
		return cmdReview(ctx, rest)
	case "index":
		return cmdIndex(ctx, rest)
	case "doctor":
		return cmdDoctor(ctx, rest)
	case "config":
		return cmdConfig(ctx, rest)
	case "cache":
		return cmdCache(ctx, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		printUsage(os.Stderr)
		return exitInvalidArgs
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `CodeExpert — read-only repository planning, help, and code review.

Usage:
  codeexpert <command> [flags] [instructions]

Commands:
  init        Write a configuration template (.codeexpert.toml)
  mcp         Run the MCP server over stdio
  serve       Run the MCP server over Streamable HTTP
  plan        Produce an implementation plan
  help        Answer an engineering question (explain, diagnose, decide, unblock)
  review      Review Git changes (working-tree, staged, range, commit, merge-base)
  index       Build/refresh the repository snapshot and report inventory
  doctor      Check environment, configuration, and provider connectivity
  config      Inspect or migrate configuration (config print|migrate)
  cache       Manage the cache (cache status|gc|clear)
  version     Print the version

Examples:
  codeexpert plan --root . "Add idempotent retries to invoice submission"
  codeexpert help "Why does the worker process some jobs twice?"
  codeexpert review --scope staged "Check migration rollback and compatibility"
  claude mcp add --transport stdio codeexpert -- codeexpert mcp --transport stdio

Environment:
  Set the provider API key in the configured variable (default SAKANA_API_KEY).
  CLAUDE_PROJECT_DIR is honored as a default root.
`)
}

// exitCodeFor maps a typed error to a process exit code.
func exitCodeFor(err error) int {
	te := schema.AsToolError(err)
	switch te.Code {
	case schema.CodeInvalidArgument, schema.CodeConfigInvalid, schema.CodeConfigUnsupportedVersion:
		return exitInvalidArgs
	case schema.CodeRootNotFound, schema.CodeRootOutsideAllowed, schema.CodeRootSymlinkEscape,
		schema.CodeNotGitRepository, schema.CodeGitNotFound, schema.CodeGitRefInvalid,
		schema.CodeSnapshotChanged, schema.CodeSnapshotTooLarge:
		return exitRootError
	case schema.CodeProviderAuth, schema.CodeProviderRateLimit, schema.CodeProviderTimeout,
		schema.CodeProviderUnsupported, schema.CodeProviderSchema:
		return exitProviderError
	case schema.CodeBudgetExhausted:
		return exitBudget
	default:
		return exitInternal
	}
}

// fail prints an error to stderr and returns its exit code.
func fail(err error) int {
	te := schema.AsToolError(err)
	fmt.Fprintf(os.Stderr, "error: %s\n", te.Error())
	return exitCodeFor(err)
}
