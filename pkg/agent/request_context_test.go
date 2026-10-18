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

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmtypes "github.com/teradata-labs/loom/pkg/llm/types"
)

func TestRequestContextNote(t *testing.T) {
	tests := []struct {
		name  string
		facts map[string]string
		want  string
	}{
		{"none", nil, ""},
		{"only blanks", map[string]string{"": "x", "k": "  "}, ""},
		{
			"sorted, one line each",
			map[string]string{"time_zone": "America/Chicago", "local_time": "2026-10-18T07:05:00-05:00"},
			"Context sent with this message (applies to this message only):\n- local_time: 2026-10-18T07:05:00-05:00\n- time_zone: America/Chicago",
		},
		{
			"line breaks flattened",
			map[string]string{"note": "first\nsecond\r\nthird"},
			"Context sent with this message (applies to this message only):\n- note: first second third",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, requestContextNote(tt.facts))
		})
	}
}

func TestRequestContextNoteLimits(t *testing.T) {
	facts := map[string]string{}
	for i := 0; i < 20; i++ {
		facts[string(rune('a'+i))] = strings.Repeat("é", 300)
	}
	facts[strings.Repeat("k", 100)] = "v"
	note := requestContextNote(facts)
	lines := strings.Split(note, "\n")[1:]
	assert.Len(t, lines, maxRequestContextEntries)
	for _, line := range lines {
		assert.LessOrEqual(t, len([]rune(line)), len("- ")+maxRequestContextKey+len(": ")+maxRequestContextValue)
	}
}

// The note reaches every LLM call of the turn, after the conversation, and
// is never added to the session (so it is not stored or shown as history).
func TestRequestContextReachesTheModelOnly(t *testing.T) {
	llm := &capturingLLM{response: "It's Sunday."}
	ag := contentBlocksTestAgent(llm)
	ctx := WithRequestContext(context.Background(), map[string]string{
		"local_time": "2026-10-18T07:05:00-05:00",
		"time_zone":  "America/Chicago",
	})

	_, err := ag.Chat(ctx, "rc-session", "What day is it?")
	require.NoError(t, err)

	sent := llm.lastMessages()
	require.NotEmpty(t, sent)
	last := sent[len(sent)-1]
	assert.Equal(t, "system", last.Role)
	assert.Contains(t, last.Content, "local_time: 2026-10-18T07:05:00-05:00")
	assert.Contains(t, last.Content, "time_zone: America/Chicago")

	session, ok := ag.GetSession("rc-session")
	require.True(t, ok)
	for _, m := range session.GetMessages() {
		assert.NotContains(t, m.Content, "local_time", "request context must not enter the session")
	}

	// A later message without context carries no note.
	_, err = ag.Chat(context.Background(), "rc-session", "Thanks")
	require.NoError(t, err)
	for _, m := range llm.lastMessages() {
		assert.NotContains(t, m.Content, "local_time")
	}
}

func TestWithRequestContextLeavesMessagesAlone(t *testing.T) {
	msgs := []llmtypes.Message{{Role: "user", Content: "hi"}}
	assert.Equal(t, msgs, withRequestContext(context.Background(), msgs), "no context: unchanged")

	ctx := WithRequestContext(context.Background(), map[string]string{"local_time": "now"})
	out := withRequestContext(ctx, msgs)
	assert.Len(t, out, 2)
	assert.Len(t, msgs, 1, "the input slice is not changed")
}
