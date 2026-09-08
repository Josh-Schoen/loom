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

package oracle

import (
	"strings"
	"testing"
)

// multiSpoolPlan is Teradata-shaped: several spool estimates, the result
// spool last.
const multiSpoolPlan = `  1) First, we lock ecom.orders for read on a reserved RowHash to
     prevent global deadlock.
  2) Next, we do an all-AMPs RETRIEVE step from ecom.orders by way of an
     all-rows scan with no residual conditions into Spool 2 (all_amps),
     which is redistributed by the hash code of (ecom.orders.customer_id)
     to all AMPs.  The size of Spool 2 is estimated with high confidence
     to be 1,204,556 rows (37,341,236 bytes).  The estimated time for this
     step is 1.42 seconds.
  3) We do an all-AMPs JOIN step from Spool 2 by way of an all-rows scan,
     which is joined to ecom.customers.  The result goes into Spool 1
     (group_amps), which is built locally on the AMPs.  The size of
     Spool 1 is estimated with low confidence to be 4,231 rows
     (139,623 bytes).  The estimated time for this step is 0.31 seconds.
  ->  The contents of Spool 1 are sent back to the user as the result of
      statement 1.  The total estimated time is 1.73 seconds.`

func TestParseTeradataExplain(t *testing.T) {
	tests := []struct {
		name           string
		plan           string
		wantFound      bool
		wantRows       int64
		wantConfidence string
	}{
		{
			name:           "multi spool prefers last estimate",
			plan:           multiSpoolPlan,
			wantFound:      true,
			wantRows:       4231,
			wantConfidence: "low confidence",
		},
		{
			name:           "thousands separators stripped",
			plan:           "The size of Spool 1 is estimated with high confidence to be 4,231 rows (139,623 bytes).",
			wantFound:      true,
			wantRows:       4231,
			wantConfidence: "high confidence",
		},
		{
			name:           "no confidence phrase",
			plan:           "The size of Spool 1 is estimated with no confidence to be 12 rows (400 bytes).",
			wantFound:      true,
			wantRows:       12,
			wantConfidence: "no confidence",
		},
		{
			name:           "index join confidence",
			plan:           "The size of Spool 3 is estimated with index join confidence to be 1,000,000 rows.",
			wantFound:      true,
			wantRows:       1000000,
			wantConfidence: "index join confidence",
		},
		{
			name:           "total returned rows sentence",
			plan:           "The total number of rows estimated to be returned is 87 rows.",
			wantFound:      true,
			wantRows:       87,
			wantConfidence: "",
		},
		{
			name:           "case insensitive",
			plan:           "THE SIZE OF SPOOL 1 IS ESTIMATED WITH HIGH CONFIDENCE TO BE 55 ROWS.",
			wantFound:      true,
			wantRows:       55,
			wantConfidence: "high confidence",
		},
		{
			name:           "singular row",
			plan:           "The size of Spool 1 is estimated with high confidence to be 1 row.",
			wantFound:      true,
			wantRows:       1,
			wantConfidence: "high confidence",
		},
		{
			name:      "no estimate found",
			plan:      "  1) First, we lock ecom.orders for read on a reserved RowHash.",
			wantFound: false,
		},
		{
			name:      "estimate without rows ignored",
			plan:      "The total estimated time is 1.73 seconds.",
			wantFound: false,
		},
		{
			name:      "row count without estimate ignored",
			plan:      "Confirmed 4,231 rows were locked.",
			wantFound: false,
		},
		{name: "empty plan", plan: "", wantFound: false},
		{
			name:      "unparseable number ignored",
			plan:      "The size of Spool 1 is estimated with high confidence to be 99999999999999999999999 rows.",
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseTeradataExplain(tt.plan)
			if got.Found != tt.wantFound {
				t.Fatalf("Found = %v, want %v (%+v)", got.Found, tt.wantFound, got)
			}
			if !tt.wantFound {
				return
			}
			if got.EstimatedRows != tt.wantRows {
				t.Errorf("EstimatedRows = %d, want %d", got.EstimatedRows, tt.wantRows)
			}
			if got.Confidence != tt.wantConfidence {
				t.Errorf("Confidence = %q, want %q", got.Confidence, tt.wantConfidence)
			}
		})
	}
}

