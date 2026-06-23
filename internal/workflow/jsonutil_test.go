package workflow

import (
	"testing"

	"github.com/gregpriday/codeexpert/internal/schema"
)

func TestExtractJSON(t *testing.T) {
	cases := map[string]bool{
		`{"a":1}`:                 true,
		"```json\n{\"a\":1}\n```": true,
		"Here is the result:\n{\"a\":[1,2,3]}\nok": true,
		"no json here":                     false,
		"```\n{\"nested\":{\"x\":1}}\n```": true,
	}
	for in, wantOK := range cases {
		got := extractJSON([]byte(in))
		if (got != nil) != wantOK {
			t.Errorf("extractJSON(%q) = %q, wantOK=%v", in, got, wantOK)
		}
	}
}

func TestDetectCycle(t *testing.T) {
	noCycle := []schema.PlanStep{
		{ID: "A", DependsOn: nil},
		{ID: "B", DependsOn: []string{"A"}},
		{ID: "C", DependsOn: []string{"A", "B"}},
	}
	if c := detectCycle(noCycle); c != "" {
		t.Errorf("unexpected cycle: %s", c)
	}
	cyclic := []schema.PlanStep{
		{ID: "A", DependsOn: []string{"C"}},
		{ID: "B", DependsOn: []string{"A"}},
		{ID: "C", DependsOn: []string{"B"}},
	}
	if c := detectCycle(cyclic); c == "" {
		t.Error("expected a cycle to be detected")
	}
}

func TestReviewComplexityHighRisk(t *testing.T) {
	if !isHighRiskPath("internal/auth/login.go") {
		t.Error("auth path should be high risk")
	}
	if isHighRiskPath("internal/util/strings.go") {
		t.Error("plain util path should not be high risk")
	}
}
