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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallScopedTools_SortedAndEmptyByDefault(t *testing.T) {
	assert.Nil(t, CallScopedTools(context.Background()))
	assert.Equal(t, context.Background(), ContextWithCallScopedTools(context.Background(), nil),
		"an empty set must not wrap the context")

	ctx := ContextWithCallScopedTools(context.Background(), []Tool{
		&MockTool{MockName: "zeta"}, &MockTool{MockName: "alpha"},
	})
	tools := CallScopedTools(ctx)
	require.Len(t, tools, 2)
	assert.Equal(t, "alpha", tools[0].Name())
	assert.Equal(t, "zeta", tools[1].Name())
}

func TestExecutor_ResolvesCallScopedToolsFromContextOnly(t *testing.T) {
	executor := NewExecutor(NewRegistry())
	scoped := &MockTool{
		MockName: "client_tool",
		MockExecute: func(ctx context.Context, params map[string]interface{}) (*Result, error) {
			return &Result{Success: true, Data: ToolCallIDFromContext(ctx)}, nil
		},
	}
	ctx := ContextWithCallScopedTools(context.Background(), []Tool{scoped})
	ctx = context.WithValue(ctx, ToolCallIDContextKey(), "call-42")

	res, err := executor.Execute(ctx, "client_tool", map[string]interface{}{})
	require.NoError(t, err)
	require.True(t, res.Success)
	assert.Equal(t, "call-42", res.Data, "the tool must see the loop's tool-call id")
	assert.Equal(t, 1, scoped.ExecuteCount)

	_, err = executor.Execute(context.Background(), "client_tool", map[string]interface{}{})
	require.Error(t, err, "a call without the scoped set must not resolve the tool")
	assert.Contains(t, err.Error(), "tool not found")
}

func TestToolCallIDFromContext_EmptyWhenUnset(t *testing.T) {
	assert.Equal(t, "", ToolCallIDFromContext(context.Background()))
}
