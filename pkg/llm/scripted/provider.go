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
package scripted

import (
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"

	"github.com/teradata-labs/loom/pkg/llm/loadtest"
	"github.com/teradata-labs/loom/pkg/shuttle"
	"github.com/teradata-labs/loom/pkg/types"
)

// ProviderName is the llm.provider value that selects this provider.
const ProviderName = "scripted"

// NewProvider returns a streaming provider that answers from script. Streaming
// and call accounting are delegated to the loadtest mock provider; this
// package only decides what the reply is.
func NewProvider(script *Script, model string) types.StreamingLLMProvider {
	return loadtest.NewProvider(loadtest.ProviderConfig{
		Name:             ProviderName,
		Model:            model,
		BaseLatency:      time.Duration(script.Timing.FirstTokenMs) * time.Millisecond,
		StreamChunkSize:  script.Timing.ChunkChars,
		StreamChunkDelay: time.Duration(script.Timing.ChunkDelayMs) * time.Millisecond,
		ResponseFunc:     script.Respond,
	})
}

// TemplateData is what templates see.
type TemplateData struct {
	// UserMessage is the newest user message.
	UserMessage string
	// ToolName is the tool whose result is the newest message ("" otherwise).
	ToolName string
	// ToolResult is that result's content as the model would read it.
	ToolResult string
	// ToolSucceeded reports whether that tool call succeeded.
	ToolSucceeded bool

	groups []string
}

// Group returns capture group n of the matched user_message, or "" when the
// rule has no such group.
func (d TemplateData) Group(n int) string {
	if n < 0 || n >= len(d.groups) {
		return ""
	}
	return d.groups[n]
}

// Respond picks the first matching rule and renders its reply or tool call.
func (s *Script) Respond(messages []types.Message, tools []shuttle.Tool) *types.LLMResponse {
	data, newestIsTool := conversationState(messages)

	offered := make(map[string]bool, len(tools))
	for _, t := range tools {
		offered[t.Name()] = true
	}

	for i := range s.Rules {
		r := &s.Rules[i]
		if r.When.AfterTool != "" {
			if !newestIsTool || data.ToolName != r.When.AfterTool {
				continue
			}
		} else if newestIsTool {
			continue
		}
		if r.When.ToolOffered != "" && !offered[r.When.ToolOffered] {
			continue
		}
		ruleData := data
		if r.userMessage != nil {
			groups := r.userMessage.FindStringSubmatch(data.UserMessage)
			if groups == nil {
				continue
			}
			ruleData.groups = groups
		}
		return r.respond(ruleData, messages)
	}
	return textResponse(render(s.defaultReply, data), messages)
}

func (r *Rule) respond(data TemplateData, messages []types.Message) *types.LLMResponse {
	if r.reply != nil {
		return textResponse(render(r.reply, data), messages)
	}
	input := make(map[string]interface{}, len(r.CallTool.arguments))
	for key, node := range r.CallTool.arguments {
		input[key] = renderArg(node, data)
	}
	return &types.LLMResponse{
		ToolCalls: []types.ToolCall{{
			ID:    "scripted_" + uuid.NewString(),
			Name:  r.CallTool.Name,
			Input: input,
		}},
		StopReason: "tool_use",
		Usage:      estimateUsage(messages, ""),
	}
}

// conversationState extracts the newest user message and, when the newest
// message is a tool result, which tool produced it.
func conversationState(messages []types.Message) (TemplateData, bool) {
	var data TemplateData
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			data.UserMessage = messageText(messages[i])
			break
		}
	}
	// Trailing system messages are notes for this call (request context, soft
	// reminders), not turns: the newest turn is the last message before them.
	last := len(messages) - 1
	for last >= 0 && messages[last].Role == "system" {
		last--
	}
	if last < 0 || messages[last].Role != "tool" {
		return data, false
	}

	newest := messages[last]
	data.ToolResult = messageText(newest)
	data.ToolSucceeded = newest.ToolResult == nil || newest.ToolResult.Success
	for i := last - 1; i >= 0 && data.ToolName == ""; i-- {
		for _, call := range messages[i].ToolCalls {
			if call.ID == newest.ToolUseID {
				data.ToolName = call.Name
				break
			}
		}
	}
	return data, true
}

func messageText(m types.Message) string {
	if m.Content != "" || len(m.ContentBlocks) == 0 {
		return m.Content
	}
	var b strings.Builder
	for _, block := range m.ContentBlocks {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

// render executes a template; a failure is rendered into the text so a
// script mistake is visible in the conversation rather than silently empty.
// renderArg renders a compiled argument value, recursing into lists and maps.
func renderArg(node argNode, data TemplateData) any {
	switch node.kind {
	case argTemplate:
		return render(node.tmpl, data)
	case argList:
		out := make([]any, 0, len(node.list))
		for _, child := range node.list {
			out = append(out, renderArg(child, data))
		}
		return out
	case argMap:
		out := make(map[string]any, len(node.fields))
		for key, child := range node.fields {
			out[key] = renderArg(child, data)
		}
		return out
	default:
		return node.literal
	}
}

func render(t *template.Template, data TemplateData) string {
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "[scripted provider template error: " + err.Error() + "]"
	}
	return b.String()
}

func textResponse(text string, messages []types.Message) *types.LLMResponse {
	return &types.LLMResponse{
		Content:    text,
		StopReason: "end_turn",
		Usage:      estimateUsage(messages, text),
	}
}

// estimateUsage reports approximate token counts (characters / 4) so cost and
// context displays have plausible, non-zero values. Cost is always zero.
func estimateUsage(messages []types.Message, output string) types.Usage {
	in := 0
	for _, m := range messages {
		in += len(messageText(m)) / 4
	}
	out := len(output) / 4
	return types.Usage{InputTokens: in, OutputTokens: out, TotalTokens: in + out}
}
