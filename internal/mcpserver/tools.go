package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gregpriday/codeexpert/internal/schema"
	"github.com/gregpriday/codeexpert/internal/workflow"
)

const planToolDescription = `Produce a detailed, evidence-backed implementation plan for a coding task: change points, dependencies, tests, validation commands, risks, and a step-by-step handoff another agent can follow. Read-only: never modifies the repository. Use this when you know WHAT to build and need a grounded plan for HOW. For diagnosing a failure or answering an open engineering question, use codeexpert_help instead; to review existing changes, use codeexpert_review.`

const helpToolDescription = `Answer a single, focused engineering question against a repository: explain how something works, diagnose why it is failing, decide between options, or unblock a stuck agent. Returns a direct answer first, with evidence, ranked hypotheses (for diagnosis), and a recommended next action. Read-only. Use this when you are stuck or need understanding — not to produce a full implementation plan (use codeexpert_plan) or to review a diff (use codeexpert_review).`

const reviewToolDescription = `Review a frozen set of Git changes and return evidence-backed findings, coverage, and limitations. Precision-first: returns no findings when nothing meets the publication threshold, and never claims a change is safe to merge. Read-only. Use this to critique an existing diff — not to plan new work (codeexpert_plan) or to answer a question (codeexpert_help).`

// readOnlyAnnotations returns the annotation set shared by both tools. They
// never modify the repository (ReadOnlyHint), but they reach the filesystem,
// git, and a remote model provider, so they operate in an open world. The model
// also varies run to run, so the calls are not idempotent. DestructiveHint and
// IdempotentHint are omitted entirely: the spec defines both only when
// ReadOnlyHint is false, so emitting them on a read-only tool is just noise.
func readOnlyAnnotations() *mcp.ToolAnnotations {
	openWorld := true
	return &mcp.ToolAnnotations{
		ReadOnlyHint:  true,
		OpenWorldHint: &openWorld,
	}
}

func planToolDef() *mcp.Tool {
	return &mcp.Tool{
		Name:        "codeexpert_plan",
		Title:       "CodeExpert Plan",
		Description: planToolDescription,
		Annotations: readOnlyAnnotations(),
	}
}

func helpToolDef() *mcp.Tool {
	return &mcp.Tool{
		Name:        "codeexpert_help",
		Title:       "CodeExpert Help",
		Description: helpToolDescription,
		Annotations: readOnlyAnnotations(),
	}
}

func reviewToolDef() *mcp.Tool {
	return &mcp.Tool{
		Name:        "codeexpert_review",
		Title:       "CodeExpert Review",
		Description: reviewToolDescription,
		Annotations: readOnlyAnnotations(),
	}
}

func registerTools(s *mcp.Server, d Deps) {
	mcp.AddTool(s, planToolDef(), func(ctx context.Context, req *mcp.CallToolRequest, in schema.PlanRequest) (*mcp.CallToolResult, schema.PlanResult, error) {
		opts := workflow.RunOptions{
			AllowedRoots: rootsFromRequest(ctx, req),
			Progress:     progressFromRequest(ctx, req),
		}
		res, err := d.Engine.Plan(ctx, in, opts)
		if err != nil {
			d.Logger.Warn("plan tool error", "code", schema.AsToolError(err).Code)
			return nil, schema.PlanResult{}, toolError(err)
		}
		return &mcp.CallToolResult{Content: textContent(planTextSummary(res))}, res, nil
	})

	mcp.AddTool(s, helpToolDef(), func(ctx context.Context, req *mcp.CallToolRequest, in schema.HelpRequest) (*mcp.CallToolResult, schema.PlanResult, error) {
		opts := workflow.RunOptions{
			AllowedRoots: rootsFromRequest(ctx, req),
			Progress:     progressFromRequest(ctx, req),
		}
		res, err := d.Engine.Help(ctx, in, opts)
		if err != nil {
			d.Logger.Warn("help tool error", "code", schema.AsToolError(err).Code)
			return nil, schema.PlanResult{}, toolError(err)
		}
		return &mcp.CallToolResult{Content: textContent(planTextSummary(res))}, res, nil
	})

	mcp.AddTool(s, reviewToolDef(), func(ctx context.Context, req *mcp.CallToolRequest, in schema.ReviewRequest) (*mcp.CallToolResult, schema.ReviewResult, error) {
		opts := workflow.RunOptions{
			AllowedRoots: rootsFromRequest(ctx, req),
			Progress:     progressFromRequest(ctx, req),
		}
		res, err := d.Engine.Review(ctx, in, opts)
		if err != nil {
			d.Logger.Warn("review tool error", "code", schema.AsToolError(err).Code)
			return nil, schema.ReviewResult{}, toolError(err)
		}
		return &mcp.CallToolResult{Content: textContent(reviewTextSummary(res))}, res, nil
	})
}

// toolError converts an internal error into one the SDK renders as an isError
// tool result. It returns the structured *schema.ToolError directly: that type
// implements error, and its Error method already renders the typed code (and
// stage, when present) so the host model can react, while preserving the code,
// stage, and details that a fmt.Errorf wrapper would discard.
func toolError(err error) error {
	return schema.AsToolError(err)
}

func textContent(s string) []mcp.Content {
	return []mcp.Content{&mcp.TextContent{Text: s}}
}

func planTextSummary(res schema.PlanResult) string {
	if res.Markdown != "" {
		return res.Markdown
	}
	return fmt.Sprintf("CodeExpert %s result %s (status %s).", res.Kind, res.RunID, res.Status)
}

func reviewTextSummary(res schema.ReviewResult) string {
	if res.Markdown != "" {
		return res.Markdown
	}
	return fmt.Sprintf("CodeExpert review %s (status %s): %s", res.RunID, res.Status, res.Summary.Conclusion)
}

// rootsFromRequest queries the client's roots, if supported, returning local
// filesystem paths. Errors (unsupported capability) yield no restriction.
func rootsFromRequest(ctx context.Context, req *mcp.CallToolRequest) []string {
	if req == nil || req.Session == nil {
		return nil
	}
	res, err := req.Session.ListRoots(ctx, &mcp.ListRootsParams{})
	if err != nil || res == nil {
		return nil
	}
	var out []string
	for _, r := range res.Roots {
		if p := fileURIToPath(r.URI); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func fileURIToPath(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		p := strings.TrimPrefix(uri, "file://")
		// Strip an empty authority (file:///path -> /path).
		if strings.HasPrefix(p, "/") || (len(p) > 1 && p[1] == ':') {
			return p
		}
		// file://host/path is unusual locally; take the path portion.
		if i := strings.IndexByte(p, '/'); i >= 0 {
			return p[i:]
		}
	}
	return ""
}

// progressFromRequest builds a progress reporter bound to the request's progress
// token. If the client did not supply a token, it is a no-op.
func progressFromRequest(ctx context.Context, req *mcp.CallToolRequest) workflow.ProgressFunc {
	if req == nil || req.Session == nil || req.Params == nil {
		return nil
	}
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil
	}
	var counter int64
	session := req.Session
	return func(stage, detail string) {
		n := atomic.AddInt64(&counter, 1)
		msg := stage
		if detail != "" {
			msg = stage + ": " + detail
		}
		_ = session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: token,
			Message:       msg,
			Progress:      float64(n),
		})
	}
}
