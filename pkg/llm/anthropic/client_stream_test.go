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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/teradata-labs/loom/pkg/llm/types"
)

// streamEvents writes an SSE reply: text deltas every gap, then the end.
func streamEvents(w http.ResponseWriter, words []string, gap time.Duration) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher := w.(http.Flusher)
	send := func(event, data string) {
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}
	send("message_start", `{"type":"message_start","message":{"usage":{"input_tokens":5}}}`)
	send("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	for _, word := range words {
		time.Sleep(gap)
		send("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, word))
	}
	send("content_block_stop", `{"type":"content_block_stop","index":0}`)
	send("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`)
	send("message_stop", `{"type":"message_stop"}`)
}

// A reply that takes longer than the HTTP timeout still finishes while
// events keep arriving (for example a large file written as one tool call).
func TestChatStream_LongReplyOutlastsTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streamEvents(w, []string{"one ", "two ", "three ", "four ", "five"}, 80*time.Millisecond)
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test-key", Endpoint: server.URL, Timeout: 150 * time.Millisecond})
	client.streamIdle = 300 * time.Millisecond
	resp, err := client.ChatStream(context.Background(), []types.Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("a long reply that keeps sending must finish, got %v", err)
	}
	if resp.Content != "one two three four five" {
		t.Errorf("content = %q", resp.Content)
	}
}

// A reply that goes silent is given up on, with a plain reason.
func TestChatStream_SilenceEndsIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streamEvents(w, []string{"one ", "then nothing"}, 400*time.Millisecond)
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test-key", Endpoint: server.URL})
	client.streamIdle = 150 * time.Millisecond
	start := time.Now()
	_, err := client.ChatStream(context.Background(), []types.Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "sent nothing for") {
		t.Fatalf("expected the silence error, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("gave up too late: %s", time.Since(start))
	}
}
