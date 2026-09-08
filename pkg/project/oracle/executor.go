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
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/teradata-labs/loom/pkg/shuttle"
)

// defaultExplainTimeout bounds the prediction call; EXPLAIN does not execute
// the query, so this is a latency guard, not a cost guard.
const defaultExplainTimeout = 15 * time.Second

// sqlParamKeys are the parameter names SQL tools use for the statement. The
// MCP backend sends "query" (pkg/backends/mcp/backend.go); agent guardrails
// read "sql". Both are accepted.
var sqlParamKeys = []string{"sql", "query"}

// planTextKeys are result fields that may carry EXPLAIN plan text.
var planTextKeys = []string{
	"plan", "plan_text", "planText", "explain", "explain_plan",
	"text", "output", "result", "content", "data", "rows",
}

// ToolExecutor is the tool-execution seam. Both *shuttle.Executor and
// *shuttle.InstrumentedExecutor satisfy it.
type ToolExecutor interface {
	Execute(ctx context.Context, toolName string, params map[string]interface{}) (*shuttle.Result, error)
}

// VerifyingExecutor attaches verification records to SQL tool results. Any
// check failure becomes a skip record: the tool result is never altered,
// delayed past the explain timeout, or failed by verification.
type VerifyingExecutor struct {
	inner ToolExecutor

	mu             sync.RWMutex
	executeTools   map[string]struct{}
	explainTools   []string
	explainTimeout time.Duration
}

// NewVerifyingExecutor wraps inner with the verification ladder's cheap rungs.
func NewVerifyingExecutor(inner ToolExecutor) *VerifyingExecutor {
	return &VerifyingExecutor{
		inner:          inner,
		executeTools:   map[string]struct{}{"execute_query": {}},
		explainTools:   []string{"explain_query"},
		explainTimeout: defaultExplainTimeout,
	}
}

// RegisterExecuteTools adds tool names treated as SQL execution.
func (e *VerifyingExecutor) RegisterExecuteTools(names ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, name := range names {
		if name != "" {
			e.executeTools[name] = struct{}{}
		}
	}
}

// RegisterExplainTools adds tool names used to obtain a plan, in preference
// order after the existing ones.
func (e *VerifyingExecutor) RegisterExplainTools(names ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, name := range names {
		if name == "" || containsString(e.explainTools, name) {
			continue
		}
		e.explainTools = append(e.explainTools, name)
	}
}

// SetExplainTimeout bounds the prediction call.
func (e *VerifyingExecutor) SetExplainTimeout(d time.Duration) {
	if d <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.explainTimeout = d
}

// Execute runs the tool, attaching a verification record when the tool is a
// SQL execution tool carrying a statement. Everything else passes through.
func (e *VerifyingExecutor) Execute(ctx context.Context, toolName string, params map[string]interface{}) (*shuttle.Result, error) {
	prefix, ok := e.matchExecuteTool(toolName)
	if !ok || !hasSQLParam(params) {
		return e.inner.Execute(ctx, toolName, params)
	}

	start := time.Now()
	prediction, explainTool, explainErr := e.predict(ctx, prefix, params)
	checkCost := time.Since(start).Milliseconds()

	result, err := e.inner.Execute(ctx, toolName, params)
	if err != nil || result == nil {
		return result, err
	}

	record := predictionRecord(prediction, explainTool, explainErr, result)
	record.CostMs += checkCost
	AttachRecords(result, record)
	return result, nil
}

// explainParams narrows the execute call's params to just the statement:
// execute-only options (max_rows, timeouts) fail the explain tool's schema
// validation, which was the first live skip this check ever produced.
func explainParams(params map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, 1)
	for _, key := range sqlParamKeys {
		if sql, ok := params[key].(string); ok && strings.TrimSpace(sql) != "" {
			out[key] = sql
		}
	}
	return out
}

