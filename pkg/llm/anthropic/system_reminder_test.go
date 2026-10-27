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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmtypes "github.com/teradata-labs/loom/pkg/llm/types"
)

// A per-call note (request context, soft reminder) sent as a trailing
// <system-reminder> user message stays out of the system field and after
// every cache breakpoint, so a note that changes on every request does not
// invalidate the cached system prompt or conversation.
func TestSystemReminderStaysAfterTheCachedPrefix(t *testing.T) {
	c := &Client{model: "claude-haiku-5-5"}
	note := "<system-reminder>\nContext sent with this message (applies to this message only):\n- local_time: 2026-10-26T08:00:00-05:00\n</system-reminder>"
	system, msgs := c.convertMessages([]llmtypes.Message{
		{Role: "system", Content: "You help one household.", CacheBreakpoint: true},
		{Role: "user", Content: "Plan dinner"},
		{Role: "assistant", Content: "Tacos?", CacheBreakpoint: true},
		{Role: "user", Content: "Yes, for four"},
		{Role: "user", Content: note},
	})

	require.Len(t, system, 1, "the note is not a system block")
	assert.Equal(t, "You help one household.", system[0].Text)

	last := msgs[len(msgs)-1]
	require.Equal(t, "user", last.Role)
	tail := last.Content[len(last.Content)-1]
	assert.Equal(t, note, tail.Text, "the note is the request's last content")
	assert.Nil(t, tail.CacheControl)

	marked := 0
	for i, m := range msgs {
		for _, b := range m.Content {
			if b.CacheControl != nil {
				marked++
				assert.Less(t, i, len(msgs)-1, "every breakpoint comes before the note's message")
			}
		}
	}
	assert.Equal(t, 1, marked, "the conversation's breakpoint survives")
}

// The graph memory recall note changes with every user message. It rides the
// same trailing <system-reminder> note, so it never reaches the system field
// and lands after the system prompt's and the conversation's breakpoints.
func TestGraphMemoryRecallNoteStaysOutOfTheSystemField(t *testing.T) {
	c := &Client{model: "claude-haiku-5-5"}
	note := "<system-reminder>\n[Graph Memory Context]\nRelevant memories from past conversations:\n\n- [2026-10-20] User adopted a dog named Max\n</system-reminder>"
	system, msgs := c.convertMessages([]llmtypes.Message{
		{Role: "system", Content: "You help one household.", CacheBreakpoint: true},
		{Role: "user", Content: "I adopted a dog"},
		{Role: "assistant", Content: "Congratulations!", CacheBreakpoint: true},
		{Role: "user", Content: "What is my dog's name?"},
		{Role: "user", Content: note},
	})

	require.Len(t, system, 1)
	assert.NotNil(t, system[0].CacheControl, "the system prompt keeps its breakpoint")
	for _, b := range system {
		assert.NotContains(t, b.Text, "[Graph Memory Context]", "the recall note is not in the system field")
	}

	// Find the note and every breakpoint by position in the conversation.
	notePos, pos, lastMark := -1, 0, -1
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.CacheControl != nil {
				lastMark = pos
			}
			if b.Text == note {
				notePos = pos
				assert.Nil(t, b.CacheControl, "the note itself is not cached")
			}
			pos++
		}
	}
	require.GreaterOrEqual(t, notePos, 0, "the note is sent")
	require.GreaterOrEqual(t, lastMark, 0, "the conversation's breakpoint survives")
	assert.Equal(t, pos-1, notePos, "the note is the request's last content")
	assert.Less(t, lastMark, notePos, "the note comes after every breakpoint")
}
