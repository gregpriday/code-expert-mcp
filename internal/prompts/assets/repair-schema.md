# Repair — Schema Validation Failure

Your previous structured output failed schema validation. You are now given the specific validation errors and the relevant sections of your prior output. Your task is to return corrected JSON that conforms to the schema.

## Rules

- **Fix only the reported problems.** Address exactly the validation errors you were given. Do not rewrite, re-reason, or re-plan unrelated parts of the output.
- **Do not invent new content.** Repairing the structure must not add new findings, steps, causes, files, symbols, or claims that were not already present and supported. This is a structural correction, not a fresh analysis.
- **Preserve everything that was already valid.** Carry over the parts that passed validation unchanged.

## Common errors and how to fix them

- **Invalid file paths** — correct the path to one that actually exists in the repository. If you cannot confirm the path is real, remove the claim or clearly mark it as unverified rather than guessing a plausible-looking path.
- **Out-of-range line numbers** — adjust the line reference to a valid range for the cited file. If the correct line cannot be determined, drop the specific line reference rather than fabricating one.
- **Broken step dependencies** — fix references so each step depends only on steps that exist and that precede it; remove cycles and dangling dependency IDs.
- **Missing validation steps** — add the required validation field using validation already established in the gathered evidence; do not invent commands that the repository does not support.
- **Type, enum, or required-field errors** — coerce values to the schema's expected types, allowed enum values, and required fields, keeping the original meaning.

## When a claim cannot be validated

If a cited path or symbol cannot be confirmed to exist, **remove it or clearly mark it as unverified** rather than fabricating a substitute. It is always better to drop an unsupported claim than to invent one to satisfy the schema.

Return only the corrected JSON, conforming exactly to the provided schema. Output nothing outside the JSON.
