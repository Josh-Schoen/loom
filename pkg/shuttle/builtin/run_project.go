// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package builtin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	// runProjectTimeout bounds one dbt invocation. dbt builds are minutes,
	// not seconds; a hung warehouse connection must not hold the agent
	// forever.
	runProjectTimeout = 15 * time.Minute

	// runProjectOutputCap bounds captured dbt output. dbt is chatty and the
	// output rides back through an LLM context, so the tail is kept (dbt
	// reports its errors last) and the head is dropped.
	runProjectOutputCap = 64 * 1024

	// runProjectTailLines is how much dbt output travels in Result.Data.
	runProjectTailLines = 20

	// runProjectCacheDir holds compiled dbt projects. Generated dbt code and
	// dbt's own target/ artifacts live under LOOM_DATA_DIR, never in the
	// user's repo: the repo holds the project document, nothing derived.
	runProjectCacheDir = "projects-cache"

	// runProjectBuildSubdir is the compiled project root inside a cache entry.
	runProjectBuildSubdir = "build"

	// runProjectPathHashLen is how much of the project path's sha256 names
	// the cache entry — enough to separate documents, short enough to read.
	runProjectPathHashLen = 16
)

// Environment variables read at Execute time (never at construction, so a
// daemon that learns its dbt location later still works).
const (
	// envDBTBin overrides the dbt binary. Looked up on PATH.
	envDBTBin = "LOOM_DBT_BIN"
	// envDBTProfilesDir overrides the dbt profiles directory — the file that
	// carries connection details. CONSTRAINT: warehouse credentials reach
	// dbt through that profile's env_var() indirection from the daemon's own
	// environment. They are never tool parameters and this tool never sees
	// them.
	envDBTProfilesDir = "LOOM_DBT_PROFILES_DIR"
)

// defaultDBTProfilesSubdir is the profiles directory inside LOOM_DATA_DIR used
// when envDBTProfilesDir is unset (~/.loom/dbt-spike by default).
const defaultDBTProfilesSubdir = "dbt-spike"

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
	binName := os.Getenv(envDBTBin)
	if strings.TrimSpace(binName) == "" {
		binName = "dbt"
	}
	binPath, lookErr := exec.LookPath(binName)
	if lookErr != nil {
		return runProjectFailure(start, "DBT_NOT_FOUND",
			fmt.Sprintf("dbt binary %q not found on PATH: %v", binName, lookErr),
			fmt.Sprintf("Install dbt with the warehouse adapter, or set %s to its absolute path. "+
				"Until then use action 'compile' — the generated project is written to %s.", envDBTBin, buildDir))
	}

	profilesDir := strings.TrimSpace(os.Getenv(envDBTProfilesDir))
	if profilesDir == "" {
		profilesDir = filepath.Join(config.GetLoomDataDir(), defaultDBTProfilesSubdir)
	}

	// --no-partial-parse: the project is regenerated from the document on
	// every compile, so a cached manifest can only be stale.
	args := []string{"build", "--no-partial-parse", "--profiles-dir", profilesDir}

	runCtx, cancel := context.WithTimeout(ctx, runProjectTimeout)
	defer cancel()

	// #nosec G204 -- binPath comes from the daemon's own environment (LOOM_DBT_BIN),
	// not from tool parameters, and the arguments are fixed.
	cmd := exec.CommandContext(runCtx, binPath, args...)
	cmd.Dir = buildDir
	// cmd.Env stays nil so the child inherits the daemon environment: that is
	// how the dbt profile's env_var() indirection reaches its credentials
	// without any secret passing through this tool.
	combined, runErr := cmd.CombinedOutput()
	output := capTail(string(combined), runProjectOutputCap)
	exitCode := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(runErr, &exitErr):
		exitCode = exitErr.ExitCode()
	case runErr != nil:
		exitCode = -1
	}

	artifactPath := filepath.Join(buildDir, "target", "run_results.json")
	artifact, readErr := os.ReadFile(artifactPath) // #nosec G304 -- path derived from LOOM_DATA_DIR, not from input
	if readErr != nil {
		return runProjectDBTFailure(start, buildDir, output, exitCode,
			fmt.Sprintf("dbt produced no run_results.json (%v)", readErr))
	}
	byCell, foldErr := project.RecordsFromRunResults(artifact)
	if foldErr != nil {
		return runProjectDBTFailure(start, buildDir, output, exitCode,
			fmt.Sprintf("dbt run_results.json is unreadable: %v", foldErr))
	}

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
			"dbt_exit":     exitCode,
			"cells":        cells,
			"records":      len(all),
			"worstVerdict": worst,
			"dbt_tail":     tailLines(output, runProjectTailLines),
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
			entry["grain"] = c.DeclaredGrain
		}
		if len(c.Inputs) > 0 {
			entry["inputs"] = c.Inputs
		}
		cells = append(cells, entry)

		// Mirrors project.Compile's model selection: only a sql or call cell
		// carrying source becomes a dbt model.
		if !emitsModel(c) {
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

// emitsModel reports whether a cell compiles to a dbt model.
func emitsModel(c project.Cell) bool {
	if strings.TrimSpace(c.Source) == "" {
		return false
	}
	return c.Lang == project.LangSQL || c.Lang == project.LangCall
}

// projectBuildDir is the compile destination for a project document: keyed by
// the document's absolute path so two documents never share a build tree, and
// rooted in LOOM_DATA_DIR so generated dbt code and dbt's target/ artifacts
// stay out of the user's git repo.
func projectBuildDir(absDocPath string) string {
	sum := sha256.Sum256([]byte(absDocPath))
	key := hex.EncodeToString(sum[:])[:runProjectPathHashLen]
	return filepath.Join(config.GetLoomDataDir(), runProjectCacheDir, key, runProjectBuildSubdir)
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

// capTail keeps the last max bytes of s, marking what was dropped. dbt reports
// its errors last, so the tail is the half worth keeping.
func capTail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return fmt.Sprintf("[... %d bytes of earlier dbt output dropped ...]\n%s", len(s)-max, s[len(s)-max:])
}

// tailLines returns the last n non-empty lines of s.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return strings.Join(kept, "\n")
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
	tail := tailLines(output, runProjectTailLines)
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
