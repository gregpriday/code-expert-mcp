package workflow

import (
	"strings"

	"github.com/gregpriday/codeexpert/internal/schema"
)

// Request validation runs at every engine entry point (and therefore for both
// the MCP and CLI paths). It rejects unknown enum values, conflicting target
// parameters, negative budgets, and request fields the runtime does not yet
// honor — so a caller can never silently believe it constrained or configured an
// analysis that the runtime ignored. Every failure is a CE_INVALID_ARGUMENT.

func validatePlanRequest(req schema.PlanRequest) error {
	if strings.TrimSpace(req.Instructions) == "" {
		return schema.NewError(schema.CodeInvalidArgument, "instructions must not be empty")
	}
	return firstErr(
		validateProfileValue(req.Profile),
		validateRetrieval(req.Retrieval),
		validateBudget(req.Budget),
		validateOutput(req.Output),
	)
}

func validateHelpRequest(req schema.HelpRequest) error {
	if strings.TrimSpace(req.Question) == "" {
		return schema.NewError(schema.CodeInvalidArgument, "question must not be empty")
	}
	return firstErr(
		validateAnswerType(req.AnswerType),
		validateProfileValue(req.Profile),
		validateRetrieval(req.Retrieval),
		validateBudget(req.Budget),
		validateOutput(req.Output),
	)
}

func validateReviewRequest(req schema.ReviewRequest) error {
	return firstErr(
		validateProfileValue(req.Profile),
		validateVerificationValue(req.Verification),
		validateReviewTarget(req.Target),
		validateReviewPolicy(req.Policy),
		validateRetrieval(req.Retrieval),
		validateBudget(req.Budget),
		validateOutput(req.Output),
	)
}

func validateProfileValue(p schema.AnalysisProfile) error {
	switch p {
	case "", schema.ProfileFast, schema.ProfileBalanced, schema.ProfileDeep:
		return nil
	}
	return schema.NewError(schema.CodeInvalidArgument, "profile %q is not one of fast, balanced, deep", p)
}

func validateAnswerType(a schema.HelpAnswerType) error {
	switch a {
	case "", schema.AnswerAuto, schema.AnswerExplain, schema.AnswerDiagnose, schema.AnswerDecide, schema.AnswerUnblock:
		return nil
	}
	return schema.NewError(schema.CodeInvalidArgument, "answer_type %q is not one of auto, explain, diagnose, decide, unblock", a)
}

func validateVerificationValue(v schema.VerificationMode) error {
	switch v {
	case "", schema.VerifyOff, schema.VerifySafe, schema.VerifyConfigured, schema.VerifyDeep:
		return nil
	}
	return schema.NewError(schema.CodeInvalidArgument, "verification %q is not one of off, safe, configured, deep", v)
}

func validateOutput(o schema.OutputOpts) error {
	switch o.Format {
	case "", schema.FormatMarkdown, schema.FormatJSON, schema.FormatBoth:
		return nil
	}
	return schema.NewError(schema.CodeInvalidArgument, "output.format %q is not one of markdown, json, both", o.Format)
}

// validateRetrieval rejects the retrieval overrides that have no honored
// implementation yet (rather than silently ignoring them), and bounds the ones
// that do. Include/Exclude/EnableEmbeddings/MaxFiles depend on the retrieval
// ladder that is being rebuilt; accepting them would be a lie.
func validateRetrieval(r schema.RetrievalOpts) error {
	if len(r.Include) > 0 {
		return unsupported("retrieval.include")
	}
	if len(r.Exclude) > 0 {
		return unsupported("retrieval.exclude")
	}
	if r.MaxFiles != 0 {
		return unsupported("retrieval.max_files")
	}
	if r.EnableEmbeddings != nil {
		return unsupported("retrieval.enable_embeddings")
	}
	if r.MaxFileReads < 0 {
		return negative("retrieval.max_file_reads")
	}
	if r.MaxContextTokens < 0 {
		return negative("retrieval.max_context_tokens")
	}
	return nil
}

