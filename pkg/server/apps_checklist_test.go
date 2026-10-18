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
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/mcp/apps"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

// groceriesSpec is a page with a checklist nested in a section.
func groceriesSpec(t *testing.T) *loomv1.UIAppSpec {
	t.Helper()
	props, err := structpb.NewStruct(map[string]interface{}{
		"title": "Produce",
		"items": []interface{}{
			map[string]interface{}{"id": "peppers", "label": "Bell peppers"},
			map[string]interface{}{"id": "onions", "label": "Onions", "checked": true},
			map[string]interface{}{"label": "Limes"},
		},
	})
	require.NoError(t, err)
	return &loomv1.UIAppSpec{
		Version: "1.0",
		Title:   "Groceries",
		Components: []*loomv1.UIComponent{{
			Type: "section",
			Children: []*loomv1.UIComponent{
				{Type: "checklist", Id: "produce", Props: props},
			},
		}},
	}
}

// newChecklistServer returns a server backed by a real registry (persisted
// to dir) and compiler, with the groceries app created through the RPC.
func newChecklistServer(t *testing.T, dir string) (*MultiAgentServer, *apps.UIResourceRegistry) {
	t.Helper()
	registry := apps.NewUIResourceRegistry()
	_, _, err := registry.EnablePersistence(dir)
	require.NoError(t, err)
	compiler, err := apps.NewCompiler()
	require.NoError(t, err)

	srv := NewMultiAgentServer(nil, nil)
	srv.SetLogger(zaptest.NewLogger(t))
	srv.SetAppProvider(registry)
	srv.SetAppCompiler(compiler)
	return srv, registry
}

func storedChecklistItems(t *testing.T, registry *apps.UIResourceRegistry, name string) map[string]bool {
	t.Helper()
	raw, err := registry.AppSpec("", name)
	require.NoError(t, err)
	spec := &loomv1.UIAppSpec{}
	require.NoError(t, protojson.Unmarshal(raw, spec))
	comp := findComponent(spec.GetComponents(), "produce")
	require.NotNil(t, comp)
	checked := map[string]bool{}
	for _, item := range apps.ChecklistItems(comp) {
		checked[apps.ChecklistItemID(item)] = item.Fields["checked"].GetBoolValue()
	}
	return checked
}

func TestSetUIAppChecklistItem_TicksAndPersists(t *testing.T) {
	dir := t.TempDir()
	srv, registry := newChecklistServer(t, dir)
	ctx := context.Background()

	_, err := srv.CreateUIApp(ctx, &loomv1.CreateUIAppRequest{Name: "groceries", Spec: groceriesSpec(t)})
	require.NoError(t, err)

	resp, err := srv.SetUIAppChecklistItem(ctx, &loomv1.SetUIAppChecklistItemRequest{
		Name: "groceries", ComponentId: "produce", ItemId: "peppers", Checked: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "groceries", resp.GetApp().GetName())

	// An item without an id is addressed by its label.
	_, err = srv.SetUIAppChecklistItem(ctx, &loomv1.SetUIAppChecklistItemRequest{
		Name: "groceries", ComponentId: "produce", ItemId: "Limes", Checked: true,
	})
	require.NoError(t, err)
	_, err = srv.SetUIAppChecklistItem(ctx, &loomv1.SetUIAppChecklistItemRequest{
		Name: "groceries", ComponentId: "produce", ItemId: "onions", Checked: false,
	})
	require.NoError(t, err)

	want := map[string]bool{"peppers": true, "onions": false, "Limes": true}
	assert.Equal(t, want, storedChecklistItems(t, registry, "groceries"))

	// The served HTML reflects the change.
	html, _, err := registry.GetAppHTML("", "groceries")
	require.NoError(t, err)
	assert.Contains(t, string(html), `"checked":true`)

	// A restarted server loads the ticks from disk.
	_, reloaded := newChecklistServer(t, dir)
	assert.Equal(t, want, storedChecklistItems(t, reloaded, "groceries"))
}

func TestSetUIAppChecklistItem_Errors(t *testing.T) {
	srv, _ := newChecklistServer(t, t.TempDir())
	ctx := context.Background()
	_, err := srv.CreateUIApp(ctx, &loomv1.CreateUIAppRequest{Name: "groceries", Spec: groceriesSpec(t)})
	require.NoError(t, err)

	tests := []struct {
		name string
		req  *loomv1.SetUIAppChecklistItemRequest
		code codes.Code
	}{
		{"invalid name", &loomv1.SetUIAppChecklistItemRequest{Name: "Bad Name", ComponentId: "produce", ItemId: "peppers"}, codes.InvalidArgument},
		{"missing component", &loomv1.SetUIAppChecklistItemRequest{Name: "groceries", ItemId: "peppers"}, codes.InvalidArgument},
		{"missing item", &loomv1.SetUIAppChecklistItemRequest{Name: "groceries", ComponentId: "produce"}, codes.InvalidArgument},
		{"unknown app", &loomv1.SetUIAppChecklistItemRequest{Name: "nope", ComponentId: "produce", ItemId: "peppers"}, codes.NotFound},
		{"unknown checklist", &loomv1.SetUIAppChecklistItemRequest{Name: "groceries", ComponentId: "dairy", ItemId: "peppers"}, codes.NotFound},
		{"unknown item", &loomv1.SetUIAppChecklistItemRequest{Name: "groceries", ComponentId: "produce", ItemId: "kale"}, codes.NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := srv.SetUIAppChecklistItem(ctx, tt.req)
			require.Error(t, err)
			assert.Equal(t, tt.code, status.Code(err))
		})
	}
}

