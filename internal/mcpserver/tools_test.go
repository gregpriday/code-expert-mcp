package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestToolAnnotationsAreReadOnlyOpenWorld locks the hint values both tools
// advertise. They are read-only but reach the filesystem, git, and a remote
// model provider (open world), and they are non-deterministic (not idempotent).
func TestToolAnnotationsAreReadOnlyOpenWorld(t *testing.T) {
	cases := []struct {
		name string
		tool *mcp.Tool
	}{
		{"plan", planToolDef()},
		{"help", helpToolDef()},
		{"review", reviewToolDef()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := c.tool.Annotations
			if a == nil {
				t.Fatal("annotations must not be nil")
			}
			if !a.ReadOnlyHint {
				t.Error("ReadOnlyHint must be true: the tools never modify the repository")
			}
			if a.OpenWorldHint == nil || !*a.OpenWorldHint {
				t.Error("OpenWorldHint must be an explicit true: the tools reach git, the filesystem, and a remote model provider")
			}
			if a.DestructiveHint != nil {
				t.Errorf("DestructiveHint must be omitted (nil) on a read-only tool; got %v", *a.DestructiveHint)
			}
			if a.IdempotentHint {
				t.Error("IdempotentHint must be false: the tools are non-deterministic and the hint is meaningless when ReadOnlyHint is true")
			}
		})
	}
}

// TestToolAnnotationsWireFormat guards the on-the-wire JSON: readOnlyHint and
// openWorldHint are present and true, while destructiveHint and idempotentHint
// are absent (omitempty), so clients never see contradictory hints.
func TestToolAnnotationsWireFormat(t *testing.T) {
	defs := map[string]*mcp.Tool{
		"plan":   planToolDef(),
		"help":   helpToolDef(),
		"review": reviewToolDef(),
	}
	for name, tool := range defs {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(tool.Annotations)
			if err != nil {
				t.Fatalf("marshal annotations: %v", err)
			}
			got := string(data)
			for _, want := range []string{`"readOnlyHint":true`, `"openWorldHint":true`} {
				if !strings.Contains(got, want) {
					t.Errorf("annotations JSON %s missing %s", got, want)
				}
			}
			for _, absent := range []string{"destructiveHint", "idempotentHint"} {
				if strings.Contains(got, absent) {
					t.Errorf("annotations JSON %s must omit %s", got, absent)
				}
			}
		})
	}
}

// TestThreePublicToolsAreDistinct locks the public surface to exactly three
// unambiguous tools — plan, help, review — with distinct names and descriptions
// that cross-reference each other, so a tool-selecting model is never forced to
// pick a mode to reach one of the three core capabilities.
func TestThreePublicToolsAreDistinct(t *testing.T) {
	tools := []*mcp.Tool{planToolDef(), helpToolDef(), reviewToolDef()}
	names := map[string]bool{}
	for _, tl := range tools {
		if !strings.HasPrefix(tl.Name, "codeexpert_") {
			t.Errorf("tool name %q must be namespaced codeexpert_*", tl.Name)
		}
		if names[tl.Name] {
			t.Errorf("duplicate tool name %q", tl.Name)
		}
		names[tl.Name] = true
		if strings.TrimSpace(tl.Description) == "" {
			t.Errorf("tool %q has no description", tl.Name)
		}
		if strings.Contains(strings.ToLower(tl.Description), `mode="`) {
			t.Errorf("tool %q description still references a mode switch: %q", tl.Name, tl.Description)
		}
	}
	for _, want := range []string{"codeexpert_plan", "codeexpert_help", "codeexpert_review"} {
		if !names[want] {
			t.Errorf("missing public tool %q", want)
		}
	}
	// Each description should steer away from the other two tools.
	if !strings.Contains(planToolDef().Description, "codeexpert_help") {
		t.Error("plan description should point ambiguous diagnosis questions at codeexpert_help")
	}
	if !strings.Contains(helpToolDef().Description, "codeexpert_plan") {
		t.Error("help description should point full-plan requests at codeexpert_plan")
	}
}

// TestResourceTemplatesHaveDescriptions ensures every exposed run-artifact
// template carries a client-facing description that points back to the tool
// that produces it.
func TestResourceTemplatesHaveDescriptions(t *testing.T) {
	tmpls := resourceTemplates()
	if len(tmpls) == 0 {
		t.Fatal("expected at least one resource template")
	}
	for _, tmpl := range tmpls {
		if strings.TrimSpace(tmpl.desc) == "" {
			t.Errorf("template %q is missing a description", tmpl.uri)
		}
		if !strings.Contains(tmpl.desc, "codeexpert_") {
			t.Errorf("template %q description should reference the originating tool; got %q", tmpl.uri, tmpl.desc)
		}
	}
}
