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
package clienttools

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/observability"
	"github.com/teradata-labs/loom/pkg/shuttle"
	"github.com/teradata-labs/loom/pkg/types"
)

// eventRecorder captures progress events and signals each client tool call.
type eventRecorder struct {
	mu     sync.Mutex
	events []types.ProgressEvent
	calls  chan *types.ClientToolCallInfo
}

func newEventRecorder() *eventRecorder {
	return &eventRecorder{calls: make(chan *types.ClientToolCallInfo, 8)}
}

func (r *eventRecorder) emit(e types.ProgressEvent) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
	if e.ClientToolCall != nil {
		r.calls <- e.ClientToolCall
	}
}

func (r *eventRecorder) heartbeats() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.Droppable {
			n++
		}
	}
	return n
}

func (r *eventRecorder) nextCall(t *testing.T) *types.ClientToolCallInfo {
	t.Helper()
	select {
	case c := <-r.calls:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no client tool call was emitted")
		return nil
	}
}

func newTestTool(t *testing.T, broker *Broker, rec *eventRecorder, timeout time.Duration) *Tool {
	t.Helper()
	specs, err := Parse([]*loomv1.ToolDefinition{{
		Name:            "device_add_reminder",
		Description:     "Adds a reminder.",
		InputSchemaJson: `{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`,
	}}, func(string) bool { return false })
	require.NoError(t, err)
	tools := Bind(specs, Call{
		SessionID:   "sess-1",
		OwnerUserID: "alice",
		Timeout:     timeout,
		Broker:      broker,
		Emit:        rec.emit,
		Tracer:      observability.NewNoOpTracer(),
	})
	require.Len(t, tools, 1)
	return tools[0].(*Tool)
}

func ctxWithCallID(id string) context.Context {
	return context.WithValue(context.Background(), shuttle.ToolCallIDContextKey(), id)
}

// runAsync executes the tool on its own goroutine, as the turn would.
func runAsync(ctx context.Context, tool *Tool, params map[string]interface{}) <-chan *shuttle.Result {
	out := make(chan *shuttle.Result, 1)
	go func() {
		res, err := tool.Execute(ctx, params)
		if err != nil {
			res = &shuttle.Result{Error: &shuttle.Error{Code: "unexpected_error", Message: err.Error()}}
		}
		out <- res
	}()
	return out
}

func waitResult(t *testing.T, ch <-chan *shuttle.Result) *shuttle.Result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not return")
		return nil
	}
}

func TestTool_HandsCallToClientAndReturnsOutput(t *testing.T) {
	broker := NewBroker()
	rec := newEventRecorder()
	tool := newTestTool(t, broker, rec, time.Minute)

	done := runAsync(ctxWithCallID("call-7"), tool, map[string]interface{}{"title": "Milk"})
	call := rec.nextCall(t)
	assert.Equal(t, "call-7", call.ToolCallID, "the call id must match the loop's tool_call_id")
	assert.Equal(t, "device_add_reminder", call.ToolName)
	assert.Equal(t, "sess-1", call.SessionID)
	assert.Equal(t, map[string]interface{}{"title": "Milk"}, call.Arguments)
	assert.Equal(t, time.Minute, call.Timeout)

	accepted, reason := broker.Submit("sess-1", "call-7", "alice", Result{
		Success: true,
		Output:  map[string]interface{}{"reminder_id": "r1"},
	})
	require.True(t, accepted, reason)

	res := waitResult(t, done)
	assert.True(t, res.Success)
	assert.Equal(t, map[string]interface{}{"reminder_id": "r1"}, res.Data)
	assert.Zero(t, broker.PendingCount())
}

func TestTool_ClientFailureReachesAgent(t *testing.T) {
	broker := NewBroker()
	rec := newEventRecorder()
	tool := newTestTool(t, broker, rec, time.Minute)

	done := runAsync(ctxWithCallID("call-1"), tool, map[string]interface{}{"title": "Milk"})
	rec.nextCall(t)
	accepted, _ := broker.Submit("sess-1", "call-1", "alice", Result{
		ErrorCode:    "DECLINED_BY_USER",
		ErrorMessage: "The user declined this action.",
	})
	require.True(t, accepted)

	res := waitResult(t, done)
	require.False(t, res.Success)
	assert.Equal(t, "DECLINED_BY_USER", res.Error.Code)
	assert.Equal(t, "The user declined this action.", res.Error.Message)
}

