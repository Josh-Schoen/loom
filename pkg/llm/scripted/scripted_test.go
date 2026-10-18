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
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/teradata-labs/loom/pkg/shuttle"
	"github.com/teradata-labs/loom/pkg/types"
)

const reminderScript = `
rules:
  - name: add-reminder
    when:
      user_message: '(?i)remind me (?:to )?(.+)'
      tool_offered: device_add_reminder
    call_tool:
      name: device_add_reminder
      arguments:
        title: '{{.Group 1}}'
        due: '{{dateAt 1 9}}'
  - name: reminder-done
    when:
      after_tool: device_add_reminder
    reply: '{{if .ToolSucceeded}}Saved: {{.ToolResult}}{{else}}Not saved: {{.ToolResult}}{{end}}'
  - name: hello
    when:
      user_message: '(?i)^hello'
    reply: 'Hi there.'
default_reply: 'No rule for: {{.UserMessage}}'
timing:
  first_token_ms: 1
  chunk_delay_ms: 1
`

func mustParse(t *testing.T, src string) *Script {
	t.Helper()
	s, err := Parse([]byte(src))
	require.NoError(t, err)
	return s
}

func offer(names ...string) []shuttle.Tool {
	tools := make([]shuttle.Tool, len(names))
	for i, n := range names {
		tools[i] = &shuttle.MockTool{MockName: n}
	}
	return tools
}

func user(text string) types.Message { return types.Message{Role: "user", Content: text} }

func TestRespond_CallsToolWhenOfferedAndMatched(t *testing.T) {
	s := mustParse(t, reminderScript)
	resp := s.Respond([]types.Message{user("Remind me to buy milk")}, offer("device_add_reminder"))

	require.Len(t, resp.ToolCalls, 1)
	call := resp.ToolCalls[0]
	assert.Equal(t, "device_add_reminder", call.Name)
	assert.True(t, strings.HasPrefix(call.ID, "scripted_"))
	assert.Equal(t, "buy milk", call.Input["title"])
	due, err := time.Parse(time.RFC3339, call.Input["due"].(string))
	require.NoError(t, err)
	assert.Equal(t, 9, due.Hour())
	assert.True(t, due.After(time.Now()))
}

func TestRespond_SkipsToolRuleWhenToolNotOffered(t *testing.T) {
	s := mustParse(t, reminderScript)
	resp := s.Respond([]types.Message{user("remind me to buy milk")}, nil)
	assert.Empty(t, resp.ToolCalls)
	assert.Equal(t, "No rule for: remind me to buy milk", resp.Content)
}

func TestRespond_AfterToolUsesResultAndDoesNotRecall(t *testing.T) {
	s := mustParse(t, reminderScript)
	history := []types.Message{
		user("remind me to buy milk"),
		{Role: "assistant", ToolCalls: []types.ToolCall{{ID: "c1", Name: "device_add_reminder"}}},
	}

	ok := append(append([]types.Message{}, history...), types.Message{
		Role: "tool", ToolUseID: "c1", Content: `{"reminder_id":"r1"}`,
		ToolResult: &shuttle.Result{Success: true},
	})
	resp := s.Respond(ok, offer("device_add_reminder"))
	assert.Empty(t, resp.ToolCalls, "a tool-calling rule must not fire on its own result")
	assert.Equal(t, `Saved: {"reminder_id":"r1"}`, resp.Content)

	declined := append(append([]types.Message{}, history...), types.Message{
		Role: "tool", ToolUseID: "c1", Content: "DECLINED_BY_USER",
		ToolResult: &shuttle.Result{Success: false},
	})
	assert.Equal(t, "Not saved: DECLINED_BY_USER", s.Respond(declined, nil).Content)
}

func TestRespond_FirstMatchingRuleWins(t *testing.T) {
	s := mustParse(t, reminderScript)
	assert.Equal(t, "Hi there.", s.Respond([]types.Message{user("Hello loom")}, nil).Content)
}

func TestRespond_ReadsTextContentBlocks(t *testing.T) {
	s := mustParse(t, reminderScript)
	msg := types.Message{Role: "user", ContentBlocks: []types.ContentBlock{{Type: "text", Text: "hello"}}}
	assert.Equal(t, "Hi there.", s.Respond([]types.Message{msg}, nil).Content)
}

