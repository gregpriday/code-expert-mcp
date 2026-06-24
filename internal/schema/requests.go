package schema

// PlanRequest is the input to codeexpert_plan: it always produces an
// implementation plan. Diagnosis and open questions are codeexpert_help.
type PlanRequest struct {
	Root         string          `json:"root,omitempty" jsonschema:"Absolute path to the repository root. Defaults to the client project root or working directory."`
	Instructions string          `json:"instructions" jsonschema:"The implementation task to plan. Required and non-empty."`
	Task         *TaskContract   `json:"task,omitempty" jsonschema:"Optional structured task contract."`
	Profile      AnalysisProfile `json:"profile,omitempty" jsonschema:"fast, balanced, or deep. Defaults to balanced."`
	Retrieval    RetrievalOpts   `json:"retrieval,omitempty"`
	Budget       Budget          `json:"budget,omitempty"`
	Output       OutputOpts      `json:"output,omitempty"`
}

// HelpRequest is the input to codeexpert_help: a single-turn engineering
// question from a smaller agent that needs an answer, a diagnosis, a decision,
// or an unblock. Question and TrustedConstraints are caller intent; everything
// in Context (and any repository content) is untrusted evidence.
type HelpRequest struct {
	Root               string          `json:"root,omitempty" jsonschema:"Absolute path to the repository root. Optional: help can answer without a repository."`
	Question           string          `json:"question" jsonschema:"The question to answer. Required and non-empty."`
	AnswerType         HelpAnswerType  `json:"answer_type,omitempty" jsonschema:"auto, explain, diagnose, decide, or unblock. Defaults to auto."`
	Task               *TaskContract   `json:"task,omitempty" jsonschema:"Optional structured task contract (untrusted intent)."`
	TrustedConstraints []string        `json:"trusted_constraints,omitempty" jsonschema:"Hard constraints from the caller, treated as intent (not repository ground truth)."`
	Context            HelpContext     `json:"context,omitempty"`
	AttemptedActions   []string        `json:"attempted_actions,omitempty" jsonschema:"What the caller already tried (untrusted evidence)."`
	RelevantPaths      []string        `json:"relevant_paths,omitempty" jsonschema:"Repository paths the caller believes are relevant."`
	Profile            AnalysisProfile `json:"profile,omitempty" jsonschema:"fast, balanced, or deep. Defaults to balanced."`
	Retrieval          RetrievalOpts   `json:"retrieval,omitempty"`
	Budget             Budget          `json:"budget,omitempty"`
	Output             OutputOpts      `json:"output,omitempty"`
}

// HelpContext carries the untrusted observations a caller attaches to a help
// question. None of it is treated as instructions or as repository ground truth.
type HelpContext struct {
	Symptoms         []string `json:"symptoms,omitempty"`
	Errors           []string `json:"errors,omitempty"`
	Logs             []string `json:"logs,omitempty"`
	Snippets         []string `json:"snippets,omitempty"`
	ExpectedBehavior string   `json:"expected_behavior,omitempty"`
	ActualBehavior   string   `json:"actual_behavior,omitempty"`
}

// ReviewRequest is the input to codeexpert_review.
type ReviewRequest struct {
	Root         string           `json:"root,omitempty" jsonschema:"Absolute path to the repository root."`
	Instructions string           `json:"instructions,omitempty" jsonschema:"Optional reviewer focus, e.g. idempotency and transaction boundaries."`
	Target       ReviewTarget     `json:"target,omitempty"`
	Task         *TaskContract    `json:"task,omitempty"`
	Profile      AnalysisProfile  `json:"profile,omitempty" jsonschema:"fast, balanced, or deep. Defaults to balanced."`
	Verification VerificationMode `json:"verification,omitempty" jsonschema:"off, safe, configured, or deep. Defaults to safe."`
	Policy       ReviewPolicy     `json:"policy,omitempty"`
	Retrieval    RetrievalOpts    `json:"retrieval,omitempty"`
	Budget       Budget           `json:"budget,omitempty"`
	Output       OutputOpts       `json:"output,omitempty"`
}

// ReviewTarget describes the Git change set to freeze and review.
type ReviewTarget struct {
	Type             ReviewTargetType `json:"type,omitempty" jsonschema:"working_tree, staged, unstaged, range, commit, or merge_base. Defaults to working_tree."`
	BaseRef          string           `json:"base_ref,omitempty"`
	HeadRef          string           `json:"head_ref,omitempty"`
	Commit           string           `json:"commit,omitempty"`
	UpstreamRef      string           `json:"upstream_ref,omitempty"`
	IncludeUntracked *bool            `json:"include_untracked,omitempty"`
}

// TaskContract is an optional structured statement of intent. Its contents are
// treated as intent evidence, never as ground truth.
type TaskContract struct {
	ID                 string   `json:"id,omitempty"`
	Title              string   `json:"title,omitempty"`
	Description        string   `json:"description,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	Constraints        []string `json:"constraints,omitempty"`
	NonGoals           []string `json:"non_goals,omitempty"`
	KnownFacts         []string `json:"known_facts,omitempty"`
	PriorPlan          string   `json:"prior_plan,omitempty"`
}

// RetrievalOpts overrides retrieval ladder behavior for a single run.
type RetrievalOpts struct {
	Include          []string `json:"include,omitempty" jsonschema:"Glob patterns to restrict analysis to. Must stay within root."`
	Exclude          []string `json:"exclude,omitempty" jsonschema:"Glob patterns to exclude from analysis."`
	MaxFiles         int      `json:"max_files,omitempty"`
	MaxFileReads     int      `json:"max_file_reads,omitempty"`
	MaxContextTokens int      `json:"max_context_tokens,omitempty"`
	EnableEmbeddings *bool    `json:"enable_embeddings,omitempty"`
}

// Budget bounds a single run. Zero fields fall back to configured defaults.
type Budget struct {
	TimeoutSeconds       int `json:"timeout_seconds,omitempty"`
	MaxModelCalls        int `json:"max_model_calls,omitempty"`
	MaxModelToolRounds   int `json:"max_model_tool_rounds,omitempty"`
	MaxInternalToolCalls int `json:"max_internal_tool_calls,omitempty"`
	MaxFilesRead         int `json:"max_files_read,omitempty"`
	MaxBytesRead         int `json:"max_bytes_read,omitempty"`
	MaxContextTokens     int `json:"max_context_tokens,omitempty"`
	MaxOutputTokens      int `json:"max_output_tokens,omitempty"`
	MaxCheckSeconds      int `json:"max_check_seconds,omitempty"`
}

// OutputOpts controls how a result is rendered.
type OutputOpts struct {
	Format       OutputFormat `json:"format,omitempty" jsonschema:"markdown, json, or both. Defaults to both."`
	IncludeTrace bool         `json:"include_trace,omitempty"`
}

// ReviewPolicy overrides publication thresholds for a single review.
type ReviewPolicy struct {
	MaxBlockingFindings int    `json:"max_blocking_findings,omitempty"`
	MaxTotalFindings    int    `json:"max_total_findings,omitempty"`
	MinimumEvidence     string `json:"minimum_evidence,omitempty"`
	IncludeStyle        bool   `json:"include_style,omitempty"`
	IncludePraise       bool   `json:"include_praise,omitempty"`
	IncludeGenerated    bool   `json:"include_generated,omitempty"`
}
