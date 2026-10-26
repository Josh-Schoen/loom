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

package types

import "strings"

// A system reminder is a transient note for one LLM call (request context,
// the turn's soft reminder), sent as a trailing user-role message wrapped in
// these tags rather than as a system message, so providers that move system
// messages to the head of the request do not invalidate the prompt cache.
const (
	SystemReminderOpen  = "<system-reminder>"
	SystemReminderClose = "</system-reminder>"
)

// IsSystemReminder reports whether m is such a note rather than a turn.
func IsSystemReminder(m Message) bool {
	return m.Role == "user" && len(m.ContentBlocks) == 0 &&
		strings.HasPrefix(strings.TrimSpace(m.Content), SystemReminderOpen)
}
