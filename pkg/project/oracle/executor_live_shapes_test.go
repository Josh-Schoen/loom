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

// Shapes observed live from teradata-pecto on 2026-08-30: results arrive as
// a JSON STRING payload with row_count and a truncated flag; the explain
// tool rejects execute-only params (max_rows). Each earned its regression.

import (
	"context"
	"strings"
	"testing"

	"github.com/teradata-labs/loom/pkg/shuttle"
)

func TestRowsFromJSONStringPayload(t *testing.T) {
	cases := []struct {
		name      string
		data      interface{}
		rows      int64
		truncated bool
	}{
		{"pecto clean", `{"columns":[{"name":"n"}],"row_count":458,"rows":[[458]],"truncated":false}`, 458, false},
		{"pecto truncated", `{"columns":[],"guidance":"Result truncated.","row_count":100,"rows":[],"truncated":true}`, 100, true},
		{"json array", `[{"a":1},{"a":2}]`, 2, false},
		{"bytes payload", []byte(`{"row_count":7}`), 7, false},
		{"plain prose", "there were many rows", -1, false},
		{"json string literal", `"not a payload"`, -1, false},
		{"broken json", `{"row_count":`, -1, false},
		{"map with truncated", map[string]interface{}{"row_count": float64(100), "truncated": true}, 100, true},
	}
	for _, c := range cases {
		rows, truncated := rowsFromData(c.data)
		if rows != c.rows || truncated != c.truncated {
			t.Errorf("%s: rowsFromData = (%d,%v), want (%d,%v)", c.name, rows, truncated, c.rows, c.truncated)
		}
	}
}

// truncMock serves a plan predicting far more rows than the truncated result
// carries — the comparison must SKIP, not warn.
type truncMock struct{ calls []map[string]interface{} }

func (m *truncMock) Execute(_ context.Context, toolName string, params map[string]interface{}) (*shuttle.Result, error) {
	m.calls = append(m.calls, params)
	if strings.Contains(toolName, "explain_query") {
		return &shuttle.Result{Success: true, Data: "The total estimated result is 2,046 rows with no confidence."}, nil
	}
	return &shuttle.Result{Success: true,
		Data: `{"columns":[{"name":"DatabaseName"}],"row_count":100,"rows":[],"truncated":true}`}, nil
}

func TestTruncatedResultSkipsNotWarns(t *testing.T) {
	mock := &truncMock{}
	e := NewVerifyingExecutor(mock)
	res, err := e.Execute(context.Background(), "teradata-pecto_execute_query",
		map[string]interface{}{"sql": "SELECT DatabaseName FROM DBC.DatabasesV", "max_rows": 100})
	if err != nil {
		t.Fatal(err)
	}
	recs := RecordsFrom(res)
	if len(recs) != 1 || recs[0].Verdict != VerdictSkip {
		t.Fatalf("want one skip record, got %+v", recs)
	}
	if !strings.Contains(recs[0].Evidence, "truncated") || !strings.Contains(recs[0].Evidence, "2046") {
		t.Fatalf("evidence should name the prediction and the truncation: %s", recs[0].Evidence)
	}
	// The explain call must carry ONLY the statement — max_rows broke the
	// explain tool's schema validation live.
	explainParams := mock.calls[0]
	if _, leaked := explainParams["max_rows"]; leaked {
		t.Fatalf("execute-only param leaked into explain call: %v", explainParams)
	}
	if explainParams["sql"] != "SELECT DatabaseName FROM DBC.DatabasesV" {
		t.Fatalf("explain params lost the statement: %v", explainParams)
	}
}

func TestUntruncatedStringPayloadGetsVerdict(t *testing.T) {
	mock := &cleanStringMock{}
	e := NewVerifyingExecutor(mock)
	res, err := e.Execute(context.Background(), "execute_query",
		map[string]interface{}{"query": "SELECT COUNT(*) FROM t"})
	if err != nil {
		t.Fatal(err)
	}
	recs := RecordsFrom(res)
	if len(recs) != 1 || recs[0].Verdict != VerdictPass {
		t.Fatalf("want pass (1 predicted vs 1 actual), got %+v", recs)
	}
}

type cleanStringMock struct{}

func (m *cleanStringMock) Execute(_ context.Context, toolName string, _ map[string]interface{}) (*shuttle.Result, error) {
	if strings.Contains(toolName, "explain_query") {
		return &shuttle.Result{Success: true, Data: "The total estimated result is 1 row with high confidence."}, nil
	}
	return &shuttle.Result{Success: true, Data: `{"columns":[{"name":"c"}],"row_count":1,"rows":[[458]],"truncated":false}`}, nil
}
