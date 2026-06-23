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
	data, err := json.Marshal(planToolDef().Annotations)
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
