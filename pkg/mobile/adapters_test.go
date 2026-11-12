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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/shuttle"
	"github.com/teradata-labs/loom/pkg/types"
)

func TestBuildModelMessage(t *testing.T) {
	tests := []struct {
		name  string
		msg   types.Message
		check func(*testing.T, *loomv1.HostModelMessage)
	}{
		{
			"plain text",
			types.Message{Role: "user", Content: "hi"},
			func(t *testing.T, m *loomv1.HostModelMessage) {
				assert.Equal(t, "user", m.GetRole())
				assert.Equal(t, "hi", m.GetContent())
				assert.Nil(t, m.GetToolResult())
			},
		},
		{
			"text blocks take precedence and images are dropped",
			types.Message{Role: "user", Content: "ignored", ContentBlocks: []types.ContentBlock{
				{Type: "text", Text: "first"}, {Type: "image", Image: &types.ImageContent{}}, {Type: "text", Text: "second"},
			}},
			func(t *testing.T, m *loomv1.HostModelMessage) {
				assert.Equal(t, "first\nsecond", m.GetContent())
			},
		},
		{
			"image-only blocks fall back to content",
			types.Message{Role: "user", Content: "caption", ContentBlocks: []types.ContentBlock{{Type: "image", Image: &types.ImageContent{}}}},
			func(t *testing.T, m *loomv1.HostModelMessage) {
				assert.Equal(t, "caption", m.GetContent())
			},
		},
		{
			"invalid UTF-8 is replaced so the message can be encoded",
			types.Message{Role: "assistant", Content: "caf\xc3"},
			func(t *testing.T, m *loomv1.HostModelMessage) {
				assert.Equal(t, "caf�", m.GetContent())
			},
		},
		{
			"assistant tool call with nil input",
			types.Message{Role: "assistant", ToolCalls: []types.ToolCall{{ID: "c1", Name: "lookup"}}},
			func(t *testing.T, m *loomv1.HostModelMessage) {
				require.Len(t, m.GetToolCalls(), 1)
				assert.Equal(t, "c1", m.GetToolCalls()[0].GetId())
				assert.NotNil(t, m.GetToolCalls()[0].GetInput())
				assert.Empty(t, m.GetToolCalls()[0].GetInput().GetFields())
			},
		},
		{
			"successful tool result",
			types.Message{Role: "tool", ToolUseID: "c1", ToolResult: &shuttle.Result{Success: true, Data: []interface{}{1, "two"}}},
			func(t *testing.T, m *loomv1.HostModelMessage) {
				assert.Equal(t, "c1", m.GetToolUseId())
				assert.Len(t, m.GetToolResult().GetListValue().GetValues(), 2)
				assert.Empty(t, m.GetToolError())
			},
		},
		{
			"failed tool result with message",
			types.Message{Role: "tool", ToolResult: &shuttle.Result{Error: &shuttle.Error{Code: "X", Message: "boom"}}},
			func(t *testing.T, m *loomv1.HostModelMessage) {
				assert.Equal(t, "boom", m.GetToolError())
			},
		},
		{
			"failed tool result without message",
			types.Message{Role: "tool", ToolResult: &shuttle.Result{}},
			func(t *testing.T, m *loomv1.HostModelMessage) {
				assert.NotEmpty(t, m.GetToolError())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildModelMessage(&tt.msg)
			require.NoError(t, err)
			tt.check(t, got)
			_, err = marshalJSON(got)
			require.NoError(t, err, "every built message must encode")
		})
	}
}

func TestBuildModelMessage_UnencodableToolData(t *testing.T) {
	_, err := buildModelMessage(&types.Message{Role: "tool", ToolResult: &shuttle.Result{Success: true, Data: make(chan int)}})
	require.Error(t, err)
}

// schemalessTool is a Loom tool that declares no input schema.
type schemalessTool struct{}

func (schemalessTool) Name() string                     { return "ping" }
func (schemalessTool) Description() string              { return "Check connectivity." }
func (schemalessTool) InputSchema() *shuttle.JSONSchema { return nil }
func (schemalessTool) Backend() string                  { return "" }
func (schemalessTool) Execute(context.Context, map[string]interface{}) (*shuttle.Result, error) {
	return &shuttle.Result{Success: true}, nil
}

func TestBuildToolSpec_NilSchemaBecomesEmptyObject(t *testing.T) {
	spec, err := buildToolSpec(schemalessTool{})
	require.NoError(t, err)
	assert.Equal(t, "ping", spec.GetName())
	assert.Equal(t, "object", spec.GetInputSchema().GetFields()["type"].GetStringValue())
}

