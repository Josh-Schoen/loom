// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/teradata-labs/loom/pkg/project"
	"github.com/teradata-labs/loom/pkg/project/oracle"
	"github.com/teradata-labs/loom/pkg/session"
	"github.com/teradata-labs/loom/pkg/shuttle"
)

// validProjectDoc has one model per interesting shape: a base sql cell with a
// declared grain, an aggregating child (which earns the Tier-A partition-sums
// test), a prose cell and a source-less call cell (both skipped by Compile).
const validProjectDoc = `apiVersion: loom/v1
kind: Project
metadata:
  name: revenue
  description: Daily revenue rollup
cells:
  - id: orders
    lang: sql
    declared_grain: order_id
    source: |
      SELECT order_id, order_day, amount FROM raw_orders
  - id: daily_totals
    lang: sql
    inputs: [orders]
    declared_grain: order_day
    source: |
      SELECT order_day, SUM(amount) AS total_amount
      FROM {{ ref('orders') }}
      GROUP BY order_day
  - id: notes
    lang: prose
    source: |
      Revenue rolls up by day.
  - id: shared_defn
    lang: call
    ref: shared@v1
`

// invalidProjectDoc names an input that is not a cell. The validation error
// must name the offending cell so the agent can fix exactly that cell.
const invalidProjectDoc = `apiVersion: loom/v1
kind: Project
metadata:
  name: revenue
cells:
  - id: orders
    lang: sql
    declared_grain: order_id
    source: |
      SELECT order_id, amount FROM raw_orders
  - id: daily_totals
    lang: sql
    inputs: [missing_upstream]
    declared_grain: order_day
    source: |
      SELECT order_day, SUM(amount) AS total_amount FROM {{ ref('orders') }} GROUP BY order_day
`

// runResultsFixture is a dbt build artifact shaped like the live one: two
// models plus the generated grain and partition-sums tests. grainStatus is
// parameterised so the same fixture serves the passing and failing runs.
func runResultsFixture(grainStatus string, failures int) string {
	return fmt.Sprintf(`{
  "metadata": {"generated_at": "2026-08-30T12:00:00.000000Z", "dbt_schema_version": "https://schemas.getdbt.com/dbt/run-results/v6.json"},
  "results": [
    {"unique_id": "model.revenue.orders", "status": "success", "execution_time": 1.5,
     "adapter_response": {"rows_affected": 42}},
    {"unique_id": "model.revenue.daily_totals", "status": "success", "execution_time": 0.75,
     "adapter_response": {"rows_affected": 7}},
    {"unique_id": "test.revenue.loom_grain_unique_orders_order_id.a1b2c3d4", "status": %q,
     "execution_time": 0.2, "failures": %d},
    {"unique_id": "test.revenue.loom_grain_unique_daily_totals_order_day.e5f6a7b8", "status": "pass",
     "execution_time": 0.2, "failures": 0},
    {"unique_id": "test.revenue.loom_partition_sums_daily_totals", "status": "pass",
     "execution_time": 0.3, "failures": 0}
  ]
}
`, grainStatus, failures)
}

// runProjectEnv is one isolated world for a run_project case: a granted repo
// holding the document, and a LOOM_DATA_DIR that owns the build cache.
type runProjectEnv struct {
	repo     string
	dataDir  string
	docPath  string
	buildDir string
}

func setupRunProject(t *testing.T, doc string) runProjectEnv {
	t.Helper()

	// LOOM_DATA_DIR first: projectBuildDir reads it, and the grant root must
	// not be masked by the data-directory allowance.
	dataDir := isolateLoomDataDir(t)
	repo := grantTestRoot(t)
	docPath := filepath.Join(repo, "project.yaml")
	if err := os.WriteFile(docPath, []byte(doc), 0o600); err != nil {
		t.Fatalf("write project document: %v", err)
	}
	return runProjectEnv{
		repo:     repo,
		dataDir:  dataDir,
		docPath:  docPath,
		buildDir: projectBuildDir(docPath),
	}
}

