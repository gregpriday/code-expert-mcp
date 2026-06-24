// Package checks runs pre-approved verification commands in an ISOLATED copy of
// the frozen repository, never inside the protected working tree or .git. This is
// what lets a review "execute" a check (earning evidence level A) without
// violating the read-only invariant: builds and test suites write caches and
// artifacts, so they run against a throwaway materialization with isolated cache,
// home, and temp directories. Commands are argv arrays from trusted configuration
// — there is no shell, and the executable must be a bare PATH command.
package checks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
	"github.com/gregpriday/codeexpert/internal/telemetry"
)

// Runner executes the verification commands appropriate to a mode in isolation.
type Runner struct {
	snap *repo.Snapshot
	cfg  config.ChecksConfig
	mode schema.VerificationMode
	log  *telemetry.Logger
}

// NewRunner builds a runner for a frozen snapshot. The effective mode is the
// request's mode, falling back to the configured checks mode when unset.
func NewRunner(snap *repo.Snapshot, cfg config.ChecksConfig, mode schema.VerificationMode, log *telemetry.Logger) *Runner {
	if log == nil {
		log = telemetry.Nop()
	}
	eff := mode
	if eff == "" {
		eff = schema.VerificationMode(cfg.Mode)
	}
	return &Runner{snap: snap, cfg: cfg, mode: eff, log: log}
}

// Enabled reports whether any check would run (so callers can skip the
// materialization entirely when nothing applies).
func (r *Runner) Enabled() bool { return len(r.applicable()) > 0 }

// applicable returns the commands to run for the effective mode:
//   - off:        none (code analysis only)
//   - safe:       only commands flagged Safe (read-only analyzers)
//   - configured: every enabled command
//   - deep:       every enabled command (with the larger total-time budget)
func (r *Runner) applicable() []config.CheckCommand {
	if r.mode == schema.VerifyOff || r.mode == "" {
		return nil
	}
	var out []config.CheckCommand
	for _, c := range r.cfg.Command {
		if !c.Enabled || len(c.Argv) == 0 {
			continue
		}
		switch r.mode {
		case schema.VerifySafe:
			if c.Safe {
				out = append(out, c)
			}
		case schema.VerifyConfigured, schema.VerifyDeep:
			out = append(out, c)
		}
	}
	return out
}

// Run materializes an isolated copy of the frozen snapshot and runs the
// applicable checks in it, honoring an overall time budget. It returns one
// CheckResult per executed check and an EvidenceRecord (kind=check, evidence
// level A) for each, plus the bounded combined output as the record summary. It
// never writes inside the protected repository.
func (r *Runner) Run(ctx context.Context, maxTotal time.Duration) ([]schema.CheckResult, []schema.EvidenceRecord, error) {
	cmds := r.applicable()
	if len(cmds) == 0 {
		return nil, nil, nil
	}
	dir, cleanup, err := r.materialize(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()

	env := isolatedEnv(dir, r.cfg.Network)
	overall := time.Now().Add(maxTotal)
	if maxTotal <= 0 {
		overall = time.Now().Add(20 * time.Minute)
	}

	var (
		results []schema.CheckResult
		evid    []schema.EvidenceRecord
	)
	for _, c := range cmds {
		if time.Now().After(overall) || ctx.Err() != nil {
			break
		}
		res, err := runOne(ctx, dir, env, c)
		if err != nil {
			r.log.Warn("check skipped", "name", c.Name, "err", err.Error())
			continue
		}
		results = append(results, res)
		status := "passed"
		if !res.Passed {
			status = fmt.Sprintf("failed (exit %d)", res.ExitCode)
		}
		evid = append(evid, schema.EvidenceRecord{
			Kind:       schema.EvidenceKindCheck,
			SnapshotID: r.snap.ID(),
			Summary:    fmt.Sprintf("check %q %s: %s", c.Name, status, res.Summary),
			Provenance: schema.Provenance{Tool: "checks", Stage: "verification", Query: strings.Join(c.Argv, " ")},
		})
	}
	return results, evid, nil
}

// materialize copies the frozen snapshot's files into a fresh temp directory. It
// reads through the snapshot (which verifies content against the frozen hash) and
// writes only into the temp tree — the protected repository is never touched.
func (r *Runner) materialize(ctx context.Context) (string, func(), error) {
	dir, err := os.MkdirTemp("", "codeexpert-checks-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	for _, fm := range r.snap.ListFiles() {
		fc, rerr := r.snap.ReadFile(ctx, fm.Path)
		if rerr != nil {
			continue
		}
		dst := filepath.Join(dir, filepath.FromSlash(fm.Path))
		if !withinDir(dir, dst) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			continue
		}
		_ = os.WriteFile(dst, fc.Bytes, 0o644)
	}
	return dir, cleanup, nil
}

