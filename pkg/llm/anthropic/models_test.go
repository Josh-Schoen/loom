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
			betas := applyModelRules(req, tt.endpoint)
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
