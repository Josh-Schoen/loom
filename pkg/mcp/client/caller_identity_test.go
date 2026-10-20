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

package client

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teradata-labs/loom/pkg/mcp/protocol"
	"github.com/teradata-labs/loom/pkg/types"
)

// callerOf returns the caller user ID in recorded tools/call params, or "".
func callerOf(t *testing.T, params json.RawMessage) string {
	t.Helper()
	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(params, &obj))
	raw, ok := obj["_meta"]
	if !ok {
		return ""
	}
	var meta map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &meta))
	var id string
	if v, ok := meta[protocol.MetaCallerUserID]; ok {
		require.NoError(t, json.Unmarshal(v, &id))
	}
	return id
}

func TestCallToolForwardsCallerIdentityWhenConfigured(t *testing.T) {
	for _, stateless := range []bool{false, true} {
		ft := newScriptedTransport()
		if stateless {
			ft.discoverResult = statelessDiscoverResult()
		}
		ft.tools = []protocol.Tool{simpleTool("do_thing", nil)}
		ft.callResult = json.RawMessage(`{"content":[],"resultType":"complete"}`)
		c := connectClient(t, ft, Config{ForwardCallerIdentity: true})
		require.NoError(t, c.Connect(context.Background(), protocol.Implementation{Name: "loom"}))

		ctx := types.ContextWithUserID(context.Background(), "sam-2")
		_, err := c.CallTool(ctx, "do_thing", map[string]interface{}{})
		require.NoError(t, err)
		// No user in the context: nothing to forward.
		_, err = c.CallTool(context.Background(), "do_thing", map[string]interface{}{})
		require.NoError(t, err)

		ft.mu.Lock()
		require.Len(t, ft.callParams, 2)
		assert.Equal(t, "sam-2", callerOf(t, ft.callParams[0]), "stateless=%v", stateless)
		assert.Empty(t, callerOf(t, ft.callParams[1]), "stateless=%v", stateless)
		if stateless {
			// The stateless core's own _meta keys are kept alongside.
			assert.Contains(t, metaOf(t, ft.callParams[0]), protocol.MetaIdempotencyKey)
		}
		ft.mu.Unlock()
	}
}

func TestCallToolKeepsCallerIdentityByDefault(t *testing.T) {
	ft := newScriptedTransport()
	ft.tools = []protocol.Tool{simpleTool("do_thing", nil)}
	ft.callResult = json.RawMessage(`{"content":[],"resultType":"complete"}`)
	c := connectClient(t, ft, Config{})
	require.NoError(t, c.Connect(context.Background(), protocol.Implementation{Name: "loom"}))

	_, err := c.CallTool(types.ContextWithUserID(context.Background(), "sam-2"), "do_thing", map[string]interface{}{})
	require.NoError(t, err)

	ft.mu.Lock()
	defer ft.mu.Unlock()
	require.Len(t, ft.callParams, 1)
	assert.Empty(t, callerOf(t, ft.callParams[0]), "servers not configured for it never see who is calling")
}
