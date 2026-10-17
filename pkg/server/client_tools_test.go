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
package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/agent"
	llmtypes "github.com/teradata-labs/loom/pkg/llm/types"
	"github.com/teradata-labs/loom/pkg/shuttle"
	"github.com/teradata-labs/loom/pkg/storage/postgres"
	"github.com/teradata-labs/loom/pkg/types"
)

const reminderToolName = "device_add_reminder"

// clientToolLLM calls the client tool on its first provider call when it is
// advertised, then answers with whatever tool result it was shown. It records
// the advertised tool names of every call.
type clientToolLLM struct {
	mu          sync.Mutex
	advertised  [][]string
	toolResults []string
}

func (m *clientToolLLM) Chat(_ context.Context, messages []llmtypes.Message, tools []shuttle.Tool) (*llmtypes.LLMResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(tools))
	offered := false
	for _, tl := range tools {
		names = append(names, tl.Name())
		offered = offered || tl.Name() == reminderToolName
	}
	m.advertised = append(m.advertised, names)

	sawResult := false
	for _, msg := range messages {
		if msg.Role == "tool" {
			m.toolResults = append(m.toolResults, msg.Content)
			sawResult = true
		}
	}
	if offered && !sawResult {
		return &llmtypes.LLMResponse{
			ToolCalls: []types.ToolCall{{
				ID:    "toolu_reminder_1",
				Name:  reminderToolName,
				Input: map[string]interface{}{"title": "Buy milk", "due": "2026-10-18T09:00:00Z"},
			}},
			Usage: llmtypes.Usage{InputTokens: 10, OutputTokens: 5},
		}, nil
	}
	return &llmtypes.LLMResponse{Content: "done", Usage: llmtypes.Usage{InputTokens: 10, OutputTokens: 5}}, nil
}

func (m *clientToolLLM) Name() string  { return "client-tool-llm" }
func (m *clientToolLLM) Model() string { return "client-tool-model" }

func (m *clientToolLLM) snapshot() ([][]string, []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]string{}, m.advertised...), append([]string{}, m.toolResults...)
}

// clientStream is a StreamWeave stream whose onCall hook plays the client:
// it runs when a frame carrying client_tool_call is sent.
type clientStream struct {
	ctx    context.Context
	onCall func(*loomv1.ClientToolCall)

	mu       sync.Mutex
	messages []*loomv1.WeaveProgress
}

func (s *clientStream) Send(p *loomv1.WeaveProgress) error {
	s.mu.Lock()
	s.messages = append(s.messages, p)
	s.mu.Unlock()
	if call := p.GetClientToolCall(); call != nil && s.onCall != nil {
		s.onCall(call)
	}
	return nil
}

func (s *clientStream) clientToolCalls() []*loomv1.ClientToolCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var calls []*loomv1.ClientToolCall
	for _, m := range s.messages {
		if c := m.GetClientToolCall(); c != nil {
			calls = append(calls, c)
		}
	}
	return calls
}

func (s *clientStream) Context() context.Context     { return s.ctx }
func (s *clientStream) SendMsg(interface{}) error    { return nil }
func (s *clientStream) RecvMsg(interface{}) error    { return nil }
func (s *clientStream) SetHeader(metadata.MD) error  { return nil }
func (s *clientStream) SendHeader(metadata.MD) error { return nil }
func (s *clientStream) SetTrailer(metadata.MD)       {}
func (s *clientStream) Header() (metadata.MD, error) { return nil, nil }
func (s *clientStream) Trailer() metadata.MD         { return nil }
func (s *clientStream) CloseSend() error             { return nil }

func reminderToolDefinition() *loomv1.ToolDefinition {
	return &loomv1.ToolDefinition{
		Name:            reminderToolName,
		Description:     "Adds a reminder on the user's phone.",
		InputSchemaJson: `{"type":"object","properties":{"title":{"type":"string"},"due":{"type":"string","format":"date-time"}},"required":["title"]}`,
	}
}

func newClientToolServer(t *testing.T) (*MultiAgentServer, *clientToolLLM, *agent.Agent) {
	t.Helper()
	llm := &clientToolLLM{}
	ag := agent.NewAgent(&mockBackend{}, llm)
	return NewMultiAgentServer(map[string]*agent.Agent{"agent1": ag}, nil), llm, ag
}

