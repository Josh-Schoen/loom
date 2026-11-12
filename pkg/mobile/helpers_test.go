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
package mobile

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// TestMain points Loom's data directory at a temporary location so the
// process-wide tool-result cache never touches the developer's ~/.loom.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "loom-mobile-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create temp dir:", err)
		os.Exit(1)
	}
	if err := os.Setenv(dataDirEnv, dir); err != nil {
		fmt.Fprintln(os.Stderr, "set env:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

const (
	testToolName = "trail_lookup"
	testSchema   = `{"type":"object","properties":{"name":{"type":"string","description":"Trail name"}},"required":["name"]}`
	testPrompt   = "Answer questions about ski trails with the trail_lookup tool."
)

// scriptedModel is a host model whose answers are produced by respond. It
// records every request it receives.
type scriptedModel struct {
	mu       sync.Mutex
	requests []*loomv1.HostModelRequest
	respond  func(*loomv1.HostModelRequest) (*loomv1.HostModelResponse, error)
}

func (m *scriptedModel) ProviderName() string { return "scripted" }
func (m *scriptedModel) ModelID() string      { return "scripted-1" }

func (m *scriptedModel) Chat(requestJSON string) (string, error) {
	req := &loomv1.HostModelRequest{}
	if err := protojson.Unmarshal([]byte(requestJSON), req); err != nil {
		return "", fmt.Errorf("scripted model could not decode request: %w", err)
	}
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	resp, err := m.respond(req)
	if err != nil {
		return "", err
	}
	return marshalJSON(resp)
}

func (m *scriptedModel) recorded() []*loomv1.HostModelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*loomv1.HostModelRequest(nil), m.requests...)
}

// lookupThenAnswer calls trail_lookup with the user's text, then answers
// with what the tool returned, the way a real model drives a tool loop.
func lookupThenAnswer(req *loomv1.HostModelRequest) (*loomv1.HostModelResponse, error) {
	last := req.GetMessages()[len(req.GetMessages())-1]
	if last.GetRole() == "tool" {
		if last.GetToolError() != "" {
			return &loomv1.HostModelResponse{Content: "tool failed: " + last.GetToolError(), StopReason: "end_turn"}, nil
		}
		data, err := protojson.Marshal(last.GetToolResult())
		if err != nil {
			return nil, err
		}
		return &loomv1.HostModelResponse{
			Content:    "trail facts: " + string(data),
			StopReason: "end_turn",
			Usage:      &loomv1.LLMCost{InputTokens: 40, OutputTokens: 10},
		}, nil
	}
	input, err := toStruct(map[string]interface{}{"name": last.GetContent()})
	if err != nil {
		return nil, err
	}
	return &loomv1.HostModelResponse{
		StopReason: "tool_use",
		ToolCalls:  []*loomv1.HostToolCall{{Id: fmt.Sprintf("call_%d", len(req.GetMessages())), Name: testToolName, Input: input}},
	}, nil
}

// fakeTool is a host tool backed by a function.
type fakeTool struct {
	name        string
	description string
	schema      string
	execute     func(inputJSON string) (string, error)
	calls       atomic.Int64
}

func (t *fakeTool) ToolName() string        { return t.name }
func (t *fakeTool) ToolDescription() string { return t.description }
func (t *fakeTool) InputSchemaJSON() string { return t.schema }

func (t *fakeTool) Execute(inputJSON string) (string, error) {
	t.calls.Add(1)
	return t.execute(inputJSON)
}

func trailTool(execute func(string) (string, error)) *fakeTool {
	return &fakeTool{name: testToolName, description: "Look up pitch and aspect for a named ski trail.", schema: testSchema, execute: execute}
}

func trailFacts(string) (string, error) {
	return `{"max_pitch_deg":34,"aspect":"NE"}`, nil
}

func validConfig(dataDir string) *loomv1.MobileEngineConfig {
	return &loomv1.MobileEngineConfig{
		DataDir: dataDir,
		Agent:   &loomv1.AgentConfig{Name: "trail-guide", SystemPrompt: testPrompt},
	}
}

func configJSON(t *testing.T, cfg *loomv1.MobileEngineConfig) string {
	t.Helper()
	out, err := marshalJSON(cfg)
	require.NoError(t, err)
	return out
}

// newTestEngine creates an engine over dataDir that is closed with the test.
func newTestEngine(t *testing.T, dataDir string, model ModelProvider) *Engine {
	t.Helper()
	e, err := NewEngine(configJSON(t, validConfig(dataDir)), model)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, e.Close()) })
	return e
}

func weaveRequest(t *testing.T, sessionID, query string) string {
	t.Helper()
	out, err := marshalJSON(&loomv1.WeaveRequest{SessionId: sessionID, Query: query})
	require.NoError(t, err)
	return out
}

func decodeWeaveResponse(t *testing.T, responseJSON string) *loomv1.WeaveResponse {
	t.Helper()
	resp := &loomv1.WeaveResponse{}
	require.NoError(t, protojson.Unmarshal([]byte(responseJSON), resp))
	return resp
}

// progressRecorder is a host progress listener that keeps every event.
type progressRecorder struct {
	mu     sync.Mutex
	events []*loomv1.WeaveProgress
	err    error
}

func (r *progressRecorder) OnProgress(progressJSON string) {
	p := &loomv1.WeaveProgress{}
	err := protojson.Unmarshal([]byte(progressJSON), p)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.err = errors.Join(r.err, err)
		return
	}
	r.events = append(r.events, p)
}

func (r *progressRecorder) recorded(t *testing.T) []*loomv1.WeaveProgress {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NoError(t, r.err)
	return append([]*loomv1.WeaveProgress(nil), r.events...)
}
