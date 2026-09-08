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

func TestGrainCheck(t *testing.T) {
	tests := []struct {
		name            string
		grain           string
		total           int64
		distinct        int64
		wantVerdict     string
		wantInEvidence  []string
		wantNotEvidence []string
	}{
		{
			name:           "exact grain passes",
			grain:          "customer_id",
			total:          4231,
			distinct:       4231,
			wantVerdict:    VerdictPass,
			wantInEvidence: []string{"customer_id", "4231 rows", "4231 distinct"},
		},
		{
			name:           "zero rows passes",
			grain:          "customer_id",
			total:          0,
			distinct:       0,
			wantVerdict:    VerdictPass,
			wantInEvidence: []string{"0 rows"},
		},
		{
			name:           "duplicates fail with duplicate count",
			grain:          "customer_id",
			total:          4231,
			distinct:       4017,
			wantVerdict:    VerdictFail,
			wantInEvidence: []string{"grain customer_id violated", "4231 rows", "4017 distinct", "214 duplicates", "fan-out"},
		},
		{
			name:           "single duplicate fails",
			grain:          "db.tbl_key",
			total:          2,
			distinct:       1,
			wantVerdict:    VerdictFail,
			wantInEvidence: []string{"1 duplicates"},
		},
		{
			name:           "no declared grain skips",
			grain:          "",
			total:          10,
			distinct:       10,
			wantVerdict:    VerdictSkip,
			wantInEvidence: []string{"no declared grain"},
		},
		{
			name:           "whitespace grain skips",
			grain:          "   ",
			total:          10,
			distinct:       10,
			wantVerdict:    VerdictSkip,
			wantInEvidence: []string{"no declared grain"},
		},
		{
			name:           "negative total skips",
			grain:          "customer_id",
			total:          -1,
			distinct:       10,
			wantVerdict:    VerdictSkip,
			wantInEvidence: []string{"row counts unavailable"},
		},
		{
			name:           "negative distinct skips",
			grain:          "customer_id",
			total:          10,
			distinct:       -1,
			wantVerdict:    VerdictSkip,
			wantInEvidence: []string{"row counts unavailable"},
		},
		{
			name:           "distinct above total skips",
			grain:          "customer_id",
			total:          10,
			distinct:       11,
			wantVerdict:    VerdictSkip,
			wantInEvidence: []string{"counts disagree"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GrainCheck(tt.grain, tt.total, tt.distinct)
			if got.Verdict != tt.wantVerdict {
				t.Errorf("verdict = %q, want %q (evidence: %s)", got.Verdict, tt.wantVerdict, got.Evidence)
			}
			if got.Rung != RungGrain {
				t.Errorf("rung = %q, want %q", got.Rung, RungGrain)
			}
			if got.At == "" {
				t.Error("At not stamped")
			}
			if got.CostMs < 0 {
				t.Errorf("CostMs = %d, want >= 0", got.CostMs)
			}
			for _, want := range tt.wantInEvidence {
				if !strings.Contains(got.Evidence, want) {
					t.Errorf("evidence %q missing %q", got.Evidence, want)
				}
			}
		})
	}
}

func TestGrainCountSQL(t *testing.T) {
	const inner = "SELECT customer_id FROM orders"

	tests := []struct {
		name  string
		grain string
		inner string
		want  string
	}{
		{
			name:  "bare identifier",
			grain: "customer_id",
			inner: inner,
			want:  "SELECT COUNT(*) AS total_rows, COUNT(DISTINCT customer_id) AS distinct_rows FROM (SELECT customer_id FROM orders) loom_grain_check",
		},
		{
			name:  "qualified identifier",
			grain: "db.tbl_key",
			inner: inner,
			want:  "SELECT COUNT(*) AS total_rows, COUNT(DISTINCT db.tbl_key) AS distinct_rows FROM (SELECT customer_id FROM orders) loom_grain_check",
		},
		{
			name:  "underscore prefix accepted",
			grain: "_internal_id",
			inner: inner,
			want:  "SELECT COUNT(*) AS total_rows, COUNT(DISTINCT _internal_id) AS distinct_rows FROM (SELECT customer_id FROM orders) loom_grain_check",
		},
		{
			name:  "digits inside accepted",
			grain: "col2",
			inner: inner,
			want:  "SELECT COUNT(*) AS total_rows, COUNT(DISTINCT col2) AS distinct_rows FROM (SELECT customer_id FROM orders) loom_grain_check",
		},
		{
			name:  "surrounding whitespace trimmed",
			grain: "  customer_id  ",
			inner: "  " + inner + " ;  ",
			want:  "SELECT COUNT(*) AS total_rows, COUNT(DISTINCT customer_id) AS distinct_rows FROM (SELECT customer_id FROM orders) loom_grain_check",
		},
		{name: "statement injection refused", grain: "1; DROP TABLE x", inner: inner, want: ""},
		{name: "comment injection refused", grain: "a--b", inner: inner, want: ""},
		{name: "block comment refused", grain: "a/*b*/", inner: inner, want: ""},
		{name: "spaces refused", grain: "customer id", inner: inner, want: ""},
		{name: "double quotes refused", grain: `"customer_id"`, inner: inner, want: ""},
		{name: "single quotes refused", grain: "'customer_id'", inner: inner, want: ""},
		{name: "backtick refused", grain: "`customer_id`", inner: inner, want: ""},
		{name: "parens refused", grain: "COALESCE(a,b)", inner: inner, want: ""},
		{name: "star refused", grain: "*", inner: inner, want: ""},
		{name: "leading digit refused", grain: "1col", inner: inner, want: ""},
		{name: "empty part refused", grain: "db..col", inner: inner, want: ""},
		{name: "trailing dot refused", grain: "db.", inner: inner, want: ""},
		{name: "newline refused", grain: "col\nDROP", inner: inner, want: ""},
		{name: "semicolon refused", grain: "col;", inner: inner, want: ""},
		{name: "empty grain refused", grain: "", inner: inner, want: ""},
		{name: "empty inner refused", grain: "customer_id", inner: "   ", want: ""},
		{name: "inner reduced to semicolons refused", grain: "customer_id", inner: " ; ", want: ""},
		{name: "overlong part refused", grain: strings.Repeat("a", 129), inner: inner, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GrainCountSQL(tt.grain, tt.inner); got != tt.want {
				t.Errorf("GrainCountSQL() = %q, want %q", got, tt.want)
			}
		})
	}
}
