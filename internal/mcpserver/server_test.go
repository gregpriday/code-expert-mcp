package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gregpriday/codeexpert/internal/cache"
	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/telemetry"
	"github.com/gregpriday/codeexpert/internal/workflow"
)

// TestServerConstructs ensures the SDK can infer input/output schemas for the
// large request/result types without panicking at registration.
func TestServerConstructs(t *testing.T) {
	d := Deps{
		Engine:  &workflow.Engine{Cfg: config.Defaults(), Log: telemetry.Nop()},
		Config:  config.Defaults(),
		Logger:  telemetry.Nop(),
		Version: "test",
	}
	s := New(d)
	if s == nil {
		t.Fatal("expected a server")
	}
}

// TestRegisteredMetadata connects an in-memory client to the live server and
// asserts the metadata clients actually receive over the wire: both tools must
// advertise read-only, open-world, non-idempotent hints, and both run-artifact
// resource templates must carry a description. This guards the full registration
// path, not just the def helpers, so a future refactor that bypasses them is
// caught.
func TestRegisteredMetadata(t *testing.T) {
	ctx := context.Background()

	c, err := cache.Open(config.CacheConfig{Enabled: true, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	d := Deps{
		Engine:  &workflow.Engine{Cfg: config.Defaults(), Log: telemetry.Nop(), Cache: c},
		Config:  config.Defaults(),
		Logger:  telemetry.Nop(),
		Version: "test",
	}
	server := New(d)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer serverSession.Close()
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer clientSession.Close()

	t.Run("tool annotations", func(t *testing.T) {
		res, err := clientSession.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		seen := map[string]bool{}
		for _, tool := range res.Tools {
			seen[tool.Name] = true
			a := tool.Annotations
			if a == nil {
				t.Errorf("%s: annotations must not be nil", tool.Name)
				continue
			}
			if !a.ReadOnlyHint {
				t.Errorf("%s: ReadOnlyHint must be true", tool.Name)
			}
			if a.OpenWorldHint == nil || !*a.OpenWorldHint {
				t.Errorf("%s: OpenWorldHint must be an explicit true", tool.Name)
			}
			if a.DestructiveHint != nil {
				t.Errorf("%s: DestructiveHint must be omitted on a read-only tool; got %v", tool.Name, *a.DestructiveHint)
			}
			if a.IdempotentHint {
				t.Errorf("%s: IdempotentHint must be false for non-deterministic tools", tool.Name)
			}
		}
		for _, want := range []string{"codeexpert_plan", "codeexpert_review"} {
			if !seen[want] {
				t.Errorf("tool %q was not registered", want)
			}
		}
	})

	t.Run("resource template descriptions", func(t *testing.T) {
		res, err := clientSession.ListResourceTemplates(ctx, nil)
		if err != nil {
			t.Fatalf("ListResourceTemplates: %v", err)
		}
		if len(res.ResourceTemplates) == 0 {
			t.Fatal("expected resource templates to be registered with the cache enabled")
		}
		for _, tmpl := range res.ResourceTemplates {
			if strings.TrimSpace(tmpl.Description) == "" {
				t.Errorf("template %q is missing a description", tmpl.URITemplate)
			}
			if !strings.Contains(tmpl.Description, "codeexpert_") {
				t.Errorf("template %q description should reference the originating tool; got %q", tmpl.URITemplate, tmpl.Description)
			}
		}
	})
}
