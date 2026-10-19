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

package script

import (
	"context"
	"time"

	"github.com/teradata-labs/loom/pkg/shuttle"
)

// ToolName is the agent tool that runs a calculation script.
const ToolName = "run_script"

// Tool is the run_script agent tool: it runs a calculation script through a
// Runner and returns its result.
type Tool struct {
	runner Runner
}

// NewTool returns the run_script tool backed by runner.
func NewTool(runner Runner) *Tool { return &Tool{runner: runner} }

// Name implements shuttle.Tool.
func (t *Tool) Name() string { return ToolName }

// Description implements shuttle.Tool.
func (t *Tool) Description() string {
	return `Run a short calculation script and get its exact result. Use it when an answer needs arithmetic you should not do in your head: splitting a bill, totals and averages, unit conversions, loan or savings figures, dates counted in days.

The script is Starlark, a small Python dialect: def, for, while, if, lists, dicts, strings, int and float math. Set the global "result" to the answer (a number, string, list or dict). Values you pass in "inputs" are in the dict "inputs" (inputs["total"]). Also available: round(x, ndigits), sum(numbers), math (math.sqrt, math.floor, math.ceil, math.pow, math.log, math.pi, ...), json (json.encode, json.decode), print() for working notes.

There are no imports, files, network, clock or random numbers, and no recursion. Unlike Python there is no ** operator (use math.pow(x, y)) and "%.2f" formatting is not supported (use round). A script stops after a few seconds or millions of steps.

Example: inputs {"total": 84.5, "tip": 0.2, "people": 3}, script:
each = round(inputs["total"] * (1 + inputs["tip"]) / inputs["people"], 2)
result = {"each": each}`
}

// InputSchema implements shuttle.Tool.
func (t *Tool) InputSchema() *shuttle.JSONSchema {
	return shuttle.NewObjectSchema(
		"A calculation script and its inputs",
		map[string]*shuttle.JSONSchema{
			"script": shuttle.NewStringSchema("Starlark source that sets the global `result`"),
			"inputs": shuttle.NewObjectSchema("Values the script reads from the dict `inputs` (optional)", nil, nil),
		},
		[]string{"script"},
	)
}

// Execute implements shuttle.Tool.
func (t *Tool) Execute(ctx context.Context, params map[string]interface{}) (*shuttle.Result, error) {
	start := time.Now()
	src, _ := params["script"].(string)
	if src == "" {
		return &shuttle.Result{
			Success:         false,
			Error:           &shuttle.Error{Code: "INVALID_PARAMS", Message: "script is required"},
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}, nil
	}
	inputs, _ := params["inputs"].(map[string]interface{})
	res, err := t.runner.Run(ctx, src, inputs)
	if err != nil {
		data := map[string]interface{}{}
		if res.Output != "" {
			data["output"] = res.Output
		}
		return &shuttle.Result{
			Success:         false,
			Data:            data,
			Error:           &shuttle.Error{Code: "SCRIPT_FAILED", Message: err.Error(), Suggestion: "Fix the script and run it again"},
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}, nil
	}
	data := map[string]interface{}{"result": res.Value}
	if res.Output != "" {
		data["output"] = res.Output
	}
	return &shuttle.Result{
		Success:         true,
		Data:            data,
		ExecutionTimeMs: time.Since(start).Milliseconds(),
	}, nil
}

// Backend implements shuttle.Tool.
func (t *Tool) Backend() string { return "" }

var _ shuttle.Tool = (*Tool)(nil)
