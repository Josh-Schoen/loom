// Copyright 2026 Teradata
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package project

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/teradata-labs/loom/pkg/project/oracle"
)

// Rungs recorded from dbt artifacts. RungGrain lives in oracle; these two
// are the runner-time rungs the compiled project adds.
const (
	RungDBTRun      = "dbt_run"
	RungMetamorphic = "metamorphic"
)

// Generated test-name prefixes, the seam that maps a dbt test back to the
// cell it guards.
const (
	grainTestPrefix     = "loom_grain_unique_"
	partitionTestPrefix = "loom_partition_sums_"
)

// runResults is the subset of dbt's run_results.json this package reads.
type runResults struct {
	Metadata struct {
		GeneratedAt string `json:"generated_at"`
	} `json:"metadata"`
	Results []runResult `json:"results"`
}

type runResult struct {
	UniqueID      string  `json:"unique_id"`
	Status        string  `json:"status"`
	ExecutionTime float64 `json:"execution_time"`
	Message       *string `json:"message"`
	Failures      *int64  `json:"failures"`
	AdapterResp   struct {
		RowsAffected *int64 `json:"rows_affected"`
	} `json:"adapter_response"`
}

// AuditRow is one row of the loom_audit table written by the generated
// post-hook.
type AuditRow struct {
	ModelName string
	RowsOut   int64
}

// RecordsFromRunResults folds a dbt run_results.json into per-cell
// verification records. Keys are cell IDs. Records carry dbt's own
// generated_at as their timestamp — the artifact is the clock, so folding is
// deterministic. Nodes that are neither models nor recognizable generated
// tests are ignored.
// NOTE: feed this the artifact of a `dbt build` (models + tests in ONE
// run_results.json). A `dbt test`-only artifact carries no model results,
// so its tests cannot be mapped to cells and fold to nothing — found live
// on the first e2e run, 2026-08-30.
func RecordsFromRunResults(runResultsJSON []byte) (map[string][]oracle.VerificationRecord, error) {
	var rr runResults
	if err := json.Unmarshal(runResultsJSON, &rr); err != nil {
		return nil, fmt.Errorf("project: run_results: %w", err)
	}
	at := rr.Metadata.GeneratedAt

	// Model IDs first: test names are matched against them.
	var modelIDs []string
	for _, r := range rr.Results {
		if id, ok := nodeName(r.UniqueID, "model"); ok {
			modelIDs = append(modelIDs, id)
		}
	}

	out := map[string][]oracle.VerificationRecord{}
	for _, r := range rr.Results {
		if id, ok := nodeName(r.UniqueID, "model"); ok {
			out[id] = append(out[id], oracle.VerificationRecord{
				Rung:     RungDBTRun,
				Verdict:  verdictFor(r.Status),
				Evidence: runEvidence(id, r),
				CostMs:   int64(r.ExecutionTime * 1000),
				At:       at,
			})
			continue
		}
		testName, ok := nodeName(r.UniqueID, "test")
		if !ok {
			continue
		}
		rung, ok := testRung(testName)
		if !ok {
			continue
		}
		cell, ok := cellForTest(testName, modelIDs)
		if !ok {
			continue
		}
		out[cell] = append(out[cell], oracle.VerificationRecord{
			Rung:     rung,
			Verdict:  verdictFor(r.Status),
			Evidence: testEvidence(rung, testName, r),
			CostMs:   int64(r.ExecutionTime * 1000),
			At:       at,
		})
	}
	return out, nil
}

// nodeName splits a dbt unique_id of the form
// <resource>.<project>.<name>[.<checksum>] and returns <name>. Generic-test
// unique_ids carry a trailing checksum; singular tests and models do not.
func nodeName(uniqueID, resource string) (string, bool) {
	parts := strings.Split(uniqueID, ".")
	if len(parts) < 3 || parts[0] != resource {
		return "", false
	}
	name := parts[2]
	if name == "" {
		return "", false
	}
	return name, true
}

func testRung(testName string) (string, bool) {
	switch {
	case strings.HasPrefix(testName, grainTestPrefix):
		return oracle.RungGrain, true
	case strings.HasPrefix(testName, partitionTestPrefix):
		return RungMetamorphic, true
	default:
		return "", false
	}
}

// cellForTest maps a generated test name back to its cell. Defensive: a
// generic-test name is <prefix><model>_<grain>, so the model is found by
// longest match against the models actually in this run rather than by
// splitting on underscores.
func cellForTest(testName string, modelIDs []string) (string, bool) {
	best := ""
	for _, id := range modelIDs {
		if !strings.Contains(testName, id) {
			continue
		}
		if len(id) > len(best) {
			best = id
		}
	}
	return best, best != ""
}

func verdictFor(status string) string {
	switch strings.ToLower(status) {
	case "success", "pass":
		return oracle.VerdictPass
	case "fail", "error", "runtime error":
		return oracle.VerdictFail
	case "warn":
		return oracle.VerdictWarn
	default:
		// skipped, and anything a later dbt adds.
		return oracle.VerdictSkip
	}
}

func runEvidence(model string, r runResult) string {
	rows := "rows_affected unavailable"
	if r.AdapterResp.RowsAffected != nil {
		rows = fmt.Sprintf("rows_affected %d", *r.AdapterResp.RowsAffected)
	}
	ev := fmt.Sprintf("dbt run %s: %s, %s in %.3fs", model, r.Status, rows, r.ExecutionTime)
	if r.Message != nil && *r.Message != "" {
		ev += " — " + *r.Message
	}
	return ev
}

func testEvidence(rung, testName string, r runResult) string {
	ev := fmt.Sprintf("%s test %s: %s", rung, testName, r.Status)
	if r.Failures != nil {
		ev += fmt.Sprintf(", %d failing rows", *r.Failures)
	}
	ev += fmt.Sprintf(" in %.3fs", r.ExecutionTime)
	if r.Message != nil && *r.Message != "" {
		ev += " — " + *r.Message
	}
	return ev
}

// RecordsFromAudit folds the loom_audit table into per-cell records: for
// each grain-bearing cell, the rows that entered from each upstream cell
// against the rows that left. Informational — the grain verdict itself comes
// from the loom_grain_unique test, so these records always pass.
func RecordsFromAudit(rows []AuditRow, doc *Document) map[string][]oracle.VerificationRecord {
	out := map[string][]oracle.VerificationRecord{}
	if doc == nil {
		return out
	}
	rowsOut := make(map[string]int64, len(rows))
	for _, r := range rows {
		rowsOut[r.ModelName] = r.RowsOut
	}
	at := time.Now().UTC().Format(time.RFC3339)

	for _, c := range doc.Cells {
		if c.DeclaredGrain == "" {
			continue
		}
		got, ok := rowsOut[c.ID]
		if !ok {
			continue
		}
		for _, in := range c.Inputs {
			upstream, ok := rowsOut[in]
			if !ok {
				continue
			}
			out[c.ID] = append(out[c.ID], oracle.VerificationRecord{
				Rung:    oracle.RungGrain,
				Verdict: oracle.VerdictPass,
				Evidence: fmt.Sprintf("grain %s at %s: rows in %d from %s, rows out %d",
					c.DeclaredGrain, c.ID, upstream, in, got),
				At: at,
			})
		}
	}
	return out
}
