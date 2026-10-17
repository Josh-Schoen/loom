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
package shuttle

import (
	"context"
	"sort"
)

// callScopedToolsKey is the context key for tools that exist only for one
// call (e.g. the client-executed tools a StreamWeave request declares).
type callScopedToolsKey struct{}

// toolCallIDKey is the context key carrying the LLM tool-call id of the call
// a tool is executing for.
type toolCallIDKey struct{}

// ContextWithCallScopedTools returns ctx carrying tools that are advertised to
// the model and resolved by the executor for this call only. They never enter
// the agent's shared Registry, so one caller's tools cannot reach another
// session — and two callers may declare different tools under the same name.
// A later call on the same ctx chain replaces, not merges, the set.
func ContextWithCallScopedTools(ctx context.Context, tools []Tool) context.Context {
	if len(tools) == 0 {
		return ctx
	}
	byName := make(map[string]Tool, len(tools))
	for _, t := range tools {
		if t != nil {
			byName[t.Name()] = t
		}
	}
	return context.WithValue(ctx, callScopedToolsKey{}, byName)
}

// CallScopedTools returns the call-scoped tools carried by ctx, name-sorted so
// the advertised order is deterministic. Returns nil when there are none.
func CallScopedTools(ctx context.Context) []Tool {
	byName, _ := ctx.Value(callScopedToolsKey{}).(map[string]Tool)
	if len(byName) == 0 {
		return nil
	}
	out := make([]Tool, 0, len(byName))
	for _, t := range byName {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// callScopedTool returns the call-scoped tool named name, if ctx carries one.
func callScopedTool(ctx context.Context, name string) (Tool, bool) {
	byName, _ := ctx.Value(callScopedToolsKey{}).(map[string]Tool)
	t, ok := byName[name]
	return t, ok
}

// ToolCallIDContextKey is the context key under which the conversation loop
// stores the LLM tool-call id a tool is executing for, so a tool that hands its
// call to another party (a client tool) can correlate it with the tool
// lifecycle progress events. Exposed as a key rather than a WithValue helper
// because the loop wraps its own Context type and must keep that interface.
func ToolCallIDContextKey() any { return toolCallIDKey{} }

// ToolCallIDFromContext returns the tool-call id stored under
// ToolCallIDContextKey, or "" when none is set.
func ToolCallIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(toolCallIDKey{}).(string)
	return id
}
