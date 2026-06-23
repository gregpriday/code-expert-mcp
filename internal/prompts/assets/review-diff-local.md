# Review — Local Diff Pass

This is a review candidate pass. You are given the diff, the enclosing changed symbols (the functions or types the changes live in), and a minimal task contract describing what the change is meant to do. You do **not** have broader repository context in this pass.

## Goal

Catch obvious defects that are visible from the change itself, without needing cross-file context:

- **Logic errors** — wrong conditions, inverted comparisons, off-by-one mistakes, incorrect operators, unreachable or dead branches, mishandled edge cases.
- **Validation gaps** — missing or incorrect input validation, unchecked assumptions about arguments, null/empty/boundary handling.
- **Error handling defects** — swallowed errors, missing error checks, wrong error propagation, resources not released, incorrect failure behavior.

## Output

Produce a **candidate** issue list — not publishable comments. These candidates will be verified and filtered in later passes, so:

- **Favor recall over precision.** If something looks wrong or suspicious, raise it as a candidate. Precision is enforced by the verifier later.
- For each candidate, point to the specific changed line(s) and explain the suspected defect, when it would trigger, and its likely consequence.
- Because you lack broader context, **record any assumption you are relying on** (for example, about how a called function behaves or what a value can be). Mark these assumptions as unsupported.
- **Request targeted evidence** for anything you could not confirm from the diff alone — name the file, symbol, or test that would settle the question.

Do not polish, rank, or deduplicate yet. Surface the raw candidates with their assumptions and evidence requests.