func TestPredictionCheck(t *testing.T) {
	found := func(rows int64, confidence string) Prediction {
		return Prediction{EstimatedRows: rows, Confidence: confidence, Found: true}
	}

	tests := []struct {
		name           string
		prediction     Prediction
		actual         int64
		wantVerdict    string
		wantInEvidence []string
	}{
		{
			name:           "not found skips",
			prediction:     Prediction{},
			actual:         100,
			wantVerdict:    VerdictSkip,
			wantInEvidence: []string{"no row estimate"},
		},
		{
			name:           "unknown actual skips",
			prediction:     found(4231, "high confidence"),
			actual:         -1,
			wantVerdict:    VerdictSkip,
			wantInEvidence: []string{"actual row count unavailable", "4231", "high confidence"},
		},
		{
			name:           "exact match passes",
			prediction:     found(4231, "high confidence"),
			actual:         4231,
			wantVerdict:    VerdictPass,
			wantInEvidence: []string{"4231 rows", "high confidence", "exact match"},
		},
		{
			name:           "confidence unstated rendered",
			prediction:     found(100, ""),
			actual:         100,
			wantVerdict:    VerdictPass,
			wantInEvidence: []string{"confidence unstated"},
		},
		{
			name:        "both zero passes",
			prediction:  found(0, "low confidence"),
			actual:      0,
			wantVerdict: VerdictPass,
		},
		{
			name:        "10x over is pass boundary",
			prediction:  found(1000, "low confidence"),
			actual:      100,
			wantVerdict: VerdictPass,
		},
		{
			name:        "10x under is pass boundary",
			prediction:  found(100, "low confidence"),
			actual:      1000,
			wantVerdict: VerdictPass,
		},
		{
			name:        "just past 10x warns",
			prediction:  found(1001, "low confidence"),
			actual:      100,
			wantVerdict: VerdictWarn,
		},
		{
			name:        "1000x is warn boundary",
			prediction:  found(100000, "low confidence"),
			actual:      100,
			wantVerdict: VerdictWarn,
		},
		{
			name:           "just past 1000x fails",
			prediction:     found(100001, "low confidence"),
			actual:         100,
			wantVerdict:    VerdictFail,
			wantInEvidence: []string{"off by"},
		},
		{
			name:           "large estimate zero actual fails",
			prediction:     found(1000, "high confidence"),
			actual:         0,
			wantVerdict:    VerdictFail,
			wantInEvidence: []string{"predicted a large result, got none"},
		},
		{
			name:        "sub-threshold estimate zero actual warns on ratio",
			prediction:  found(999, "high confidence"),
			actual:      0,
			wantVerdict: VerdictFail,
		},
		{
			name:           "one row estimate large actual fails",
			prediction:     found(1, "no confidence"),
			actual:         10000,
			wantVerdict:    VerdictFail,
			wantInEvidence: []string{"predicted at most one row"},
		},
		{
			name:           "zero estimate large actual fails",
			prediction:     found(0, "no confidence"),
			actual:         10000,
			wantVerdict:    VerdictFail,
			wantInEvidence: []string{"predicted at most one row"},
		},
		{
			name:        "one row estimate small actual warns",
			prediction:  found(1, "no confidence"),
			actual:      50,
			wantVerdict: VerdictWarn,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PredictionCheck(tt.prediction, tt.actual)
			if got.Verdict != tt.wantVerdict {
				t.Errorf("verdict = %q, want %q (evidence: %s)", got.Verdict, tt.wantVerdict, got.Evidence)
			}
			if got.Rung != RungExplainPrediction {
				t.Errorf("rung = %q, want %q", got.Rung, RungExplainPrediction)
			}
			if got.At == "" {
				t.Error("At not stamped")
			}
			for _, want := range tt.wantInEvidence {
				if !strings.Contains(got.Evidence, want) {
					t.Errorf("evidence %q missing %q", got.Evidence, want)
				}
			}
		})
	}
}
