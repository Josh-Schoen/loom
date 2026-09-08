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
	"strings"
	"testing"

	"github.com/teradata-labs/loom/pkg/project/oracle"
)

// passingRunResults is the run_results.json shape the spike produced (dbt
// 1.12.3 / dbt-teradata 1.11.0): two models built, the generated grain test
// and the generated singular metamorphic test both passing. The macro node
// is present because run-operation nodes appear in the same array and must
// be ignored.
const passingRunResults = `{
  "metadata": {
    "dbt_schema_version": "https://schemas.getdbt.com/dbt/run-results/v6.json",
    "dbt_version": "1.12.3",
    "generated_at": "2026-08-30T15:59:41.447618Z",
    "invocation_id": "6fb3bef2-7dbc-45a3-9c58-1e415e11428a"
  },
  "results": [
    {
      "status": "success",
      "execution_time": 2.133,
      "adapter_response": {"_message": "OK", "rows_affected": 100},
      "message": "OK",
      "failures": null,
      "unique_id": "model.at_risk_revenue.orders"
    },
    {
      "status": "success",
      "execution_time": 1.456,
      "adapter_response": {"_message": "OK", "rows_affected": 10},
      "message": "OK",
      "failures": null,
      "unique_id": "model.at_risk_revenue.customer_totals"
    },
    {
      "status": "pass",
      "execution_time": 0.743,
      "adapter_response": {},
      "message": null,
      "failures": 0,
      "unique_id": "test.at_risk_revenue.loom_grain_unique_customer_totals_customer_id.140ada432e"
    },
    {
      "status": "pass",
      "execution_time": 0.862,
      "adapter_response": {},
      "message": null,
      "failures": 0,
      "unique_id": "test.at_risk_revenue.loom_partition_sums_customer_totals"
    },
    {
      "status": "success",
      "execution_time": 6.777,
      "adapter_response": {},
      "message": null,
      "failures": 0,
      "unique_id": "macro.at_risk_revenue.loom_teardown"
    }
  ],
  "elapsed_time": 12.9
}`

// failingRunResults: a model errored, the grain test failed with failing
// rows, the metamorphic test was skipped because its parent failed.
const failingRunResults = `{
  "metadata": {"generated_at": "2026-08-30T16:20:00.000000Z"},
  "results": [
    {
      "status": "success",
      "execution_time": 2.0,
      "adapter_response": {"rows_affected": 100},
      "unique_id": "model.at_risk_revenue.orders"
    },
    {
      "status": "error",
      "execution_time": 0.5,
      "adapter_response": {},
      "message": "[Teradata Database] [Error 3706] Syntax error",
      "unique_id": "model.at_risk_revenue.customer_totals"
    },
    {
      "status": "fail",
      "execution_time": 0.9,
      "adapter_response": {},
      "failures": 3,
      "unique_id": "test.at_risk_revenue.loom_grain_unique_customer_totals_customer_id.140ada432e"
    },
    {
      "status": "skipped",
      "execution_time": 0.0,
      "adapter_response": {},
      "unique_id": "test.at_risk_revenue.loom_partition_sums_customer_totals"
    }
  ]
}`

func findRecord(recs []oracle.VerificationRecord, rung string) (oracle.VerificationRecord, bool) {
	for _, r := range recs {
		if r.Rung == rung {
			return r, true
		}
	}
	return oracle.VerificationRecord{}, false
}