// predictionRecord never panics: a panic in check logic becomes a skip.
func predictionRecord(p Prediction, explainTool string, explainErr error, result *shuttle.Result) (record VerificationRecord) {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			record = newRecord(RungExplainPrediction, VerdictSkip,
				fmt.Sprintf("explain prediction skipped: check panicked (%v)", r), start)
		}
	}()

	if explainErr != nil {
		if explainTool == "" {
			explainTool = "explain"
		}
		return newRecord(RungExplainPrediction, VerdictSkip,
			fmt.Sprintf("%s not available: %v", explainTool, explainErr), start)
	}
	rows, truncated := actualRowsInfo(result)
	// A truncated result's row count is a floor, not the actual — comparing
	// it to the estimate manufactures false warnings, the one failure the
	// counter-metrics name as worse than no badge.
	if truncated && rows >= 0 {
		return newRecord(RungExplainPrediction, VerdictSkip,
			fmt.Sprintf("explain predicted %d rows (%s); result truncated at %d rows — not comparable",
				p.EstimatedRows, confidenceText(p), rows), start)
	}
	return PredictionCheck(p, rows)
}

// predict calls the explain tool with the same params and parses the plan.
// Returns the tool it tried and why it produced no prediction. When the
// execute tool was prefixed (an MCP server's namespace), the SAME prefix is
// tried first — the explain must run where the query will.
func (e *VerifyingExecutor) predict(ctx context.Context, prefix string, params map[string]interface{}) (p Prediction, tool string, err error) {
	defer func() {
		if r := recover(); r != nil {
			p = Prediction{}
			err = fmt.Errorf("explain check panicked: %v", r)
		}
	}()

	e.mu.RLock()
	bases := append([]string(nil), e.explainTools...)
	timeout := e.explainTimeout
	e.mu.RUnlock()

	if len(bases) == 0 {
		return Prediction{}, "", fmt.Errorf("no explain tool registered")
	}

	var tools []string
	if prefix != "" {
		for _, base := range bases {
			tools = append(tools, prefix+base)
		}
	}
	tools = append(tools, bases...)

	for _, name := range tools {
		tool = name
		p, err = e.explainOnce(ctx, name, params, timeout)
		if err == nil {
			return p, tool, nil
		}
	}
	return Prediction{}, tool, err
}

func (e *VerifyingExecutor) explainOnce(ctx context.Context, tool string, params map[string]interface{}, timeout time.Duration) (Prediction, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := e.inner.Execute(cctx, tool, explainParams(params))
	if err != nil {
		return Prediction{}, err
	}
	if result == nil {
		return Prediction{}, fmt.Errorf("returned no result")
	}
	if !result.Success {
		msg := "tool reported failure"
		if result.Error != nil && result.Error.Message != "" {
			msg = result.Error.Message
		}
		return Prediction{}, fmt.Errorf("%s", msg)
	}

	planText := PlanText(result.Data)
	if strings.TrimSpace(planText) == "" {
		return Prediction{}, fmt.Errorf("returned no plan text")
	}
	p := ParseTeradataExplain(planText)
	if !p.Found {
		return Prediction{}, fmt.Errorf("plan text carried no row estimate")
	}
	return p, nil
}

