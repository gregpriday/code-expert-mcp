package mcpserver

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gregpriday/codeexpert/internal/cache"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// resourceTemplateDef describes one run-artifact resource template. Descriptions
// are surfaced to MCP clients as hints, so they say what the artifact is and how
// to obtain it.
type resourceTemplateDef struct {
	uri  string
	mime string
	name string
	desc string
}

// resourceTemplates returns the run-artifact resource templates the server
// exposes. Defined as a function so the wiring can be asserted in tests without
// constructing a full server.
func resourceTemplates() []resourceTemplateDef {
	return []resourceTemplateDef{
		{
			uri:  "codeexpert://runs/{run_id}/report",
			mime: "text/markdown",
			name: "Run report (Markdown)",
			desc: "Markdown report for a completed CodeExpert run. Use to retrieve the formatted plan, help, or review output after calling codeexpert_plan or codeexpert_review.",
		},
		{
			uri:  "codeexpert://runs/{run_id}/result.json",
			mime: "application/json",
			name: "Run result (JSON)",
			desc: "JSON result for a completed CodeExpert run. Use to retrieve the structured, machine-readable output after calling codeexpert_plan or codeexpert_review.",
		},
	}
}

// registerResources exposes run artifacts as read-only MCP resources. Access
// enforces the same run-store path containment as the originating run.
func registerResources(s *mcp.Server, d Deps) {
	if d.Engine == nil || d.Engine.Cache == nil || !d.Engine.Cache.Enabled() {
		return
	}
	for _, t := range resourceTemplates() {
		s.AddResourceTemplate(&mcp.ResourceTemplate{
			URITemplate: t.uri,
			Name:        t.name,
			Description: t.desc,
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
