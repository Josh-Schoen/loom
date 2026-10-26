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
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/teradata-labs/loom/pkg/types"
)

// Request context: facts the caller sends with one request that the model
// should know for that turn only (WeaveRequest.context), such as a phone's
// local time and time zone. The agent adds them as a trailing system note to
// every LLM call of the turn; they are never added to the session, persisted,
// or returned in conversation history.

// Limits on request context, so a caller cannot grow the prompt without bound.
const (
	maxRequestContextEntries = 16
	maxRequestContextKey     = 64
	maxRequestContextValue   = 256
)

type requestContextKey struct{}

// WithRequestContext returns a context carrying request facts for the agent
// to show the model during this turn. Empty keys and values are dropped; keys
// and values are cut to their limits and to one line; at most 16 entries are
// kept (in key order).
func WithRequestContext(ctx context.Context, facts map[string]string) context.Context {
	if len(facts) == 0 {
		return ctx
	}
	return context.WithValue(ctx, requestContextKey{}, requestContextNote(facts))
}

// requestContextNoteFromContext returns the system note for the request
// context in ctx, or "" when there is none.
func requestContextNoteFromContext(ctx context.Context) string {
	note, _ := ctx.Value(requestContextKey{}).(string)
	return note
}

// requestContextNote renders request facts as the system note the model sees.
func requestContextNote(facts map[string]string) string {
	keys := make([]string, 0, len(facts))
	for k, v := range facts {
		if oneLine(k, maxRequestContextKey) != "" && oneLine(v, maxRequestContextValue) != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	if len(keys) > maxRequestContextEntries {
		keys = keys[:maxRequestContextEntries]
	}
	var sb strings.Builder
	sb.WriteString("Context sent with this message (applies to this message only):\n")
	for _, k := range keys {
		sb.WriteString("- ")
		sb.WriteString(oneLine(k, maxRequestContextKey))
		sb.WriteString(": ")
		sb.WriteString(oneLine(facts[k], maxRequestContextValue))
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// oneLine trims s, replaces line breaks with spaces, and cuts it to max runes.
func oneLine(s string, max int) string {
	s = strings.TrimSpace(strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s))
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

// withTransientTail returns msgs with this call's transient notes, the
// request context (WeaveRequest.context, e.g. the caller's local time) and
// the turn's soft reminder, appended as one trailing user message wrapped in
// <system-reminder> tags (a copy; msgs is not changed), or msgs itself when
// there are none.
//
// USER role, never system: providers fold system-role messages into the
// system prompt at the HEAD of the request (Anthropic's "system" field),
// where a note that changes with every request would invalidate every
// cached segment after it. A trailing user message stays in place, after
// the last cache breakpoint. The tag marks it as harness state, not a user
// ask.
func withTransientTail(ctx context.Context, msgs []Message, softReminder string) []Message {
	var parts []string
	if note := requestContextNoteFromContext(ctx); note != "" {
		parts = append(parts, note)
	}
	if r := strings.TrimSpace(softReminder); r != "" {
		parts = append(parts, r)
	}
	if len(parts) == 0 {
		return msgs
	}
	out := make([]Message, len(msgs), len(msgs)+1)
	copy(out, msgs)
	return append(out, Message{Role: "user",
		Content: types.SystemReminderOpen + "\n" + strings.Join(parts, "\n\n") + "\n" + types.SystemReminderClose})
}
