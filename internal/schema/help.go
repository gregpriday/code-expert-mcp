package schema

// HelpReport is the structured output in help mode.
type HelpReport struct {
	ProblemRestatement   string              `json:"problem_restatement"`
	ObservedEvidence     []EvidenceStatement `json:"observed_evidence"`
	LikelyCauses         []CauseHypothesis   `json:"likely_causes"`
	RecommendedDirection string              `json:"recommended_direction"`
	InvestigationSteps   []InvestigationStep `json:"investigation_steps"`
	ValidationSteps      []ValidationStep    `json:"validation_steps"`
	Alternatives         []Alternative       `json:"alternatives"`
	Risks                []Risk              `json:"risks"`
	Assumptions          []Assumption        `json:"assumptions"`
	Confidence           Confidence          `json:"confidence"`
}

// CauseHypothesis is a candidate root cause with separated fact vs inference.
type CauseHypothesis struct {
	Hypothesis  string     `json:"hypothesis"`
	Likelihood  Confidence `json:"likelihood"`
	Verified    bool       `json:"verified"`
	EvidenceIDs []string   `json:"evidence_ids,omitempty"`
	Reasoning   string     `json:"reasoning,omitempty"`
}

// InvestigationStep is a concrete next action to confirm or refute a cause.
type InvestigationStep struct {
	Action      string `json:"action"`
	Where       string `json:"where,omitempty"`
	Expectation string `json:"expectation,omitempty"`
}