// The full round trip: the agent is offered the client tool, its call is
// handed to the stream as client_tool_call, the client's submitted output
// becomes the tool result the model reads, and the turn completes.
func TestStreamWeave_ClientToolRoundTrip(t *testing.T) {
	srv, llm, _ := newClientToolServer(t)
	ctx := postgres.ContextWithUserID(context.Background(), "alice")

	var submitResp *loomv1.SubmitClientToolResultResponse
	var submitErr error
	stream := &clientStream{ctx: ctx}
	stream.onCall = func(call *loomv1.ClientToolCall) {
		output, err := structpb.NewValue(map[string]interface{}{"reminder_id": "rem-123", "list": "Reminders"})
		require.NoError(t, err)
		submitResp, submitErr = srv.SubmitClientToolResult(ctx, &loomv1.SubmitClientToolResultRequest{
			SessionId:  call.GetSessionId(),
			ToolCallId: call.GetToolCallId(),
			Success:    true,
			Output:     output,
		})
	}

	err := srv.StreamWeave(&loomv1.WeaveRequest{
		Query:                    "remind me to buy milk tomorrow at 9",
		SessionId:                "sess-client-tools",
		ClientTools:              []*loomv1.ToolDefinition{reminderToolDefinition()},
		ClientToolTimeoutSeconds: 30,
	}, stream)
	require.NoError(t, err)

	calls := stream.clientToolCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "toolu_reminder_1", calls[0].GetToolCallId(), "the hand-off must carry the model's tool_call_id")
	assert.Equal(t, reminderToolName, calls[0].GetToolName())
	assert.Equal(t, "sess-client-tools", calls[0].GetSessionId())
	assert.Equal(t, int32(30), calls[0].GetTimeoutSeconds())
	assert.JSONEq(t, `{"title":"Buy milk","due":"2026-10-18T09:00:00Z"}`, calls[0].GetArgumentsJson())

	require.NoError(t, submitErr)
	require.True(t, submitResp.GetAccepted(), submitResp.GetError())

	advertised, results := llm.snapshot()
	require.NotEmpty(t, advertised)
	assert.Contains(t, advertised[0], reminderToolName)
	require.NotEmpty(t, results, "the model must be shown the client's result")
	assert.True(t, strings.Contains(strings.Join(results, "\n"), "rem-123"),
		"tool result %q must carry the client's output", results)
	assert.Zero(t, srv.clientTools.PendingCount())
}

// Client tools exist only for the call that declared them: a later call in
// the same session without client_tools must not advertise them.
func TestStreamWeave_ClientToolsDoNotOutliveTheirCall(t *testing.T) {
	srv, llm, ag := newClientToolServer(t)
	ctx := context.Background()

	stream := &clientStream{ctx: ctx}
	stream.onCall = func(call *loomv1.ClientToolCall) {
		_, _ = srv.SubmitClientToolResult(ctx, &loomv1.SubmitClientToolResultRequest{
			SessionId: call.GetSessionId(), ToolCallId: call.GetToolCallId(), Success: true,
		})
	}
	require.NoError(t, srv.StreamWeave(&loomv1.WeaveRequest{
		Query: "first", SessionId: "sess-a", ClientTools: []*loomv1.ToolDefinition{reminderToolDefinition()},
	}, stream))

	before, _ := llm.snapshot()
	require.NoError(t, srv.StreamWeave(&loomv1.WeaveRequest{Query: "second", SessionId: "sess-a"}, &clientStream{ctx: ctx}))
	after, _ := llm.snapshot()

	require.Greater(t, len(after), len(before))
	for _, names := range after[len(before):] {
		assert.NotContains(t, names, reminderToolName)
	}
	assert.False(t, ag.HasTool(reminderToolName), "client tools must never be registered on the agent")
}

// A declined call reaches the model as a failed tool result with the client's code.
func TestStreamWeave_ClientToolFailureReachesModel(t *testing.T) {
	srv, llm, _ := newClientToolServer(t)
	ctx := context.Background()

	stream := &clientStream{ctx: ctx}
	stream.onCall = func(call *loomv1.ClientToolCall) {
		_, _ = srv.SubmitClientToolResult(ctx, &loomv1.SubmitClientToolResultRequest{
			SessionId:    call.GetSessionId(),
			ToolCallId:   call.GetToolCallId(),
			ErrorCode:    "DECLINED_BY_USER",
			ErrorMessage: "The user declined this action.",
		})
	}
	require.NoError(t, srv.StreamWeave(&loomv1.WeaveRequest{
		Query: "remind me", ClientTools: []*loomv1.ToolDefinition{reminderToolDefinition()},
	}, stream))

	_, results := llm.snapshot()
	joined := strings.Join(results, "\n")
	assert.Contains(t, joined, "DECLINED_BY_USER")
}

// Only the user whose stream received the call can answer it.
func TestSubmitClientToolResult_BoundToStreamOwner(t *testing.T) {
	srv, _, _ := newClientToolServer(t)
	alice := postgres.ContextWithUserID(context.Background(), "alice")
	mallory := postgres.ContextWithUserID(context.Background(), "mallory")

	var foreign, own *loomv1.SubmitClientToolResultResponse
	stream := &clientStream{ctx: alice}
	stream.onCall = func(call *loomv1.ClientToolCall) {
		req := &loomv1.SubmitClientToolResultRequest{
			SessionId: call.GetSessionId(), ToolCallId: call.GetToolCallId(), Success: true,
		}
		foreign, _ = srv.SubmitClientToolResult(mallory, req)
		own, _ = srv.SubmitClientToolResult(alice, req)
	}
	require.NoError(t, srv.StreamWeave(&loomv1.WeaveRequest{
		Query: "remind me", ClientTools: []*loomv1.ToolDefinition{reminderToolDefinition()},
	}, stream))

	require.NotNil(t, foreign)
	assert.False(t, foreign.GetAccepted())
	require.NotNil(t, own)
	assert.True(t, own.GetAccepted())
}