// dbt fake behaviours. No test touches a real dbt binary or a network.
const (
	dbtNone       = "none"        // action needs no dbt
	dbtPass       = "pass"        // writes a passing artifact, exit 0
	dbtFailedTest = "failed_test" // writes an artifact with a failing test, exit 1
	dbtNoArtifact = "no_artifact" // writes nothing, exit 1 (config/connection error)
	dbtMissing    = "missing"     // LOOM_DBT_BIN points at nothing
	dbtShowFails  = "show_fails"  // build works, every `dbt show` errors out
)

// writeFakeDBT installs a shell script standing in for dbt. It branches on the
// positional action — `show` answers with a canned preview payload, anything
// else (build, run-operation) optionally copies a canned run_results.json into
// ./target (dbt writes that artifact relative to its project directory) — and
// exits with the requested code.
func writeFakeDBT(t *testing.T, mode string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the dbt stand-in is a POSIX shell script")
	}

	dir := t.TempDir()
	if mode == dbtMissing {
		return filepath.Join(dir, "dbt-not-installed")
	}

	var body strings.Builder
	body.WriteString("#!/bin/sh\n")
	// Preview capture is a SECOND kind of invocation: run_project calls
	// `dbt show --select <cell> ...` after the build. The stand-in branches on
	// the positional action ($1) exactly as dbt does, so a test can make the
	// build succeed while every preview fails.
	body.WriteString("if [ \"$1\" = \"show\" ]; then\n")
	if mode == dbtShowFails {
		body.WriteString("  echo \"12:00:06  Database Error in model $3: relation not found\" >&2\n")
		body.WriteString("  exit 1\n")
	} else {
		// Shaped like dbt 1.12's ShowNode log event: ordinary log lines, then
		// the payload keyed by the node name, its first line carrying the
		// logger's timestamp prefix — so the JSON does not start at a line
		// boundary and the column order lives only in the row key order.
		body.WriteString("  sel=\"$3\"\n")
		body.WriteString("  echo \"12:00:06  Running with dbt=1.12.0 (stand-in)\"\n")
		body.WriteString("  echo \"12:00:07  Previewing node '$sel':\"\n")
		body.WriteString("  echo \"12:00:07  {\"\n")
		body.WriteString("  echo \"  \\\"$sel\\\": [\"\n")
		body.WriteString("  echo '    {\"order_day\": \"2026-08-01\", \"total_amount\": 1234.5, \"label\": \"aug\"},'\n")
		body.WriteString("  echo '    {\"order_day\": \"2026-08-02\", \"total_amount\": 9007199254740993, \"label\": null}'\n")
		body.WriteString("  echo \"  ]\"\n")
		body.WriteString("  echo \"}\"\n")
		body.WriteString("  echo \"12:00:08  Done.\"\n")
		body.WriteString("  exit 0\n")
	}
	body.WriteString("fi\n")
	body.WriteString("echo \"12:00:00  Running with dbt=1.12.0 (stand-in)\"\n")

	exitCode := 0
	switch mode {
	case dbtPass, dbtFailedTest, dbtShowFails:
		artifact := runResultsFixture("pass", 0)
		if mode == dbtFailedTest {
			artifact = runResultsFixture("fail", 3)
			exitCode = 1
		}
		fixture := filepath.Join(dir, "run_results.fixture.json")
		if err := os.WriteFile(fixture, []byte(artifact), 0o600); err != nil {
			t.Fatalf("write artifact fixture: %v", err)
		}
		body.WriteString("mkdir -p target\n")
		fmt.Fprintf(&body, "cp %q target/run_results.json\n", fixture)
		if mode == dbtFailedTest {
			body.WriteString("echo \"12:00:04  Failure in test loom_grain_unique_orders_order_id (models/schema.yml)\"\n")
		}
		body.WriteString("echo \"12:00:05  Done. PASS=4 WARN=0 ERROR=0\"\n")
	case dbtNoArtifact:
		exitCode = 1
		body.WriteString("echo \"12:00:01  Encountered an error: Credentials in profile 'revenue' are invalid\" >&2\n")
	default:
		t.Fatalf("unknown dbt mode %q", mode)
	}
	fmt.Fprintf(&body, "exit %d\n", exitCode)

	script := filepath.Join(dir, "dbt")
	if err := os.WriteFile(script, []byte(body.String()), 0o700); err != nil { // #nosec G302 -- must be executable
		t.Fatalf("write dbt stand-in: %v", err)
	}
	return script
}

