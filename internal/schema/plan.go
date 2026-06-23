package schema

// ImplementationPlan is the structured plan returned in plan mode.
type ImplementationPlan struct {
	Goal                string              `json:"goal"`
	CurrentBehavior     []EvidenceStatement `json:"current_behavior"`
	Constraints         []string            `json:"constraints"`
	Assumptions         []Assumption        `json:"assumptions"`
	ImpactedAreas       []ImpactedArea      `json:"impacted_areas"`
	RecommendedApproach string              `json:"recommended_approach"`
	Steps               []PlanStep          `json:"steps"`
	TestPlan            []ValidationStep    `json:"test_plan"`
	Compatibility       []string            `json:"compatibility"`
	Rollout             []string            `json:"rollout,omitempty"`
	Risks               []Risk              `json:"risks"`
	Alternatives        []Alternative       `json:"alternatives"`
	OpenQuestions       []OpenQuestion      `json:"open_questions"`
	DefinitionOfDone    []string            `json:"definition_of_done"`
	AgentHandoff        string              `json:"agent_handoff"`
}

// PlanStep is one ordered, dependency-aware unit of implementation work.
type PlanStep struct {
	ID              string           `json:"id"`
	Title           string           `json:"title"`
	Objective       string           `json:"objective"`
	DependsOn       []string         `json:"depends_on"`
	Files           []FileTarget     `json:"files"`
	Symbols         []SymbolTarget   `json:"symbols,omitempty"`
	DetailedChanges []string         `json:"detailed_changes"`
	Invariants      []string         `json:"invariants"`
	Validation      []ValidationStep `json:"validation"`
	Risks           []string         `json:"risks"`
	EvidenceIDs     []string         `json:"evidence_ids"`
}

// EvidenceStatement is a claim about the repository tied to evidence.
type EvidenceStatement struct {
	Statement   string   `json:"statement"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// Assumption records something assumed in lieu of asking the user.
type Assumption struct {
	Statement   string `json:"statement"`
	Rationale   string `json:"rationale,omitempty"`
	Investigate string `json:"investigate,omitempty"`
}

// ImpactedArea names a subsystem the change touches.
type ImpactedArea struct {
	Area        string   `json:"area"`
	Paths       []string `json:"paths,omitempty"`
	Description string   `json:"description,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

// FileTarget points at a file the step modifies or relies on.
type FileTarget struct {
	Path   string `json:"path"`
	Action string `json:"action,omitempty"` // modify | create | reference | delete
	Note   string `json:"note,omitempty"`
}

// SymbolTarget points at a symbol the step touches.
type SymbolTarget struct {
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Heuristic bool   `json:"heuristic,omitempty"`
}

// ValidationStep is a concrete way to verify a step or the whole plan.
type ValidationStep struct {
	Description string `json:"description"`
	Command     string `json:"command,omitempty"`
	Expectation string `json:"expectation,omitempty"`
}

// Risk is a hazard with optional mitigation.
type Risk struct {
	Description string   `json:"description"`
	Severity    Severity `json:"severity,omitempty"`
	Mitigation  string   `json:"mitigation,omitempty"`
}

// Alternative is a different approach considered.
type Alternative struct {
	Approach string `json:"approach"`
	Tradeoff string `json:"tradeoff,omitempty"`
}

// OpenQuestion is an unresolved question turned into an investigation.
type OpenQuestion struct {
	Question     string `json:"question"`
	WhyItMatters string `json:"why_it_matters,omitempty"`
	Investigate  string `json:"investigate,omitempty"`
}