func TestParseModelResponse(t *testing.T) {
	input, err := toStruct(map[string]interface{}{"name": "Corbet's"})
	require.NoError(t, err)

	tests := []struct {
		name    string
		resp    *loomv1.HostModelResponse
		wantErr string
		check   func(*testing.T, *types.LLMResponse)
	}{
		{
			name: "text with usage; total is derived when the host omits it",
			resp: &loomv1.HostModelResponse{
				Content: "hi", StopReason: "end_turn", Thinking: "thought",
				Usage: &loomv1.LLMCost{InputTokens: 7, OutputTokens: 3, CostUsd: 0.01, CacheReadInputTokens: 2},
			},
			check: func(t *testing.T, r *types.LLMResponse) {
				assert.Equal(t, "hi", r.Content)
				assert.Equal(t, "end_turn", r.StopReason)
				assert.Equal(t, "thought", r.Thinking)
				assert.Equal(t, 10, r.Usage.TotalTokens)
				assert.Equal(t, 2, r.Usage.CacheReadInputTokens)
				assert.InDelta(t, 0.01, r.Usage.CostUSD, 1e-9)
			},
		},
		{
			name: "host-supplied total is kept",
			resp: &loomv1.HostModelResponse{Usage: &loomv1.LLMCost{InputTokens: 7, OutputTokens: 3, TotalTokens: 99}},
			check: func(t *testing.T, r *types.LLMResponse) {
				assert.Equal(t, 99, r.Usage.TotalTokens)
			},
		},
		{
			name: "tool calls",
			resp: &loomv1.HostModelResponse{ToolCalls: []*loomv1.HostToolCall{
				{Id: "c1", Name: "lookup", Input: input}, {Id: "c2", Name: "ping"},
			}},
			check: func(t *testing.T, r *types.LLMResponse) {
				require.Len(t, r.ToolCalls, 2)
				assert.Equal(t, "Corbet's", r.ToolCalls[0].Input["name"])
				assert.NotNil(t, r.ToolCalls[1].Input, "a call without arguments gets an empty map")
			},
		},
		{name: "tool call without name", resp: &loomv1.HostModelResponse{ToolCalls: []*loomv1.HostToolCall{{Id: "c1"}}}, wantErr: "has no name"},
		{name: "tool call without id", resp: &loomv1.HostModelResponse{ToolCalls: []*loomv1.HostToolCall{{Name: "lookup"}}}, wantErr: "has no id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseModelResponse(tt.resp)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			tt.check(t, got)
		})
	}
}

func TestModelAdapter(t *testing.T) {
	t.Run("identifies the host provider", func(t *testing.T) {
		a := &modelAdapter{host: &scriptedModel{}}
		assert.Equal(t, "scripted", a.Name())
		assert.Equal(t, "scripted-1", a.Model())
	})

	t.Run("does not call the host once the turn is cancelled", func(t *testing.T) {
		model := &scriptedModel{respond: answerOnly("x")}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := (&modelAdapter{host: model}).Chat(ctx, []types.Message{{Role: "user", Content: "hi"}}, nil)
		require.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, model.recorded())
	})

	t.Run("ignores response fields it does not know", func(t *testing.T) {
		a := &modelAdapter{host: rawModel(`{"content":"hi","added_in_a_later_contract":true}`)}
		resp, err := a.Chat(context.Background(), []types.Message{{Role: "user", Content: "hi"}}, nil)
		require.NoError(t, err)
		assert.Equal(t, "hi", resp.Content)
	})

	t.Run("rejects a response that is not JSON", func(t *testing.T) {
		a := &modelAdapter{host: rawModel("not json")}
		_, err := a.Chat(context.Background(), []types.Message{{Role: "user", Content: "hi"}}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode model response")
	})
}

// rawModel is a host model that returns a fixed string.
type rawModel string

func (rawModel) ProviderName() string          { return "raw" }
func (rawModel) ModelID() string               { return "raw-1" }
func (r rawModel) Chat(string) (string, error) { return string(r), nil }

func TestToolAdapter_Execute(t *testing.T) {
	t.Run("passes arguments as a JSON object and decodes the result", func(t *testing.T) {
		var got string
		a, err := newToolAdapter(trailTool(func(in string) (string, error) {
			got = in
			return `[1,2]`, nil
		}))
		require.NoError(t, err)

		res, err := a.Execute(context.Background(), map[string]interface{}{"name": "Corbet's"})
		require.NoError(t, err)
		assert.JSONEq(t, `{"name":"Corbet's"}`, got)
		assert.True(t, res.Success)
		assert.Equal(t, []interface{}{float64(1), float64(2)}, res.Data)
	})

	t.Run("nil arguments are sent as an empty object", func(t *testing.T) {
		var got string
		a, err := newToolAdapter(trailTool(func(in string) (string, error) {
			got = in
			return "", nil
		}))
		require.NoError(t, err)

		res, err := a.Execute(context.Background(), nil)
		require.NoError(t, err)
		assert.Equal(t, "{}", got)
		assert.True(t, res.Success)
		assert.Nil(t, res.Data)
	})

	t.Run("does not call the host once the turn is cancelled", func(t *testing.T) {
		tool := trailTool(trailFacts)
		a, err := newToolAdapter(tool)
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = a.Execute(ctx, nil)
		require.ErrorIs(t, err, context.Canceled)
		assert.Zero(t, tool.calls.Load())
	})

	t.Run("describes itself from the values read at registration", func(t *testing.T) {
		a, err := newToolAdapter(trailTool(trailFacts))
		require.NoError(t, err)
		assert.Equal(t, testToolName, a.Name())
		assert.NotEmpty(t, a.Description())
		assert.Equal(t, "object", a.InputSchema().Type)
		assert.Empty(t, a.Backend())
	})
}

func TestHostBackend_ReportsNoData(t *testing.T) {
	ctx := context.Background()
	b := hostBackend{}
	assert.Equal(t, "host", b.Name())
	_, err := b.ExecuteQuery(ctx, "select 1")
	require.ErrorIs(t, err, errNoBackend)
	_, err = b.GetSchema(ctx, "t")
	require.ErrorIs(t, err, errNoBackend)
	_, err = b.ExecuteCustomOperation(ctx, "op", nil)
	require.ErrorIs(t, err, errNoBackend)
	resources, err := b.ListResources(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, resources)
	require.NoError(t, b.Ping(ctx))
	require.NoError(t, b.Close())
	assert.NotNil(t, b.Capabilities())
}
