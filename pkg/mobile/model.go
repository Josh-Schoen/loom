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
	"fmt"
	"strings"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/shuttle"
	"github.com/teradata-labs/loom/pkg/types"
)

// ModelProvider is implemented by the host to perform model calls. A host
// can back it with an on-device model or with a network call to a relay it
// controls.
type ModelProvider interface {
	// ProviderName identifies the provider, for example "apple-on-device".
	ProviderName() string

	// ModelID identifies the model the provider calls.
	ModelID() string

	// Chat performs one model call. requestJSON is a HostModelRequest and the
	// return value is a HostModelResponse, both as proto3 JSON.
	Chat(requestJSON string) (string, error)
}

// modelAdapter presents a host ModelProvider as a Loom LLM provider.
type modelAdapter struct {
	host ModelProvider
}

func (m *modelAdapter) Name() string  { return m.host.ProviderName() }
func (m *modelAdapter) Model() string { return m.host.ModelID() }

func (m *modelAdapter) Chat(ctx context.Context, messages []types.Message, tools []shuttle.Tool) (*types.LLMResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req, err := buildModelRequest(messages, tools)
	if err != nil {
		return nil, fmt.Errorf("encode model request: %w", err)
	}
	reqJSON, err := marshalJSON(req)
	if err != nil {
		return nil, fmt.Errorf("encode model request: %w", err)
	}
	respJSON, err := m.host.Chat(reqJSON)
	if err != nil {
		return nil, fmt.Errorf("host model: %w", err)
	}
	resp := &loomv1.HostModelResponse{}
	if err := lenientUnmarshal.Unmarshal([]byte(respJSON), resp); err != nil {
		return nil, fmt.Errorf("decode model response: %w", err)
	}
	return parseModelResponse(resp)
}

func buildModelRequest(messages []types.Message, tools []shuttle.Tool) (*loomv1.HostModelRequest, error) {
	req := &loomv1.HostModelRequest{
		Messages: make([]*loomv1.HostModelMessage, 0, len(messages)),
		Tools:    make([]*loomv1.HostToolSpec, 0, len(tools)),
	}
	for i := range messages {
		hm, err := buildModelMessage(&messages[i])
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		req.Messages = append(req.Messages, hm)
	}
	for _, t := range tools {
		spec, err := buildToolSpec(t)
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", t.Name(), err)
		}
		req.Tools = append(req.Tools, spec)
	}
	return req, nil
}

func buildModelMessage(msg *types.Message) (*loomv1.HostModelMessage, error) {
	hm := &loomv1.HostModelMessage{
		Role:      msg.Role,
		Content:   strings.ToValidUTF8(messageText(msg), "�"),
		ToolUseId: msg.ToolUseID,
	}
	for _, tc := range msg.ToolCalls {
		input, err := toStruct(nonNilMap(tc.Input))
		if err != nil {
			return nil, fmt.Errorf("tool call %q input: %w", tc.Name, err)
		}
		hm.ToolCalls = append(hm.ToolCalls, &loomv1.HostToolCall{Id: tc.ID, Name: tc.Name, Input: input})
	}
	if msg.ToolResult != nil {
		value, err := toValue(msg.ToolResult.Data)
		if err != nil {
			return nil, fmt.Errorf("tool result: %w", err)
		}
		hm.ToolResult = value
		hm.ToolError = strings.ToValidUTF8(toolErrorText(msg.ToolResult), "�")
	}
	return hm, nil
}

// messageText returns the text of a message. Content blocks take precedence
// over Content, matching the providers; only text blocks are carried.
func messageText(msg *types.Message) string {
	if len(msg.ContentBlocks) == 0 {
		return msg.Content
	}
	var parts []string
	for _, b := range msg.ContentBlocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	if len(parts) == 0 {
		return msg.Content
	}
	return strings.Join(parts, "\n")
}

func toolErrorText(r *shuttle.Result) string {
	if r.Success {
		return ""
	}
	if r.Error != nil && r.Error.Message != "" {
		return r.Error.Message
	}
	return "tool failed without an error message"
}

func buildToolSpec(t shuttle.Tool) (*loomv1.HostToolSpec, error) {
	schema := t.InputSchema()
	if schema == nil {
		schema = &shuttle.JSONSchema{Type: "object"}
	}
	s, err := toStruct(schema)
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	return &loomv1.HostToolSpec{Name: t.Name(), Description: t.Description(), InputSchema: s}, nil
}

func parseModelResponse(resp *loomv1.HostModelResponse) (*types.LLMResponse, error) {
	out := &types.LLMResponse{
		Content:    resp.GetContent(),
		StopReason: resp.GetStopReason(),
		Thinking:   resp.GetThinking(),
	}
	for i, tc := range resp.GetToolCalls() {
		if tc.GetName() == "" {
			return nil, fmt.Errorf("decode model response: tool call %d has no name", i)
		}
		if tc.GetId() == "" {
			return nil, fmt.Errorf("decode model response: tool call %q has no id", tc.GetName())
		}
		out.ToolCalls = append(out.ToolCalls, types.ToolCall{
			ID:    tc.GetId(),
			Name:  tc.GetName(),
			Input: nonNilMap(tc.GetInput().AsMap()),
		})
	}
	if u := resp.GetUsage(); u != nil {
		out.Usage = types.Usage{
			InputTokens:              int(u.GetInputTokens()),
			OutputTokens:             int(u.GetOutputTokens()),
			TotalTokens:              int(u.GetTotalTokens()),
			CostUSD:                  u.GetCostUsd(),
			CacheReadInputTokens:     int(u.GetCacheReadInputTokens()),
			CacheCreationInputTokens: int(u.GetCacheCreationInputTokens()),
		}
		if out.Usage.TotalTokens == 0 {
			out.Usage.TotalTokens = out.Usage.InputTokens + out.Usage.OutputTokens
		}
	}
	return out, nil
}

func nonNilMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return map[string]interface{}{}
	}
	return m
}
