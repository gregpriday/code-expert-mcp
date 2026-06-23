package report

import (
	"encoding/json"

	"github.com/gregpriday/codeexpert/internal/schema"
)

// PlanJSON renders a PlanResult as pretty-printed JSON with two-space indent.
func PlanJSON(res schema.PlanResult) (string, error) {
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ReviewJSON renders a ReviewResult as pretty-printed JSON with two-space indent.
func ReviewJSON(res schema.ReviewResult) (string, error) {
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