func resultData(t *testing.T, res *shuttle.Result) map[string]interface{} {
	t.Helper()
	data, ok := res.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("Data is %T, want map[string]interface{}", res.Data)
	}
	return data
}

func dataStrings(t *testing.T, data map[string]interface{}, key string) []string {
	t.Helper()
	switch v := data[key].(type) {
	case nil:
		return nil
	case []string:
		return v
	default:
		t.Fatalf("data[%q] is %T, want []string", key, v)
		return nil
	}
}

// relFiles lists the files under dir, relative to it, for cleanliness asserts.
func relFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

// previewsDir is where run_project persists sampled results.
func previewsDir(buildDir string) string {
	return filepath.Join(buildDir, "target", "loom_previews")
}

// readPreview loads one persisted preview, keeping the raw bytes: the literal
// text is the only place a number's precision can be checked.
func readPreview(t *testing.T, buildDir, cellID string) (project.CellPreview, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(previewsDir(buildDir), cellID+".json"))
	if err != nil {
		t.Fatalf("read preview for %s: %v", cellID, err)
	}
	var p project.CellPreview
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("preview for %s is not valid JSON: %v", cellID, err)
	}
	return p, raw
}

func rungVerdicts(records []oracle.VerificationRecord) map[string][]string {
	out := map[string][]string{}
	for _, r := range records {
		out[r.Rung] = append(out[r.Rung], r.Verdict)
	}
	return out
}

