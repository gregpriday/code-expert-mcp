package cli

import "github.com/gregpriday/codeexpert/internal/schema"

// CLI enum validators. Each returns a CE_INVALID_ARGUMENT error (exit code 2)
// with a precise message instead of letting an unknown value silently fall back.

func validateProfile(s string) error {
	switch s {
	case "", "fast", "balanced", "deep":
		return nil
	}
	return schema.NewError(schema.CodeInvalidArgument, "invalid --profile %q; must be fast, balanced, or deep", s)
}

func validateFormat(s string) error {
	switch s {
	case "markdown", "json", "both":
		return nil
	}
	return schema.NewError(schema.CodeInvalidArgument, "invalid --format %q; must be markdown, json, or both", s)
}

func validateScope(s string) error {
	switch s {
	case "working-tree", "staged", "unstaged", "range", "commit", "merge-base":
		return nil
	}
	return schema.NewError(schema.CodeInvalidArgument,
		"invalid --scope %q; must be working-tree, staged, unstaged, range, commit, or merge-base", s)
}

func validateVerification(s string) error {
	switch s {
	case "", "off", "safe", "configured", "deep":
		return nil
	}
	return schema.NewError(schema.CodeInvalidArgument, "invalid --verification %q; must be off, safe, configured, or deep", s)
}

func validateFailOn(s string) error {
	switch s {
	case "", "critical", "high", "medium":
		return nil
	}
	return schema.NewError(schema.CodeInvalidArgument, "invalid --fail-on %q; must be critical, high, or medium", s)
}

func validateProviderName(s string) error {
	switch s {
	case "openai", "sakana", "generic":
		return nil
	}
	return schema.NewError(schema.CodeInvalidArgument, "invalid --provider %q; must be openai, sakana, or generic", s)
}

func validateAPIName(s string) error {
	switch s {
	case "responses", "chat-completions":
		return nil
	}
	return schema.NewError(schema.CodeInvalidArgument, "invalid --api %q; must be responses or chat-completions", s)
}
