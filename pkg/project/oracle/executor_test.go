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
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/teradata-labs/loom/pkg/shuttle"
)

// mockExecutor records calls and returns canned results per tool.
type mockExecutor struct {
	mu      sync.Mutex
	calls   []string
	results map[string]*shuttle.Result
	errs    map[string]error
}

func newMockExecutor() *mockExecutor {
	return &mockExecutor{
		results: make(map[string]*shuttle.Result),
		errs:    make(map[string]error),
	}
}

func (m *mockExecutor) Execute(ctx context.Context, toolName string, params map[string]interface{}) (*shuttle.Result, error) {
	m.mu.Lock()
	m.calls = append(m.calls, toolName)
	m.mu.Unlock()

	if err, ok := m.errs[toolName]; ok {
		return nil, err
	}
	if result, ok := m.results[toolName]; ok {
		return result, nil
	}
	return nil, errors.New("tool not found: " + toolName)
}

func (m *mockExecutor) called() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.calls...)
}

// panicStringer panics when the plan-text extractor reads it.
type panicStringer struct{}

func (panicStringer) String() string { panic("plan text exploded") }

func explainResult(planText interface{}) *shuttle.Result {
	return &shuttle.Result{Success: true, Data: planText}
}

func TestVerifyingExecutor_ShuttleExecutorSatisfiesSeam(t *testing.T) {
	var _ ToolExecutor = (*shuttle.Executor)(nil)
	var _ ToolExecutor = (*shuttle.InstrumentedExecutor)(nil)
}

func TestVerifyingExecutor_PassThrough(t *testing.T) {
	tests := []struct {
		name   string
		tool   string
		params map[string]interface{}
	}{
		{name: "non sql tool", tool: "file_read", params: map[string]interface{}{"path": "/tmp/x"}},
		{name: "sql tool without statement", tool: "execute_query", params: map[string]interface{}{"max_rows": 10}},
		{name: "sql tool with empty statement", tool: "execute_query", params: map[string]interface{}{"sql": "  "}},
		{name: "sql tool with non string statement", tool: "execute_query", params: map[string]interface{}{"sql": 42}},
		{name: "explain tool called directly", tool: "explain_query", params: map[string]interface{}{"sql": "SELECT 1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := newMockExecutor()
			want := &shuttle.Result{
				Success:         true,
				Data:            []interface{}{map[string]interface{}{"a": 1}},
				Metadata:        map[string]interface{}{"backend": "file"},
				ExecutionTimeMs: 7,
			}
			inner.results[tt.tool] = want

			got, err := NewVerifyingExecutor(inner).Execute(context.Background(), tt.tool, tt.params)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got != want {
				t.Fatalf("result was replaced, want the inner result verbatim")
			}
			if _, ok := got.Metadata[MetadataKey]; ok {
				t.Errorf("verification metadata attached to pass-through tool")
			}
			if len(got.Metadata) != 1 {
				t.Errorf("metadata mutated: %v", got.Metadata)
			}
			if calls := inner.called(); len(calls) != 1 || calls[0] != tt.tool {
				t.Errorf("inner calls = %v, want exactly [%s]", calls, tt.tool)
			}
		})
	}
}

