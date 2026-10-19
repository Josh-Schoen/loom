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

// Package script runs small calculation scripts that an agent writes, in
// Starlark (go.starlark.net), with no access to files, the network,
// processes, modules, the clock, or randomness. Inputs come in as JSON
// values and the result goes back as JSON.
//
// Evaluate runs a script in the calling process with step and time limits
// only. Starlark has no memory limit, and running out of memory cannot be
// recovered from in Go, so servers run scripts in a child process instead:
// ChildRunner starts one per script and ServeChild is what the child runs
// (see docs/plans/sandboxed-script-execution.md for the measurements behind
// this design).
package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	starlarkjson "go.starlark.net/lib/json"
	starlarkmath "go.starlark.net/lib/math"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// Limits bound one script run.
type Limits struct {
	// MaxSteps is the most Starlark execution steps a run may take.
	MaxSteps uint64 `json:"max_steps"`
	// Timeout is the most wall-clock time a run may take.
	Timeout time.Duration `json:"timeout"`
	// MaxHeapBytes is the most heap the child process may use (ServeChild).
	MaxHeapBytes uint64 `json:"max_heap_bytes"`
	// MaxScriptBytes is the longest script accepted.
	MaxScriptBytes int `json:"max_script_bytes"`
	// MaxOutputBytes caps what print() and the result may produce.
	MaxOutputBytes int `json:"max_output_bytes"`
}

// DefaultLimits suit calculations in a conversation: generous for real
// arithmetic, small enough that a runaway script ends in seconds.
var DefaultLimits = Limits{
	MaxSteps:       10_000_000,
	Timeout:        5 * time.Second,
	MaxHeapBytes:   256 << 20,
	MaxScriptBytes: 20_000,
	MaxOutputBytes: 64 << 10,
}

// Result is a finished run: the value of the script's global `result`
// (JSON-compatible) and anything it printed.
type Result struct {
	Value  any    `json:"result"`
	Output string `json:"output,omitempty"`
}

// ErrNoResult is returned when the script does not set `result`.
var ErrNoResult = errors.New("the script did not set `result`")

// fileOptions are the Starlark dialect scripts use: Python-like statements
// at top level, while loops and reassignable globals. Recursion stays off
// (the default); loops cover calculations and the step limit bounds them.
var fileOptions = &syntax.FileOptions{
	Set:             true,
	While:           true,
	TopLevelControl: true,
	GlobalReassign:  true,
}

// Evaluate runs src with inputs bound to the global `inputs` (a dict) and
// returns the value of its global `result`. Only step and time limits
// apply; see the package comment.
func Evaluate(ctx context.Context, src string, inputs map[string]any, limits Limits) (Result, error) {
	if limits.MaxScriptBytes > 0 && len(src) > limits.MaxScriptBytes {
		return Result{}, fmt.Errorf("the script is longer than %d characters", limits.MaxScriptBytes)
	}
	in, err := toStarlark(inputs, 0)
	if err != nil {
		return Result{}, fmt.Errorf("inputs: %w", err)
	}

	var out strings.Builder
	outputFull := false
	thread := &starlark.Thread{
		Name: "script",
		Print: func(_ *starlark.Thread, msg string) {
			if limits.MaxOutputBytes > 0 && out.Len()+len(msg)+1 > limits.MaxOutputBytes {
				outputFull = true
				return
			}
			out.WriteString(msg)
			out.WriteByte('\n')
		},
		// load() is not available: scripts are self-contained.
		Load: func(*starlark.Thread, string) (starlark.StringDict, error) {
			return nil, errors.New("load is not available")
		},
	}
	if limits.MaxSteps > 0 {
		thread.SetMaxExecutionSteps(limits.MaxSteps)
	}
	if limits.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limits.Timeout)
		defer cancel()
	}
	stop := context.AfterFunc(ctx, func() { thread.Cancel("time limit") })
	defer stop()

	predeclared := starlark.StringDict{
		"inputs": in,
		"math":   starlarkmath.Module,
		"json":   starlarkjson.Module,
		"round":  starlark.NewBuiltin("round", roundBuiltin),
		"sum":    starlark.NewBuiltin("sum", sumBuiltin),
	}
	globals, err := starlark.ExecFileOptions(fileOptions, thread, "script", src, predeclared)
	if err != nil {
		return Result{Output: out.String()}, explain(err, ctx, limits)
	}
	v, ok := globals["result"]
	if !ok {
		return Result{Output: out.String()}, ErrNoResult
	}
	value, err := fromStarlark(v, 0)
	if err != nil {
		return Result{Output: out.String()}, fmt.Errorf("result: %w", err)
	}
	res := Result{Value: value, Output: out.String()}
	if outputFull {
		res.Output += "[more output was cut off]\n"
	}
	if limits.MaxOutputBytes > 0 {
		if data, err := json.Marshal(res.Value); err == nil && len(data) > limits.MaxOutputBytes {
			return Result{}, fmt.Errorf("the result is larger than %d bytes", limits.MaxOutputBytes)
		}
	}
	return res, nil
}

