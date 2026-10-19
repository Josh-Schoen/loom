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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain lets the test binary act as a script child, the way looms does.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == ChildCommand {
		os.Exit(ServeChild(os.Stdin, os.Stdout, os.Exit))
	}
	os.Exit(m.Run())
}

func eval(t *testing.T, src string, inputs map[string]any) (Result, error) {
	t.Helper()
	return Evaluate(context.Background(), src, inputs, DefaultLimits)
}

func TestCalculations(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		inputs map[string]any
		want   any
	}{
		{"bill split", `result = round(inputs["total"] * (1 + inputs["tip"]) / inputs["people"], 2)`,
			map[string]any{"total": 84.5, "tip": 0.2, "people": 3}, 33.8},
		{"integer inputs stay ints", `result = inputs["a"] + inputs["b"]`, map[string]any{"a": 2.0, "b": 3.0}, int64(5)},
		{"round to int", `result = round(2.5)`, nil, int64(3)},
		{"round negative digits", `result = round(1234, -2)`, nil, int64(1200)},
		{"math module", `result = math.sqrt(16)`, nil, 4.0},
		{"sum", `result = sum([1, 2, 3.5])`, nil, 6.5},
		{"sum with start", `result = sum(inputs["xs"], 10)`, map[string]any{"xs": []any{1.0, 2.0}}, int64(13)},
		{"loops and dicts", `
totals = {}
for name, amount in [("rent", 1200), ("food", 450), ("food", 50)]:
    totals[name] = totals.get(name, 0) + amount
result = totals`, nil, map[string]any{"rent": int64(1200), "food": int64(500)}},
		{"while loop", `
n = 0
while n < 10:
    n += 3
result = n`, nil, int64(12)},
		{"list result", `result = [x * x for x in range(4)]`, nil, []any{int64(0), int64(1), int64(4), int64(9)}},
		{"json module", `result = json.decode('{"a": [1, 2]}')["a"][1]`, nil, int64(2)},
		{"big ints become strings", `result = 1 << 80`, nil, "1208925819614629174706176"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := eval(t, tt.src, tt.inputs)
			require.NoError(t, err)
			assert.Equal(t, tt.want, res.Value)
		})
	}
}

func TestPrintIsKept(t *testing.T) {
	res, err := eval(t, "print('subtotal', 10)\nresult = 1", nil)
	require.NoError(t, err)
	assert.Equal(t, "subtotal 10\n", res.Output)
}

// Nothing outside the script is reachable.
func TestNoAccessOutsideTheScript(t *testing.T) {
	for _, src := range []string{
		`result = open("/etc/passwd").read()`,
		`exec("id")`,
		`load("os.star", "x")`,
		`result = eval("1")`,
		`result = env["HOME"]`,
		`result = time.now()`,
		`result = __import__("os")`,
		`result = globals()`,
	} {
		_, err := eval(t, src, nil)
		assert.Error(t, err, src)
	}
}

func TestLimits(t *testing.T) {
	_, err := eval(t, "while True:\n    pass", nil)
	assert.ErrorContains(t, err, "too many steps")

	_, err = Evaluate(context.Background(), "while True:\n    pass", nil, Limits{Timeout: 50 * time.Millisecond})
	assert.ErrorContains(t, err, "ran longer than 50ms")

	_, err = eval(t, "def f(n):\n    return f(n + 1)\nresult = f(0)", nil)
	assert.ErrorContains(t, err, "recursively")

	_, err = eval(t, `result = "x" * (1024 * 1024 * 1024)`, nil)
	assert.ErrorContains(t, err, "excessive repeat")

	_, err = eval(t, strings.Repeat("x = 1\n", 5000), nil)
	assert.ErrorContains(t, err, "longer than 20000 characters")

	res, err := Evaluate(context.Background(), "for i in range(100000):\n    print(i)\nresult = 1", nil, Limits{MaxOutputBytes: 1000})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(res.Output), 1100)
	assert.Contains(t, res.Output, "cut off")

	_, err = Evaluate(context.Background(), `result = "x" * 5000`, nil, Limits{MaxOutputBytes: 1000})
	assert.ErrorContains(t, err, "result is larger than 1000 bytes")
}