func TestVerifyingExecutor_PredictionAttached(t *testing.T) {
	tests := []struct {
		name        string
		sqlKey      string
		explainData interface{}
		queryData   interface{}
		wantVerdict string
		wantSnippet string
	}{
		{
			name:        "string plan and slice rows pass",
			sqlKey:      "sql",
			explainData: multiSpoolPlan,
			queryData:   makeRows(4200),
			wantVerdict: VerdictPass,
			wantSnippet: "explain predicted 4231 rows (low confidence); actual 4200 rows",
		},
		{
			name:        "query param key honoured",
			sqlKey:      "query",
			explainData: "The size of Spool 1 is estimated with high confidence to be 10 rows.",
			queryData:   makeRows(10),
			wantVerdict: VerdictPass,
		},
		{
			name:        "map plan text field",
			sqlKey:      "sql",
			explainData: map[string]interface{}{"plan": multiSpoolPlan},
			queryData:   map[string]interface{}{"rows": makeRows(4231), "columns": []interface{}{"c"}},
			wantVerdict: VerdictPass,
		},
		{
			name:        "map content array plan text",
			sqlKey:      "sql",
			explainData: map[string]interface{}{"content": []interface{}{map[string]interface{}{"text": multiSpoolPlan}}},
			queryData:   map[string]interface{}{"row_count": float64(4231)},
			wantVerdict: VerdictPass,
		},
		{
			name:        "row_count numeric int",
			sqlKey:      "sql",
			explainData: multiSpoolPlan,
			queryData:   map[string]interface{}{"rowCount": 100},
			wantVerdict: VerdictWarn,
		},
		{
			name:        "fan out fails",
			sqlKey:      "sql",
			explainData: "The size of Spool 1 is estimated with low confidence to be 4,231 rows.",
			queryData:   makeRows(0),
			wantVerdict: VerdictFail,
			wantSnippet: "predicted a large result, got none",
		},
		{
			name:        "unknown row shape skips",
			sqlKey:      "sql",
			explainData: multiSpoolPlan,
			queryData:   "a text blob",
			wantVerdict: VerdictSkip,
			wantSnippet: "actual row count unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := newMockExecutor()
			inner.results["explain_query"] = explainResult(tt.explainData)
			queryResult := &shuttle.Result{Success: true, Data: tt.queryData}
			inner.results["execute_query"] = queryResult

			got, err := NewVerifyingExecutor(inner).Execute(
				context.Background(), "execute_query", map[string]interface{}{tt.sqlKey: "SELECT 1"})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got != queryResult {
				t.Fatal("result was replaced")
			}
			if !got.Success || got.Error != nil {
				t.Errorf("result verdict fields altered: success=%v err=%v", got.Success, got.Error)
			}
			if !sameData(got.Data, tt.queryData) {
				t.Errorf("Data altered: %#v", got.Data)
			}

			records := RecordsFrom(got)
			if len(records) != 1 {
				t.Fatalf("records = %d, want 1 (%v)", len(records), records)
			}
			if records[0].Rung != RungExplainPrediction {
				t.Errorf("rung = %q", records[0].Rung)
			}
			if records[0].Verdict != tt.wantVerdict {
				t.Errorf("verdict = %q, want %q (evidence: %s)", records[0].Verdict, tt.wantVerdict, records[0].Evidence)
			}
			if tt.wantSnippet != "" && !strings.Contains(records[0].Evidence, tt.wantSnippet) {
				t.Errorf("evidence %q missing %q", records[0].Evidence, tt.wantSnippet)
			}
			if records[0].At == "" {
				t.Error("At not stamped")
			}
			if calls := inner.called(); len(calls) != 2 || calls[0] != "explain_query" || calls[1] != "execute_query" {
				t.Errorf("calls = %v, want [explain_query execute_query]", calls)
			}
		})
	}
}

func TestVerifyingExecutor_ExplainUnavailableSkips(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*mockExecutor)
		wantSnippet string
	}{
		{
			name:        "explain tool absent from registry",
			setup:       func(m *mockExecutor) {},
			wantSnippet: "explain_query not available: tool not found",
		},
		{
			name: "explain tool errors",
			setup: func(m *mockExecutor) {
				m.errs["explain_query"] = errors.New("connection refused")
			},
			wantSnippet: "explain_query not available: connection refused",
		},
		{
			name: "explain tool reports failure",
			setup: func(m *mockExecutor) {
				m.results["explain_query"] = &shuttle.Result{
					Success: false,
					Error:   &shuttle.Error{Code: "SYNTAX", Message: "syntax error at line 1"},
				}
			},
			wantSnippet: "syntax error at line 1",
		},
		{
			name: "explain tool returns nil result",
			setup: func(m *mockExecutor) {
				m.results["explain_query"] = nil
			},
			wantSnippet: "returned no result",
		},
		{
			name: "explain plan has no estimate",
			setup: func(m *mockExecutor) {
				m.results["explain_query"] = explainResult("  1) First, we lock ecom.orders for read")
			},
			wantSnippet: "no row estimate",
		},
		{
			name: "explain plan empty",
			setup: func(m *mockExecutor) {
				m.results["explain_query"] = explainResult("   ")
			},
			wantSnippet: "no plan text",
		},
		{
			name: "explain data unknown shape",
			setup: func(m *mockExecutor) {
				m.results["explain_query"] = explainResult(map[string]interface{}{"unrelated": 5})
			},
			wantSnippet: "no plan text",
		},
		{
			name: "plan text extraction panics",
			setup: func(m *mockExecutor) {
				m.results["explain_query"] = explainResult(panicStringer{})
			},
			wantSnippet: "panicked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := newMockExecutor()
			tt.setup(inner)
			rows := makeRows(3)
			queryResult := &shuttle.Result{Success: true, Data: rows}
			inner.results["execute_query"] = queryResult

			got, err := NewVerifyingExecutor(inner).Execute(
				context.Background(), "execute_query", map[string]interface{}{"sql": "SELECT 1"})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got != queryResult || !got.Success {
				t.Fatal("query result did not survive a failed check")
			}
			if !sameData(got.Data, rows) {
				t.Errorf("Data altered: %#v", got.Data)
			}

			records := RecordsFrom(got)
			if len(records) != 1 {
				t.Fatalf("records = %d, want 1", len(records))
			}
			if records[0].Verdict != VerdictSkip {
				t.Errorf("verdict = %q, want skip (evidence: %s)", records[0].Verdict, records[0].Evidence)
			}
			if !strings.Contains(records[0].Evidence, tt.wantSnippet) {
				t.Errorf("evidence %q missing %q", records[0].Evidence, tt.wantSnippet)
			}
		})
	}
}

