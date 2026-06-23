// Package schema defines the canonical, provider-neutral data types that flow
// through CodeExpert: tool requests and results, plans, reviews, evidence, and
// the typed error model. These types are the contract every other internal
// package compiles against. They are deliberately free of behavior so they can
// be marshaled to MCP structured content and JSON reports without coupling to
// any subsystem.
package schema

// PlanMode selects between a full implementation plan and focused engineering
// help. Help is exposed through codeexpert_plan with mode="help".
type PlanMode string

const (
	PlanModePlan PlanMode = "plan"
	PlanModeHelp PlanMode = "help"
)

// AnalysisProfile controls retrieval depth and model routing.
type AnalysisProfile string

const (
	ProfileFast     AnalysisProfile = "fast"
	ProfileBalanced AnalysisProfile = "balanced"
	ProfileDeep     AnalysisProfile = "deep"
)

// RunStatus is the terminal (or near-terminal) state of a run.
type RunStatus string

const (
	StatusComplete  RunStatus = "complete"
	StatusPartial   RunStatus = "partial"
	StatusFailed    RunStatus = "failed"
	StatusCancelled RunStatus = "cancelled"
	StatusStale     RunStatus = "stale"
)

// ReviewTargetType identifies which Git change set is under review.
type ReviewTargetType string

const (
	TargetWorkingTree ReviewTargetType = "working_tree"
	TargetStaged      ReviewTargetType = "staged"
	TargetUnstaged    ReviewTargetType = "unstaged"
	TargetRange       ReviewTargetType = "range"
	TargetCommit      ReviewTargetType = "commit"
	TargetMergeBase   ReviewTargetType = "merge_base"
)

// VerificationMode controls whether external commands may run during review.
type VerificationMode string

const (
	VerifyOff        VerificationMode = "off"
	VerifySafe       VerificationMode = "safe"
	VerifyConfigured VerificationMode = "configured"
	VerifyDeep       VerificationMode = "deep"
)

// OutputFormat selects the rendering of a result for CLI consumers.
type OutputFormat string

const (
	FormatMarkdown OutputFormat = "markdown"
	FormatJSON     OutputFormat = "json"
	FormatBoth     OutputFormat = "both"
)

// Severity is the engineering impact of a review finding. It is intentionally
// independent of Blocking and EvidenceLevel.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

// FindingCategory groups review findings by the kind of defect.
type FindingCategory string

const (
	CategoryCorrectness   FindingCategory = "correctness"
	CategorySecurity      FindingCategory = "security"
	CategoryDataIntegrity FindingCategory = "data_integrity"
	CategoryConcurrency   FindingCategory = "concurrency"
	CategoryCompatibility FindingCategory = "compatibility"
	CategoryPerformance   FindingCategory = "performance"
	CategoryReliability   FindingCategory = "reliability"
	CategoryTesting       FindingCategory = "testing"
	CategoryStyle         FindingCategory = "style"
)

// EvidenceLevel grades how strongly a claim is supported, from executable proof
// (A) to speculative (D). Publication policy keys off this.
type EvidenceLevel string

const (
	EvidenceExecutable  EvidenceLevel = "A" // reproduced by a deterministic command or test
	EvidenceTool        EvidenceLevel = "B" // compiler/analyzer/schema/reference tooling supports it
	EvidenceCodePath    EvidenceLevel = "C" // a concrete repository path supports it, not executable
	EvidenceSpeculative EvidenceLevel = "D" // material assumptions remain
)

// EvidenceKind labels the source of an evidence record.
type EvidenceKind string

const (
	EvidenceKindFile     EvidenceKind = "file"
	EvidenceKindDiff     EvidenceKind = "diff"
	EvidenceKindSymbol   EvidenceKind = "symbol"
	EvidenceKindSearch   EvidenceKind = "search"
	EvidenceKindCheck    EvidenceKind = "check"
	EvidenceKindHistory  EvidenceKind = "history"
	EvidenceKindGuidance EvidenceKind = "guidance"
)

// Confidence is a coarse qualitative confidence used in help reports.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)
