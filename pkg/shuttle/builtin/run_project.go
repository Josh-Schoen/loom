// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/teradata-labs/loom/pkg/config"
	"github.com/teradata-labs/loom/pkg/project"
	"github.com/teradata-labs/loom/pkg/project/oracle"
	"github.com/teradata-labs/loom/pkg/session"
	"github.com/teradata-labs/loom/pkg/shuttle"
)

// run_project actions.
const (
	runProjectActionValidate = "validate"
	runProjectActionCompile  = "compile"
	runProjectActionRun      = "run"
)

const (
	// runProjectTailLines is how much dbt output travels in Result.Data.
	runProjectTailLines = 20
)

// RunProjectTool validates, compiles and runs a Loom project document.
//
// This is the agentic-first loop: the agent authors project.yaml in its
// granted repo, calls this tool, and reads per-cell verification records back
// off the result. Records ride Result.Metadata[oracle.MetadataKey], the same
// wire the desktop already renders as badges — so a verdict reaches the user
// with no UI change.
type RunProjectTool struct{}

// NewRunProjectTool creates the run_project tool. Path confinement comes from
// the per-request working-directory grant, so there is nothing to configure.
func NewRunProjectTool() *RunProjectTool {
	return &RunProjectTool{}
}

func (t *RunProjectTool) Name() string {
	return "run_project"
}

// Description returns the tool description.
// Deprecated: Description is loaded from PromptRegistry when one is
// configured. This fallback is used only when prompts are not configured.
func (t *RunProjectTool) Description() string {
	return `Validates, compiles, and runs a Loom project document (a project.yaml of typed cells) through dbt, ` +
		`returning per-cell verification records (grain, metamorphic, dbt_run). ` +
		`Actions: 'validate' checks the document and runs no dbt; ` +
		`'compile' writes the generated dbt project outside the repo; ` +
		`'run' compiles, runs dbt build, and returns the records. ` +
		`Paths resolve inside the active working-directory grant. ` +
		`A failing test is a result, not an error: the records report it so it can be fixed.`
}

func (t *RunProjectTool) InputSchema() *shuttle.JSONSchema {
	return shuttle.NewObjectSchema(
		"Parameters for running a Loom project document",
		map[string]*shuttle.JSONSchema{
			"path": shuttle.NewStringSchema(
				"Path to the project document (required), e.g. 'project.yaml'. Relative paths resolve inside the granted working directory."),
			"action": shuttle.NewStringSchema(
				"'validate' (fast, no dbt), 'compile' (write the dbt project), or 'run' (compile, dbt build, records). Default 'run'.").
				WithEnum(runProjectActionValidate, runProjectActionCompile, runProjectActionRun).
				WithDefault(runProjectActionRun),
		},
		[]string{"path"},
	)
}

func (t *RunProjectTool) Backend() string {
	return "" // Backend-agnostic: the warehouse is dbt's concern, not ours.
}

// Execute runs one action against the project document at params["path"].
//
// A dbt failure is never a tool failure while dbt produced verdicts: see
// runProject. Execute returns (result, nil) for every expected condition —
// the error return is reserved for nothing at all, matching the other
// builtins.
func (t *RunProjectTool) Execute(ctx context.Context, params map[string]interface{}) (result *shuttle.Result, err error) {
	start := time.Now()

	// A panic in compile or artifact folding degrades to a failed result, the
	// same contract the oracle executor holds: the agent loop never dies of a
	// verification bug.
	defer func() {
		if r := recover(); r != nil {
			result = &shuttle.Result{
				Success: false,
				Error: &shuttle.Error{
					Code:       "PANIC",
					Message:    fmt.Sprintf("run_project panicked: %v", r),
					Suggestion: "Report this with the project document; the document itself is unchanged.",
				},
				ExecutionTimeMs: time.Since(start).Milliseconds(),
			}
			err = nil
		}
	}()

	path, ok := params["path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return runProjectFailure(start, "INVALID_PARAMS", "path is required",
			"Provide the project document path, e.g. 'project.yaml'"), nil
	}

	action := runProjectActionRun
	if a, ok := params["action"].(string); ok && strings.TrimSpace(a) != "" {
		action = strings.ToLower(strings.TrimSpace(a))
	}
	switch action {
	case runProjectActionValidate, runProjectActionCompile, runProjectActionRun:
	default:
		return runProjectFailure(start, "INVALID_PARAMS",
			fmt.Sprintf("unknown action %q", action),
			fmt.Sprintf("Use %q, %q or %q", runProjectActionValidate, runProjectActionCompile, runProjectActionRun)), nil
	}

	// Same confinement as the file tools: the grant subtree, LOOM_DATA_DIR, or
	// a temp location. An empty grant leaves relative paths relative to the
	// process working directory, which then fails the containment test unless
	// it lands in LOOM_DATA_DIR or temp.
	grant := session.WorkingDirFromContext(ctx)
	docPath, allowed := resolveGrantedPath(path, grant)
	if !allowed {
		return runProjectFailure(start, "PATH_RESTRICTED",
			fmt.Sprintf("Path outside the granted directory: %s", docPath),
			runProjectGrantSuggestion(grant)), nil
	}

	doc, err := project.Load(docPath)
	if err != nil {
		// Validation errors name the offending cell; hand that straight to
		// the agent so the next edit is targeted.
		return runProjectFailure(start, "PROJECT_INVALID", err.Error(),
			"Fix the cell named in the message in the project document, then run 'validate' again."), nil
	}

	if action == runProjectActionValidate {
		return &shuttle.Result{
			Success:         true,
			Data:            validateSummary(doc, docPath),
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}, nil
	}

	buildDir := projectBuildDir(docPath)
	skipped, err := project.Compile(doc, buildDir)
	if err != nil {
		return runProjectFailure(start, "COMPILE_FAILED", err.Error(),
			"Fix the cell named in the message; every {{ ref('x') }} in a cell's source must also appear in that cell's inputs."), nil
	}

	if action == runProjectActionCompile {
		files, _ := countFiles(buildDir)
		return &shuttle.Result{
			Success: true,
			Data: map[string]interface{}{
				"action":   runProjectActionCompile,
				"project":  doc.Metadata.Name,
				"buildDir": buildDir,
				"files":    files,
				"skipped":  skipped,
			},
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}, nil
	}

	return runProject(ctx, doc, docPath, buildDir, skipped, start), nil
}

