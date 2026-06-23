package mcpserver

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gregpriday/codeexpert/internal/cache"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// registerResources exposes run artifacts as read-only MCP resources. Access
// enforces the same run-store path containment as the originating run.
func registerResources(s *mcp.Server, d Deps) {
	if d.Engine == nil || d.Engine.Cache == nil || !d.Engine.Cache.Enabled() {
		return
	}
	templates := []struct {
		uri  string
		mime string
		name string
	}{
		{"codeexpert://runs/{run_id}/report", "text/markdown", "Run report (Markdown)"},
		{"codeexpert://runs/{run_id}/result.json", "application/json", "Run result (JSON)"},
		{"codeexpert://runs/{run_id}/evidence/{evidence_id}", "application/json", "Run evidence record"},
	}
	for _, t := range templates {
		s.AddResourceTemplate(&mcp.ResourceTemplate{
			URITemplate: t.uri,
			Name:        t.name,
			MIMEType:    t.mime,
		}, makeResourceHandler(d))
	}
}

func makeResourceHandler(d Deps) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := req.Params.URI
		runID, name, ok := cache.ParseURI(uri)
		if !ok {
			return nil, schema.NewError(schema.CodeInvalidArgument, "unrecognized resource URI %q", uri)
		}
		data, found := d.Engine.Cache.ReadResource(runID, name)
		if !found {
			return nil, schema.NewError(schema.CodeRootNotFound, "resource %q not found or expired", uri)
		}
		mime := "text/plain"
		switch {
		case strings.HasSuffix(name, ".json"):
			mime = "application/json"
		case name == "report":
			mime = "text/markdown"
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      uri,
				MIMEType: mime,
				Text:     string(data),
			}},
		}, nil
	}
}