func TestVerifyingExecutor_ExecuteToolErrorPassedThrough(t *testing.T) {
	inner := newMockExecutor()
	inner.results["explain_query"] = explainResult(multiSpoolPlan)
	wantErr := errors.New("warehouse offline")
	inner.errs["execute_query"] = wantErr

	got, err := NewVerifyingExecutor(inner).Execute(
		context.Background(), "execute_query", map[string]interface{}{"sql": "SELECT 1"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if got != nil {
		t.Fatalf("result = %#v, want nil", got)
	}
}

func TestVerifyingExecutor_ExistingMetadataPreserved(t *testing.T) {
	inner := newMockExecutor()
	inner.results["explain_query"] = explainResult(multiSpoolPlan)
	prior := GrainCheck("customer_id", 10, 10)
	inner.results["execute_query"] = &shuttle.Result{
		Success: true,
		Data:    makeRows(4231),
		Metadata: map[string]interface{}{
			"backend":     "teradata",
			MetadataKey:   []VerificationRecord{prior},
			"cache_layer": "none",
		},
	}

	got, err := NewVerifyingExecutor(inner).Execute(
		context.Background(), "execute_query", map[string]interface{}{"sql": "SELECT 1"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got.Metadata["backend"] != "teradata" || got.Metadata["cache_layer"] != "none" {
		t.Errorf("unrelated metadata lost: %v", got.Metadata)
	}
	records := RecordsFrom(got)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0].Rung != RungGrain || records[1].Rung != RungExplainPrediction {
		t.Errorf("records out of order: %v", records)
	}
}

func TestVerifyingExecutor_RegisterTools(t *testing.T) {
	inner := newMockExecutor()
	inner.results["td_explain"] = explainResult(multiSpoolPlan)
	inner.results["td_query"] = &shuttle.Result{Success: true, Data: makeRows(4231)}

	verifier := NewVerifyingExecutor(inner)
	verifier.RegisterExecuteTools("td_query", "")
	verifier.RegisterExplainTools("td_explain", "td_explain", "")

	got, err := verifier.Execute(context.Background(), "td_query", map[string]interface{}{"query": "SELECT 1"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	records := RecordsFrom(got)
	if len(records) != 1 || records[0].Verdict != VerdictPass {
		t.Fatalf("records = %v, want one pass record", records)
	}
	// explain_query is tried first and is absent; td_explain then answers.
	if calls := inner.called(); len(calls) != 3 {
		t.Errorf("calls = %v, want explain_query, td_explain, td_query", calls)
	}
}

func TestAttachRecordsCreatesMetadata(t *testing.T) {
	result := &shuttle.Result{Success: true}
	AttachRecords(result, GrainCheck("customer_id", 4231, 4017))

	records := RecordsFrom(result)
	if len(records) != 1 || records[0].Verdict != VerdictFail {
		t.Fatalf("records = %v", records)
	}
	AttachRecords(result)
	AttachRecords(nil, GrainCheck("customer_id", 1, 1))
	if len(RecordsFrom(result)) != 1 {
		t.Error("no-op attach changed records")
	}
	if RecordsFrom(nil) != nil {
		t.Error("RecordsFrom(nil) should be nil")
	}
}

func TestActualRows(t *testing.T) {
	tests := []struct {
		name   string
		result *shuttle.Result
		want   int64
	}{
		{name: "nil result", result: nil, want: -1},
		{name: "slice data", result: &shuttle.Result{Data: makeRows(3)}, want: 3},
		{name: "empty slice data", result: &shuttle.Result{Data: []interface{}{}}, want: 0},
		{name: "typed row slice", result: &shuttle.Result{Data: []map[string]interface{}{{"a": 1}}}, want: 1},
		{name: "map with rows", result: &shuttle.Result{Data: map[string]interface{}{"rows": makeRows(5)}}, want: 5},
		{name: "map with typed rows", result: &shuttle.Result{Data: map[string]interface{}{"rows": []map[string]interface{}{{"a": 1}, {"a": 2}}}}, want: 2},
		{name: "map with row_count int", result: &shuttle.Result{Data: map[string]interface{}{"row_count": 12}}, want: 12},
		{name: "map with rowCount float", result: &shuttle.Result{Data: map[string]interface{}{"rowCount": float64(9)}}, want: 9},
		{name: "map with row_count int64", result: &shuttle.Result{Data: map[string]interface{}{"row_count": int64(4)}}, want: 4},
		{name: "map without counts", result: &shuttle.Result{Data: map[string]interface{}{"note": "x"}}, want: -1},
		{name: "string data", result: &shuttle.Result{Data: "text"}, want: -1},
		{name: "nil data", result: &shuttle.Result{}, want: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ActualRows(tt.result); got != tt.want {
				t.Errorf("ActualRows() = %d, want %d", got, tt.want)
			}
		})
	}
}

func makeRows(n int) []interface{} {
	rows := make([]interface{}, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, map[string]interface{}{"id": i})
	}
	return rows
}

func sameData(got, want interface{}) bool {
	gotRows, gotOK := got.([]interface{})
	wantRows, wantOK := want.([]interface{})
	if gotOK && wantOK {
		return len(gotRows) == len(wantRows)
	}
	return gotOK == wantOK
}