// explain turns a Starlark failure into a message an agent can act on.
func explain(err error, ctx context.Context, limits Limits) error {
	var evalErr *starlark.EvalError
	if errors.As(err, &evalErr) {
		msg := evalErr.Msg
		switch {
		case strings.Contains(msg, "too many steps"):
			return fmt.Errorf("the script took too many steps (more than %d); simplify it or loop less", limits.MaxSteps)
		case ctx.Err() != nil || strings.Contains(msg, "time limit"):
			return fmt.Errorf("the script ran longer than %s", limits.Timeout)
		}
		if bt := evalErr.Backtrace(); bt != "" {
			lines := strings.Split(strings.TrimSpace(bt), "\n")
			return fmt.Errorf("%s (%s)", msg, strings.TrimSpace(lines[len(lines)-2]))
		}
		return errors.New(msg)
	}
	var syntaxErr syntax.Error
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("syntax error at line %d: %s", syntaxErr.Pos.Line, syntaxErr.Msg)
	}
	return err
}

// roundBuiltin is Python's round(x, ndigits=0) for numbers: half away from
// zero, an int when ndigits is 0 and x an int.
func roundBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var x starlark.Value
	ndigits := 0
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "x", &x, "ndigits?", &ndigits); err != nil {
		return nil, err
	}
	if ndigits < -15 || ndigits > 15 {
		return nil, fmt.Errorf("round: ndigits must be between -15 and 15")
	}
	switch v := x.(type) {
	case starlark.Int:
		if ndigits >= 0 {
			return v, nil
		}
		f, _ := starlark.AsFloat(v)
		p := math.Pow(10, float64(-ndigits))
		return starlark.NumberToInt(starlark.Float(math.Round(f/p) * p))
	case starlark.Float:
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return v, nil
		}
		p := math.Pow(10, float64(ndigits))
		r := math.Round(f*p) / p
		if ndigits == 0 && len(args) < 2 {
			return starlark.NumberToInt(starlark.Float(r))
		}
		return starlark.Float(r), nil
	default:
		return nil, fmt.Errorf("round: got %s, want a number", x.Type())
	}
}

// sumBuiltin is Python's sum(iterable, start=0) for numbers, which Starlark
// lacks and models use constantly.
func sumBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var iterable starlark.Iterable
	var total starlark.Value = starlark.MakeInt(0)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "iterable", &iterable, "start?", &total); err != nil {
		return nil, err
	}
	iter := iterable.Iterate()
	defer iter.Done()
	var x starlark.Value
	for iter.Next(&x) {
		switch x.(type) {
		case starlark.Int, starlark.Float:
		default:
			return nil, fmt.Errorf("sum: got %s, want numbers", x.Type())
		}
		next, err := starlark.Binary(syntax.PLUS, total, x)
		if err != nil {
			return nil, fmt.Errorf("sum: %w", err)
		}
		total = next
	}
	return total, nil
}

// maxDepth bounds nesting when converting values in either direction.
const maxDepth = 32

// toStarlark converts a JSON value (as decoded by encoding/json with
// UseNumber, or plain Go values) to Starlark.
func toStarlark(v any, depth int) (starlark.Value, error) {
	if depth > maxDepth {
		return nil, errors.New("nested too deeply")
	}
	switch x := v.(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(x), nil
	case string:
		return starlark.String(x), nil
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return starlark.MakeInt64(i), nil
		}
		f, err := x.Float64()
		if err != nil {
			return nil, fmt.Errorf("bad number %q", x)
		}
		return starlark.Float(f), nil
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return starlark.MakeInt64(int64(x)), nil
		}
		return starlark.Float(x), nil
	case int:
		return starlark.MakeInt(x), nil
	case int64:
		return starlark.MakeInt64(x), nil
	case []any:
		items := make([]starlark.Value, 0, len(x))
		for _, e := range x {
			sv, err := toStarlark(e, depth+1)
			if err != nil {
				return nil, err
			}
			items = append(items, sv)
		}
		return starlark.NewList(items), nil
	case map[string]any:
		d := starlark.NewDict(len(x))
		for k, e := range x {
			sv, err := toStarlark(e, depth+1)
			if err != nil {
				return nil, err
			}
			if err := d.SetKey(starlark.String(k), sv); err != nil {
				return nil, err
			}
		}
		return d, nil
	default:
		return nil, fmt.Errorf("unsupported input type %T", v)
	}
}

// fromStarlark converts a script's result to a JSON-compatible Go value.
func fromStarlark(v starlark.Value, depth int) (any, error) {
	if depth > maxDepth {
		return nil, errors.New("nested too deeply")
	}
	switch x := v.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(x), nil
	case starlark.String:
		return string(x), nil
	case starlark.Int:
		if i, ok := x.Int64(); ok {
			return i, nil
		}
		return x.String(), nil
	case starlark.Float:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%v is not a number JSON can carry", f)
		}
		return f, nil
	case *starlark.List:
		out := make([]any, 0, x.Len())
		for i := 0; i < x.Len(); i++ {
			e, err := fromStarlark(x.Index(i), depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, nil
	case starlark.Tuple:
		out := make([]any, 0, len(x))
		for _, item := range x {
			e, err := fromStarlark(item, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, nil
	case *starlark.Dict:
		out := make(map[string]any, x.Len())
		for _, item := range x.Items() {
			k, ok := item[0].(starlark.String)
			if !ok {
				return nil, fmt.Errorf("dict keys must be strings, got %s", item[0].Type())
			}
			e, err := fromStarlark(item[1], depth+1)
			if err != nil {
				return nil, err
			}
			out[string(k)] = e
		}
		return out, nil
	default:
		return nil, fmt.Errorf("a %s cannot be returned; use numbers, strings, lists or dicts", v.Type())
	}
}