// runProject invokes dbt in buildDir and folds its artifact into records.
//
// The dbt invocation itself lives in pkg/project (project.Run): the desktop's
// single-cell rerun shares it, and one copy of "which flags dbt gets" is the
// whole point. What stays here is the REPORTING contract below.
//
// FAILURE SEMANTICS, deliberately asymmetric:
//   - dbt exits nonzero but wrote target/run_results.json → Success:true. A
//     failing grain or metamorphic test IS a successful verification run: the
//     oracle did its job and the fail verdict is the signal the agent needs
//     to self-correct. Reporting that as a tool error would train the agent
//     to retry instead of to fix.
//   - no readable run_results.json → Success:false. dbt never got as far as
//     running anything (profile, credentials, connection), so there is nothing
//     to verify and the dbt output is the only useful evidence.
func runProject(ctx context.Context, doc *project.Document, docPath, buildDir string, skipped []string, start time.Time) *shuttle.Result {
	binPath, profilesDir, err := project.ResolveDBT(config.GetLoomDataDir())
	if err != nil {
		return runProjectFailure(start, "DBT_NOT_FOUND", err.Error(),
			fmt.Sprintf("Install dbt with the warehouse adapter, or set %s to its absolute path. "+
				"Until then use action 'compile' — the generated project is written to %s.", project.EnvDBTBin, buildDir))
	}

	outcome := project.Run(ctx, doc, buildDir, project.RunOptions{
		DBTBin:      binPath,
		ProfilesDir: profilesDir,
	})
	if outcome.Err != nil {
		return runProjectDBTFailure(start, buildDir, outcome.Output, outcome.ExitCode, outcome.Err.Error())
	}
	byCell := outcome.Records

	// Deterministic ordering: dependency order of the document, so the
	// records arrive in the order the cells ran.
	order, orderErr := doc.TopoOrder()
	if orderErr != nil {
		order = sortedKeys(byCell)
	}
	seen := make(map[string]bool, len(byCell))
	cells := make([]map[string]interface{}, 0, len(byCell))
	all := make([]oracle.VerificationRecord, 0, len(byCell))
	worst := oracle.VerdictPass
	for _, id := range order {
		records := byCell[id]
		if len(records) == 0 {
			continue
		}
		seen[id] = true
		cells = append(cells, cellVerdicts(id, records))
		all = append(all, records...)
		worst = worseVerdict(worst, records)
	}
	// Anything dbt reported that the document no longer names still travels:
	// dropping it would hide a stale build directory.
	for _, id := range sortedKeys(byCell) {
		if seen[id] {
			continue
		}
		cells = append(cells, cellVerdicts(id, byCell[id]))
		all = append(all, byCell[id]...)
		worst = worseVerdict(worst, byCell[id])
	}

	result := &shuttle.Result{
		Success: true,
		Data: map[string]interface{}{
			"action":       runProjectActionRun,
			"project":      doc.Metadata.Name,
			"document":     docPath,
			"buildDir":     buildDir,
			"skipped":      skipped,
			"dbt_exit":     outcome.ExitCode,
			"cells":        cells,
			"records":      len(all),
			"previews":     outcome.Previews,
			"worstVerdict": worst,
			"dbt_tail":     project.TailLines(outcome.Output, runProjectTailLines),
		},
		ExecutionTimeMs: time.Since(start).Milliseconds(),
	}
	// The whole point: records ride the existing tool_verification wire.
	oracle.AttachRecords(result, all...)
	return result
}

