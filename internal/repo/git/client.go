// Package git is a strictly read-only adapter over the installed Git CLI. Every
// command is invoked with exec.CommandContext and an explicit argument array;
// shell strings are never constructed. The set of permitted subcommands is fixed
// and excludes anything that mutates the repository or Git refs.
package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/gregpriday/codeexpert/internal/schema"
)

// allowedSubcommands is the closed allowlist of read-only Git operations.
var allowedSubcommands = map[string]bool{
	"rev-parse":    true,
	"status":       true,
	"diff":         true,
	"show":         true,
	"cat-file":     true,
	"ls-files":     true,
	"ls-tree":      true,
	"log":          true,
	"merge-base":   true,
	"check-ignore": true,
	"symbolic-ref": true,
	"for-each-ref": true,
	"archive":      true,
}

// Client runs read-only Git commands within a fixed working directory.
type Client struct {
	dir     string
	gitPath string
}

// New constructs a Client rooted at dir. It returns CE_GIT_NOT_FOUND if the git
// executable is unavailable.
func New(dir string) (*Client, error) {
	path, err := exec.LookPath("git")
	if err != nil {
		return nil, schema.NewError(schema.CodeGitNotFound, "git executable not found in PATH")
	}
	return &Client{dir: dir, gitPath: path}, nil
}

// Available reports whether git is installed.
func Available() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// run executes a read-only git subcommand and returns stdout. The first argument
// must be an allowlisted subcommand; anything else is refused before exec.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, schema.NewError(schema.CodeInternal, "git: empty argument list")
	}
	if !allowedSubcommands[args[0]] {
		return nil, schema.NewError(schema.CodeInternal, "git subcommand %q is not on the read-only allowlist", args[0])
	}
	// -c protocol.* hardening and disabling any external/global config that could
	// run hooks or external diff tools. We prepend safety flags before the
	// subcommand.
	full := append([]string{
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "diff.external=",
		"--no-pager",
	}, args...)
	cmd := exec.CommandContext(ctx, c.gitPath, full...)
	cmd.Dir = c.dir
	// Minimal, deterministic environment. We pass through PATH so git finds its
	// helpers but suppress prompts and locale-dependent output.
	cmd.Env = append([]string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"LC_ALL=C",
	}, passthroughEnv()...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.Bytes(), &commandError{args: args, stderr: strings.TrimSpace(stderr.String()), err: err}
		}
		return nil, schema.NewError(schema.CodeInternal, "git %s: %v", args[0], err)
	}
	return stdout.Bytes(), nil
}

// runText is run() returning a trimmed string.
func (c *Client) runText(ctx context.Context, args ...string) (string, error) {
	out, err := c.run(ctx, args...)
	return strings.TrimSpace(string(out)), err
}

type commandError struct {
	args   []string
	stderr string
	err    error
}

func (e *commandError) Error() string {
	if e.stderr != "" {
		return "git " + e.args[0] + ": " + e.stderr
	}
	return "git " + e.args[0] + ": " + e.err.Error()
}

func passthroughEnv() []string {
	// Keep PATH and HOME so git can locate itself and credential-less config.
	keep := []string{"PATH", "HOME", "SystemRoot", "USERPROFILE", "APPDATA", "ProgramData"}
	var out []string
	for _, k := range keep {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}
