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


package apps

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSharedAppsAreSeenAndChangedByEveryone(t *testing.T) {
	r := NewUIResourceRegistry()

	info, replaced, err := r.CreateSharedAppWithSpec("alice", "groceries", "Groceries", "For the house", []byte("<p>v1</p>"), []byte(`{"title":"g"}`), false)
	require.NoError(t, err)
	assert.False(t, replaced)
	assert.True(t, info.Shared)
	assert.Equal(t, "alice", info.SharedBy)

	// Bob sees it, reads it, and changes it (a checklist tick is an update).
	var bobSees []string
	for _, a := range r.ListAppInfo("bob") {
		if a.Dynamic {
			bobSees = append(bobSees, a.Name)
		}
	}
	assert.Equal(t, []string{"groceries"}, bobSees)
	spec, err := r.AppSpec("bob", "groceries")
	require.NoError(t, err)
	assert.JSONEq(t, `{"title":"g"}`, string(spec))
	updated, err := r.UpdateAppWithSpec("bob", "groceries", "", "", []byte("<p>v2</p>"), []byte(`{"title":"g2"}`))
	require.NoError(t, err)
	assert.True(t, updated.Shared, "an update keeps it shared")
	assert.Equal(t, "alice", updated.SharedBy, "an update keeps who shared it")
	html, err := r.AppHTML("alice", "groceries")
	require.NoError(t, err)
	assert.Equal(t, "<p>v2</p>", string(html), "Alice sees Bob's change")

	// Only Alice, who shared it, deletes it.
	err = r.DeleteApp("bob", "groceries")
	assert.True(t, errors.Is(err, ErrNotSharer), "%v", err)
	require.NoError(t, r.DeleteApp("alice", "groceries"))
	_, err = r.AppHTML("bob", "groceries")
	assert.Error(t, err)
}

func TestSharedNamesNeverCollide(t *testing.T) {
	r := NewUIResourceRegistry()
	_, _, err := r.CreateSharedAppWithSpec("alice", "groceries", "Groceries", "", []byte("<p>g</p>"), nil, false)
	require.NoError(t, err)

	// Nobody can make an own app with a shared app's name (even overwriting).
	_, _, err = r.CreateAppWithSpec("bob", "groceries", "Mine", "", []byte("<p>b</p>"), nil, true)
	assert.True(t, errors.Is(err, ErrNameInUse), "%v", err)

	// A shared app can't take a name someone uses for their own app.
	_, _, err = r.CreateAppWithSpec("bob", "chores", "Chores", "", []byte("<p>c</p>"), nil, false)
	require.NoError(t, err)
	_, _, err = r.CreateSharedAppWithSpec("alice", "chores", "Chores", "", []byte("<p>c</p>"), nil, false)
	assert.True(t, errors.Is(err, ErrNameInUse), "%v", err)
	_, _, err = r.CreateSharedAppWithSpec("bob", "chores", "Chores", "", []byte("<p>c</p>"), nil, false)
	assert.True(t, errors.Is(err, ErrNameInUse), "share your own instead: %v", err)

	// Without overwrite, an existing shared app is not replaced; with it,
	// it is, and who shared it stays.
	_, _, err = r.CreateSharedAppWithSpec("bob", "groceries", "G", "", []byte("<p>x</p>"), nil, false)
	assert.Error(t, err)
	info, replaced, err := r.CreateSharedAppWithSpec("bob", "groceries", "G", "", []byte("<p>x</p>"), nil, true)
	require.NoError(t, err)
	assert.True(t, replaced)
	assert.Equal(t, "alice", info.SharedBy)
}

func TestSetAppSharing(t *testing.T) {
	r := NewUIResourceRegistry()
	_, _, err := r.CreateAppWithSpec("alice", "chores", "Chores", "", []byte("<p>c</p>"), []byte(`{"t":1}`), false)
	require.NoError(t, err)

	info, err := r.SetAppSharing("alice", "chores", true)
	require.NoError(t, err)
	assert.True(t, info.Shared)
	assert.Equal(t, "alice", info.SharedBy)
	html, err := r.AppHTML("bob", "chores")
	require.NoError(t, err, "Bob sees it once shared")
	assert.Equal(t, "<p>c</p>", string(html))
	spec, err := r.AppSpec("bob", "chores")
	require.NoError(t, err)
	assert.JSONEq(t, `{"t":1}`, string(spec), "the spec moves with it")

	// Sharing again is a no-op; only Alice stops sharing.
	_, err = r.SetAppSharing("alice", "chores", true)
	require.NoError(t, err)
	_, err = r.SetAppSharing("bob", "chores", false)
	assert.True(t, errors.Is(err, ErrNotSharer), "%v", err)
	info, err = r.SetAppSharing("alice", "chores", false)
	require.NoError(t, err)
	assert.False(t, info.Shared)
	_, err = r.AppHTML("bob", "chores")
	assert.Error(t, err, "back to Alice's own")
	_, err = r.AppHTML("alice", "chores")
	assert.NoError(t, err)

	// Sharing is refused when someone else has an own app with the name.
	_, _, err = r.CreateAppWithSpec("bob", "budget", "Bob's", "", []byte("<p>b</p>"), nil, false)
	require.NoError(t, err)
	_, _, err = r.CreateAppWithSpec("alice", "budget", "Alice's", "", []byte("<p>a</p>"), nil, false)
	require.NoError(t, err)
	_, err = r.SetAppSharing("alice", "budget", true)
	assert.True(t, errors.Is(err, ErrNameInUse), "%v", err)

	_, err = r.SetAppSharing("alice", "missing", true)
	assert.Error(t, err)
}

func TestSharedAppsPersist(t *testing.T) {
	dir := t.TempDir()
	r := NewUIResourceRegistry()
	_, _, err := r.EnablePersistence(dir)
	require.NoError(t, err)
	_, _, err = r.CreateSharedAppWithSpec("alice", "groceries", "Groceries", "", []byte("<p>g</p>"), []byte(`{"t":1}`), false)
	require.NoError(t, err)
	_, _, err = r.CreateAppWithSpec("bob", "chores", "Chores", "", []byte("<p>c</p>"), nil, false)
	require.NoError(t, err)
	_, err = r.SetAppSharing("bob", "chores", true)
	require.NoError(t, err)

	reloaded := NewUIResourceRegistry()
	loaded, skipped, err := reloaded.EnablePersistence(dir)
	require.NoError(t, err)
	assert.Empty(t, skipped)
	assert.Equal(t, 2, loaded, "the moved app is stored once, as shared")
	infos := map[string]AppInfo{}
	for _, a := range reloaded.ListAppInfo("carol") {
		infos[a.Name] = a
	}
	assert.True(t, infos["groceries"].Shared)
	assert.Equal(t, "alice", infos["groceries"].SharedBy)
	assert.True(t, infos["chores"].Shared)
	assert.Equal(t, "bob", infos["chores"].SharedBy)
}
