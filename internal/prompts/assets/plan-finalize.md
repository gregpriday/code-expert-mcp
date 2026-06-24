# Planning — Finalize Pass

Produce **only** the required structured implementation plan as JSON. You will be given a strict output schema; conform to it exactly. Output nothing outside the JSON.

No repository tools are available in this pass. Work solely from the evidence already gathered during exploration. If something was not established, treat it as an unknown rather than inventing it.

## Requirements for the plan

- **Repository-specific.** Reference the real files, symbols, and structures of this codebase. No generic, boilerplate, or hypothetical steps.
- **Ordered and dependency-aware.** Sequence the steps so each one can begin only after its prerequisites. Make dependencies between steps explicit.
- **Explicit about files and symbols.** For each step, name the concrete files and the functions, types, or other symbols it touches.
- **Explicit about tests and validation.** For each step, state how it will be tested and which validation commands (build, lint, type-check, test) confirm it. Do not leave steps unverified.
- **Clear about assumptions and unknowns.** State each assumption plainly. Where information is missing, record it as an unknown and include an investigation step rather than guessing.
- **Scoped to the request.** Address what was asked. Do not expand scope, add unrelated refactors, or gold-plate.
- **Traceable to every acceptance criterion.** Populate `traceability`: one entry per acceptance criterion the caller supplied, mapping it to the `step_ids` that implement it and the `tests` (validation descriptions or commands) that confirm it. Every criterion must map to at least one real step — a plan that leaves a criterion unimplemented is incomplete. If a criterion cannot be satisfied, do not omit it; record it with the blocking unknown instead.
- **No code patches.** Describe what to change and why. Do not include diffs, full file rewrites, or code blocks meant to be applied.

## Evidence grounding

Every claim about the repository — every file, symbol, behavior, dependency, or test you reference — must cite the evidence IDs gathered during exploration. If a claim has no supporting evidence, either drop it or convert it into an explicit assumption with an investigation step. Distinguish verified facts from inferences throughout.
