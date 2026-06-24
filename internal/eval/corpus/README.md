# Eval corpus

Each `*.json` file is one evaluation case: a request for one capability (`plan`,
`help`, or `review`) plus the deterministic `expect`ations its result must meet.

Run the suite with:

    codeexpert eval --cases internal/eval/corpus --root /path/to/target/repo

`--root` overrides every case's repository root, so the example cases here use a
`REPLACE_WITH_REPO_ROOT` placeholder you can point at any repository. Add
`--judge` to include the model-as-judge grader, and `--min-pass 0.95` to gate CI
on the pass-rate.

These four files are a seed, not the benchmark. A representative suite is built
by adding many real cases — including clean diffs and seeded defects for review,
and adversarial cases (prompt injection, path escapes, oversized inputs) — and
turning every production failure into a regression case here.
