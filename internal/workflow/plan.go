package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gregpriday/codeexpert/internal/budget"
	"github.com/gregpriday/codeexpert/internal/evidence"
	"github.com/gregpriday/codeexpert/internal/index"
	"github.com/gregpriday/codeexpert/internal/llmtools"
	"github.com/gregpriday/codeexpert/internal/prompts"
	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/report"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// planHelpInput is the internal union of the inputs the shared plan/help runner
// needs. The two public tools (codeexpert_plan, codeexpert_help) each build one,
// so neither has to carry the other's request fields and the public surface
// stays unambiguous.
type planHelpInput struct {
	Root         string
	Instructions string
	Mode         schema.PlanMode
	AnswerType   schema.HelpAnswerType
	Task         *schema.TaskContract
	Profile      schema.AnalysisProfile
	Retrieval    schema.RetrievalOpts
	Budget       schema.Budget
	IncludeTrace bool
}

// Plan runs the implementation-planning workflow and returns a structured plan.
func (e *Engine) Plan(ctx context.Context, req schema.PlanRequest, opts RunOptions) (schema.PlanResult, error) {
	if err := validatePlanRequest(req); err != nil {
		return schema.PlanResult{}, err
	}
	runID := opts.RunID
	if runID == "" {
		runID = newRunID("plan")
	}
	in := planHelpInput{
		Root: req.Root, Instructions: req.Instructions, Mode: schema.PlanModePlan,
		Task: req.Task, Profile: req.Profile, Retrieval: req.Retrieval, Budget: req.Budget,
		IncludeTrace: req.Output.IncludeTrace,
	}
	return e.runPlanHelp(ctx, in, runID, opts)
}

