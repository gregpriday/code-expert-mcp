package schema

// ReviewFinding is a single published review finding. Severity, blocking
// status, evidence level, and model confidence are deliberately separate.
type ReviewFinding struct {
	ID              string           `json:"id"`
	Title           string           `json:"title"`
	Category        FindingCategory  `json:"category"`
	Severity        Severity         `json:"severity"`
	Blocking        bool             `json:"blocking"`
	EvidenceLevel   EvidenceLevel    `json:"evidence_level"`
	Location        SourceLocation   `json:"location"`
	Claim           string           `json:"claim"`
	Trigger         string           `json:"trigger"`
	Impact          string           `json:"impact"`
	Evidence        []EvidenceRef    `json:"evidence"`
	Recommendation  string           `json:"recommendation"`
	Verification    VerificationInfo `json:"verification"`
	Assumptions     []string         `json:"assumptions,omitempty"`
	RelatedFindings []string         `json:"related_finding_ids,omitempty"`
}

// SourceLocation is a precise, validated repository location.
type SourceLocation struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
}

// VerificationInfo records how a finding was checked.
type VerificationInfo struct {
	Method    string `json:"method"`
	Confirmed bool   `json:"confirmed"`
	CheckID   string `json:"check_id,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// ReviewSummary is the high-level review outcome.
type ReviewSummary struct {
	Headline        string   `json:"headline"`
	TotalFindings   int      `json:"total_findings"`
	BlockingCount   int      `json:"blocking_count"`
	FilesReviewed   int      `json:"files_reviewed"`
	FilesChanged    int      `json:"files_changed"`
	HighestSeverity Severity `json:"highest_severity,omitempty"`
	Conclusion      string   `json:"conclusion"`
}

// RiskArea is a deterministically-derived focus area for the review.
type RiskArea struct {
	Category  FindingCategory `json:"category"`
	Rationale string          `json:"rationale"`
	Paths     []string        `json:"paths,omitempty"`
	Priority  int             `json:"priority"`
}

// ReviewCoverage reports what was and was not reviewed.
type ReviewCoverage struct {
	ReviewedFiles       []string      `json:"reviewed_files"`
	SkippedFiles        []SkippedFile `json:"skipped_files,omitempty"`
	ChangedLineEstimate int           `json:"changed_line_estimate"`
	SpecialistPasses    []string      `json:"specialist_passes,omitempty"`
	ChecksRun           []string      `json:"checks_run,omitempty"`
	Unindexed           []string      `json:"unindexed,omitempty"`
	BudgetLimited       bool          `json:"budget_limited"`
}

// SkippedFile records a file omitted from review and why.
type SkippedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// CheckResult records the outcome of one configured check.
type CheckResult struct {
	CheckID     string  `json:"check_id"`
	Name        string  `json:"name"`
	ExitCode    int     `json:"exit_code"`
	DurationSec float64 `json:"duration_seconds"`
	Summary     string  `json:"summary"`
	OutputURI   string  `json:"output_uri,omitempty"`
	Passed      bool    `json:"passed"`
}

// SuppressionStats reports candidates that did not survive the gates.
type SuppressionStats struct {
	TotalCandidates int            `json:"total_candidates"`
	Published       int            `json:"published"`
	Suppressed      int            `json:"suppressed"`
	ByReason        map[string]int `json:"by_reason,omitempty"`
}
