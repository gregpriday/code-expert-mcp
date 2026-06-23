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

// Plan runs the plan/help workflow and returns a structured result.
func (e *Engine) Plan(ctx context.Context, req schema.PlanRequest, opts RunOptions) (schema.PlanResult, error) {
	runID := opts.RunID
	if runID == "" {
		runID = newRunID("plan")
	}
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
	limits := resolveLimits(e.Cfg, profile, req.Budget)
	tracker := budget.New(limits)
	usage := &usageAccumulator{}
	evid := evidence.NewStore(snap.ID())

	progress("inventory", "building repository brief")
	it := normalizeTask(req, snap)
	it.Mode = mode
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
	synthStage := "plan_final"
	if mode == schema.PlanModeHelp {
		synthStage = "help_final"
	}
	model, effort := e.routedSynthesisModel(synthStage, profile, complexity, false)
	sess.SwitchModel(model, effort)

	var result schema.PlanResult
	var limitations []schema.Limitation
	if mode == schema.PlanModeHelp {
		help, lims, herr := e.synthesizeHelp(ctx, sess, snap, evid)
		if herr != nil {
			return schema.PlanResult{}, herr
		}
		result.Help = help
		limitations = lims
	} else {
		plan, lims, perr := e.synthesizePlan(ctx, sess, snap, evid)
		if perr != nil {
			return schema.PlanResult{}, perr
		}
		result.Plan = plan
		limitations = lims
	}

	progress("validation", "assembling result")
	status := schema.StatusComplete
	if tracker.TimedOut() {
		status = schema.StatusPartial
		limitations = append(limitations, schema.Limitation{Stage: "budget", Message: "time budget reached before completion"})
	}
	if snap.Truncated() {
		limitations = append(limitations, schema.Limitation{Stage: "snapshot", Message: "snapshot truncated at the configured byte limit; some files were not fully indexed"})
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
	b.WriteString("\n# Deterministic preflight context\n")
	b.WriteString(preflight)
	b.WriteString("\n\nUse the read-only tools to gather the evidence you need, then stop. Cite evidence IDs.")
	return b.String()
}

func (e *Engine) synthesizePlan(ctx context.Context, sess *Session, snap *repo.Snapshot, evid *evidence.Store) (*schema.ImplementationPlan, []schema.Limitation, error) {
	out := inferSchema[schema.ImplementationPlan]("implementation_plan")
	instr := prompts.MustGet(prompts.PlanFinalize) + "\n\n" + evidenceCatalog(evid) +
		"\n\nReturn ONLY the implementation plan as a JSON object matching the schema. Every repository claim must reference evidence IDs listed above."
	raw, err := sess.Synthesize(ctx, instr, out)
	if err != nil {
		return nil, nil, err
	}
	plan, verr := decodePlan(raw)
	if verr == nil {
		if issues := validatePlan(ctx, plan, snap, evid, e.Cfg); len(issues) > 0 {
			plan, verr = e.repairPlan(ctx, sess, snap, evid, issues)
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

func (e *Engine) synthesizeHelp(ctx context.Context, sess *Session, snap *repo.Snapshot, evid *evidence.Store) (*schema.HelpReport, []schema.Limitation, error) {
	out := inferSchema[schema.HelpReport]("help_report")
	instr := prompts.MustGet(prompts.HelpFinalize) + "\n\n" + evidenceCatalog(evid) +
		"\n\nReturn ONLY the help report as a JSON object matching the schema. Distinguish verified facts from inference; cite evidence IDs."
	raw, err := sess.Synthesize(ctx, instr, out)
	if err != nil {
		return nil, nil, err
	}
	var help schema.HelpReport
	if jerr := json.Unmarshal(raw, &help); jerr != nil {
		return nil, nil, schema.NewError(schema.CodeOutputInvalid, "help report not parseable: %v", jerr)
	}
	lims := validateHelp(ctx, &help, snap, evid)
	return &help, lims, nil
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
