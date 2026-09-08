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
	"context"
	"testing"

	"github.com/teradata-labs/loom/pkg/shuttle"
)

// prefixMock records calls and serves an MCP-shaped explain + execute pair
// under a server namespace, the shape seen live from teradata-pecto.
type prefixMock struct {
	calls []string
}

func (m *prefixMock) Execute(_ context.Context, toolName string, _ map[string]interface{}) (*shuttle.Result, error) {
	m.calls = append(m.calls, toolName)
	switch toolName {
	case "teradata-pecto_explain_query":
		return &shuttle.Result{Success: true, Data: "The total estimated result is 4,231 rows with high confidence."}, nil
	case "teradata-pecto_execute_query":
		rows := make([]interface{}, 4200)
		return &shuttle.Result{Success: true, Data: rows}, nil
	}
	return &shuttle.Result{Success: false, Error: &shuttle.Error{Message: "no such tool: " + toolName}}, nil
}

func TestMatchExecuteTool(t *testing.T) {
	e := NewVerifyingExecutor(&prefixMock{})
	cases := []struct {
		name   string
		tool   string
		prefix string
		ok     bool
	}{
		{"bare", "execute_query", "", true},
		{"mcp underscore", "teradata-pecto_execute_query", "teradata-pecto_", true},
		{"mcp colon", "teradata-pecto:execute_query", "teradata-pecto:", true},
		{"unrelated", "list_databases", "", false},
		{"suffix without separator rejected", "myexecute_query", "", false},
		{"empty", "", "", false},
	}
	for _, c := range cases {
		prefix, ok := e.matchExecuteTool(c.tool)
		if ok != c.ok || prefix != c.prefix {
			t.Errorf("%s: matchExecuteTool(%q) = (%q,%v), want (%q,%v)", c.name, c.tool, prefix, ok, c.prefix, c.ok)
		}
	}
}

func TestVerifyingExecutorPrefixedTools(t *testing.T) {
	mock := &prefixMock{}
	e := NewVerifyingExecutor(mock)

	res, err := e.Execute(context.Background(), "teradata-pecto_execute_query",
		map[string]interface{}{"query": "SELECT 1"})
	if err != nil {
		t.Fatal(err)
	}
	recs := RecordsFrom(res)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d (%+v)", len(recs), recs)
	}
	if recs[0].Rung != RungExplainPrediction || recs[0].Verdict != VerdictPass {
		t.Fatalf("record = %+v, want explain_prediction pass (4231 est vs 4200 actual)", recs[0])
	}
	// The explain ran in the SAME namespace, before the execute.
	if len(mock.calls) != 2 || mock.calls[0] != "teradata-pecto_explain_query" || mock.calls[1] != "teradata-pecto_execute_query" {
		t.Fatalf("calls = %v", mock.calls)
	}
}