func TestSubmitClientToolResult_Validation(t *testing.T) {
	srv, _, _ := newClientToolServer(t)
	ctx := context.Background()

	_, err := srv.SubmitClientToolResult(ctx, &loomv1.SubmitClientToolResultRequest{ToolCallId: "c"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = srv.SubmitClientToolResult(ctx, &loomv1.SubmitClientToolResultRequest{SessionId: "s"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	resp, err := srv.SubmitClientToolResult(ctx, &loomv1.SubmitClientToolResultRequest{SessionId: "s", ToolCallId: "c"})
	require.NoError(t, err)
	assert.False(t, resp.GetAccepted())
	assert.NotEmpty(t, resp.GetError())
}

func TestWeave_RejectsClientTools(t *testing.T) {
	srv, llm, _ := newClientToolServer(t)
	_, err := srv.Weave(context.Background(), &loomv1.WeaveRequest{
		Query: "hi", ClientTools: []*loomv1.ToolDefinition{reminderToolDefinition()},
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	advertised, _ := llm.snapshot()
	assert.Empty(t, advertised, "the agent must not run")
}

func TestStreamWeave_RejectsInvalidClientTools(t *testing.T) {
	tests := []struct {
		name string
		defs []*loomv1.ToolDefinition
	}{
		{"shadows an agent tool", []*loomv1.ToolDefinition{{Name: "local_tool"}}},
		{"duplicate names", []*loomv1.ToolDefinition{{Name: "a"}, {Name: "a"}}},
		{"invalid name", []*loomv1.ToolDefinition{{Name: "has space"}}},
		{"non-object schema", []*loomv1.ToolDefinition{{Name: "a", InputSchemaJson: `{"type":"array"}`}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, llm, ag := newClientToolServer(t)
			ag.RegisterTool(&shuttle.MockTool{MockName: "local_tool"})

			err := srv.StreamWeave(&loomv1.WeaveRequest{Query: "hi", ClientTools: tt.defs}, &clientStream{ctx: context.Background()})
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			advertised, _ := llm.snapshot()
			assert.Empty(t, advertised, "the agent must not run")
		})
	}
}

func TestSingleAgentServer_RejectsClientTools(t *testing.T) {
	srv := NewServer(agent.NewAgent(&mockBackend{}, &clientToolLLM{}), nil)
	req := &loomv1.WeaveRequest{Query: "hi", ClientTools: []*loomv1.ToolDefinition{reminderToolDefinition()}}

	_, err := srv.Weave(context.Background(), req)
	assert.Equal(t, codes.Unimplemented, status.Code(err))
	err = srv.StreamWeave(req, &clientStream{ctx: context.Background()})
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}

// Over the SSE endpoint the hand-off frame must be usable by a plain JSON
// client: arguments arrive as a JSON string, not a protobuf Struct encoding.
func TestStreamWeaveSSE_ClientToolCallFrame(t *testing.T) {
	srv, llm, _ := newClientToolServer(t)
	h := NewHTTPServer(srv, "", "", zaptest.NewLogger(t))
	ts := httptest.NewServer(http.HandlerFunc(h.handleStreamWeaveSSE))
	defer ts.Close()

	body, err := json.Marshal(map[string]interface{}{
		"query":        "remind me to buy milk",
		"session_id":   "sess-sse",
		"client_tools": []map[string]interface{}{{"name": reminderToolName, "input_schema_json": `{"type":"object"}`}},
	})
	require.NoError(t, err)
	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var call map[string]interface{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimPrefix(scanner.Text(), "data: ")
		if line == scanner.Text() {
			continue
		}
		var frame map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(line), &frame))
		c, ok := frame["client_tool_call"].(map[string]interface{})
		if !ok || call != nil {
			continue
		}
		call = c
		accepted, err := srv.SubmitClientToolResult(context.Background(), &loomv1.SubmitClientToolResultRequest{
			SessionId:  c["session_id"].(string),
			ToolCallId: c["tool_call_id"].(string),
			Success:    true,
			Output:     structpb.NewStringValue("rem-sse"),
		})
		require.NoError(t, err)
		require.True(t, accepted.GetAccepted(), accepted.GetError())
	}
	require.NoError(t, scanner.Err())

	require.NotNil(t, call, "the stream must carry a client_tool_call frame")
	assert.Equal(t, reminderToolName, call["tool_name"])
	args, ok := call["arguments_json"].(string)
	require.True(t, ok, "arguments_json must be a JSON string, got %T", call["arguments_json"])
	assert.JSONEq(t, `{"title":"Buy milk","due":"2026-10-18T09:00:00Z"}`, args)

	_, results := llm.snapshot()
	assert.Contains(t, strings.Join(results, "\n"), "rem-sse")
}
