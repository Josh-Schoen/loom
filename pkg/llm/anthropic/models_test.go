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

package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teradata-labs/loom/pkg/types"
)

func TestAcceptsSamplingParams(t *testing.T) {
	for model, want := range map[string]bool{
		"claude-sonnet-4-5-20250929": true,
		"claude-opus-4-6":            true,
		"claude-haiku-4-5":           true,
		"claude-opus-4-7":            false,
		"claude-opus-4-8":            false,
		"claude-opus-5":              false,
		"claude-opus-5-5":            false,
		"claude-fable-5-1":           false,
		"claude-mythos-5-1":          false,
		"claude-sonnet-5":            false,
		"claude-sonnet-5-5":          false,
		"claude-haiku-5-5":           false,
	} {
		assert.Equal(t, want, acceptsSamplingParams(model), model)
	}
}

func TestApplyModelRules(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		endpoint     string
		wantTemp     float64
		wantFallback string
		wantBetas    string
	}{
		{"older model keeps temperature", "claude-sonnet-4-5-20250929", DefaultAnthropicEndpoint, 0.7, "", "prompt-caching-2024-07-31"},
		{"opus 5.5 on the Claude API", "claude-opus-5-5", DefaultAnthropicEndpoint, 0, "default", "prompt-caching-2024-07-31,server-side-fallback-2026-07-01"},
		{"opus 5.5 through a proxy", "claude-opus-5-5", "https://proxy.example.com/v1/messages", 0, "", "prompt-caching-2024-07-31"},
		{"haiku 5.5: no sampling, no fallback", "claude-haiku-5-5", DefaultAnthropicEndpoint, 0, "", "prompt-caching-2024-07-31"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &MessagesRequest{Model: tt.model, Temperature: 0.7}
			betas := applyModelRules(req, tt.endpoint, true)
			assert.Equal(t, tt.wantTemp, req.Temperature)
			assert.Equal(t, tt.wantFallback, req.Fallbacks)
			assert.Equal(t, tt.wantBetas, betas)
		})
	}
}

// The request a current model receives has no temperature field at all.
func TestChat_OmitsTemperatureForCurrentModels(t *testing.T) {
	var body map[string]json.RawMessage
	var beta string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		beta = r.Header.Get("anthropic-beta")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MessagesResponse{
			ID: "msg_1", Type: "message", Role: "assistant", Model: "claude-opus-5-5", StopReason: "end_turn",
			Content: []ContentBlock{{Type: "text", Text: "Hi"}},
		})
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "k", Endpoint: server.URL, Model: "claude-opus-5-5", Temperature: 1.0})
	_, err := client.Chat(&mockContext{Context: context.Background()}, []types.Message{{Role: "user", Content: "Hello"}}, nil)
	require.NoError(t, err)
	_, hasTemp := body["temperature"]
	assert.False(t, hasTemp, "claude-opus-5-5 rejects temperature")
	_, hasFallbacks := body["fallbacks"]
	assert.False(t, hasFallbacks, "fallbacks only on the Claude API endpoint")
	assert.Equal(t, "prompt-caching-2024-07-31", beta)
}

// fakeAPI answers requests to the Claude API endpoint in-process, recording
// each request body and anthropic-beta header.
type fakeAPI struct {
	bodies []map[string]json.RawMessage
	betas  []string
	answer func(body map[string]json.RawMessage) (int, string)
}

func (f *fakeAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	data, _ := io.ReadAll(r.Body)
	var body map[string]json.RawMessage
	_ = json.Unmarshal(data, &body)
	f.bodies = append(f.bodies, body)
	f.betas = append(f.betas, r.Header.Get("anthropic-beta"))
	code, text := f.answer(body)
	return &http.Response{
		StatusCode: code,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(text)),
		Request:    r,
	}, nil
}

const okReply = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"end_turn","content":[{"type":"text","text":"Hi"}],"usage":{"input_tokens":1,"output_tokens":1}}`

func newFakeAPIClient(api *fakeAPI) *Client {
	client := NewClient(Config{APIKey: "k", Endpoint: DefaultAnthropicEndpoint, Model: "claude-opus-5-5"})
	client.httpClient = &http.Client{Transport: api}
	return client
}

func hello() []types.Message { return []types.Message{{Role: "user", Content: "Hello"}} }

// If the API does not accept the fallback option, the request is sent again
// without it, and later requests leave it out.
func TestChat_RetriesWithoutRejectedFallbacks(t *testing.T) {
	tests := []struct {
		name     string
		rejected string
	}{
		{"unknown field", `{"type":"error","error":{"type":"invalid_request_error","message":"fallbacks: Extra inputs are not permitted"}}`},
		{"unknown beta", `{"type":"error","error":{"type":"invalid_request_error","message":"Unexpected value(s) ` + "`server-side-fallback-2026-07-01`" + ` for the ` + "`anthropic-beta`" + ` header."}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeAPI{answer: func(body map[string]json.RawMessage) (int, string) {
				if _, ok := body["fallbacks"]; ok {
					return http.StatusBadRequest, tt.rejected
				}
				return http.StatusOK, okReply
			}}
			client := newFakeAPIClient(api)
			ctx := &mockContext{Context: context.Background()}

			resp, err := client.Chat(ctx, hello(), nil)
			require.NoError(t, err)
			assert.Equal(t, "Hi", resp.Content)
			require.Len(t, api.bodies, 2, "rejected, then sent again")
			assert.Contains(t, api.betas[0], serverSideFallbackBeta)
			_, retried := api.bodies[1]["fallbacks"]
			assert.False(t, retried)
			assert.Equal(t, promptCachingBeta, api.betas[1])

			_, err = client.Chat(ctx, hello(), nil)
			require.NoError(t, err)
			require.Len(t, api.bodies, 3, "later requests go out once, without the fallback")
			_, again := api.bodies[2]["fallbacks"]
			assert.False(t, again)
		})
	}
}

func TestChat_SendsFallbacksWhenAccepted(t *testing.T) {
	api := &fakeAPI{answer: func(map[string]json.RawMessage) (int, string) { return http.StatusOK, okReply }}
	client := newFakeAPIClient(api)
	_, err := client.Chat(&mockContext{Context: context.Background()}, hello(), nil)
	require.NoError(t, err)
	require.Len(t, api.bodies, 1)
	assert.JSONEq(t, `"default"`, string(api.bodies[0]["fallbacks"]))
	assert.Equal(t, promptCachingBeta+","+serverSideFallbackBeta, api.betas[0])
}

// Other 400s are errors as before: not retried, fallback kept for next time.
func TestChat_OtherBadRequestsAreNotRetried(t *testing.T) {
	api := &fakeAPI{answer: func(map[string]json.RawMessage) (int, string) {
		return http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error","message":"messages: at least one message is required"}}`
	}}
	client := newFakeAPIClient(api)
	_, err := client.Chat(&mockContext{Context: context.Background()}, hello(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one message is required")
	assert.Len(t, api.bodies, 1)
	assert.False(t, client.noFallbacks.Load())
}
