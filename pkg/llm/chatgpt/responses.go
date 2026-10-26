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

package chatgpt

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/teradata-labs/loom/pkg/llm"
	llmtypes "github.com/teradata-labs/loom/pkg/llm/types"
	"github.com/teradata-labs/loom/pkg/shuttle"
)

// responsesRequest is a Responses API request (POST /v1/responses) in the
// form plan usage accepts: streamed, not stored, the whole conversation in
// input, and no sampling or length fields (the preview rejects
// temperature, top_p, max_output_tokens and others).
type responsesRequest struct {
	Model        string          `json:"model"`
	Instructions string          `json:"instructions,omitempty"`
	Input        []inputItem     `json:"input"`
	Tools        []functionTool  `json:"tools,omitempty"`
	Stream       bool            `json:"stream"`
	Store        bool            `json:"store"`
	Reasoning    *reasoningParam `json:"reasoning,omitempty"`
}

type reasoningParam struct {
	Effort string `json:"effort,omitempty"`
}

// inputItem is one item of a request's input: a message (Role set), a
// function call the model made earlier, or that call's output.
type inputItem struct {
	Type      string        `json:"type"`
	Role      string        `json:"role,omitempty"`
	Content   []contentPart `json:"content,omitempty"`
	CallID    string        `json:"call_id,omitempty"`
	Name      string        `json:"name,omitempty"`
	Arguments string        `json:"arguments,omitempty"`
	Output    *string       `json:"output,omitempty"`
}

type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type functionTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

// buildInput turns loom messages into instructions and input items. System
// messages are joined into instructions (plan usage rejects system-role
// items); tool calls and results become function_call and
// function_call_output items. Calls and results whose pair is missing are
// dropped, as the Responses API rejects an output without its call.
func buildInput(messages []llmtypes.Message) (string, []inputItem) {
	var instructions []string
	calls := make(map[string]bool)
	results := make(map[string]bool)
	for _, m := range messages {
		switch m.Role {
		case "assistant":
			for _, tc := range m.ToolCalls {
				if tc.ID != "" && llm.SanitizeToolName(tc.Name) != "" {
					calls[tc.ID] = true
				}
			}
		case "tool":
			results[m.ToolUseID] = true
		}
	}

	var items []inputItem
	for _, m := range messages {
		switch m.Role {
		case "system":
			if s := strings.TrimSpace(m.Content); s != "" {
				instructions = append(instructions, s)
			}
		case "user":
			parts := userParts(m)
			if len(parts) > 0 {
				items = append(items, inputItem{Type: "message", Role: "user", Content: parts})
			}
		case "assistant":
			if m.Content != "" {
				items = append(items, inputItem{Type: "message", Role: "assistant", Content: []contentPart{{Type: "output_text", Text: m.Content}}})
			}
			for _, tc := range m.ToolCalls {
				name := llm.SanitizeToolName(tc.Name)
				if !calls[tc.ID] || !results[tc.ID] {
					continue
				}
				args, err := json.Marshal(tc.Input)
				if err != nil || string(args) == "null" {
					args = []byte("{}")
				}
				items = append(items, inputItem{Type: "function_call", CallID: tc.ID, Name: name, Arguments: string(args)})
			}
		case "tool":
			if !calls[m.ToolUseID] {
				continue
			}
			out := m.Content
			items = append(items, inputItem{Type: "function_call_output", CallID: m.ToolUseID, Output: &out})
		}
	}
	return strings.Join(instructions, "\n\n"), items
}

func userParts(m llmtypes.Message) []contentPart {
	if len(m.ContentBlocks) == 0 {
		if m.Content == "" {
			return nil
		}
		return []contentPart{{Type: "input_text", Text: m.Content}}
	}
	var parts []contentPart
	for _, b := range m.ContentBlocks {
		switch b.Type {
		case "text":
			parts = append(parts, contentPart{Type: "input_text", Text: b.Text})
		case "image":
			if b.Image == nil {
				continue
			}
			url := b.Image.Source.URL
			if b.Image.Source.Type == "base64" {
				url = fmt.Sprintf("data:%s;base64,%s", b.Image.Source.MediaType, b.Image.Source.Data)
			}
			if url != "" {
				parts = append(parts, contentPart{Type: "input_image", ImageURL: url})
			}
		}
	}
	return parts
}

// buildTools turns loom tools into function tools, returning the map from
// the sanitized names sent to the original names.
func buildTools(tools []shuttle.Tool) ([]functionTool, map[string]string) {
	names := make(map[string]string, len(tools))
	out := make([]functionTool, 0, len(tools))
	for _, t := range tools {
		name := llm.SanitizeToolName(t.Name())
		if name == "" {
			continue
		}
		names[name] = t.Name()
		params := map[string]any{"type": "object", "properties": map[string]any{}}
		if s := t.InputSchema(); s != nil {
			if data, err := json.Marshal(s); err == nil {
				var m map[string]any
				if json.Unmarshal(data, &m) == nil {
					if m["type"] == nil || m["type"] == "" {
						m["type"] = "object"
					}
					if m["properties"] == nil {
						m["properties"] = map[string]any{}
					}
					params = m
				}
			}
		}
		out = append(out, functionTool{Type: "function", Name: name, Description: t.Description(), Parameters: params})
	}
	return out, names
}