func TestRunProjectTool_Actions(t *testing.T) {
	tests := []struct {
		name            string
		doc             string
		action          string
		dbtMode         string
		wantSuccess     bool
		wantErrCode     string
		wantErrContains []string
		check           func(t *testing.T, res *shuttle.Result, env runProjectEnv)
	}{
		{
			name:        "validate accepts a well formed document",
			doc:         validProjectDoc,
			action:      "validate",
			dbtMode:     dbtNone,
			wantSuccess: true,
			check: func(t *testing.T, res *shuttle.Result, env runProjectEnv) {
				data := resultData(t, res)
				if data["project"] != "revenue" {
					t.Errorf("project = %v, want revenue", data["project"])
				}
				cells, ok := data["cells"].([]map[string]interface{})
				if !ok || len(cells) != 4 {
					t.Fatalf("cells = %v, want 4 entries", data["cells"])
				}
				// TopoOrder emits level by level, sorted by ID within a
				// level, so the source-less cells lead and daily_totals
				// (which depends on orders) is last.
				var ids []string
				byID := map[string]map[string]interface{}{}
				for _, c := range cells {
					id, _ := c["id"].(string)
					ids = append(ids, id)
					byID[id] = c
				}
				if got := strings.Join(ids, ","); got != "notes,orders,shared_defn,daily_totals" {
					t.Errorf("cell order = %q, want dependency order", got)
				}
				if byID["orders"]["grain"] != "order_id" || byID["orders"]["lang"] != "sql" {
					t.Errorf("orders cell = %v, want sql/order_id", byID["orders"])
				}
				if inputs, _ := byID["daily_totals"]["inputs"].([]string); len(inputs) != 1 || inputs[0] != "orders" {
					t.Errorf("daily_totals inputs = %v, want [orders]", byID["daily_totals"]["inputs"])
				}
				skipped := dataStrings(t, data, "skipped")
				if len(skipped) != 2 {
					t.Fatalf("skipped = %v, want notes and shared_defn", skipped)
				}
				unresolved := dataStrings(t, data, "unresolved_call_cells")
				if len(unresolved) != 1 || unresolved[0] != "shared_defn" {
					t.Errorf("unresolved_call_cells = %v, want [shared_defn]", unresolved)
				}
				// validate must not compile: no build directory yet.
				if _, err := os.Stat(env.buildDir); !os.IsNotExist(err) {
					t.Errorf("validate created %s (stat err %v); it must not compile", env.buildDir, err)
				}
			},
		},
		{
			name:            "validate names the broken cell",
			doc:             invalidProjectDoc,
			action:          "validate",
			dbtMode:         dbtNone,
			wantSuccess:     false,
			wantErrCode:     "PROJECT_INVALID",
			wantErrContains: []string{"daily_totals", "missing_upstream"},
			check: func(t *testing.T, res *shuttle.Result, _ runProjectEnv) {
				if res.Error.Suggestion == "" {
					t.Error("validation failure carries no suggestion for the agent")
				}
			},
		},
		{
			name:        "compile writes the dbt project outside the repo",
			doc:         validProjectDoc,
			action:      "compile",
			dbtMode:     dbtNone,
			wantSuccess: true,
			check: func(t *testing.T, res *shuttle.Result, env runProjectEnv) {
				data := resultData(t, res)
				buildDir, _ := data["buildDir"].(string)
				if buildDir != env.buildDir {
					t.Fatalf("buildDir = %q, want %q", buildDir, env.buildDir)
				}
				cache := filepath.Join(env.dataDir, "projects-cache")
				if !strings.HasPrefix(buildDir, cache+string(filepath.Separator)) {
					t.Errorf("buildDir %q is not under %q", buildDir, cache)
				}
				for _, rel := range []string{"dbt_project.yml", "package.yaml", "models/orders.sql", "models/daily_totals.sql", "models/schema.yml", "tests/loom_partition_sums_daily_totals.sql"} {
					if _, err := os.Stat(filepath.Join(buildDir, rel)); err != nil {
						t.Errorf("missing generated %s: %v", rel, err)
					}
				}
				if n, _ := data["files"].(int); n < 6 {
					t.Errorf("files = %v, want at least 6", data["files"])
				}
				// The data-in-git rule's cousin: nothing generated lands in
				// the user's repo.
				if got := relFiles(t, env.repo); len(got) != 1 || got[0] != "project.yaml" {
					t.Errorf("repo is dirty after compile: %v", got)
				}
			},
		},
		{
			name:        "run attaches records to the result metadata",
			doc:         validProjectDoc,
			action:      "run",
			dbtMode:     dbtPass,
			wantSuccess: true,
			check: func(t *testing.T, res *shuttle.Result, _ runProjectEnv) {
				records := oracle.RecordsFrom(res)
				if len(records) != 5 {
					t.Fatalf("records = %d, want 5: %+v", len(records), records)
				}
				byRung := rungVerdicts(records)
				for rung, want := range map[string]int{
					project.RungDBTRun:      2,
					oracle.RungGrain:        2,
					project.RungMetamorphic: 1,
				} {
					if got := len(byRung[rung]); got != want {
						t.Errorf("rung %s has %d records, want %d", rung, got, want)
					}
					for _, v := range byRung[rung] {
						if v != oracle.VerdictPass {
							t.Errorf("rung %s verdict %q, want pass", rung, v)
						}
					}
				}
				for _, r := range records {
					if r.At != "2026-08-30T12:00:00.000000Z" {
						t.Errorf("record At = %q, want dbt's generated_at", r.At)
					}
					if r.Evidence == "" {
						t.Errorf("record %+v has no evidence", r)
					}
				}
				data := resultData(t, res)
				if data["worstVerdict"] != oracle.VerdictPass {
					t.Errorf("worstVerdict = %v, want pass", data["worstVerdict"])
				}
				if data["dbt_exit"] != 0 {
					t.Errorf("dbt_exit = %v, want 0", data["dbt_exit"])
				}
				// Records must arrive in dependency order.
				cells, _ := data["cells"].([]map[string]interface{})
				if len(cells) != 2 || cells[0]["id"] != "orders" || cells[1]["id"] != "daily_totals" {
					t.Errorf("cells = %v, want orders then daily_totals", cells)
				}
				if tail, _ := data["dbt_tail"].(string); !strings.Contains(tail, "Done.") {
					t.Errorf("dbt_tail = %q, want the last dbt lines", tail)
				}
			},
		},
		{
			name:   "run with a failing test still succeeds and shows the fail verdict",
			doc:    validProjectDoc,
			action: "run",
			// A failing grain test is a SUCCESSFUL verification run: the
			// oracle worked. Reporting it as a tool error would teach the
			// agent to retry rather than to fix the cell.
			dbtMode:     dbtFailedTest,
			wantSuccess: true,
			check: func(t *testing.T, res *shuttle.Result, _ runProjectEnv) {
				records := oracle.RecordsFrom(res)
				if len(records) != 5 {
					t.Fatalf("records = %d, want 5", len(records))
				}
				fails := 0
				for _, r := range records {
					if r.Verdict == oracle.VerdictFail {
						fails++
						if r.Rung != oracle.RungGrain {
							t.Errorf("failing record on rung %q, want grain", r.Rung)
						}
						if !strings.Contains(r.Evidence, "3 failing rows") {
							t.Errorf("evidence %q omits the failing row count", r.Evidence)
						}
					}
				}
				if fails != 1 {
					t.Errorf("fail verdicts = %d, want 1", fails)
				}
				data := resultData(t, res)
				if data["worstVerdict"] != oracle.VerdictFail {
					t.Errorf("worstVerdict = %v, want fail", data["worstVerdict"])
				}
				if data["dbt_exit"] != 1 {
					t.Errorf("dbt_exit = %v, want 1 (dbt reports test failures with a nonzero exit)", data["dbt_exit"])
				}
			},
		},
		{
			name:        "run captures one preview per model cell",
			doc:         validProjectDoc,
			action:      "run",
			dbtMode:     dbtPass,
			wantSuccess: true,
			check: func(t *testing.T, res *shuttle.Result, env runProjectEnv) {
				data := resultData(t, res)
				if data["previews"] != 2 {
					t.Errorf("previews = %v, want 2 (one per model cell)", data["previews"])
				}
				// The preview selection IS the model selection: the prose cell
				// and the source-less call cell never became models.
				if got := relFiles(t, previewsDir(env.buildDir)); len(got) != 2 ||
					got[0] != "daily_totals.json" || got[1] != "orders.json" {
					t.Fatalf("preview files = %v, want exactly the two model cells", got)
				}
				for _, id := range []string{"orders", "daily_totals"} {
					p, raw := readPreview(t, env.buildDir, id)
					// Column ORDER is the payload's row-key order, not
					// alphabetical — sorting would put label first.
					if strings.Join(p.Columns, ",") != "order_day,total_amount,label" {
						t.Errorf("%s columns = %v, want dbt's column order", id, p.Columns)
					}
					if len(p.Rows) != 2 {
						t.Fatalf("%s rows = %v, want 2", id, p.Rows)
					}
					if len(p.Rows[0]) != 3 || p.Rows[0][0] != "2026-08-01" {
						t.Errorf("%s first row = %v, want values aligned to columns", id, p.Rows[0])
					}
					// A row's missing/null value stays null rather than
					// shifting the columns along.
					if p.Rows[1][2] != nil {
						t.Errorf("%s null cell = %v, want nil", id, p.Rows[1][2])
					}
					if p.Truncated {
						t.Errorf("%s Truncated = true with 2 rows", id)
					}
					// A warehouse id past 2^53 must survive verbatim: the
					// parser keeps numbers literal instead of via float64.
					if !strings.Contains(string(raw), "9007199254740993") {
						t.Errorf("%s lost number precision: %s", id, raw)
					}
				}
			},
		},
		{
			name:   "a run whose previews all fail is unchanged",
			doc:    validProjectDoc,
			action: "run",
			// Previews are BEST-EFFORT: dbt show erroring for every cell must
			// leave the records, the verdicts and the success untouched.
			dbtMode:     dbtShowFails,
			wantSuccess: true,
			check: func(t *testing.T, res *shuttle.Result, env runProjectEnv) {
				if got := len(oracle.RecordsFrom(res)); got != 5 {
					t.Errorf("records = %d, want the same 5 as a clean run", got)
				}
				data := resultData(t, res)
				if data["worstVerdict"] != oracle.VerdictPass {
					t.Errorf("worstVerdict = %v, want pass", data["worstVerdict"])
				}
				if data["previews"] != 0 {
					t.Errorf("previews = %v, want 0", data["previews"])
				}
				// No file at all: a missing preview IS the signal, so nothing
				// half-written is left behind.
				if got := relFiles(t, previewsDir(env.buildDir)); len(got) != 0 {
					t.Errorf("preview files = %v, want none", got)
				}
			},
		},
		{
			name:            "run without an artifact fails with the dbt tail",
			doc:             validProjectDoc,
			action:          "run",
			dbtMode:         dbtNoArtifact,
			wantSuccess:     false,
			wantErrCode:     "DBT_NO_ARTIFACT",
			wantErrContains: []string{"run_results.json", "Credentials in profile"},
			check: func(t *testing.T, res *shuttle.Result, env runProjectEnv) {
				if len(oracle.RecordsFrom(res)) != 0 {
					t.Error("no dbt artifact must mean no records")
				}
				if tail, _ := res.Error.Details["dbt_tail"].(string); !strings.Contains(tail, "Credentials") {
					t.Errorf("Details[dbt_tail] = %q, want the dbt error", tail)
				}
				// Nothing ran, so there is nothing to preview: capture is
				// skipped entirely rather than asking dbt show 2 more times.
				if _, err := os.Stat(previewsDir(env.buildDir)); !os.IsNotExist(err) {
					t.Errorf("previews directory exists (stat err %v) after a run that produced no artifact", err)
				}
			},
		},
		{
			name:            "run reports a missing dbt binary honestly",
			doc:             validProjectDoc,
			action:          "run",
			dbtMode:         dbtMissing,
			wantSuccess:     false,
			wantErrCode:     "DBT_NOT_FOUND",
			wantErrContains: []string{"not found on PATH"},
			check: func(t *testing.T, res *shuttle.Result, env runProjectEnv) {
				if !strings.Contains(res.Error.Suggestion, "LOOM_DBT_BIN") {
					t.Errorf("suggestion %q does not name LOOM_DBT_BIN", res.Error.Suggestion)
				}
				// Compile already happened, so the suggestion's pointer at the
				// build directory is truthful.
				if _, err := os.Stat(filepath.Join(env.buildDir, "dbt_project.yml")); err != nil {
					t.Errorf("compile output missing: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupRunProject(t, tt.doc)
			if tt.dbtMode != dbtNone {
				t.Setenv(project.EnvDBTBin, writeFakeDBT(t, tt.dbtMode))
				// Never let a test read a real profiles file.
				t.Setenv(project.EnvDBTProfilesDir, filepath.Join(env.dataDir, "dbt-profiles"))
			}

			ctx := session.ContextWithWorkingDir(context.Background(), env.repo)
			tool := NewRunProjectTool()
			res, err := tool.Execute(ctx, map[string]interface{}{
				"path":   "project.yaml", // relative: resolves inside the grant
				"action": tt.action,
			})
			if err != nil {
				t.Fatalf("Execute returned error: %v", err)
			}
			if res.Success != tt.wantSuccess {
				t.Fatalf("Success = %v, want %v (error %+v)", res.Success, tt.wantSuccess, res.Error)
			}
			if tt.wantSuccess {
				if res.Error != nil {
					t.Errorf("successful result carries error %+v", res.Error)
				}
			} else {
				if res.Error == nil {
					t.Fatal("failed result carries no error")
				}
				if tt.wantErrCode != "" && res.Error.Code != tt.wantErrCode {
					t.Errorf("Error.Code = %q, want %q", res.Error.Code, tt.wantErrCode)
				}
				for _, want := range tt.wantErrContains {
					if !strings.Contains(res.Error.Message, want) {
						t.Errorf("Error.Message %q does not contain %q", res.Error.Message, want)
					}
				}
			}
			if tt.check != nil {
				tt.check(t, res, env)
			}
		})
	}
}

func TestRunProjectTool_PathConfinement(t *testing.T) {
	tests := []struct {
		name string
		// path is built from the grant root and an outside root.
		path func(grant, outside string) string
	}{
		{
			name: "absolute path outside the grant",
			path: func(_, outside string) string { return filepath.Join(outside, "project.yaml") },
		},
		{
			name: "traversal escape from the grant",
			path: func(grant, _ string) string {
				return filepath.Join(filepath.Base(grant), "..", "..", "escape", "project.yaml")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateLoomDataDir(t)
			grant := grantTestRoot(t)
			outside := grantTestRoot(t)
			if err := os.WriteFile(filepath.Join(outside, "project.yaml"), []byte(validProjectDoc), 0o600); err != nil {
				t.Fatalf("write outside document: %v", err)
			}

			ctx := session.ContextWithWorkingDir(context.Background(), grant)
			res, err := NewRunProjectTool().Execute(ctx, map[string]interface{}{
				"path":   tt.path(grant, outside),
				"action": "validate",
			})
			if err != nil {
				t.Fatalf("Execute returned error: %v", err)
			}
			if res.Success {
				t.Fatalf("path outside the grant was accepted: %+v", res.Data)
			}
			if res.Error.Code != "PATH_RESTRICTED" {
				t.Fatalf("Error.Code = %q, want PATH_RESTRICTED", res.Error.Code)
			}
			if !strings.Contains(res.Error.Suggestion, grant) {
				t.Errorf("suggestion %q does not name the granted directory %q", res.Error.Suggestion, grant)
			}
		})
	}
}

func TestRunProjectTool_ParamValidation(t *testing.T) {
	tests := []struct {
		name     string
		params   map[string]interface{}
		wantCode string
	}{
		{name: "missing path", params: map[string]interface{}{}, wantCode: "INVALID_PARAMS"},
		{name: "blank path", params: map[string]interface{}{"path": "  "}, wantCode: "INVALID_PARAMS"},
		{name: "unknown action", params: map[string]interface{}{"path": "project.yaml", "action": "deploy"}, wantCode: "INVALID_PARAMS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := NewRunProjectTool().Execute(context.Background(), tt.params)
			if err != nil {
				t.Fatalf("Execute returned error: %v", err)
			}
			if res.Success || res.Error == nil || res.Error.Code != tt.wantCode {
				t.Fatalf("got success=%v error=%+v, want code %s", res.Success, res.Error, tt.wantCode)
			}
		})
	}
}

func TestRunProjectTool_Registration(t *testing.T) {
	tool := ByName("run_project")
	if tool == nil {
		t.Fatal("ByName(\"run_project\") returned nil")
	}
	if tool.Name() != "run_project" {
		t.Errorf("Name() = %q, want run_project", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description() is empty")
	}
	if tool.Backend() != "" {
		t.Errorf("Backend() = %q, want backend-agnostic", tool.Backend())
	}

	found := false
	for _, name := range Names() {
		if name == "run_project" {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() does not contain run_project: %v", Names())
	}

	inAll := false
	for _, tl := range All(nil) {
		if tl.Name() == "run_project" {
			inAll = true
		}
	}
	if !inAll {
		t.Error("All(nil) does not contain run_project")
	}
}

func TestRunProjectTool_InputSchema(t *testing.T) {
	schema := NewRunProjectTool().InputSchema()
	if schema.Type != "object" {
		t.Fatalf("schema type = %q, want object", schema.Type)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "path" {
		t.Errorf("required = %v, want [path]", schema.Required)
	}
	path := schema.Properties["path"]
	if path == nil || path.Type != "string" {
		t.Fatalf("path property = %+v, want a string schema", path)
	}
	action := schema.Properties["action"]
	if action == nil || action.Type != "string" {
		t.Fatalf("action property = %+v, want a string schema", action)
	}
	if action.Default != "run" {
		t.Errorf("action default = %v, want run", action.Default)
	}
	want := []interface{}{"validate", "compile", "run"}
	if len(action.Enum) != len(want) {
		t.Fatalf("action enum = %v, want %v", action.Enum, want)
	}
	for i, v := range want {
		if action.Enum[i] != v {
			t.Errorf("action enum[%d] = %v, want %v", i, action.Enum[i], v)
		}
	}
	if len(schema.Properties) != 2 {
		t.Errorf("schema has %d properties, want exactly path and action", len(schema.Properties))
	}
}

// TestParseDBTShow pins the tolerant parser against every payload shape dbt
// has wrapped its preview in, plus the noise it wraps them with. The parser is
// deliberately shape-agnostic (see project.ParseDBTShow) — these cases are the reason.
func TestParseDBTShow(t *testing.T) {
	// hundredRows is a payload at the row cap, which is how Truncated is
	// decided: dbt stopped at --limit, so there is more result than this.
	hundredRows := func() string {
		var b strings.Builder
		b.WriteString(`{"daily_totals": [`)
		for i := 0; i < 100; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"n": %d}`, i)
		}
		b.WriteString("]}")
		return b.String()
	}()

	tests := []struct {
		name          string
		output        string
		wantOK        bool
		wantColumns   []string
		wantRows      int
		wantTruncated bool
		// wantFirst is the first row, compared by its rendered JSON so a
		// json.Number and a float both read as their literal text.
		wantFirst string
	}{
		{
			name: "dbt 1.12 select shape, log-prefixed and pretty printed",
			output: "12:00:06  Running with dbt=1.12.0\n" +
				"12:00:07  Previewing node 'daily_totals':\n" +
				"12:00:07  {\n  \"daily_totals\": [\n" +
				"    {\"order_day\": \"2026-08-01\", \"total_amount\": 1234.5},\n" +
				"    {\"order_day\": \"2026-08-02\", \"total_amount\": 99}\n" +
				"  ]\n}\n12:00:08  Done.\n",
			wantOK:      true,
			wantColumns: []string{"order_day", "total_amount"},
			wantRows:    2,
			wantFirst:   `["2026-08-01",1234.5]`,
		},
		{
			name:        "inline show shape",
			output:      `{"show": [{"a": 1, "b": "x"}]}`,
			wantOK:      true,
			wantColumns: []string{"a", "b"},
			wantRows:    1,
			wantFirst:   `[1,"x"]`,
		},
		{
			name:        "object carrying the node name beside the rows",
			output:      "12:00:07  {\"node\": \"daily_totals\", \"show\": [{\"a\": 2}]}\n",
			wantOK:      true,
			wantColumns: []string{"a"},
			wantRows:    1,
			wantFirst:   `[2]`,
		},
		{
			name:        "bare array of rows",
			output:      "[{\"z\": 1, \"a\": 2}]\n",
			wantOK:      true,
			wantColumns: []string{"z", "a"}, // key order, not sorted
			wantRows:    1,
			wantFirst:   `[1,2]`,
		},
		{
			name:        "ragged rows: the first row's order, then the extras sorted",
			output:      `{"show": [{"b": 1, "a": 2}, {"b": 3, "a": 4, "z": 5, "c": 6}]}`,
			wantOK:      true,
			wantColumns: []string{"b", "a", "c", "z"},
			wantRows:    2,
			wantFirst:   `[1,2,null,null]`,
		},
		{
			name:          "a payload at the row cap is truncated",
			output:        hundredRows,
			wantOK:        true,
			wantColumns:   []string{"n"},
			wantRows:      100,
			wantTruncated: true,
			wantFirst:     `[0]`,
		},
		{
			name:   "log output with no payload",
			output: "12:00:06  Running with dbt=1.12.0\n12:00:07  Nothing to do.\n",
		},
		{
			name:   "a database error instead of a preview",
			output: "12:00:06  Database Error in model daily_totals: relation not found\n",
		},
		{
			// An empty result yields no preview file, and the absent file is
			// the UI's "no preview" signal — better than an empty grid that
			// claims the result is empty when the payload was simply unread.
			name:   "an empty row array is not a preview",
			output: `{"show": []}`,
		},
		{
			name:   "empty output",
			output: "",
		},
		{
			name:   "truncated JSON",
			output: `{"show": [{"a": 1`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := project.ParseDBTShow([]byte(tt.output))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (got %+v)", ok, tt.wantOK, got)
			}
			if !tt.wantOK {
				return
			}
			if strings.Join(got.Columns, ",") != strings.Join(tt.wantColumns, ",") {
				t.Errorf("columns = %v, want %v", got.Columns, tt.wantColumns)
			}
			if len(got.Rows) != tt.wantRows {
				t.Fatalf("rows = %d, want %d", len(got.Rows), tt.wantRows)
			}
			if got.Truncated != tt.wantTruncated {
				t.Errorf("Truncated = %v, want %v", got.Truncated, tt.wantTruncated)
			}
			first, err := json.Marshal(got.Rows[0])
			if err != nil {
				t.Fatalf("marshal first row: %v", err)
			}
			if string(first) != tt.wantFirst {
				t.Errorf("first row = %s, want %s", first, tt.wantFirst)
			}
			for i, row := range got.Rows {
				if len(row) != len(got.Columns) {
					t.Fatalf("row %d has %d values, want %d", i, len(row), len(got.Columns))
				}
			}
		})
	}
}
