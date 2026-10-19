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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime/debug"
	"runtime/metrics"
	"sync"
	"time"
)

// ChildCommand is the argument that makes a binary act as a script child
// (the binary's main calls ServeChild when it sees it).
const ChildCommand = "sandbox-run"

// request is what the parent writes to the child's stdin.
type request struct {
	Script string         `json:"script"`
	Inputs map[string]any `json:"inputs,omitempty"`
	Limits Limits         `json:"limits"`
}

// response is what the child writes to stdout.
type response struct {
	Result *Result `json:"run,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// heapPollInterval is how often the child checks its heap. A script grows
// its heap by at most a few MB per millisecond (spike measurements), so the
// cap is overshot by tens of MB at most.
const heapPollInterval = 5 * time.Millisecond

// ServeChild is a script child: it reads one request from in, runs it under
// its limits (including the heap cap, enforced by stopping the process),
// writes one response to out, and returns the process exit code. exit is
// called when the heap cap is passed; tests pass a func that does not exit.
func ServeChild(in io.Reader, out io.Writer, exit func(code int)) int {
	var (
		mu      sync.Mutex
		written bool
	)
	reply := func(r response) {
		mu.Lock()
		defer mu.Unlock()
		if written {
			return
		}
		written = true
		_ = json.NewEncoder(out).Encode(r)
	}

	var req request
	dec := json.NewDecoder(io.LimitReader(in, 4<<20))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		reply(response{Error: "bad request: " + err.Error()})
		return 2
	}
	limits := req.Limits
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if limits.MaxHeapBytes > 0 {
		// The GC works harder near the cap; the watchdog enforces it.
		debug.SetMemoryLimit(int64(limits.MaxHeapBytes))
		go watchHeap(ctx, limits.MaxHeapBytes, func() {
			reply(response{Error: fmt.Sprintf("the script used more than %d MB of memory", limits.MaxHeapBytes>>20)})
			exit(3)
		})
	}
	res, err := Evaluate(ctx, req.Script, req.Inputs, limits)
	if err != nil {
		reply(response{Error: err.Error(), Result: &Result{Output: res.Output}})
		return 0
	}
	reply(response{Result: &res})
	return 0
}

// watchHeap calls over once the heap in use passes max.
func watchHeap(ctx context.Context, max uint64, over func()) {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	ticker := time.NewTicker(heapPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			metrics.Read(sample)
			if sample[0].Value.Kind() == metrics.KindUint64 && sample[0].Value.Uint64() > max {
				over()
				return
			}
		}
	}
}

// Runner runs one script.
type Runner interface {
	Run(ctx context.Context, src string, inputs map[string]any) (Result, error)
}

// ChildRunner runs each script in a new child process: Command (for
// example the server binary and ChildCommand) with an empty environment, so
// the child sees no keys or settings. The child is killed at
// Limits.Timeout plus a second; its stdout is capped.
type ChildRunner struct {
	Command []string
	Limits  Limits
}

// ErrScriptCrashed is returned when the child ends without a response.
var ErrScriptCrashed = errors.New("the script stopped unexpectedly (it may have used too much memory)")

// Run implements Runner.
func (r ChildRunner) Run(ctx context.Context, src string, inputs map[string]any) (Result, error) {
	if len(r.Command) == 0 {
		return Result{}, errors.New("script runner has no command")
	}
	limits := r.Limits
	if limits.MaxScriptBytes > 0 && len(src) > limits.MaxScriptBytes {
		return Result{}, fmt.Errorf("the script is longer than %d characters", limits.MaxScriptBytes)
	}
	body, err := json.Marshal(request{Script: src, Inputs: inputs, Limits: limits})
	if err != nil {
		return Result{}, fmt.Errorf("inputs: %w", err)
	}
	if limits.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limits.Timeout+time.Second)
		defer cancel()
	}
	// #nosec G204 -- Command is set by the server (its own binary), not by the script.
	cmd := exec.CommandContext(ctx, r.Command[0], r.Command[1:]...)
	cmd.Env = []string{"GOMAXPROCS=2"}
	cmd.Stdin = bytes.NewReader(body)
	stdout := &cappedBuffer{max: limits.MaxOutputBytes + 64<<10}
	cmd.Stdout = stdout
	cmd.Stderr = io.Discard
	runErr := cmd.Run()

	var resp response
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("the script ran longer than %s", limits.Timeout)
		}
		if runErr != nil {
			return Result{}, ErrScriptCrashed
		}
		return Result{}, fmt.Errorf("unreadable answer from the script runner: %w", err)
	}
	if resp.Error != "" {
		var out Result
		if resp.Result != nil {
			out.Output = resp.Result.Output
		}
		return out, errors.New(resp.Error)
	}
	if resp.Result == nil {
		return Result{}, ErrScriptCrashed
	}
	return *resp.Result, nil
}

// cappedBuffer keeps the first max bytes written and drops the rest.
type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}