func TestTool_FailureWithoutCodeGetsDefaultCode(t *testing.T) {
	broker := NewBroker()
	rec := newEventRecorder()
	tool := newTestTool(t, broker, rec, time.Minute)

	done := runAsync(ctxWithCallID("call-1"), tool, nil)
	rec.nextCall(t)
	accepted, _ := broker.Submit("sess-1", "call-1", "alice", Result{ErrorMessage: "boom"})
	require.True(t, accepted)
	assert.Equal(t, "CLIENT_TOOL_FAILED", waitResult(t, done).Error.Code)
}

func TestTool_TimeoutFailsCallAndRejectsLateResult(t *testing.T) {
	broker := NewBroker()
	rec := newEventRecorder()
	tool := newTestTool(t, broker, rec, 50*time.Millisecond)

	res := waitResult(t, runAsync(ctxWithCallID("call-1"), tool, nil))
	require.False(t, res.Success)
	assert.Equal(t, CodeTimeout, res.Error.Code)

	accepted, reason := broker.Submit("sess-1", "call-1", "alice", Result{Success: true})
	assert.False(t, accepted, "a result after the timeout must not be accepted")
	assert.Equal(t, notPendingReason, reason)
	assert.Zero(t, broker.PendingCount())
}

func TestTool_StreamEndFailsCall(t *testing.T) {
	broker := NewBroker()
	rec := newEventRecorder()
	tool := newTestTool(t, broker, rec, time.Minute)

	ctx, cancel := context.WithCancel(ctxWithCallID("call-1"))
	done := runAsync(ctx, tool, nil)
	rec.nextCall(t)
	cancel()

	res := waitResult(t, done)
	assert.Equal(t, CodeTimeout, res.Error.Code)
	assert.Zero(t, broker.PendingCount())
}

func TestTool_HeartbeatsWhileWaiting(t *testing.T) {
	broker := NewBroker()
	rec := newEventRecorder()
	tool := newTestTool(t, broker, rec, time.Minute)
	tool.heartbeat = 10 * time.Millisecond

	done := runAsync(ctxWithCallID("call-1"), tool, nil)
	rec.nextCall(t)
	require.Eventually(t, func() bool { return rec.heartbeats() >= 2 }, 5*time.Second, 5*time.Millisecond)

	accepted, _ := broker.Submit("sess-1", "call-1", "alice", Result{Success: true})
	require.True(t, accepted)
	assert.True(t, waitResult(t, done).Success)
}

func TestTool_DuplicatePendingCallID(t *testing.T) {
	broker := NewBroker()
	rec := newEventRecorder()
	tool := newTestTool(t, broker, rec, time.Minute)

	first := runAsync(ctxWithCallID("call-1"), tool, nil)
	rec.nextCall(t)

	second := waitResult(t, runAsync(ctxWithCallID("call-1"), tool, nil))
	require.False(t, second.Success)
	assert.Equal(t, CodeDuplicateCall, second.Error.Code)

	accepted, _ := broker.Submit("sess-1", "call-1", "alice", Result{Success: true})
	require.True(t, accepted)
	assert.True(t, waitResult(t, first).Success)
}

func TestTool_GeneratesCallIDWhenLoopSetNone(t *testing.T) {
	broker := NewBroker()
	rec := newEventRecorder()
	tool := newTestTool(t, broker, rec, time.Minute)

	done := runAsync(context.Background(), tool, nil)
	call := rec.nextCall(t)
	require.NotEmpty(t, call.ToolCallID)
	accepted, _ := broker.Submit("sess-1", call.ToolCallID, "alice", Result{Success: true})
	require.True(t, accepted)
	assert.True(t, waitResult(t, done).Success)
}

// MARK: - Broker

