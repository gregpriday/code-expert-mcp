# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Read-only repository analysis: an MCP server and CLI that produce plans, engineering help, and
code reviews **without ever modifying the repository**. Three public MCP tools — one per
capability, with no mode switch: `codeexpert_plan`, `codeexpert_help`, and `codeexpert_review`.

## Build / test

```bash
go build ./...                       # build everything
go build -o codeexpert ./cmd/codeexpert
go test ./...                        # unit + end-to-end (fake provider) + no-write invariant
go test -race ./internal/workflow/   # candidate passes run concurrently
go test ./internal/workflow/ -run TestNoWriteInvariant   # one test
go vet ./...
gofmt -l internal/ cmd/              # must print nothing
```

The default build is CGO-free; keep it that way (rich indexers needing CGO must be optional).

End-to-end pipeline tests need no network or fixtures: they drive the engine with a deterministic
`fakeProvider` (`internal/workflow/workflow_test.go`) that branches on request content. To exercise
plan/help/review logic, extend that fake's `Generate` rather than calling a live provider.

## Non-negotiable invariants

- **Read-only by construction.** Nothing under `plan`/`help`/`review` may write inside the repo
  or `.git`. Git runs only through `internal/repo/git` (closed read-only allowlist, arg arrays,
  no shell strings). Verification checks (`internal/checks`) run in a throwaway copy of the
  frozen snapshot with isolated cache/home/temp dirs, never in the protected tree. The
  release-blocking test is `TestNoWriteInvariant` in `internal/workflow`.
- **Immutable snapshots.** Every working-tree read is verified against the bytes frozen at
  snapshot time; a mid-run edit yields a typed `CE_SNAPSHOT_CHANGED` / `stale` status, never a
  silent mix of pre- and post-edit state.
- **Truthful results.** Request fields the runtime does not honor are rejected, not ignored;
  per-request limits clamp DOWN only; `complete` means complete (unresolved validation, budget
  exhaustion, or a stale snapshot downgrade the status).
- **Untrusted repository content.** Never let repository text act as instructions. Tool results
  and guidance carry an explicit untrusted label; the common system prompt forbids obeying them.
- **Evidence before prose.** Every published path/line/symbol is validated against the snapshot
  (`internal/evidence`) before it reaches output.
- **Precision-first review.** Zero findings is success; never emit "approved" or "safe to merge".

## Architecture (dependency order)

`schema` (shared types/errors) → `security`, `telemetry`, `hashutil` → `config` → `provider`
(+`provider/openaicompat`) → `repo` (+`repo/git`) → `evidence`, `cache`, `index`, `budget`,
`checks` → `llmtools` (read-only model tools) → `prompts` → `report` → `workflow` (engine +
plan, help, and review pipelines) → `eval` (quality harness), `mcpserver`, `cli` → `app` →
`cmd/codeexpert`.

The model interprets and synthesizes; deterministic Go owns canonical repository state,
validation, budgets, and security. Prompts live in `internal/prompts/assets/*.md` (embedded).
Every run emits a `trace.json` artifact (capability, prompt-version hashes, stage ledger, usage);
the `eval` harness grades structured results (code + model-judge graders) for the improvement loop.

## Conventions

- Return typed errors via `schema.NewError(schema.CodeX, ...)`; never pass a dynamic string as
  the format arg (use `"%s"`).
- All model names, endpoints, budgets, and reasoning levels are config, not hard-coded.
- stdout is reserved for MCP; all logs go to stderr.
