# Review — Repository Context Pass

This is a review candidate pass with repository context. In addition to the diff and the changed symbols, you are given supporting context: relevant definitions, references and call sites, related tests, configuration, and any project guidance or conventions.

## Goal

Use the broader context to identify defects that are invisible from the diff alone — cross-file and contract-level problems:

- **Cross-file inconsistencies** — the change conflicts with how a symbol is defined or used elsewhere, breaks an assumption that callers rely on, or updates one side of a relationship but not the other.
- **Contract violations** — the change breaks an interface, signature, return-value expectation, error contract, or invariant that other code depends on.
- **Caller and callee mismatches** — arguments, types, ordering, nullability, or units that no longer line up across call sites.
- **Test and configuration gaps** — behavior changed without corresponding test updates, or configuration and code that have drifted out of agreement.
- **Convention and guidance violations** — the change contradicts documented project conventions or guidance present in the context.

## Output

Produce a **candidate** issue list only — not publishable comments. Later passes verify and filter these.

- Ground each candidate in the provided context: cite the specific definitions, references, tests, or configuration that reveal the problem, using repository paths and evidence IDs.
- For each candidate, explain when it triggers and the consequence.
- Favor recall: surface plausible cross-file and contract defects even if not fully certain, and note what remaining evidence would confirm them.
- Do not rank, merge, or finalize. Output the raw candidates with their supporting evidence.