func TestBroker_SubmitRejections(t *testing.T) {
	broker := NewBroker()
	ch, err := broker.register("sess-1", "call-1", "alice")
	require.NoError(t, err)

	tests := []struct {
		name      string
		sessionID string
		callID    string
		user      string
	}{
		{"unknown call", "sess-1", "call-2", "alice"},
		{"other session", "sess-2", "call-1", "alice"},
		{"other user", "sess-1", "call-1", "mallory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accepted, reason := broker.Submit(tt.sessionID, tt.callID, tt.user, Result{Success: true})
			assert.False(t, accepted)
			assert.Equal(t, notPendingReason, reason, "every rejection reads the same")
		})
	}

	accepted, _ := broker.Submit("sess-1", "call-1", "alice", Result{Success: true})
	require.True(t, accepted, "rejected submissions must not consume the pending call")
	assert.True(t, (<-ch).Success)
}

func TestBroker_ConcurrentSubmitsAcceptExactlyOne(t *testing.T) {
	broker := NewBroker()
	ch, err := broker.register("sess-1", "call-1", "alice")
	require.NoError(t, err)

	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := broker.Submit("sess-1", "call-1", "alice", Result{Success: true}); ok {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), accepted.Load())
	assert.Len(t, ch, 1)
}

func TestBroker_WithdrawRacesSubmit(t *testing.T) {
	// Whichever of withdraw and Submit removes the entry first wins; the
	// other must observe that. A Submit that won leaves its result buffered.
	for i := 0; i < 200; i++ {
		broker := NewBroker()
		ch, err := broker.register("s", "c", "u")
		require.NoError(t, err)

		var submitted, withdrawn bool
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); submitted, _ = broker.Submit("s", "c", "u", Result{Success: true}) }()
		go func() { defer wg.Done(); withdrawn = broker.withdraw("s", "c") }()
		wg.Wait()

		require.NotEqual(t, submitted, withdrawn, "exactly one side must win")
		if submitted {
			require.Len(t, ch, 1)
		} else {
			require.Len(t, ch, 0)
		}
	}
}

// MARK: - Parse / Config

func TestParse_Validation(t *testing.T) {
	reserved := func(name string) bool { return name == "contact_human" }
	obj := `{"type":"object"}`
	tests := []struct {
		name    string
		defs    []*loomv1.ToolDefinition
		wantErr string
	}{
		{"valid", []*loomv1.ToolDefinition{{Name: "a_tool", InputSchemaJson: obj}}, ""},
		{"empty schema means no params", []*loomv1.ToolDefinition{{Name: "a_tool"}}, ""},
		{"bad name", []*loomv1.ToolDefinition{{Name: "a tool"}}, "must match"},
		{"empty name", []*loomv1.ToolDefinition{{}}, "must match"},
		{"duplicate", []*loomv1.ToolDefinition{{Name: "a"}, {Name: "a"}}, "duplicate name"},
		{"shadows agent tool", []*loomv1.ToolDefinition{{Name: "contact_human"}}, "already an agent tool"},
		{"invalid json", []*loomv1.ToolDefinition{{Name: "a", InputSchemaJson: "{"}}, "not valid JSON Schema"},
		{"non-object schema", []*loomv1.ToolDefinition{{Name: "a", InputSchemaJson: `{"type":"string"}`}}, `must have type "object"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			specs, err := Parse(tt.defs, reserved)
			if tt.wantErr == "" {
				require.NoError(t, err)
				require.Len(t, specs, len(tt.defs))
				assert.Equal(t, "object", specs[0].Schema.Type)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestParse_KeepsSchemaProperties(t *testing.T) {
	specs, err := Parse([]*loomv1.ToolDefinition{{
		Name:            "a",
		Description:     "desc",
		InputSchemaJson: `{"type":"object","properties":{"due":{"type":"string","format":"date-time"}},"required":["due"]}`,
	}}, func(string) bool { return false })
	require.NoError(t, err)
	assert.Equal(t, "desc", specs[0].Description)
	assert.Equal(t, []string{"due"}, specs[0].Schema.Required)
	assert.Equal(t, "date-time", specs[0].Schema.Properties["due"].Format)
}

func TestConfig_Timeout(t *testing.T) {
	cfg := Config{DefaultTimeout: 300 * time.Second, MaxTimeout: 900 * time.Second}
	tests := []struct {
		requested int32
		want      time.Duration
	}{
		{0, 300 * time.Second},
		{-5, 300 * time.Second},
		{60, 60 * time.Second},
		{900, 900 * time.Second},
		{3600, 900 * time.Second},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, cfg.Timeout(tt.requested), "requested=%d", tt.requested)
	}
}
