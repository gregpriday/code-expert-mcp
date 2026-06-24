package schema

// HelpReport is the structured output of codeexpert_help. It is direct-answer
// first: DirectAnswer is the answer a stuck agent reads first; the remaining
// sections are supporting detail. Only a diagnosis needs ranked LikelyCauses; an
// explain/decide/unblock answer leaves them empty rather than forcing a
// root-cause template.
type HelpReport struct {
	DirectAnswer          string              `json:"direct_answer"`
	AnswerType            HelpAnswerType      `json:"answer_type,omitempty"`
	ProblemRestatement    string              `json:"problem_restatement,omitempty"`
	VerifiedFacts         []EvidenceStatement `json:"verified_facts,omitempty"`
	Inferences            []string            `json:"inferences,omitempty"`
	ObservedEvidence      []EvidenceStatement `json:"observed_evidence,omitempty"`
	LikelyCauses          []CauseHypothesis   `json:"likely_causes,omitempty"`
	RecommendedNextAction string              `json:"recommended_next_action,omitempty"`
	RecommendedDirection  string              `json:"recommended_direction,omitempty"`
	InvestigationSteps    []InvestigationStep `json:"investigation_steps,omitempty"`
	ValidationSteps       []ValidationStep    `json:"validation_steps,omitempty"`
	Alternatives          []Alternative       `json:"alternatives,omitempty"`
	Risks                 []Risk              `json:"risks,omitempty"`
	Assumptions           []Assumption        `json:"assumptions,omitempty"`
	Confidence            Confidence          `json:"confidence"`
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
