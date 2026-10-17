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
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/teradata-labs/loom/pkg/observability"
	"github.com/teradata-labs/loom/pkg/shuttle"
	"github.com/teradata-labs/loom/pkg/types"
)

// Error codes returned to the agent for calls the client never answered.
const (
	// CodeTimeout: no result arrived within the call's timeout, or the stream
	// that would carry it ended first.
	CodeTimeout = "CLIENT_TOOL_TIMEOUT"

	// CodeDuplicateCall: the tool-call id was already pending in the session.
	CodeDuplicateCall = "CLIENT_TOOL_DUPLICATE_CALL"
)

// clientToolProgress is the completion percentage on client-tool events. It
// matches the HITL hold events so a consumer's indicator does not flicker
// between the hand-off and the heartbeats that follow.
const clientToolProgress int32 = 50

// Tool is a client-executed tool: a shuttle.Tool whose Execute hands the call
// to the caller and waits for the submitted result.
type Tool struct {
	name        string
	description string
	schema      *shuttle.JSONSchema

	sessionID   string
	ownerUserID string
	timeout     time.Duration
	heartbeat   time.Duration

	broker *Broker
	emit   types.ProgressCallback
	tracer observability.Tracer
}

var _ shuttle.Tool = (*Tool)(nil)

// Name implements shuttle.Tool.
func (t *Tool) Name() string { return t.name }

// Description implements shuttle.Tool.
func (t *Tool) Description() string { return t.description }

// InputSchema implements shuttle.Tool.
func (t *Tool) InputSchema() *shuttle.JSONSchema { return t.schema }

// Backend implements shuttle.Tool. Client tools need no server backend.
func (t *Tool) Backend() string { return "" }

// Execute hands the call to the caller as a progress event carrying
// ClientToolCall, then waits for the caller's result, the timeout, or the end
// of the stream. Heartbeats keep the stream producing traffic while it waits.
// Execute never runs the tool on the server.
func (t *Tool) Execute(ctx context.Context, params map[string]interface{}) (*shuttle.Result, error) {
	callID := shuttle.ToolCallIDFromContext(ctx)
	if callID == "" {
		callID = uuid.NewString()
	}

	ctx, span := t.tracer.StartSpan(ctx, "client_tool.execute")
	defer t.tracer.EndSpan(span)
	span.SetAttribute("client_tool.name", t.name)
	span.SetAttribute("client_tool.call_id", callID)
	span.SetAttribute("client_tool.timeout_ms", t.timeout.Milliseconds())

	resultCh, err := t.broker.register(t.sessionID, callID, t.ownerUserID)
	if err != nil {
		span.SetAttribute("client_tool.outcome", "duplicate_call")
		return failure(CodeDuplicateCall, fmt.Sprintf("tool call %s is already pending", callID)), nil
	}

	start := time.Now()
	t.emit(types.ProgressEvent{
		Stage:     types.StageToolExecution,
		Progress:  clientToolProgress,
		Message:   fmt.Sprintf("Waiting for the client to run %s", t.name),
		ToolName:  t.name,
		Timestamp: start,
		ClientToolCall: &types.ClientToolCallInfo{
			ToolCallID: callID,
			ToolName:   t.name,
			Arguments:  params,
			SessionID:  t.sessionID,
			Timeout:    t.timeout,
		},
	})

	result, answered := t.await(ctx, callID, resultCh)
	span.SetAttribute("client_tool.wait_ms", time.Since(start).Milliseconds())
	if !answered {
		span.SetAttribute("client_tool.outcome", "timeout")
		return failure(CodeTimeout, fmt.Sprintf(
			"the client did not return a result for %s within %s", t.name, t.timeout)), nil
	}

	span.SetAttribute("client_tool.success", result.Success)
	if !result.Success {
		span.SetAttribute("client_tool.outcome", "client_error")
		code := result.ErrorCode
		if code == "" {
			code = "CLIENT_TOOL_FAILED"
		}
		return failure(code, result.ErrorMessage), nil
	}
	span.SetAttribute("client_tool.outcome", "success")
	return &shuttle.Result{Success: true, Data: result.Output}, nil
}

// await blocks until the call is answered, times out, or ctx ends. It reports
// false when no result arrived. On the timeout and ctx paths it withdraws the
// call; if Submit removed it first, the result is already buffered and is
// returned, so a result the broker accepted is never dropped.
func (t *Tool) await(ctx context.Context, callID string, resultCh <-chan Result) (Result, bool) {
	timer := time.NewTimer(t.timeout)
	defer timer.Stop()

	var beats <-chan time.Time
	if t.heartbeat > 0 {
		ticker := time.NewTicker(t.heartbeat)
		defer ticker.Stop()
		beats = ticker.C
	}

	for {
		select {
		case result := <-resultCh:
			return result, true
		case <-beats:
			t.emit(types.ProgressEvent{
				Stage:     types.StageToolExecution,
				Progress:  clientToolProgress,
				Message:   fmt.Sprintf("Still waiting for the client to run %s", t.name),
				Timestamp: time.Now(),
				Droppable: true,
			})
		case <-timer.C:
			return t.giveUp(callID, resultCh)
		case <-ctx.Done():
			return t.giveUp(callID, resultCh)
		}
	}
}

func (t *Tool) giveUp(callID string, resultCh <-chan Result) (Result, bool) {
	if t.broker.withdraw(t.sessionID, callID) {
		return Result{}, false
	}
	return <-resultCh, true
}

func failure(code, message string) *shuttle.Result {
	return &shuttle.Result{
		Success: false,
		Error: &shuttle.Error{
			Code:    code,
			Message: message,
		},
	}
}