// streamEvent is the part of a Responses API stream event the client reads.
type streamEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta"`
	Item     *outputItem     `json:"item"`
	Response *responseObject `json:"response"`
	// Code and Message are set on "error" events.
	Code    string `json:"code"`
	Message string `json:"message"`
}

type outputItem struct {
	Type      string        `json:"type"`
	ID        string        `json:"id"`
	CallID    string        `json:"call_id"`
	Name      string        `json:"name"`
	Arguments string        `json:"arguments"`
	Content   []contentPart `json:"content"`
	Summary   []contentPart `json:"summary"`
}

type responseObject struct {
	Status            string       `json:"status"`
	Output            []outputItem `json:"output"`
	Usage             *usage       `json:"usage"`
	Error             *apiError    `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

type usage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Param   string `json:"param"`
}

// readStream reads a Responses API event stream to its end and builds the
// response. onText is called with each piece of output text. Only
// response.completed (or response.incomplete, which keeps what was
// written) counts as an answer; response.failed and error events become a
// *PlanError, even after HTTP 200.
func readStream(body io.Reader, names map[string]string, onText func(string)) (*llmtypes.LLMResponse, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var (
		text     strings.Builder
		thinking strings.Builder
		done     []outputItem
		final    *responseObject
	)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, fmt.Errorf("chatgpt: read stream event: %w", err)
		}
		switch ev.Type {
		case "response.output_text.delta":
			text.WriteString(ev.Delta)
			if onText != nil && ev.Delta != "" {
				onText(ev.Delta)
			}
		case "response.reasoning_summary_text.delta":
			thinking.WriteString(ev.Delta)
		case "response.output_item.done":
			if ev.Item != nil {
				done = append(done, *ev.Item)
			}
		case "response.completed", "response.incomplete":
			final = ev.Response
		case "response.failed":
			if ev.Response != nil && ev.Response.Error != nil {
				return nil, newPlanError(0, ev.Response.Error.Code, ev.Response.Error.Message, ev.Response.Error.Param)
			}
			return nil, newPlanError(0, "", "the response failed", "")
		case "error":
			return nil, newPlanError(0, ev.Code, ev.Message, "")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("chatgpt: read stream: %w", err)
	}
	if final == nil {
		return nil, fmt.Errorf("chatgpt: the stream ended before the response completed")
	}

	items := final.Output
	if len(items) == 0 {
		items = done
	}
	resp := &llmtypes.LLMResponse{StopReason: "end_turn", Thinking: thinking.String()}
	var finalText strings.Builder
	for _, it := range items {
		switch it.Type {
		case "message":
			for _, p := range it.Content {
				if p.Type == "output_text" {
					finalText.WriteString(p.Text)
				}
			}
		case "function_call":
			input := map[string]interface{}{}
			if strings.TrimSpace(it.Arguments) != "" {
				if err := json.Unmarshal([]byte(it.Arguments), &input); err != nil {
					return nil, fmt.Errorf("chatgpt: tool call %q arguments: %w", it.Name, err)
				}
			}
			name := it.Name
			if orig, ok := names[name]; ok {
				name = orig
			}
			resp.ToolCalls = append(resp.ToolCalls, llmtypes.ToolCall{ID: it.CallID, Name: name, Input: input})
		case "reasoning":
			if resp.Thinking == "" {
				for _, p := range it.Summary {
					resp.Thinking += p.Text
				}
			}
		}
	}
	resp.Content = finalText.String()
	if resp.Content == "" {
		resp.Content = text.String()
	}
	if len(resp.ToolCalls) > 0 {
		resp.StopReason = "tool_use"
	} else if final.Status == "incomplete" {
		resp.StopReason = "max_tokens"
		if final.IncompleteDetails != nil && final.IncompleteDetails.Reason != "" && final.IncompleteDetails.Reason != "max_output_tokens" {
			resp.StopReason = final.IncompleteDetails.Reason
		}
	}
	if u := final.Usage; u != nil {
		cached := u.InputTokensDetails.CachedTokens
		resp.Usage = llmtypes.Usage{
			InputTokens:          u.InputTokens - cached,
			OutputTokens:         u.OutputTokens,
			TotalTokens:          u.InputTokens - cached + u.OutputTokens,
			CacheReadInputTokens: cached,
		}
	}
	return resp, nil
}
