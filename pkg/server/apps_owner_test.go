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

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/mcp/apps"
	"github.com/teradata-labs/loom/pkg/storage/postgres"
	"github.com/teradata-labs/loom/pkg/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func asUser(id string) context.Context {
	return postgres.ContextWithUserID(context.Background(), id)
}

func dynamicAppNames(t *testing.T, srv *MultiAgentServer, ctx context.Context) []string {
	t.Helper()
	resp, err := srv.ListUIApps(ctx, &loomv1.ListUIAppsRequest{})
	require.NoError(t, err)
	var names []string
	for _, app := range resp.GetApps() {
		if app.GetDynamic() {
			names = append(names, app.GetName())
		}
	}
	return names
}

func TestDefaultOwnerIsDefaultUserID(t *testing.T) {
	assert.Equal(t, types.DefaultUserID, apps.DefaultOwner)
	assert.Equal(t, types.DefaultUserID, appOwner(context.Background()))
	assert.Equal(t, "alice", appOwner(asUser("alice")))
}

func TestUIAppRPCsAreScopedToTheCaller(t *testing.T) {
	srv, _ := newChecklistServer(t, t.TempDir())
	alice, bob := asUser("alice"), asUser("bob")

	// Both can have a page with the same name.
	_, err := srv.CreateUIApp(alice, &loomv1.CreateUIAppRequest{Name: "groceries", Spec: groceriesSpec(t)})
	require.NoError(t, err)
	_, err = srv.CreateUIApp(bob, &loomv1.CreateUIAppRequest{Name: "groceries", Spec: groceriesSpec(t)})
	require.NoError(t, err)
	_, err = srv.CreateUIApp(bob, &loomv1.CreateUIAppRequest{Name: "budget", Spec: groceriesSpec(t)})
	require.NoError(t, err)

	assert.Equal(t, []string{"groceries"}, dynamicAppNames(t, srv, alice))
	assert.Equal(t, []string{"budget", "groceries"}, dynamicAppNames(t, srv, bob))
	assert.Empty(t, dynamicAppNames(t, srv, context.Background()))

	// Alice cannot read, change, tick, or delete Bob's budget.
	_, err = srv.GetUIApp(alice, &loomv1.GetUIAppRequest{Name: "budget"})
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = srv.UpdateUIApp(alice, &loomv1.UpdateUIAppRequest{Name: "budget", Spec: groceriesSpec(t)})
	assert.Error(t, err)
	_, err = srv.SetUIAppChecklistItem(alice, &loomv1.SetUIAppChecklistItemRequest{
		Name: "budget", ComponentId: "produce", ItemId: "peppers", Checked: true,
	})
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = srv.DeleteUIApp(alice, &loomv1.DeleteUIAppRequest{Name: "budget"})
	assert.Error(t, err)
	_, err = srv.GetUIApp(bob, &loomv1.GetUIAppRequest{Name: "budget"})
	assert.NoError(t, err, "Bob's page must be untouched")

	// Ticking Alice's list does not tick Bob's list of the same name.
	_, err = srv.SetUIAppChecklistItem(alice, &loomv1.SetUIAppChecklistItemRequest{
		Name: "groceries", ComponentId: "produce", ItemId: "peppers", Checked: true,
	})
	require.NoError(t, err)
	aliceHTML, err := srv.GetUIApp(alice, &loomv1.GetUIAppRequest{Name: "groceries"})
	require.NoError(t, err)
	bobHTML, err := srv.GetUIApp(bob, &loomv1.GetUIAppRequest{Name: "groceries"})
	require.NoError(t, err)
	assert.NotEqual(t, string(aliceHTML.GetContent()), string(bobHTML.GetContent()))
}

func TestUIAppToolsActForTheTurnsUser(t *testing.T) {
	_, registry := newChecklistServer(t, t.TempDir())
	compiler, err := apps.NewCompiler()
	require.NoError(t, err)
	tools := UIAppTools(compiler, registry)

	spec := map[string]interface{}{
		"version": "1.0",
		"title":   "Notes",
		"components": []interface{}{
			map[string]interface{}{"type": "text", "props": map[string]interface{}{"content": "hi"}},
		},
	}
	create := tools[0]
	require.Equal(t, "create_ui_app", create.Name())
	result, err := create.Execute(asUser("alice"), map[string]interface{}{"name": "notes", "spec": spec})
	require.NoError(t, err)
	require.True(t, result.Success, "%+v", result.Error)

	_, _, err = registry.GetAppHTML("alice", "notes")
	assert.NoError(t, err)
	_, _, err = registry.GetAppHTML("bob", "notes")
	assert.Error(t, err)

	get := &getUIAppTool{provider: registry}
	result, err = get.Execute(asUser("bob"), map[string]interface{}{"name": "notes"})
	require.NoError(t, err)
	assert.False(t, result.Success, "Bob's assistant must not read Alice's page")

	del := &deleteUIAppTool{provider: registry}
	result, err = del.Execute(asUser("bob"), map[string]interface{}{"name": "notes"})
	require.NoError(t, err)
	assert.False(t, result.Success, "Bob's assistant must not delete Alice's page")
	_, _, err = registry.GetAppHTML("alice", "notes")
	assert.NoError(t, err)
}

func TestAppsPagesOverHTTPAreScopedToTheCaller(t *testing.T) {
	registry := apps.NewUIResourceRegistry()
	_, _, err := registry.CreateApp("alice", "notes", "Notes", "", []byte("<p>alice</p>"), false)
	require.NoError(t, err)

	srv := &HTTPServer{appHTMLProvider: registry, gatewayMux: runtime.NewServeMux()}
	srv.SetStreamInterceptors(UserIDStreamInterceptor(UserIDConfig{}))

	get := func(path, user string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if user != "" {
			req.Header.Set("Grpc-Metadata-X-User-Id", user)
		}
		rr := httptest.NewRecorder()
		srv.handleApps(rr, req)
		return rr
	}

	rr := get("/apps/notes", "alice")
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "<p>alice</p>", rr.Body.String())

	assert.Equal(t, http.StatusNotFound, get("/apps/notes", "bob").Code)
	assert.Equal(t, http.StatusNotFound, get("/apps/notes", "").Code)

	assert.Contains(t, get("/apps/", "alice").Body.String(), "/apps/notes")
	assert.NotContains(t, get("/apps/", "bob").Body.String(), "/apps/notes")
}