// PlanText extracts plan text from tool result data. Tolerant by design:
// unknown shapes yield "".
func PlanText(data interface{}) string {
	switch v := data.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	case fmt.Stringer:
		return v.String()
	case []interface{}:
		return joinTextValues(v)
	case []string:
		return strings.Join(v, "\n")
	case map[string]interface{}:
		for _, key := range planTextKeys {
			value, ok := v[key]
			if !ok {
				continue
			}
			if text := PlanText(value); strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	return ""
}

// joinTextValues collects string-ish elements, including one level of
// single-field row maps as MCP text content arrives.
func joinTextValues(items []interface{}) string {
	var parts []string
	for _, item := range items {
		switch v := item.(type) {
		case string:
			parts = append(parts, v)
		case map[string]interface{}:
			for _, key := range planTextKeys {
				if text, ok := v[key].(string); ok && text != "" {
					parts = append(parts, text)
					break
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

// ActualRows counts rows in a tool result, or -1 when unknown.
func ActualRows(result *shuttle.Result) int64 {
	rows, _ := actualRowsInfo(result)
	return rows
}

// actualRowsInfo counts rows and reports whether the result declares itself
// truncated. SQL MCP tools return a JSON STRING payload
// ({"columns":…, "row_count":N, "rows":[[…]], "truncated":bool}) — seen
// live from teradata-pecto — so string data gets one decode attempt.
func actualRowsInfo(result *shuttle.Result) (int64, bool) {
	if result == nil {
		return -1, false
	}
	return rowsFromData(result.Data)
}

func rowsFromData(data interface{}) (int64, bool) {
	switch v := data.(type) {
	case []interface{}:
		return int64(len(v)), false
	case []map[string]interface{}:
		return int64(len(v)), false
	case string:
		return rowsFromJSONText(v)
	case []byte:
		return rowsFromJSONText(string(v))
	case map[string]interface{}:
		truncated, _ := v["truncated"].(bool)
		for _, key := range []string{"row_count", "rowCount"} {
			if n, ok := numericValue(v[key]); ok {
				return n, truncated
			}
		}
		switch rows := v["rows"].(type) {
		case []interface{}:
			return int64(len(rows)), truncated
		case []map[string]interface{}:
			return int64(len(rows)), truncated
		}
	}
	return -1, false
}

func rowsFromJSONText(text string) (int64, bool) {
	text = strings.TrimSpace(text)
	if len(text) == 0 || (text[0] != '{' && text[0] != '[') {
		return -1, false
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return -1, false
	}
	if _, isText := decoded.(string); isText {
		return -1, false // a JSON string is not a result payload
	}
	return rowsFromData(decoded)
}

func numericValue(value interface{}) (int64, bool) {
	switch n := value.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return int64(n), true
	case float32:
		return int64(n), true
	case float64:
		return int64(n), true
	case json.Number:
		if parsed, err := n.Int64(); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

// AttachRecords appends records under MetadataKey, creating the map if nil.
// Nothing else on the result is touched.
func AttachRecords(result *shuttle.Result, records ...VerificationRecord) {
	if result == nil || len(records) == 0 {
		return
	}
	if result.Metadata == nil {
		result.Metadata = make(map[string]interface{}, 1)
	}
	existing, _ := result.Metadata[MetadataKey].([]VerificationRecord)
	result.Metadata[MetadataKey] = append(existing, records...)
}

// RecordsFrom returns the records attached to a result, if any.
func RecordsFrom(result *shuttle.Result) []VerificationRecord {
	if result == nil || result.Metadata == nil {
		return nil
	}
	records, _ := result.Metadata[MetadataKey].([]VerificationRecord)
	return records
}

// executeToolSeparators are how MCP registration namespaces a server's tools
// (seen live: "teradata-pecto_execute_query", displayed with ":").
var executeToolSeparators = []string{"_", ":"}

// matchExecuteTool reports whether toolName is a registered SQL-execution
// tool, either exactly or as a namespaced "<server><sep><base>" form. The
// returned prefix includes the separator so the explain tool can be
// addressed in the same namespace; "" means the bare name matched.
func (e *VerifyingExecutor) matchExecuteTool(toolName string) (string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if _, ok := e.executeTools[toolName]; ok {
		return "", true
	}
	for base := range e.executeTools {
		for _, sep := range executeToolSeparators {
			if strings.HasSuffix(toolName, sep+base) && len(toolName) > len(sep+base) {
				return toolName[:len(toolName)-len(base)], true
			}
		}
	}
	return "", false
}

func hasSQLParam(params map[string]interface{}) bool {
	for _, key := range sqlParamKeys {
		if sql, ok := params[key].(string); ok && strings.TrimSpace(sql) != "" {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