// runOne executes a single pre-approved command in the materialized tree.
func runOne(ctx context.Context, dir string, env []string, c config.CheckCommand) (schema.CheckResult, error) {
	if len(c.Argv) == 0 {
		return schema.CheckResult{}, fmt.Errorf("empty argv")
	}
	if strings.ContainsAny(c.Argv[0], "/\\") {
		// The executable must be a bare PATH command so a binary inside the repo
		// can never be invoked relative to the process working directory.
		return schema.CheckResult{}, fmt.Errorf("check %q: executable %q must be a bare command name", c.Name, c.Argv[0])
	}
	timeout := c.Timeout.Std()
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cwd := dir
	if c.Cwd != "" {
		cwd = filepath.Join(dir, filepath.FromSlash(c.Cwd))
		if !withinDir(dir, cwd) {
			return schema.CheckResult{}, fmt.Errorf("check %q: cwd escapes the materialized tree", c.Name)
		}
	}
	cmd := exec.CommandContext(cctx, c.Argv[0], c.Argv[1:]...)
	cmd.Dir = cwd
	cmd.Env = env

	start := time.Now()
	out, err := cmd.CombinedOutput()
	dur := time.Since(start)

	exit := 0
	passed := err == nil
	switch e := err.(type) {
	case nil:
	case *exec.ExitError:
		exit = e.ExitCode()
	default:
		exit = -1
	}
	name := c.Name
	if name == "" {
		name = c.Argv[0]
	}
	return schema.CheckResult{
		CheckID:     name,
		Name:        name,
		ExitCode:    exit,
		DurationSec: dur.Seconds(),
		Summary:     boundedOutput(out, 2000),
		Passed:      passed,
	}, nil
}

// isolatedEnv builds the command environment: the inherited PATH plus cache,
// home, and temp directories redirected into the throwaway tree, so a build or
// test writes its caches there rather than in the user's home. Network is
// disabled (Go module proxy off) unless the configuration explicitly allows it.
func isolatedEnv(dir string, network bool) []string {
	sub := func(name string) string {
		p := filepath.Join(dir, name)
		_ = os.MkdirAll(p, 0o755)
		return p
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + sub(".home"),
		"TMPDIR=" + sub(".tmp"),
		"GOCACHE=" + sub(".gocache"),
		"GOMODCACHE=" + sub(".gomodcache"),
		"GOPATH=" + sub(".gopath"),
		"GOFLAGS=-mod=mod",
	}
	if !network {
		env = append(env, "GOPROXY=off", "GONOSUMCHECK=1", "GIT_TERMINAL_PROMPT=0")
	}
	return env
}

// boundedOutput trims trailing whitespace and caps the captured output so a
// noisy check cannot bloat the result or evidence.
func boundedOutput(b []byte, max int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= max {
		return s
	}
	return s[:max] + "…(truncated)"
}

// withinDir reports whether path is contained in root after cleaning, guarding
// against a cwd or destination that escapes the materialized tree.
func withinDir(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