func TestRespond_UnknownGroupRendersEmpty(t *testing.T) {
	s := mustParse(t, `
rules:
  - when: {user_message: 'x'}
    reply: '[{{.Group 3}}]'
`)
	assert.Equal(t, "[]", s.Respond([]types.Message{user("x")}, nil).Content)
}

func TestParse_Validation(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{"empty", `rules: []`, "at least one rule or a default_reply"},
		{"both actions", "rules:\n  - reply: hi\n    call_tool: {name: x}", "exactly one of call_tool and reply"},
		{"no action", "rules:\n  - when: {user_message: x}", "exactly one of call_tool and reply"},
		{"tool without name", "rules:\n  - call_tool: {arguments: {a: b}}", "call_tool.name is required"},
		{"bad regex", "rules:\n  - when: {user_message: '('}\n    reply: hi", "user_message"},
		{"bad template", "rules:\n  - reply: '{{'", "template"},
		{"unknown field", "rules:\n  - replyy: hi", "field replyy not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.src))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestParse_TimingDefaults(t *testing.T) {
	s := mustParse(t, `default_reply: hi`)
	assert.Equal(t, Timing{FirstTokenMs: 300, ChunkChars: 8, ChunkDelayMs: 20}, s.Timing)
}

func TestProvider_StreamsScriptedReply(t *testing.T) {
	provider := NewProvider(mustParse(t, reminderScript), "test-script")
	assert.Equal(t, ProviderName, provider.Name())
	assert.Equal(t, "test-script", provider.Model())

	var chunks []string
	resp, err := provider.ChatStream(context.Background(), []types.Message{user("hello")}, nil, func(c string) {
		chunks = append(chunks, c)
	})
	require.NoError(t, err)
	assert.Equal(t, "Hi there.", resp.Content)
	assert.Equal(t, "Hi there.", strings.Join(chunks, ""))
	assert.Greater(t, len(chunks), 1, "the reply must arrive in several chunks")
	assert.Greater(t, resp.Usage.InputTokens+resp.Usage.OutputTokens, 0)
	assert.Zero(t, resp.Usage.CostUSD)
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.yaml")
	require.NoError(t, os.WriteFile(path, []byte(reminderScript), 0o600))
	s, err := LoadFile(path)
	require.NoError(t, err)
	assert.Len(t, s.Rules, 3)

	_, err = LoadFile(filepath.Join(t.TempDir(), "missing.yaml"))
	assert.Error(t, err)
}

func TestRespond_StructuredArguments(t *testing.T) {
	s := mustParse(t, `
rules:
  - name: make-page
    when:
      user_message: '(?i)plan my (\w+)'
      tool_offered: create_ui_app
    call_tool:
      name: create_ui_app
      arguments:
        name: '{{.Group 1}}-plan'
        overwrite: true
        spec:
          version: "1.0"
          title: 'Plan: {{.Group 1}}'
          components:
            - type: stat-cards
              props:
                items:
                  - {label: Days, value: 7}
            - type: text
              props: {content: 'For {{.Group 1}}'}
`)
	resp := s.Respond([]types.Message{user("Plan my meals")}, offer("create_ui_app"))
	require.Len(t, resp.ToolCalls, 1)
	in := resp.ToolCalls[0].Input
	assert.Equal(t, "meals-plan", in["name"])
	assert.Equal(t, true, in["overwrite"], "non-string scalars keep their type")

	spec, ok := in["spec"].(map[string]any)
	require.True(t, ok, "maps stay maps")
	assert.Equal(t, "Plan: meals", spec["title"], "strings at any depth are templates")
	components, ok := spec["components"].([]any)
	require.True(t, ok)
	require.Len(t, components, 2)
	items := components[0].(map[string]any)["props"].(map[string]any)["items"].([]any)
	assert.Equal(t, 7, items[0].(map[string]any)["value"])
	assert.Equal(t, "For meals", components[1].(map[string]any)["props"].(map[string]any)["content"])
}

func TestParse_BadTemplateInNestedArgument(t *testing.T) {
	_, err := Parse([]byte(`
rules:
  - call_tool:
      name: x
      arguments:
        spec:
          items: ['{{.Nope']
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "arguments.spec.items[0]")
}