// validateSummary is what the agent gets back from a clean 'validate': the
// shape of the document it just wrote, and which cells will not become dbt
// models.
func validateSummary(doc *project.Document, docPath string) map[string]interface{} {
	order, err := doc.TopoOrder()
	if err != nil {
		order = make([]string, 0, len(doc.Cells))
		for _, c := range doc.Cells {
			order = append(order, c.ID)
		}
	}
	cells := make([]map[string]interface{}, 0, len(order))
	var willSkip, unresolvedCalls []string
	for _, id := range order {
		c, ok := doc.Cell(id)
		if !ok {
			continue
		}
		entry := map[string]interface{}{"id": c.ID, "lang": c.Lang}
		if c.DeclaredGrain != "" {
			entry["grain"] = string(c.DeclaredGrain)
		}
		if len(c.Inputs) > 0 {
			entry["inputs"] = c.Inputs
		}
		cells = append(cells, entry)

		// Mirrors project.Compile's model selection: only a sql or call cell
		// carrying source becomes a dbt model.
		if !project.EmitsModel(c) {
			willSkip = append(willSkip, c.ID)
			if c.Lang == project.LangCall {
				unresolvedCalls = append(unresolvedCalls, c.ID)
			}
		}
	}
	data := map[string]interface{}{
		"action":   runProjectActionValidate,
		"project":  doc.Metadata.Name,
		"document": docPath,
		"cells":    cells,
		"skipped":  willSkip,
	}
	if len(unresolvedCalls) > 0 {
		// v1 compiles a call cell only when it carries its own source;
		// registry resolution is out of scope, so these cells are skippable.
		data["unresolved_call_cells"] = unresolvedCalls
	}
	return data
}

// projectBuildDir is the compile destination for a document under the
// daemon's data directory. The derivation itself lives in project.BuildDir —
// cmd/loom-desktop reads the same tree and a byte of drift would show the user
// "no runs yet" forever, with nothing to indicate why.
func projectBuildDir(absDocPath string) string {
	return project.BuildDir(config.GetLoomDataDir(), absDocPath)
}

// cellVerdicts summarizes one cell's records as "<rung>=<verdict>" strings.
func cellVerdicts(id string, records []oracle.VerificationRecord) map[string]interface{} {
	verdicts := make([]string, 0, len(records))
	for _, r := range records {
		verdicts = append(verdicts, r.Rung+"="+r.Verdict)
	}
	return map[string]interface{}{"id": id, "verdicts": verdicts}
}

// verdictSeverity orders verdicts so the run can report its worst one.
func verdictSeverity(verdict string) int {
	switch verdict {
	case oracle.VerdictFail:
		return 3
	case oracle.VerdictWarn:
		return 2
	case oracle.VerdictSkip:
		return 1
	default:
		return 0
	}
}

func worseVerdict(current string, records []oracle.VerificationRecord) string {
	for _, r := range records {
		if verdictSeverity(r.Verdict) > verdictSeverity(current) {
			current = r.Verdict
		}
	}
	return current
}

func sortedKeys(m map[string][]oracle.VerificationRecord) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// countFiles counts the regular files written under dir.
func countFiles(dir string) (int, error) {
	n := 0
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			n++
		}
		return nil
	})
	return n, err
}

func runProjectFailure(start time.Time, code, message, suggestion string) *shuttle.Result {
	return &shuttle.Result{
		Success: false,
		Error: &shuttle.Error{
			Code:       code,
			Message:    message,
			Suggestion: suggestion,
		},
		ExecutionTimeMs: time.Since(start).Milliseconds(),
	}
}

// runProjectDBTFailure reports a dbt invocation that produced no verdicts. The
// dbt tail is the whole diagnosis, so it goes in the message.
func runProjectDBTFailure(start time.Time, buildDir, output string, exitCode int, reason string) *shuttle.Result {
	tail := project.TailLines(output, runProjectTailLines)
	if tail == "" {
		tail = "(dbt produced no output)"
	}
	result := runProjectFailure(start, "DBT_NO_ARTIFACT",
		fmt.Sprintf("%s. dbt exited %d. Last output:\n%s", reason, exitCode, tail),
		"dbt did not run anything, so nothing was verified. Check the dbt profile and warehouse connection; "+
			"credentials come from the daemon environment via the profile's env_var() entries, not from this tool.")
	result.Error.Details = map[string]interface{}{
		"buildDir": buildDir,
		"dbt_exit": exitCode,
		"dbt_tail": tail,
	}
	return result
}

// runProjectGrantSuggestion names the directories a path may live in.
func runProjectGrantSuggestion(grant string) string {
	if grant == "" {
		return "No working-directory grant is active; use an absolute path inside LOOM_DATA_DIR or a temporary directory, " +
			"or attach a repository first."
	}
	return fmt.Sprintf("Keep the project document within %s, LOOM_DATA_DIR, or a temporary directory", grant)
}
