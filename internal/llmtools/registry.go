// Package llmtools exposes the read-only function tools that the model may call
// during exploration. These are NOT public MCP tools. Every tool is read-only,
// every path is repository-relative, every result carries an evidence ID and the
// snapshot ID, and result sizes are capped. There is intentionally no shell,
// exec, write, or Git-mutation tool.
package llmtools

import (
	"context"
	"encoding/json"

	"github.com/gregpriday/codeexpert/internal/budget"
	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/evidence"
	"github.com/gregpriday/codeexpert/internal/index"
	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
	"github.com/gregpriday/codeexpert/internal/telemetry"
)

// CheckRunner runs pre-approved checks by stable ID. The model can request a
// check but cannot define one.
type CheckRunner interface {
	Available() []string
	Run(ctx context.Context, checkID string) (schema.CheckResult, error)
}

// Registry holds the tool set and the read-only state it operates on.
type Registry struct {
	snap     *repo.Snapshot
	review   *repo.ReviewSnapshot // nil for plan/help
	lex      *index.LexicalEngine
	syms     *index.SymbolIndex
	evid     *evidence.Store
	tracker  *budget.Tracker
	cfg      config.Config
	guidance []repo.Guidance
	checks   CheckRunner
	log      *telemetry.Logger

	tools map[string]tool
	order []string
}

type tool struct {
	name        string
	description string
	parameters  json.RawMessage
	handler     func(ctx context.Context, raw json.RawMessage) (any, error)
}

// Options configures a Registry.
type Options struct {
	Snapshot *repo.Snapshot
	Review   *repo.ReviewSnapshot
	Evidence *evidence.Store
	Budget   *budget.Tracker
	Config   config.Config
	Guidance []repo.Guidance
	Checks   CheckRunner
	Logger   *telemetry.Logger
}

// New builds a Registry and registers the read-only tools appropriate to the run
// kind (review tools only when a review snapshot is present).
func New(opts Options) *Registry {
	log := opts.Logger
	if log == nil {
		log = telemetry.Nop()
	}
	r := &Registry{
		snap:     opts.Snapshot,
		review:   opts.Review,
		evid:     opts.Evidence,
		tracker:  opts.Budget,
		cfg:      opts.Config,
		guidance: opts.Guidance,
		checks:   opts.Checks,
		log:      log,
		lex: index.NewLexicalEngine(opts.Snapshot, opts.Config.Retrieval.SearchResultLimit,
			index.WithTrigramFilter(opts.Config.Retrieval.TrigramFilter, opts.Config.Retrieval.TrigramMaxPerFile)),
		syms:  index.NewSymbolIndex(opts.Snapshot),
		tools: map[string]tool{},
	}
	r.registerCommon()
	if r.review != nil {
		r.registerReview()
	}
	return r
}

func (r *Registry) register(t tool) {
	r.tools[t.name] = t
	r.order = append(r.order, t.name)
}

// FunctionTools returns the provider-neutral tool definitions to advertise.
func (r *Registry) FunctionTools() []provider.FunctionTool {
	out := make([]provider.FunctionTool, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		out = append(out, provider.FunctionTool{
			Name:        t.name,
			Description: t.description,
			Parameters:  t.parameters,
		})
	}
	return out
}

// Names lists registered tool names.
func (r *Registry) Names() []string { return append([]string(nil), r.order...) }

// Execute runs a tool call and returns its JSON result string. Tool-level errors
// (including budget exhaustion) are returned as a JSON {"error": ...} payload so
// the model can adapt rather than aborting the run.
func (r *Registry) Execute(ctx context.Context, call provider.ToolCall) string {
	t, ok := r.tools[call.Name]
	if !ok {
		return errorResult("unknown tool %q", call.Name)
	}
	if r.tracker != nil {
		if err := r.tracker.ChargeInternalTool(); err != nil {
			return errorResult("%s", err.Error())
		}
	}
	raw := json.RawMessage(call.Arguments)
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	result, err := t.handler(ctx, raw)
	if err != nil {
		return errorResult("%s", err.Error())
	}
	b, mErr := json.Marshal(result)
	if mErr != nil {
		return errorResult("failed to encode result: %v", mErr)
	}
	return string(b)
}

func errorResult(format string, args ...any) string {
	b, _ := json.Marshal(map[string]string{"error": sprintf(format, args...)})
	return string(b)
}