func TestSetUIAppChecklistItem_NoSpecProvider(t *testing.T) {
	srv := newTestAppsServerWithCompiler(t, sampleAppProvider(), &mockAppCompiler{})
	_, err := srv.SetUIAppChecklistItem(context.Background(), &loomv1.SetUIAppChecklistItemRequest{
		Name: "chart", ComponentId: "produce", ItemId: "peppers", Checked: true,
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestSetUIAppChecklistItem_ConcurrentTicksAllLand(t *testing.T) {
	srv, registry := newChecklistServer(t, t.TempDir())
	ctx := context.Background()

	labels := make([]interface{}, 20)
	for i := range labels {
		labels[i] = map[string]interface{}{"id": fmt.Sprintf("item-%d", i), "label": fmt.Sprintf("Item %d", i)}
	}
	props, err := structpb.NewStruct(map[string]interface{}{"items": labels})
	require.NoError(t, err)
	spec := &loomv1.UIAppSpec{Version: "1.0", Title: "List", Components: []*loomv1.UIComponent{
		{Type: "checklist", Id: "produce", Props: props},
	}}
	_, err = srv.CreateUIApp(ctx, &loomv1.CreateUIAppRequest{Name: "list", Spec: spec})
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := range labels {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := srv.SetUIAppChecklistItem(ctx, &loomv1.SetUIAppChecklistItemRequest{
				Name: "list", ComponentId: "produce", ItemId: fmt.Sprintf("item-%d", i), Checked: true,
			})
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()

	for id, checked := range storedChecklistItems(t, registry, "list") {
		assert.True(t, checked, "%s lost its tick", id)
	}
}

func TestGetUIAppTool_ReturnsSpecWithTicks(t *testing.T) {
	srv, registry := newChecklistServer(t, t.TempDir())
	ctx := context.Background()
	_, err := srv.CreateUIApp(ctx, &loomv1.CreateUIAppRequest{Name: "groceries", Spec: groceriesSpec(t)})
	require.NoError(t, err)
	_, err = srv.SetUIAppChecklistItem(ctx, &loomv1.SetUIAppChecklistItemRequest{
		Name: "groceries", ComponentId: "produce", ItemId: "peppers", Checked: true,
	})
	require.NoError(t, err)

	tool := &getUIAppTool{provider: registry}
	result, err := tool.Execute(ctx, map[string]interface{}{"name": "groceries"})
	require.NoError(t, err)
	require.True(t, result.Success, "%+v", result.Error)

	data := result.Data.(map[string]interface{})
	spec := data["spec"].(map[string]interface{})
	section := spec["components"].([]interface{})[0].(map[string]interface{})
	checklist := section["children"].([]interface{})[0].(map[string]interface{})
	items := checklist["props"].(map[string]interface{})["items"].([]interface{})
	assert.Equal(t, true, items[0].(map[string]interface{})["checked"])
}

func TestGetUIAppTool_Errors(t *testing.T) {
	_, registry := newChecklistServer(t, t.TempDir())
	ctx := context.Background()

	result, err := (&getUIAppTool{provider: registry}).Execute(ctx, map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, "INVALID_PARAMS", result.Error.Code)

	result, err = (&getUIAppTool{provider: registry}).Execute(ctx, map[string]interface{}{"name": "missing"})
	require.NoError(t, err)
	assert.Equal(t, "NOT_FOUND", result.Error.Code)

	result, err = (&getUIAppTool{provider: sampleAppProvider()}).Execute(ctx, map[string]interface{}{"name": "chart"})
	require.NoError(t, err)
	assert.Equal(t, "PRECONDITION_FAILED", result.Error.Code)
}