// runPlanHelp is the shared exploration→synthesis pipeline behind both the plan
// and help tools.
func (e *Engine) runPlanHelp(ctx context.Context, req planHelpInput, runID string, opts RunOptions) (schema.PlanResult, error) {
	mode := req.Mode
	if mode == "" {
		mode = schema.PlanModePlan
	}
	progress := func(stage, detail string) {
		if opts.Progress != nil {
			opts.Progress(stage, detail)
		}
	}

	if strings.TrimSpace(req.Instructions) == "" {
		return schema.PlanResult{}, schema.NewError(schema.CodeInvalidArgument, "instructions must not be empty")
	}

	progress("snapshot", "freezing repository")
	snap, err := e.resolveAndSnapshot(ctx, req.Root, opts.AllowedRoots)
	if err != nil {
		return schema.PlanResult{}, err
	}

	profile := resolveProfile(req.Profile, e.Cfg.Plan.DefaultProfile)
	limits := resolveLimits(e.Cfg, profile, req.Budget, req.Retrieval)
	tracker := budget.New(limits)
	// Apply the time budget as a real context deadline so a single long provider
	// call cannot run past it (the round-boundary poll alone could not bound it).
	ctx, cancel := tracker.Deadline(ctx)
	defer cancel()
	usage := &usageAccumulator{}
	evid := evidence.NewStore(snap.ID())

	progress("inventory", "building repository brief")
	it := normalizeTask(req, snap)
	it.Mode = mode
	if req.AnswerType != "" {
		it.AnswerType = string(req.AnswerType)
	}
	lex := index.NewLexicalEngine(snap, e.Cfg.Retrieval.SearchResultLimit)
	guidance := e.guidanceList(ctx, snap)

	reg := llmtools.New(llmtools.Options{
		Snapshot: snap, Evidence: evid, Budget: tracker, Config: e.Cfg,
		Guidance: guidance, Logger: e.Log,
	})

	// Exploration with the scout model and read-only tools.
	progress("exploration", "exploring repository")
	scoutModel, scoutEffort := e.routedScoutModel()
	system := prompts.MustGet(prompts.CommonSystem) + "\n\n" + prompts.MustGet(prompts.PlanExplore)
	sess := e.NewSession(scoutModel, scoutEffort, system, reg, tracker, usage, opts.Progress)
	sess.AddUser(buildPlanExploreMessage(it, e.preflight(ctx, snap, it, lex)))
	if err := sess.RunToolLoop(ctx, limits.MaxModelToolRounds); err != nil {
		if schema.AsToolError(err).Code == schema.CodeCancelled {
			return schema.PlanResult{}, err
		}
		e.Log.Warn("exploration ended early", "err", err.Error())
	}

	// Synthesis: switch to the planner/verifier model, no tools.
	progress("synthesis", "synthesizing")
	complexity := len(evid.All())*2 + len(it.SearchAnchors)
	model, effort := e.routedSynthesisModel("plan_final", profile, complexity, false)
	sess.SwitchModel(model, effort)

	var result schema.PlanResult
	var limitations []schema.Limitation
	var synthErr error
	plan, lims, perr := e.synthesizePlan(ctx, sess, it.AcceptanceCriteria, snap, evid)
	if perr != nil {
		synthErr = perr
	} else {
		result.Plan = plan
		limitations = lims
	}
	// A synthesis failure caused by the time or call budget is not a hard error:
	// return a truthful partial result (no plan/help) with the reason recorded.
	// Any other failure (bad output, provider auth, caller cancel) still aborts.
	if synthErr != nil {
		if !isBudgetTimeout(synthErr) {
			return schema.PlanResult{}, synthErr
		}
		limitations = append(limitations, schema.Limitation{Stage: "synthesis",
			Message: "synthesis did not complete within the budget: " + schema.AsToolError(synthErr).Message})
	}

	progress("validation", "assembling result")
	status := schema.StatusComplete
	// An incomplete synthesis (budget/timeout) is a partial result even if the
	// tracker's own counters did not trip (e.g. a context-token overflow). So is a
	// plan/help that came back with unresolved validation issues after the repair
	// attempt: "complete" must mean complete.
	if synthErr != nil || (result.Plan == nil && result.Help == nil) || hasStageLimitation(limitations, "validation") {
		status = schema.StatusPartial
	}
	if tracker.TimedOut() {
		status = schema.StatusPartial
		limitations = append(limitations, schema.Limitation{Stage: "budget", Message: "time budget reached before completion"})
	}
	if reason, limited := tracker.Exhausted(); limited {
		status = schema.StatusPartial
		limitations = append(limitations, schema.Limitation{Stage: "budget", Message: reason + "; exploration stopped early and context may be incomplete"})
	}
	if snap.Truncated() {
		limitations = append(limitations, schema.Limitation{Stage: "snapshot", Message: "snapshot truncated at the configured byte limit; some files were not fully indexed"})
	}
	if snap.Stale() {
		status = schema.StatusStale
		limitations = append(limitations, schema.Limitation{Stage: "snapshot", Message: "the working tree changed after the snapshot was frozen; results may mix repository states — rerun to refreeze"})
	}

	result.RunID = runID
	result.Kind = mode
	result.Status = status
	result.Snapshot = snap.Ref()
	result.Request = it
	result.Repository = snap.Brief(e.Cfg.Repository)
	result.Evidence = sortedEvidenceRefs(evid.Refs())
	result.Limitations = limitations
	result.Usage = finalizeUsage(usage, tracker)
	e.attachPlanTrace(&result, profile, []string{prompts.CommonSystem, prompts.PlanExplore, prompts.PlanFinalize, prompts.RepairSchema}, req.IncludeTrace)

	// Render and persist.
	md := report.PlanMarkdown(result)
	result.Markdown = md
	if uri := e.persistArtifact(runID, "report", []byte(md)); uri != "" {
		result.ReportURI = uri
	}
	if mj, _ := report.PlanJSON(result); mj != "" {
		e.persistArtifact(runID, "result.json", []byte(mj))
	}
	progress("complete", "done")
	return result, nil
}

