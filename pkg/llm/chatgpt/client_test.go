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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmtypes "github.com/teradata-labs/loom/pkg/llm/types"
	"github.com/teradata-labs/loom/pkg/shuttle"
	"github.com/teradata-labs/loom/pkg/types"
)

// staticTokens hands out a fixed token, counting invalidations.
type staticTokens struct {
	mu          sync.Mutex
	tokens      []string
	invalidated int
}

func (s *staticTokens) Token(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[min(s.invalidated, len(s.tokens)-1)], nil
}

func (s *staticTokens) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidated++
}

func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, "event: x\ndata: %s\n\n", e)
	}
	return b.String()
}

type weatherTool struct{}

func (weatherTool) Name() string        { return "weather:get" }
func (weatherTool) Description() string { return "Weather for a city" }
func (weatherTool) Backend() string     { return "" }
func (weatherTool) InputSchema() *shuttle.JSONSchema {
	return &shuttle.JSONSchema{Type: "object", Properties: map[string]*shuttle.JSONSchema{"city": {Type: "string"}}, Required: []string{"city"}}
}
func (weatherTool) Execute(context.Context, map[string]interface{}) (*shuttle.Result, error) {
	return &shuttle.Result{Success: true}, nil
}

func TestChatStreamRequestAndAnswer(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/responses", r.URL.Path)
		auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &got))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(
			`{"type":"response.created","response":{"status":"in_progress"}}`,
			`{"type":"response.output_text.delta","delta":"Hel"}`,
			`{"type":"response.output_text.delta","delta":"lo"}`,
			`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Hello"}]}],"usage":{"input_tokens":120,"output_tokens":5,"total_tokens":125,"input_tokens_details":{"cached_tokens":100}}}}`,
		))
	}))
	defer srv.Close()

	c, err := NewClient(Config{Model: "gpt-test", Tokens: &staticTokens{tokens: []string{"tok-1"}}, BaseURL: srv.URL + "/v1"})
	require.NoError(t, err)
	var streamed strings.Builder
	resp, err := c.ChatStream(context.Background(), []llmtypes.Message{
		{Role: "system", Content: "Answer briefly."},
		{Role: "user", Content: "What's the weather?"},
		{Role: "assistant", ToolCalls: []llmtypes.ToolCall{{ID: "call_1", Name: "weather:get", Input: map[string]interface{}{"city": "Austin"}}}},
		{Role: "tool", ToolUseID: "call_1", Content: `{"temp":30}`},
		{Role: "assistant", ToolCalls: []llmtypes.ToolCall{{ID: "call_orphan", Name: "weather:get"}}},
		{Role: "user", Content: "Thanks"},
	}, []shuttle.Tool{weatherTool{}}, func(s string) { streamed.WriteString(s) })
	require.NoError(t, err)

	assert.Equal(t, "Bearer tok-1", auth)
	assert.Equal(t, "Hello", resp.Content)
	assert.Equal(t, "Hello", streamed.String())
	assert.Equal(t, "end_turn", resp.StopReason)
	assert.Equal(t, 20, resp.Usage.InputTokens, "cache reads are split out")
	assert.Equal(t, 100, resp.Usage.CacheReadInputTokens)
	assert.Equal(t, 25, resp.Usage.TotalTokens)
	assert.Zero(t, resp.Usage.CostUSD, "plan usage is not billed per call")

	assert.Equal(t, "gpt-test", got["model"])
	assert.Equal(t, true, got["stream"])
	assert.Equal(t, false, got["store"])
	assert.Equal(t, "Answer briefly.", got["instructions"], "system messages become instructions")
	for _, rejected := range []string{"temperature", "top_p", "max_output_tokens", "previous_response_id", "metadata", "user"} {
		assert.NotContains(t, got, rejected)
	}
	input := got["input"].([]any)
	var kinds []string
	for _, it := range input {
		m := it.(map[string]any)
		kind := m["type"].(string)
		if r, ok := m["role"].(string); ok {
			kind += ":" + r
		}
		kinds = append(kinds, kind)
		assert.NotEqual(t, "system", m["role"], "no system-role items")
	}
	assert.Equal(t, []string{"message:user", "function_call", "function_call_output", "message:user"}, kinds,
		"a call without its result is dropped")
	tools := got["tools"].([]any)
	require.Len(t, tools, 1)
	tool := tools[0].(map[string]any)
	assert.Equal(t, "function", tool["type"])
	assert.Equal(t, "weather_get", tool["name"], "names are sanitized")
}

func TestChatStreamToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sse(
			`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_9","name":"weather_get","arguments":"{\"city\":\"Paris\"}"}}`,
			`{"type":"response.completed","response":{"status":"completed","output":[]}}`,
		))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Model: "m", Tokens: &staticTokens{tokens: []string{"t"}}, BaseURL: srv.URL})
	require.NoError(t, err)
	resp, err := c.Chat(context.Background(), []llmtypes.Message{{Role: "user", Content: "Paris?"}}, []shuttle.Tool{weatherTool{}})
	require.NoError(t, err)
	assert.Equal(t, "tool_use", resp.StopReason)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "weather:get", resp.ToolCalls[0].Name, "the original tool name is restored")
	assert.Equal(t, "call_9", resp.ToolCalls[0].ID)
	assert.Equal(t, "Paris", resp.ToolCalls[0].Input["city"])
}

func TestChatStreamErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		is     error
	}{
		{"usage limit before the stream", 429, `{"error":{"code":"subscription_sharing_usage_limit_exceeded","message":"The ChatGPT user has reached their Subscription Sharing usage limit."}}`, ErrUsageLimit},
		{"usage limit inside the stream", 200, sse(`{"type":"response.failed","response":{"status":"failed","error":{"code":"subscription_sharing_usage_limit_exceeded","message":"limit"}}}`), ErrUsageLimit},
		{"not eligible", 403, `{"error":{"code":"subscription_sharing_user_not_eligible","message":"no"}}`, ErrNotEligible},
		{"scope", 403, `{"error":{"code":"chatpass_v2_scope_not_authorized","message":"no"}}`, ErrSignInAgain},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			c, err := NewClient(Config{Model: "m", Tokens: &staticTokens{tokens: []string{"t"}}, BaseURL: srv.URL})
			require.NoError(t, err)
			_, err = c.Chat(context.Background(), []llmtypes.Message{{Role: "user", Content: "hi"}}, nil)
			assert.ErrorIs(t, err, tc.is)
		})
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sse(`{"type":"response.output_text.delta","delta":"cut"}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Model: "m", Tokens: &staticTokens{tokens: []string{"t"}}, BaseURL: srv.URL})
	require.NoError(t, err)
	_, err = c.Chat(context.Background(), []llmtypes.Message{{Role: "user", Content: "hi"}}, nil)
	assert.ErrorContains(t, err, "before the response completed", "only response.completed is an answer")
}

func TestChatRefreshesOnceOn401(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") == "Bearer old" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"detail":"token expired"}`)
			return
		}
		_, _ = io.WriteString(w, sse(`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}}`))
	}))
	defer srv.Close()
	tokens := &staticTokens{tokens: []string{"old", "new"}}
	c, err := NewClient(Config{Model: "m", Tokens: tokens, BaseURL: srv.URL})
	require.NoError(t, err)
	resp, err := c.Chat(context.Background(), []llmtypes.Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
	assert.Equal(t, []string{"Bearer old", "Bearer new"}, seen)
	assert.Equal(t, 1, tokens.invalidated)
}

// A plan answers only the person who signed in: another user's request is
// refused before anything is sent.
func TestChatRefusesOtherUsers(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, sse(`{"type":"response.completed","response":{"status":"completed","output":[]}}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Model: "m", Tokens: &staticTokens{tokens: []string{"t"}}, BaseURL: srv.URL, UserID: "sam"})
	require.NoError(t, err)
	msgs := []llmtypes.Message{{Role: "user", Content: "hi"}}

	_, err = c.Chat(types.ContextWithUserID(context.Background(), "ana"), msgs, nil)
	assert.ErrorIs(t, err, ErrOtherUser)
	_, err = c.Chat(context.Background(), msgs, nil)
	assert.ErrorIs(t, err, ErrOtherUser, "a request for nobody in particular is not the plan's user either")
	assert.Zero(t, calls)

	_, err = c.Chat(types.ContextWithUserID(context.Background(), "sam"), msgs, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/models", r.URL.Path)
		require.Equal(t, "Bearer t", r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"models":[{"slug":"gpt-a","display_name":"A","visibility":"list"},{"slug":"gpt-hidden","visibility":"hide"}]}`)
	}))
	defer srv.Close()
	models, err := ListModels(context.Background(), &staticTokens{tokens: []string{"t"}}, srv.URL, nil)
	require.NoError(t, err)
	assert.Equal(t, []Model{{Slug: "gpt-a", DisplayName: "A", Visibility: "list"}}, models)
}

func TestCostsNothing(t *testing.T) {
	c, err := NewClient(Config{Model: "m", Tokens: &staticTokens{tokens: []string{"t"}}})
	require.NoError(t, err)
	assert.True(t, c.CostsNothing())
	assert.Equal(t, "chatgpt", c.Name())
	_, err = NewClient(Config{Tokens: &staticTokens{tokens: []string{"t"}}})
	assert.Error(t, err)
}
