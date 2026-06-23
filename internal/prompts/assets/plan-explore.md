# Planning — Exploration Pass

Your task in this pass is to explore the repository with the read-only tools and gather the evidence needed to build a strong implementation plan later. You are **not** producing the final plan yet. Produce targeted findings and the evidence that supports them.

## What to identify

Using the read-only repository tools, investigate and document:

- **Current behavior** — how the relevant area works today, including the entry points, key functions, and control flow that the request would touch.
- **Likely change points** — the specific files and symbols that probably need to be modified, added, or removed to satisfy the request.
- **Dependencies** — internal modules, external libraries, configuration, and data that the affected code relies on or that rely on it.
- **Relevant tests** — existing tests that cover the area, the testing conventions in use, and gaps where new coverage would be needed.
- **Validation commands** — how this project builds, lints, type-checks, and runs its tests, so steps can be validated later.
- **Major risks** — areas where a change is likely to break behavior, surprise callers, or have wide blast radius.
- **Compatibility constraints** — public interfaces, serialized formats, schemas, configuration keys, or contracts that must not change without care.

## How to work

- Be bounded and efficient. Start from the most relevant files and follow concrete references; do not crawl the whole repository.
- Make each tool call earn its place. Gather just enough evidence to support each finding, then stop.
- Record concrete evidence: repository paths, symbol names, and the specific lines or snippets that justify each claim.
- Separate confirmed facts from inferences. Where you must infer, say so and note what would confirm it.
- Do not write a polished, structured final plan here. Output the raw, evidence-backed findings that the finalize pass will turn into a plan.
