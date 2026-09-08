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

// declared_grain accepts a scalar or a sequence — the first live agentic
// probe wrote `declared_grain: [customer_id]` and was rejected; the API
// bends, not the agent. Composite grains validate per column.

import (
	"strings"
	"testing"
)

func grainDoc(grain string) string {
	return strings.ReplaceAll(`apiVersion: loom/v1
kind: Project
metadata: {name: g}
cells:
  - id: base
    lang: sql
    source: SELECT 1 AS a
  - id: agg
    lang: sql
    inputs: [base]
    declared_grain: GRAIN
    source: SELECT a FROM {{ ref('base') }} GROUP BY a
`, "GRAIN", grain)
}

func TestDeclaredGrainShapes(t *testing.T) {
	cases := []struct {
		name  string
		grain string
		want  string
		bad   bool
	}{
		{"scalar", "customer_id", "customer_id", false},
		{"single-element list", "[customer_id]", "customer_id", false},
		{"composite list", "[customer_id, month]", "customer_id, month", false},
		{"qualified in list", "[db.tbl_key]", "db.tbl_key", false},
		{"injection in list", "[\"1; DROP TABLE x\"]", "", true},
		{"empty list", "[]", "", false},
	}
	for _, c := range cases {
		doc, err := Parse([]byte(grainDoc(c.grain)))
		if c.bad {
			if err == nil {
				t.Errorf("%s: want rejection", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := string(doc.Cells[1].DeclaredGrain); got != c.want {
			t.Errorf("%s: grain = %q, want %q", c.name, got, c.want)
		}
	}
}
