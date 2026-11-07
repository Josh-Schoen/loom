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
package registry

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
)

func TestSearchTool_DiscoveryHook(t *testing.T) {
	reg, err := New(Config{DBPath: filepath.Join(t.TempDir(), "tools.db")})
	require.NoError(t, err)
	defer func() { _ = reg.Close() }()

	ctx := context.Background()
	now := time.Now().Format(time.RFC3339)
	for _, tool := range []*loomv1.IndexedTool{
		{Id: "mcp:slack:send_message", Name: "send_message", Description: "Send a message to a Slack channel",
			Source: loomv1.ToolSource_TOOL_SOURCE_MCP, McpServer: "slack", IndexedAt: now},
		{Id: "builtin:http_request", Name: "http_request", Description: "Make HTTP requests to REST APIs",
			Source: loomv1.ToolSource_TOOL_SOURCE_BUILTIN, IndexedAt: now},
	} {
		require.NoError(t, reg.RegisterTool(ctx, tool))
	}

	st := NewSearchTool(reg)
	var got []string
	st.SetDiscoveryHook(func(_ context.Context, names []string) { got = append(got, names...) })

	res, err := st.Execute(ctx, map[string]interface{}{"query": "send a message to Slack", "mode": "fast"})
	require.NoError(t, err)
	require.True(t, res.Success)
	assert.Contains(t, got, "send_message", "plain name is reported")
	assert.Contains(t, got, "slack:send_message", "MCP tools are also reported server-qualified")

	// The advertisable filter applies before the hook: a tool the agent could
	// not run is never revealed.
	got = nil
	st.SetToolFilter(func(name string) bool { return name != "send_message" })
	_, err = st.Execute(ctx, map[string]interface{}{"query": "send a message to Slack", "mode": "fast"})
	require.NoError(t, err)
	assert.NotContains(t, got, "send_message")
	assert.NotContains(t, got, "slack:send_message")
}
