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
package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/observability"
	"github.com/teradata-labs/loom/pkg/session"
)

// newDynamicScopeAgent builds an Agent through NewAgent (so the executor's
// dynamic-registration hook is installed) whose executor resolves gh_list_issue
// from a stub tool registry, as tool_search discovery does.
func newDynamicScopeAgent(t *testing.T) *Agent {
	t.Helper()
	cfg := DefaultConfig()
	cfg.PatternConfig = DefaultPatternConfig()
	cfg.PatternConfig.UseLLMClassifier = false
	a := NewAgent(&mockBackend{}, &mockLLMProvider{}, WithConfig(cfg))
	a.executor.SetToolRegistry(&stubToolRegistry{resp: &loomv1.SearchToolsResponse{
		Results: []*loomv1.ToolSearchResult{{Tool: &loomv1.IndexedTool{
			Name: "gh_list_issue", Description: "List issues",
			Source: loomv1.ToolSource_TOOL_SOURCE_MCP, McpServer: "github",
			InputSchema: `{"type":"object"}`,
		}}},
	}})
	a.executor.SetMCPManager(toolbenchMCPManager{})
	a.captureBaseTools()
	return a
}

func execInSession(t *testing.T, a *Agent, sessionID, tool string) {
	t.Helper()
	ctx := &agentContext{
		Context: session.WithSessionID(context.Background(), sessionID),
		session: &Session{ID: sessionID},
		tracer:  observability.NewNoOpTracer(),
	}
	res, err := a.executeToolWithSelfCorrection(ctx, tool, map[string]interface{}{}, sessionID)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.Success)
}

// A tool resolved from the registry on one session's call is advertised to
// that session only, and leaves with it.
func TestDynamicTool_ScopedToResolvingSession(t *testing.T) {
	a := newDynamicScopeAgent(t)
	sA, sB := &Session{ID: "sess-a"}, &Session{ID: "sess-b"}

	execInSession(t, a, sA.ID, "gh_list_issue")

	assert.Contains(t, advertisedNames(a, sA), "gh_list_issue")
	assert.NotContains(t, advertisedNames(a, sB), "gh_list_issue",
		"a tool session A loaded must not be advertised to session B")

	a.DeleteSession(sA.ID)
	assert.NotContains(t, advertisedNames(a, sA), "gh_list_issue",
		"a deleted session's loaded tools are no longer advertised")
	assert.True(t, a.tools.IsRegistered("gh_list_issue"),
		"the definition stays registered for reuse")
}

// A second session that calls a tool another session already loaded adopts
// it: no new registration happens, but the tool is now advertised to it.
func TestDynamicTool_SecondSessionAdoptsOnUse(t *testing.T) {
	a := newDynamicScopeAgent(t)
	sA, sB := &Session{ID: "sess-a"}, &Session{ID: "sess-b"}

	execInSession(t, a, sA.ID, "gh_list_issue")
	execInSession(t, a, sB.ID, "gh_list_issue")

	assert.Contains(t, advertisedNames(a, sA), "gh_list_issue")
	assert.Contains(t, advertisedNames(a, sB), "gh_list_issue")
	assert.NotContains(t, advertisedNames(a, &Session{ID: "sess-c"}), "gh_list_issue")
}

// Base tools are agent-wide: executing one never scopes it.
func TestDynamicTool_BaseToolNotScopedByUse(t *testing.T) {
	a := newDynamicScopeAgent(t)
	a.tools.Register(namedStubTool{name: "base_tool"})
	a.baseToolsOnce = sync.Once{}
	a.captureBaseTools()

	execInSession(t, a, "sess-a", "base_tool")
	assert.Contains(t, advertisedNames(a, &Session{ID: "sess-b"}), "base_tool")
}

// Concurrent sessions resolving and using tools must not race on the ledger.
func TestDynamicTool_ConcurrentSessions(t *testing.T) {
	a := newDynamicScopeAgent(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			// require must not run off the test goroutine; report instead.
			ctx := &agentContext{
				Context: session.WithSessionID(context.Background(), id),
				session: &Session{ID: id},
				tracer:  observability.NewNoOpTracer(),
			}
			res, err := a.executeToolWithSelfCorrection(ctx, "gh_list_issue", map[string]interface{}{}, id)
			if err == nil && (res == nil || !res.Success) {
				err = fmt.Errorf("session %s: tool call did not succeed: %+v", id, res)
			}
			if err != nil {
				errs <- err
			}
			_ = advertisedNames(a, &Session{ID: id})
		}(string(rune('a' + i)))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for i := 0; i < 8; i++ {
		assert.Contains(t, advertisedNames(a, &Session{ID: string(rune('a' + i))}), "gh_list_issue")
	}
	assert.NotContains(t, advertisedNames(a, &Session{ID: "other"}), "gh_list_issue")
}