func TestErrorsExplainTheProblem(t *testing.T) {
	_, err := eval(t, "x = 1", nil)
	assert.ErrorIs(t, err, ErrNoResult)

	_, err = eval(t, "result = (", nil)
	assert.ErrorContains(t, err, "syntax error at line 1")

	_, err = eval(t, "x = 1\nresult = x / 0", nil)
	assert.ErrorContains(t, err, "division by zero")

	_, err = eval(t, `result = "%.2f" % 1.5`, nil)
	assert.Error(t, err)

	_, err = eval(t, `result = sum(["a"])`, nil)
	assert.ErrorContains(t, err, "want numbers")

	_, err = eval(t, "result = 2 ** 8", nil)
	assert.ErrorContains(t, err, "syntax error", "no ** operator: the tool description says so")

	_, err = eval(t, "result = lambda x: x", nil)
	assert.ErrorContains(t, err, "cannot be returned")

	_, err = eval(t, "result = float('nan')", nil)
	assert.ErrorContains(t, err, "not a number JSON can carry")
}

func childRunner(limits Limits) ChildRunner {
	return ChildRunner{Command: []string{os.Args[0], ChildCommand}, Limits: limits}
}

func TestChildRunner(t *testing.T) {
	r := childRunner(DefaultLimits)
	res, err := r.Run(context.Background(), `result = round(inputs["total"] / inputs["people"], 2)`,
		map[string]any{"total": 100.0, "people": 3.0})
	require.NoError(t, err)
	assert.Equal(t, 33.33, res.Value)

	_, err = r.Run(context.Background(), "result = (", nil)
	assert.ErrorContains(t, err, "syntax error")
}

// A script that eats memory ends its child, not the caller.
func TestChildStopsAtTheHeapCap(t *testing.T) {
	limits := DefaultLimits
	limits.MaxHeapBytes = 64 << 20
	limits.MaxSteps = 0
	limits.Timeout = 20 * time.Second
	start := time.Now()
	_, err := childRunner(limits).Run(context.Background(), `
s = "x"
parts = []
while True:
    parts.append(s * 1000000)`, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than 64 MB")
	assert.Less(t, time.Since(start), 10*time.Second)
}

func TestChildIsKilledAtTheTimeout(t *testing.T) {
	limits := DefaultLimits
	limits.MaxSteps = 0
	limits.Timeout = 200 * time.Millisecond
	_, err := childRunner(limits).Run(context.Background(), "while True:\n    pass", nil)
	assert.ErrorContains(t, err, "ran longer than 200ms")
}

func TestChildSeesNoEnvironment(t *testing.T) {
	t.Setenv("SECRET_KEY", "sk-test")
	// The script cannot read the environment at all; the child also starts
	// with none, so nothing could leak even through a future builtin.
	_, err := childRunner(DefaultLimits).Run(context.Background(), `result = env`, nil)
	assert.Error(t, err)
}

func TestTool(t *testing.T) {
	tool := NewTool(childRunner(DefaultLimits))
	assert.Equal(t, "run_script", tool.Name())

	res, err := tool.Execute(context.Background(), map[string]interface{}{
		"script": `result = {"each": round(inputs["total"] * 1.2 / 3, 2)}`,
		"inputs": map[string]interface{}{"total": 84.5},
	})
	require.NoError(t, err)
	require.True(t, res.Success, "%+v", res.Error)
	assert.Equal(t, map[string]interface{}{"each": 33.8}, res.Data.(map[string]interface{})["result"])

	res, err = tool.Execute(context.Background(), map[string]interface{}{"script": "print('hi')\nresult = 1 / 0"})
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Equal(t, "SCRIPT_FAILED", res.Error.Code)
	assert.Contains(t, res.Error.Message, "division by zero")
	assert.Equal(t, "hi\n", res.Data.(map[string]interface{})["output"])

	res, err = tool.Execute(context.Background(), map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, "INVALID_PARAMS", res.Error.Code)
}
