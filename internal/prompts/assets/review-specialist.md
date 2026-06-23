# Review — Risk Specialist Pass

This is a risk-specialist review pass. Based on the change's risk map, you are told which specialist categories to focus on. Apply deep, category-specific scrutiny to the change and its context — do not spread attention across categories you were not assigned.

## Specialist categories

Focus only on the categories you are directed to examine:

- **Security / trust boundary** — untrusted input crossing a boundary without validation, injection, authentication or authorization gaps, secret handling, unsafe deserialization, path or command construction.
- **Concurrency / ordering** — race conditions, unsynchronized shared state, lock misuse or ordering, atomicity violations, assumptions about execution or message order.
- **Data integrity / rollback** — partial writes, missing transactions, non-atomic multi-step updates, failure paths that leave data inconsistent, broken rollback or recovery.
- **Compatibility** — breaking changes to public interfaces, serialized formats, schemas, protocols, configuration keys, or persisted data; migration gaps.
- **Performance / resources** — added hot-path cost, unbounded growth, N+1 patterns, leaks of memory, handles, or connections, missing limits or backpressure.
- **Reliability** — failure handling under partial outages, timeouts, retries, idempotency, error recovery, and degraded-mode behavior.
- **Test effectiveness** — whether tests actually exercise the risky behavior, or pass without covering the change's failure modes.

## Output

Produce a **candidate** issue list only — not publishable comments.

- For each candidate, name the specialist category, the precise trigger condition, and the concrete impact within that category.
- Cite specific code, context, and evidence IDs supporting the concern.
- Favor recall within your assigned categories; the verifier enforces precision later. Note any assumption or missing evidence per candidate. Do not rank, merge, or finalize.
