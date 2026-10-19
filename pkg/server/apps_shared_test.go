// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/shuttle"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A shared list: Alex makes it, Sam sees it and ticks an item, both see the
// tick, and only Alex can delete it.
func TestSharedListThroughTheRPCs(t *testing.T) {
	srv, _ := newChecklistServer(t, t.TempDir())
	alex, sam := ctxFor("alex"), ctxFor("sam")

	created, err := srv.CreateUIApp(alex, &loomv1.CreateUIAppRequest{Name: "groceries", Spec: groceriesSpec(t), Shared: true})
	require.NoError(t, err)
	assert.True(t, created.GetApp().GetShared())
	assert.Equal(t, "alex", created.GetApp().GetSharedBy())

	list, err := srv.ListUIApps(sam, &loomv1.ListUIAppsRequest{})
	require.NoError(t, err)
	var samSees *loomv1.UIApp
	for _, a := range list.GetApps() {
		if a.GetName() == "groceries" {
			samSees = a
		}
	}
	require.NotNil(t, samSees, "Sam sees the shared list")
	assert.True(t, samSees.GetShared())

	_, err = srv.SetUIAppChecklistItem(sam, &loomv1.SetUIAppChecklistItemRequest{
		Name: "groceries", ComponentId: "produce", ItemId: "peppers", Checked: true,
	})
	require.NoError(t, err, "Sam can tick it")
	page, err := srv.GetUIApp(alex, &loomv1.GetUIAppRequest{Name: "groceries"})
	require.NoError(t, err)
	assert.Contains(t, string(page.GetContent()), "peppers", "Alex sees the page")

	// Nobody can make their own app with the shared app's name.
	_, err = srv.CreateUIApp(sam, &loomv1.CreateUIAppRequest{Name: "groceries", Spec: groceriesSpec(t), Overwrite: true})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))

	// Only Alex deletes it or stops sharing it.
	_, err = srv.DeleteUIApp(sam, &loomv1.DeleteUIAppRequest{Name: "groceries"})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	_, err = srv.SetUIAppSharing(sam, &loomv1.SetUIAppSharingRequest{Name: "groceries", Shared: false})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	_, err = srv.DeleteUIApp(alex, &loomv1.DeleteUIAppRequest{Name: "groceries"})
	require.NoError(t, err)
}

func TestSetUIAppSharing(t *testing.T) {
	srv, _ := newChecklistServer(t, t.TempDir())
	alex, sam := ctxFor("alex"), ctxFor("sam")

	_, err := srv.CreateUIApp(alex, &loomv1.CreateUIAppRequest{Name: "chores", Spec: groceriesSpec(t)})
	require.NoError(t, err)
	_, err = srv.GetUIApp(sam, &loomv1.GetUIAppRequest{Name: "chores"})
	assert.Error(t, err, "Alex's own page is hidden from Sam")

	resp, err := srv.SetUIAppSharing(alex, &loomv1.SetUIAppSharingRequest{Name: "chores", Shared: true})
	require.NoError(t, err)
	assert.True(t, resp.GetApp().GetShared())
	_, err = srv.GetUIApp(sam, &loomv1.GetUIAppRequest{Name: "chores"})
	require.NoError(t, err, "shared: Sam sees it")

	resp, err = srv.SetUIAppSharing(alex, &loomv1.SetUIAppSharingRequest{Name: "chores", Shared: false})
	require.NoError(t, err)
	assert.False(t, resp.GetApp().GetShared())
	_, err = srv.GetUIApp(sam, &loomv1.GetUIAppRequest{Name: "chores"})
	assert.Error(t, err, "not shared any more")

	_, err = srv.SetUIAppSharing(alex, &loomv1.SetUIAppSharingRequest{Name: "missing", Shared: true})
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = srv.SetUIAppSharing(alex, &loomv1.SetUIAppSharingRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestShareUIAppTool(t *testing.T) {
	srv, registry := newChecklistServer(t, t.TempDir())
	alex, sam := ctxFor("alex"), ctxFor("sam")
	_, err := srv.CreateUIApp(alex, &loomv1.CreateUIAppRequest{Name: "chores", Spec: groceriesSpec(t)})
	require.NoError(t, err)

	tools := UIAppTools(nil, registry)
	tool := tools[len(tools)-1]
	require.Equal(t, "share_ui_app", tool.Name())

	res, err := tool.Execute(alex, map[string]interface{}{"name": "chores"})
	require.NoError(t, err)
	require.True(t, res.Success, "%+v", res.Error)
	assert.Equal(t, true, res.Data.(map[string]interface{})["shared"])
	_, err = registry.AppHTML("sam", "chores")
	assert.NoError(t, err, "shared by the tool")

	// Reading a shared app tells the agent its text may come from others.
	var get shuttle.Tool
	for _, tl := range tools {
		if tl.Name() == "get_ui_app" {
			get = tl
		}
	}
	require.NotNil(t, get)
	res, err = get.Execute(sam, map[string]interface{}{"name": "chores"})
	require.NoError(t, err)
	require.True(t, res.Success, "%+v", res.Error)
	data := res.Data.(map[string]interface{})
	assert.Equal(t, true, data["shared"])
	assert.Equal(t, "alex", data["shared_by"])
	assert.Equal(t, sharedAppNote, data["note"])

	res, err = tool.Execute(sam, map[string]interface{}{"name": "chores", "shared": false})
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Equal(t, "NOT_SHARER", res.Error.Code)
}
