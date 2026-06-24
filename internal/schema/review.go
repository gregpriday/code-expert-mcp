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

// VerificationInfo records how a finding was checked. Confirmed is reserved for
// findings reproduced by an executed check; Status is the honest confirmation
// vocabulary for everything else, so a model-only judgment is never presented as
// "confirmed".
type VerificationInfo struct {
	Method    string             `json:"method"`
	Status    ConfirmationStatus `json:"status"`
	Confirmed bool               `json:"confirmed"`
	CheckID   string             `json:"check_id,omitempty"`
	Detail    string             `json:"detail,omitempty"`
}

// ConfirmationStatus is the honest strength of a finding's confirmation, derived
// deterministically from its (capped) evidence level.
type ConfirmationStatus string

const (
	ConfirmedByExecution ConfirmationStatus = "confirmed_by_execution" // a check reproduced it (evidence A)
	CorroboratedByCode   ConfirmationStatus = "corroborated_by_code"   // independent caller/def/test/analyzer (B)
	PlausibleCodePath    ConfirmationStatus = "plausible_code_path"    // a concrete code path supports it (C)
	Unconfirmed          ConfirmationStatus = "unconfirmed"            // model judgment only, material assumptions (D)
)

// ConfirmationFromEvidence maps a capped evidence level to its honest
// confirmation status.
func ConfirmationFromEvidence(level EvidenceLevel) ConfirmationStatus {
	switch level {
	case EvidenceExecutable:
		return ConfirmedByExecution
	case EvidenceTool:
		return CorroboratedByCode
	case EvidenceCodePath:
		return PlausibleCodePath
	default:
		return Unconfirmed
	}
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

// ReviewCoverage reports what was and was not reviewed. Files carries a terminal
// state for every changed file so coverage reflects what was demonstrably handled
// rather than mere manifest membership.
type ReviewCoverage struct {
	Files               []FileCoverage `json:"files,omitempty"`
	ReviewedFiles       []string       `json:"reviewed_files"`
	SkippedFiles        []SkippedFile  `json:"skipped_files,omitempty"`
	ChangedLineEstimate int            `json:"changed_line_estimate"`
	SpecialistPasses    []string       `json:"specialist_passes,omitempty"`
	ChecksRun           []string       `json:"checks_run,omitempty"`
	Unindexed           []string       `json:"unindexed,omitempty"`
	BudgetLimited       bool           `json:"budget_limited"`
}

// CoverageState is the terminal state of one changed file in a review.
type CoverageState string

const (
	CoverageReviewed    CoverageState = "reviewed"           // sent through the candidate passes
	CoverageExcluded    CoverageState = "excluded-by-policy" // generated/style excluded by config or policy
	CoverageUnsupported CoverageState = "unsupported-binary" // binary content the text passes cannot read
	CoverageVendored    CoverageState = "vendored"           // vendored dependency, out of scope
	CoverageTruncated   CoverageState = "truncated"          // reviewed, but its diff was capped for size
	CoverageFailed      CoverageState = "failed"             // a pass errored before covering it
)

// FileCoverage is the terminal review state of one changed file.
type FileCoverage struct {
	Path   string        `json:"path"`
	Status string        `json:"status,omitempty"` // git status letter (A/M/D/R/C)
	State  CoverageState `json:"state"`
	Reason string        `json:"reason,omitempty"`
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