func buildPlanExploreMessage(it schema.InterpretedTask, preflight string) string {
	var b strings.Builder
	b.WriteString("# Task\n")
	if it.Mode == schema.PlanModeHelp {
		b.WriteString("Mode: HELP (diagnose and recommend a direction; do not write a full implementation plan yet).\n")
	} else {
		b.WriteString("Mode: PLAN (produce an implementation plan another agent can follow).\n")
	}
	if it.Title != "" {
		fmt.Fprintf(&b, "\nTitle: %s\n", it.Title)
	}
	fmt.Fprintf(&b, "\nGoal:\n%s\n", it.Goal)
	if len(it.Constraints) > 0 {
		fmt.Fprintf(&b, "\nConstraints (intent evidence, not ground truth):\n- %s\n", strings.Join(it.Constraints, "\n- "))
	}
	if len(it.AcceptanceCriteria) > 0 {
		fmt.Fprintf(&b, "\nAcceptance criteria:\n- %s\n", strings.Join(it.AcceptanceCriteria, "\n- "))
	}
	if len(it.NonGoals) > 0 {
		fmt.Fprintf(&b, "\nNon-goals:\n- %s\n", strings.Join(it.NonGoals, "\n- "))
	}
	if len(it.KnownFacts) > 0 {
		fmt.Fprintf(&b, "\nKnown facts claimed by the caller (UNTRUSTED — verify against the repository before relying on them):\n- %s\n", strings.Join(it.KnownFacts, "\n- "))
	}
	if it.PriorPlan != "" {
		fmt.Fprintf(&b, "\nPrior plan / investigation supplied by the caller (UNTRUSTED context to build on, not ground truth):\n%s\n", it.PriorPlan)
	}
	b.WriteString("\n# Deterministic preflight context\n")
	b.WriteString(preflight)
	b.WriteString("\n\nUse the read-only tools to gather the evidence you need, then stop. Cite evidence IDs.")
	return b.String()
}

func (e *Engine) synthesizePlan(ctx context.Context, sess *Session, criteria []string, snap *repo.Snapshot, evid *evidence.Store) (*schema.ImplementationPlan, []schema.Limitation, error) {
	out := inferSchema[schema.ImplementationPlan]("implementation_plan")
	instr := prompts.MustGet(prompts.PlanFinalize) + "\n\n" + evidenceCatalog(evid) +
		"\n\nReturn ONLY the implementation plan as a JSON object matching the schema. Every repository claim must reference evidence IDs listed above. Populate `traceability`: map every acceptance criterion to the step IDs that implement it and the tests that validate it."
	raw, err := sess.Synthesize(ctx, instr, out)
	if err != nil {
		return nil, nil, err
	}
	plan, verr := decodePlan(raw)
	if verr == nil {
		if issues := validatePlan(ctx, plan, criteria, snap, evid, e.Cfg); len(issues) > 0 {
			plan, verr = e.repairPlan(ctx, sess, criteria, snap, evid, issues)
		}
	}
	if verr != nil {
		// One repair attempt failed: return a partial with limitations.
		if plan == nil {
			return nil, nil, schema.NewError(schema.CodeOutputInvalid, "plan synthesis failed: %v", verr)
		}
		return plan, []schema.Limitation{{Stage: "validation", Message: "plan returned with unresolved validation issues: " + verr.Error()}}, nil
	}
	return plan, nil, nil
}

func decodePlan(raw []byte) (*schema.ImplementationPlan, error) {
	var plan schema.ImplementationPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// evidenceCatalog renders the gathered evidence IDs the model may cite.
func evidenceCatalog(evid *evidence.Store) string {
	all := evid.All()
	if len(all) == 0 {
		return "## Evidence catalog\n(no evidence gathered)"
	}
	var b strings.Builder
	b.WriteString("## Evidence catalog (cite these IDs)\n")
	for _, r := range all {
		loc := r.Path
		if r.StartLine > 0 {
			loc = fmt.Sprintf("%s:%d-%d", r.Path, r.StartLine, r.EndLine)
		}
		if r.Symbol != "" {
			loc += " [" + r.Symbol + "]"
		}
		fmt.Fprintf(&b, "- %s  %s  %s\n", r.ID, loc, truncateStr(r.Summary, 80))
	}
	return b.String()
}

// SwitchModel changes the active model/effort mid-session (exploration -> synthesis).
func (s *Session) SwitchModel(model, effort string) {
	s.model = model
	s.reasoning = effort
}

func finalizeUsage(usage *usageAccumulator, tracker *budget.Tracker) schema.RunUsage {
	u := usage.snapshot()
	bs := tracker.Snapshot()
	u.InternalToolCalls = bs.InternalTools
	u.FilesRead = bs.FilesRead
	u.BytesRead = bs.BytesRead
	u.WallSeconds = bs.Wall.Seconds()
	return u
}

var _ = provider.RoleUser