func validateBudget(b schema.Budget) error {
	for _, f := range []struct {
		name string
		val  int
	}{
		{"budget.timeout_seconds", b.TimeoutSeconds},
		{"budget.max_model_calls", b.MaxModelCalls},
		{"budget.max_model_tool_rounds", b.MaxModelToolRounds},
		{"budget.max_internal_tool_calls", b.MaxInternalToolCalls},
		{"budget.max_files_read", b.MaxFilesRead},
		{"budget.max_bytes_read", b.MaxBytesRead},
		{"budget.max_context_tokens", b.MaxContextTokens},
		{"budget.max_output_tokens", b.MaxOutputTokens},
		{"budget.max_check_seconds", b.MaxCheckSeconds},
	} {
		if f.val < 0 {
			return negative(f.name)
		}
	}
	return nil
}

func validateReviewPolicy(p schema.ReviewPolicy) error {
	if p.MaxTotalFindings < 0 {
		return negative("policy.max_total_findings")
	}
	// A blocker is never demoted or dropped to fit a count, so a blocking-findings
	// cap would silently do nothing. Reject it rather than ignore it.
	if p.MaxBlockingFindings != 0 {
		return unsupported("policy.max_blocking_findings")
	}
	if p.IncludePraise {
		return unsupported("policy.include_praise")
	}
	switch strings.ToLower(strings.TrimSpace(p.MinimumEvidence)) {
	case "", "a", "b", "c", "d", "executable", "tool-supported", "tool", "code-path", "speculative":
		return nil
	}
	return schema.NewError(schema.CodeInvalidArgument,
		"policy.minimum_evidence %q is not one of A, B, C, D (or executable, tool-supported, code-path, speculative)", p.MinimumEvidence)
}

// validateReviewTarget rejects target parameters that conflict with the selected
// target type, so a caller cannot believe it scoped the review to a range while
// the runtime reviews the working tree.
func validateReviewTarget(t schema.ReviewTarget) error {
	switch t.Type {
	case "", schema.TargetWorkingTree, schema.TargetStaged, schema.TargetUnstaged,
		schema.TargetRange, schema.TargetCommit, schema.TargetMergeBase:
	default:
		return schema.NewError(schema.CodeInvalidArgument,
			"target.type %q is not one of working_tree, staged, unstaged, range, commit, merge_base", t.Type)
	}
	conflict := func(field, val string, allowed ...schema.ReviewTargetType) error {
		if strings.TrimSpace(val) == "" {
			return nil
		}
		for _, a := range allowed {
			if t.Type == a {
				return nil
			}
		}
		return schema.NewError(schema.CodeInvalidArgument,
			"target.%s is only valid when target.type is %s", field, joinTargets(allowed))
	}
	if err := conflict("commit", t.Commit, schema.TargetCommit); err != nil {
		return err
	}
	if err := conflict("base_ref", t.BaseRef, schema.TargetRange); err != nil {
		return err
	}
	if err := conflict("head_ref", t.HeadRef, schema.TargetRange, schema.TargetMergeBase); err != nil {
		return err
	}
	if err := conflict("upstream_ref", t.UpstreamRef, schema.TargetMergeBase); err != nil {
		return err
	}
	if t.Commit == "" && t.Type == schema.TargetCommit {
		return schema.NewError(schema.CodeInvalidArgument, "target.type commit requires target.commit")
	}
	return nil
}

func joinTargets(ts []schema.ReviewTargetType) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = string(t)
	}
	return strings.Join(parts, " or ")
}

func unsupported(field string) error {
	return schema.NewError(schema.CodeInvalidArgument,
		"%s is not supported by this build; omit it rather than relying on it", field)
}

func negative(field string) error {
	return schema.NewError(schema.CodeInvalidArgument, "%s must not be negative", field)
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
