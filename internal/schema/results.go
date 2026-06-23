package schema

// PlanResult is the structured output of codeexpert_plan.
type PlanResult struct {
	RunID       string              `json:"run_id"`
	Kind        PlanMode            `json:"kind"`
	Status      RunStatus           `json:"status"`
	Snapshot    SnapshotRef         `json:"snapshot"`
	Request     InterpretedTask     `json:"request"`
	Repository  RepositoryBrief     `json:"repository"`
	Plan        *ImplementationPlan `json:"plan,omitempty"`
	Help        *HelpReport         `json:"help,omitempty"`
	Evidence    []EvidenceRef       `json:"evidence"`
	Limitations []Limitation        `json:"limitations"`
	Usage       RunUsage            `json:"usage"`
	ReportURI   string              `json:"report_uri,omitempty"`
	Markdown    string              `json:"markdown,omitempty"`
}

// ReviewResult is the structured output of codeexpert_review.
type ReviewResult struct {
	RunID       string           `json:"run_id"`
	Status      RunStatus        `json:"status"`
	Snapshot    ReviewSnapshot   `json:"snapshot"`
	Summary     ReviewSummary    `json:"summary"`
	RiskMap     []RiskArea       `json:"risk_map"`
	Findings    []ReviewFinding  `json:"findings"`
	Coverage    ReviewCoverage   `json:"coverage"`
	Checks      []CheckResult    `json:"checks"`
	Suppressed  SuppressionStats `json:"suppressed"`
	Limitations []Limitation     `json:"limitations"`
	Usage       RunUsage         `json:"usage"`
	ReportURI   string           `json:"report_uri,omitempty"`
	Markdown    string           `json:"markdown,omitempty"`
}

// SnapshotRef identifies the immutable input frozen for a plan/help run.
type SnapshotRef struct {
	SnapshotID string `json:"snapshot_id"`
	Root       string `json:"root"`
	IsGit      bool   `json:"is_git"`
	HeadSHA    string `json:"head_sha,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Dirty      bool   `json:"dirty"`
	FileCount  int    `json:"file_count"`
}

// ReviewSnapshot identifies the frozen base/head views for a review.
type ReviewSnapshot struct {
	SnapshotID string `json:"snapshot_id"`
	Root       string `json:"root"`
	Target     string `json:"target"`
	BaseSHA    string `json:"base_sha,omitempty"`
	HeadSHA    string `json:"head_sha,omitempty"`
	BaseLabel  string `json:"base_label,omitempty"`
	HeadLabel  string `json:"head_label,omitempty"`
}

// InterpretedTask is the normalized understanding of the request.
type InterpretedTask struct {
	Goal               string   `json:"goal"`
	Mode               PlanMode `json:"mode"`
	Constraints        []string `json:"constraints,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	NonGoals           []string `json:"non_goals,omitempty"`
	SearchAnchors      []string `json:"search_anchors,omitempty"`
	ExplicitPaths      []string `json:"explicit_paths,omitempty"`
}

// RepositoryBrief is the deterministic orientation summary.
type RepositoryBrief struct {
	Root           string         `json:"root"`
	IsGit          bool           `json:"is_git"`
	Branch         string         `json:"branch,omitempty"`
	HeadSHA        string         `json:"head_sha,omitempty"`
	Dirty          bool           `json:"dirty"`
	TrackedFiles   int            `json:"tracked_files"`
	UntrackedFiles int            `json:"untracked_files"`
	Languages      []LanguageStat `json:"languages"`
	Manifests      []string       `json:"manifests,omitempty"`
	GuidanceFiles  []string       `json:"guidance_files,omitempty"`
	BuildSystems   []string       `json:"build_systems,omitempty"`
	TestSystems    []string       `json:"test_systems,omitempty"`
	SourceDirs     []string       `json:"source_dirs,omitempty"`
	TestDirs       []string       `json:"test_dirs,omitempty"`
}

// LanguageStat reports a detected language and its weight.
type LanguageStat struct {
	Language string  `json:"language"`
	Files    int     `json:"files"`
	Bytes    int64   `json:"bytes"`
	Indexer  string  `json:"indexer,omitempty"`
	Share    float64 `json:"share"`
}

// Limitation explains a gap in coverage or confidence.
type Limitation struct {
	Stage   string `json:"stage,omitempty"`
	Message string `json:"message"`
}

// RunUsage records provider/orchestration accounting for a run.
type RunUsage struct {
	ModelCalls          int            `json:"model_calls"`
	InternalToolCalls   int            `json:"internal_tool_calls"`
	FilesRead           int            `json:"files_read"`
	BytesRead           int64          `json:"bytes_read"`
	InputTokens         int            `json:"input_tokens"`
	CachedInputTokens   int            `json:"cached_input_tokens,omitempty"`
	OutputTokens        int            `json:"output_tokens"`
	OrchestrationInTok  int            `json:"orchestration_input_tokens,omitempty"`
	OrchestrationOutTok int            `json:"orchestration_output_tokens,omitempty"`
	WallSeconds         float64        `json:"wall_seconds"`
	CacheHits           int            `json:"cache_hits"`
	ModelsUsed          []string       `json:"models_used,omitempty"`
	ProviderRequestIDs  []string       `json:"provider_request_ids,omitempty"`
	Extra               map[string]int `json:"extra,omitempty"`
}

// EvidenceRef is a compact pointer to an evidence record used in outputs.
type EvidenceRef struct {
	ID        string       `json:"id"`
	Kind      EvidenceKind `json:"kind"`
	Path      string       `json:"path,omitempty"`
	StartLine int          `json:"start_line,omitempty"`
	EndLine   int          `json:"end_line,omitempty"`
	Symbol    string       `json:"symbol,omitempty"`
	Summary   string       `json:"summary,omitempty"`
	URI       string       `json:"uri,omitempty"`
}
