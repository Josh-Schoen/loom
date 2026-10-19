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
	"testing"

	llmtypes "github.com/teradata-labs/loom/pkg/llm/types"
)

// A model that rejects sampling parameters gets a request with no
// temperature field at all; an older model keeps the configured one.
func TestChat_OmitsTemperatureForModelsThatRejectIt(t *testing.T) {
	for _, tc := range []struct {
		model    string
		wantTemp bool
	}{
		{"claude-opus-5-5", false},
		{"claude-sonnet-5", false},
		{"claude-sonnet-4-5-20250929", true},
	} {
		t.Run(tc.model, func(t *testing.T) {
			var gotBody string
			srv := thinkingSrv(t, &gotBody, nativePlainResponse)
			defer srv.Close()
			c := NewClient(Config{APIKey: "k", Model: tc.model, Endpoint: srv.URL, Temperature: 0.2})
			if _, err := c.Chat(context.Background(), []llmtypes.Message{{Role: "user", Content: "hi"}}, nil); err != nil {
				t.Fatalf("Chat: %v", err)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
				t.Fatalf("request body: %v", err)
			}
			if _, has := body["temperature"]; has != tc.wantTemp {
				t.Errorf("temperature in request = %v, want %v (body %s)", has, tc.wantTemp, gotBody)
			}
		})
	}
}