func TestRecordsFromRunResults(t *testing.T) {
	t.Parallel()

	type want struct {
		cell     string
		rung     string
		verdict  string
		evidence []string // substrings
		costMs   int64
	}

	tests := []struct {
		name    string
		json    string
		cells   []string // exactly the keys expected
		records []want
	}{
		{
			name:  "all green",
			json:  passingRunResults,
			cells: []string{"customer_totals", "orders"},
			records: []want{
				{
					cell: "orders", rung: RungDBTRun, verdict: oracle.VerdictPass,
					evidence: []string{"dbt run orders", "success", "rows_affected 100", "2.133s"},
					costMs:   2133,
				},
				{
					cell: "customer_totals", rung: RungDBTRun, verdict: oracle.VerdictPass,
					evidence: []string{"rows_affected 10"},
					costMs:   1456,
				},
				{
					cell: "customer_totals", rung: oracle.RungGrain, verdict: oracle.VerdictPass,
					evidence: []string{"loom_grain_unique_customer_totals_customer_id", "pass", "0 failing rows"},
					costMs:   743,
				},
				{
					cell: "customer_totals", rung: RungMetamorphic, verdict: oracle.VerdictPass,
					evidence: []string{"loom_partition_sums_customer_totals", "pass"},
					costMs:   862,
				},
			},
		},
		{
			name:  "model error and failing grain",
			json:  failingRunResults,
			cells: []string{"customer_totals", "orders"},
			records: []want{
				{
					cell: "customer_totals", rung: RungDBTRun, verdict: oracle.VerdictFail,
					evidence: []string{"error", "rows_affected unavailable", "Error 3706"},
				},
				{
					cell: "customer_totals", rung: oracle.RungGrain, verdict: oracle.VerdictFail,
					evidence: []string{"3 failing rows"},
				},
				{
					cell: "customer_totals", rung: RungMetamorphic, verdict: oracle.VerdictSkip,
					evidence: []string{"skipped"},
				},
				{
					cell: "orders", rung: RungDBTRun, verdict: oracle.VerdictPass,
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := RecordsFromRunResults([]byte(tt.json))
			if err != nil {
				t.Fatalf("RecordsFromRunResults: %v", err)
			}
			if len(got) != len(tt.cells) {
				t.Fatalf("cells = %v, want %v", keysOf(got), tt.cells)
			}
			for _, cell := range tt.cells {
				if _, ok := got[cell]; !ok {
					t.Fatalf("cell %q missing; got %v", cell, keysOf(got))
				}
			}
			for _, w := range tt.records {
				rec, ok := findRecord(got[w.cell], w.rung)
				if !ok {
					t.Fatalf("cell %q has no %q record: %+v", w.cell, w.rung, got[w.cell])
				}
				if rec.Verdict != w.verdict {
					t.Fatalf("cell %q rung %q verdict = %q, want %q", w.cell, w.rung, rec.Verdict, w.verdict)
				}
				for _, sub := range w.evidence {
					if !strings.Contains(rec.Evidence, sub) {
						t.Fatalf("cell %q rung %q evidence %q missing %q", w.cell, w.rung, rec.Evidence, sub)
					}
				}
				if w.costMs != 0 && rec.CostMs != w.costMs {
					t.Fatalf("cell %q rung %q costMs = %d, want %d", w.cell, w.rung, rec.CostMs, w.costMs)
				}
				if rec.At == "" {
					t.Fatalf("cell %q rung %q has no timestamp", w.cell, w.rung)
				}
			}
		})
	}
}

func TestRecordsFromRunResultsTimestampFromArtifact(t *testing.T) {
	t.Parallel()

	got, err := RecordsFromRunResults([]byte(passingRunResults))
	if err != nil {
		t.Fatalf("RecordsFromRunResults: %v", err)
	}
	for cell, recs := range got {
		for _, r := range recs {
			if r.At != "2026-08-30T15:59:41.447618Z" {
				t.Fatalf("cell %q rung %q at = %q, want the artifact's generated_at", cell, r.Rung, r.At)
			}
		}
	}
}

func TestRecordsFromRunResultsMalformed(t *testing.T) {
	t.Parallel()

	if _, err := RecordsFromRunResults([]byte("not json")); err == nil {
		t.Fatal("RecordsFromRunResults: want error for malformed JSON")
	}
}

func TestCellForTest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		testName string
		models   []string
		want     string
		wantOK   bool
	}{
		{
			name:     "grain test",
			testName: "loom_grain_unique_customer_totals_customer_id",
			models:   []string{"orders", "customer_totals"},
			want:     "customer_totals", wantOK: true,
		},
		{
			name:     "longest model wins",
			testName: "loom_grain_unique_customer_totals_customer_id",
			models:   []string{"totals", "customer_totals"},
			want:     "customer_totals", wantOK: true,
		},
		{
			name:     "partition test",
			testName: "loom_partition_sums_customer_totals",
			models:   []string{"orders", "customer_totals"},
			want:     "customer_totals", wantOK: true,
		},
		{
			name:     "no model matches",
			testName: "loom_grain_unique_unknown_thing_id",
			models:   []string{"orders"},
			want:     "", wantOK: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := cellForTest(tt.testName, tt.models)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("cellForTest = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestNodeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		uniqueID string
		resource string
		want     string
		wantOK   bool
	}{
		{"model.p.orders", "model", "orders", true},
		{"test.p.loom_partition_sums_totals", "test", "loom_partition_sums_totals", true},
		{"test.p.loom_grain_unique_totals_id.140ada432e", "test", "loom_grain_unique_totals_id", true},
		{"model.p.orders", "test", "", false},
		{"macro.p.loom_teardown", "model", "", false},
		{"model.p", "model", "", false},
		{"", "model", "", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.uniqueID+"/"+tt.resource, func(t *testing.T) {
			t.Parallel()
			got, ok := nodeName(tt.uniqueID, tt.resource)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("nodeName(%q, %q) = (%q, %v), want (%q, %v)",
					tt.uniqueID, tt.resource, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestRecordsFromAudit(t *testing.T) {
	t.Parallel()

	doc, err := Parse([]byte(exampleDocument))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	tests := []struct {
		name  string
		rows  []AuditRow
		doc   *Document
		cells map[string]string // cell → evidence substring
	}{
		{
			name: "adjacent pairs with grain",
			rows: []AuditRow{
				{ModelName: "orders", RowsOut: 100},
				{ModelName: "customer_totals", RowsOut: 10},
				{ModelName: "customer_report", RowsOut: 9},
			},
			doc: doc,
			cells: map[string]string{
				"customer_totals": "grain customer_id at customer_totals: rows in 100 from orders, rows out 10",
				"customer_report": "rows in 10 from customer_totals, rows out 9",
			},
		},
		{
			name:  "upstream missing from audit",
			rows:  []AuditRow{{ModelName: "customer_totals", RowsOut: 10}},
			doc:   doc,
			cells: map[string]string{},
		},
		{
			name:  "nil document",
			rows:  []AuditRow{{ModelName: "orders", RowsOut: 100}},
			doc:   nil,
			cells: map[string]string{},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := RecordsFromAudit(tt.rows, tt.doc)
			if len(got) != len(tt.cells) {
				t.Fatalf("cells = %v, want %d entries", keysOf(got), len(tt.cells))
			}
			for cell, sub := range tt.cells {
				recs, ok := got[cell]
				if !ok || len(recs) != 1 {
					t.Fatalf("cell %q records = %+v", cell, recs)
				}
				if recs[0].Rung != oracle.RungGrain {
					t.Fatalf("cell %q rung = %q", cell, recs[0].Rung)
				}
				if recs[0].Verdict != oracle.VerdictPass {
					t.Fatalf("cell %q verdict = %q, want informational pass", cell, recs[0].Verdict)
				}
				if !strings.Contains(recs[0].Evidence, sub) {
					t.Fatalf("cell %q evidence %q missing %q", cell, recs[0].Evidence, sub)
				}
			}
		})
	}
}

func keysOf(m map[string][]oracle.VerificationRecord) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
