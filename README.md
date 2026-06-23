# CodeExpert

A **read-only** repository analysis service that produces high-quality software-engineering
guidance without ever modifying the repository. It runs both as a local
[Model Context Protocol](https://modelcontextprotocol.io) (MCP) server and as a command-line tool.

CodeExpert exposes exactly **two MCP tools**:

- `codeexpert_plan` — explores a repository and returns a detailed implementation plan, or
  focused engineering help (`mode="help"`).
- `codeexpert_review` — reviews a frozen set of Git changes and returns evidence-backed
  findings, coverage, and limitations.

It is deliberately more constrained than a general coding agent:

- It **never** edits source files, and never stages, commits, resets, checks out, rebases,
  merges, or changes Git refs — read-only **by construction**, not just by prompt.
- It never asks the user questions during a run.
- It treats all repository text (code, comments, tickets, commit messages, guidance files) as
  **untrusted evidence**, never as instructions.
- It returns structured, schema-validated output with exact repository evidence.
- Review is **precision-first**: "no findings" is a valid, successful result, and it never
  claims a change is safe to merge.

## Requirements

- Go 1.25+ (the default build is CGO-free).
- Git (required for review and for Git-aware planning; planning/help fall back to a filesystem
  snapshot in non-Git folders).
- An OpenAI-compatible model endpoint. The default targets **Sakana Fugu** via its Responses API.

## Build

```bash
go build -o codeexpert ./cmd/codeexpert
```

Cross-compilation is CGO-free:

```bash
CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -o codeexpert ./cmd/codeexpert
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o codeexpert.exe ./cmd/codeexpert
```

## Quick start

```bash
# 1. Write a config template (never writes a key).
./codeexpert init --provider sakana

# 2. Provide your provider key via the environment.
export SAKANA_API_KEY="sk-..."

# 3. Check your environment and provider connectivity.
./codeexpert doctor --probe

# 4. Plan, get help, or review.
./codeexpert plan   --root . "Add idempotent retries to invoice submission without changing the public API"
./codeexpert help   --root . "Why does the queue worker sometimes process a job twice?"
./codeexpert review --scope staged "Check migration rollback and backward compatibility"

# Optional: give review the change's intended behavior to check against. The
# contract is treated as an UNTRUSTED hypothesis, never ground truth.
./codeexpert review --scope staged --task-file ./task.json
```

### Register with Claude Code (or any MCP host)

```bash
claude mcp add --transport stdio codeexpert \
  --env SAKANA_API_KEY="$SAKANA_API_KEY" \
  -- codeexpert mcp --transport stdio
```

`stdout` is reserved exclusively for MCP messages; all logs go to `stderr`. CodeExpert also
honors `CLAUDE_PROJECT_DIR` as a default root and the MCP client's advertised roots.

## CLI

```
codeexpert init        Write a configuration template (.codeexpert.toml)
codeexpert mcp         Run the MCP server over stdio
codeexpert serve       Run the MCP server over Streamable HTTP (loopback by default)
codeexpert plan        Produce an implementation plan
codeexpert help        Diagnose a problem and recommend a direction
codeexpert review      Review Git changes
codeexpert index       Build/refresh the snapshot and print the inventory
codeexpert doctor      Check environment, config, and provider connectivity
codeexpert config print
codeexpert cache       status | gc | clear
codeexpert version
```

Review scopes: `working-tree` (default), `staged`, `unstaged`, `range` (`--base`/`--head`),
`commit` (`--commit`), `merge-base` (`--upstream`/`--head`).

Output: `--format markdown|json|both`, `--output FILE`, `--quiet`. By default review findings
do not change the exit code; CI users can set `--fail-on critical|high|medium` (exit 7).

Exit codes: `0` success (including no findings) · `2` bad args/config · `3` root/repo error ·
`4` provider error · `5` budget/timeout · `6` internal/validation · `7` `--fail-on` threshold met.

## Configuration

Resolved lowest-to-highest precedence: built-in defaults → user config
(`os.UserConfigDir()/CodeExpert/config.toml`) → project `<root>/.codeexpert.toml` →
`CODEEXPERT_*` environment variables → CLI/MCP arguments.

**Secrets are never stored in config, logs, cache, or reports** — only the *name* of the
environment variable holding the key is configured. To target a generic OpenAI-compatible
endpoint instead of Sakana, run `codeexpert init --provider generic` and edit `base_url`,
`api`, and `api_key_env`.

## How it works

A deterministic shell around a bounded probabilistic core:

1. **Snapshot** — freeze the repository (Git inventory or filesystem walk) into an immutable,
   content-addressed snapshot. Symlinks are not followed by default; reads are confined to the
   root.
2. **Retrieval ladder** — task anchors → exact lexical/path search → Go-semantic and heuristic
   symbol navigation → related tests/diffs. The smallest useful context is sent first; the whole
   repository is never sent by default.
3. **Bounded exploration** — the model calls a fixed set of **read-only** function tools
   (`repo_search`, `repo_read`, `repo_find_symbol`, `repo_git_diff`, …). There is no shell,
   exec, write, or Git-mutation tool. Every tool result carries an evidence ID.
4. **Structured synthesis** — a tool-free model call constrained to a JSON schema, validated
   **independently in Go** (paths exist, line ranges valid, step DAG acyclic, evidence cited),
   with one bounded repair attempt.
5. **Review pipeline** — deterministic risk map → independent candidate passes (diff-local,
   repository-context, risk-specialist) run concurrently → dedupe → verifier rejects unsupported
   candidates → deterministic publication gates and limits → ranked findings.

Every run is bounded (wall time, model calls, tool calls, files/bytes read, output size);
exhaustion yields a `partial` result with explicit limitations rather than an unbounded loop.

## Security

- Repository content is always labeled **untrusted**; the system prompt forbids following any
  instructions found in it. The model receives no write or general-command capability.
- Root containment uses canonical paths and rejects symlink escape and model-returned absolute
  paths. Git is invoked only via `exec.CommandContext` with argument arrays from a closed
  read-only allowlist — never shell strings.
- Configured checks (when enabled) run only in an isolated copy of the snapshot, never in the
  working tree, with an environment allowlist. Check commands must be literal argument arrays;
  shell metacharacters are rejected at config-validation time.
- The Streamable HTTP transport binds to loopback by default with Origin verification and
  DNS-rebinding protection; non-loopback binds require an auth token. Logs and check output are
  scrubbed for common secret patterns.

## Status / scope

Fully implemented: the two MCP tools (stdio + Streamable HTTP), the full CLI, configuration,
the OpenAI-compatible provider (Responses + Chat Completions, streaming, bounded retries),
Git/filesystem snapshots, the change manifest for all six review targets, lexical search, the
Go-AST + heuristic symbol index, related-test and co-changed-file retrieval, the evidence store
and citation validation, run-artifact persistence and resources, the plan/help and review
pipelines with validation and repair, and Markdown + JSON reporting.

Deferred (interfaces/config present, not active by default): the optional embeddings tier and
on-demand summaries; the sandboxed check **runner** (the policy, config, and `repo_run_check`
gating exist; `checks.mode` defaults to `off`); SCIP/LSP/Ctags adapters; OpenTelemetry traces
and metrics (structured logging is implemented); result memoization (the content-addressed cache
key exists, but identical snapshot+request+config+model runs are not yet served from a prior
result). Run artifacts use a filesystem content store rather than SQLite to keep the default
binary trivially CGO-free. The review finalizer ranks and phrases verified candidates
deterministically (it cannot introduce new findings).

## Testing

```bash
go test ./...          # unit + end-to-end pipeline tests (with a fake provider)
go test -race ./...
```

The suite includes a release-blocking **no-write invariant** test: a full plan + review run
must leave the repository and all Git state byte-for-byte unchanged.
