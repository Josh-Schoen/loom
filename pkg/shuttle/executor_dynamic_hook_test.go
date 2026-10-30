// Copyright 2026 Josh Schoen
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

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
)

type hookTestRegistry struct{ tool *loomv1.IndexedTool }

func (r hookTestRegistry) Search(context.Context, *loomv1.SearchToolsRequest) (*loomv1.SearchToolsResponse, error) {
	return &loomv1.SearchToolsResponse{Results: []*loomv1.ToolSearchResult{{Tool: r.tool}}}, nil
}

type hookTestMCPManager struct{}

func (hookTestMCPManager) GetClient(string) (interface{}, error) { return hookTestMCPClient{}, nil }

type hookTestMCPClient struct{}

func (hookTestMCPClient) CallTool(context.Context, string, map[string]interface{}) (interface{}, error) {
	return map[string]interface{}{"ok": true}, nil
}

type hookTestCtxKey struct{}

// The hook runs once per registry-resolved registration, with the resolving
// call's context, before the tool is visible in the shared registry — so a
// host can scope it to that call's session with no window in which another
// session's projection sees it unscoped.
func TestExecutor_DynamicRegistrationHook_RunsBeforeRegister(t *testing.T) {
	reg := NewRegistry()
	e := NewExecutor(reg)
	e.SetToolRegistry(hookTestRegistry{tool: &loomv1.IndexedTool{
		Name: "gh_list_issue", Source: loomv1.ToolSource_TOOL_SOURCE_MCP,
		McpServer: "github", InputSchema: `{"type":"object"}`,
	}})
	e.SetMCPManager(hookTestMCPManager{})

	var calls int
	var sawValue interface{}
	var registeredAtHook bool
	e.SetDynamicRegistrationHook(func(ctx context.Context, tool Tool) {
		calls++
		sawValue = ctx.Value(hookTestCtxKey{})
		registeredAtHook = reg.IsRegistered(tool.Name())
	})

	ctx := context.WithValue(context.Background(), hookTestCtxKey{}, "sess-a")
	res, err := e.Execute(ctx, "gh_list_issue", map[string]interface{}{})
	require.NoError(t, err)
	require.True(t, res.Success)

	assert.Equal(t, 1, calls)
	assert.Equal(t, "sess-a", sawValue, "hook must receive the resolving call's context")
	assert.False(t, registeredAtHook, "hook must run before the tool enters the shared registry")
	assert.True(t, reg.IsRegistered("gh_list_issue"))

	// A second call finds the registered tool: no new registration, no hook.
	_, err = e.Execute(ctx, "gh_list_issue", map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}
