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
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOwnersSeeOnlyTheirOwnApps(t *testing.T) {
	r := NewUIResourceRegistry()
	require.NoError(t, RegisterEmbeddedApps(r))

	_, _, err := r.CreateAppWithSpec("alice", "meal-plan", "Alice's plan", "", []byte("<p>alice</p>"), []byte(`{"title":"a"}`), false)
	require.NoError(t, err)
	// The same name for another owner is a separate app.
	_, _, err = r.CreateAppWithSpec("bob", "meal-plan", "Bob's plan", "", []byte("<p>bob</p>"), []byte(`{"title":"b"}`), false)
	require.NoError(t, err)
	_, _, err = r.CreateApp("bob", "budget", "Budget", "", []byte("<p>budget</p>"), false)
	require.NoError(t, err)

	html, info, err := r.GetAppHTML("alice", "meal-plan")
	require.NoError(t, err)
	assert.Equal(t, "<p>alice</p>", string(html))
	assert.Equal(t, "Alice's plan", info.DisplayName)

	html, err = r.AppHTML("bob", "meal-plan")
	require.NoError(t, err)
	assert.Equal(t, "<p>bob</p>", string(html))

	spec, err := r.AppSpec("bob", "meal-plan")
	require.NoError(t, err)
	assert.JSONEq(t, `{"title":"b"}`, string(spec))

	// Alice cannot see, change, read the spec of, or delete Bob's budget.
	_, _, err = r.GetAppHTML("alice", "budget")
	assert.Error(t, err)
	_, err = r.UpdateApp("alice", "budget", "", "", []byte("<p>x</p>"))
	assert.Error(t, err)
	_, err = r.AppSpec("alice", "budget")
	assert.Error(t, err)
	assert.Error(t, r.DeleteApp("alice", "budget"))

	dynamicNames := func(owner string) []string {
		var names []string
		for _, info := range r.ListAppInfo(owner) {
			if info.Dynamic {
				names = append(names, info.Name)
			}
		}
		return names
	}
	assert.Equal(t, []string{"meal-plan"}, dynamicNames("alice"))
	assert.Equal(t, []string{"budget", "meal-plan"}, dynamicNames("bob"))
	assert.Empty(t, dynamicNames(DefaultOwner))

	// Everyone sees the embedded apps.
	for _, owner := range []string{"alice", "bob", ""} {
		assert.Contains(t, r.AppNames(owner), "data-chart", owner)
	}

	// Deleting Bob's meal plan leaves Alice's.
	require.NoError(t, r.DeleteApp("bob", "meal-plan"))
	_, _, err = r.GetAppHTML("alice", "meal-plan")
	assert.NoError(t, err)
}

func TestEmptyOwnerIsDefaultOwner(t *testing.T) {
	r := NewUIResourceRegistry()
	_, _, err := r.CreateApp("", "notes", "Notes", "", []byte("<p>n</p>"), false)
	require.NoError(t, err)

	_, _, err = r.GetAppHTML(DefaultOwner, "notes")
	assert.NoError(t, err)
	// The owner-less MCP view is DefaultOwner's.
	_, err = r.Read("ui://loom/notes")
	assert.NoError(t, err)

	_, _, err = r.CreateApp("alice", "private", "Private", "", []byte("<p>p</p>"), false)
	require.NoError(t, err)
	_, err = r.Read("ui://loom/private")
	assert.Error(t, err, "another owner's app must not be readable without identity")
	for _, res := range r.List() {
		assert.NotEqual(t, "ui://loom/private", res.URI)
	}
}

func TestOwnersCannotTakeEmbeddedNames(t *testing.T) {
	r := NewUIResourceRegistry()
	require.NoError(t, RegisterEmbeddedApps(r))
	_, _, err := r.CreateApp("alice", "data-chart", "Mine", "", []byte("<p>x</p>"), true)
	assert.ErrorContains(t, err, "embedded")
	assert.ErrorContains(t, r.DeleteApp("alice", "data-chart"), "embedded")
}

func TestLimitsArePerOwner(t *testing.T) {
	r := NewUIResourceRegistry()
	r.maxDynamic = 2
	for i := 0; i < 2; i++ {
		_, _, err := r.CreateApp("alice", fmt.Sprintf("a-%d", i), "", "", []byte("<p/>"), false)
		require.NoError(t, err)
	}
	_, _, err := r.CreateApp("alice", "a-2", "", "", []byte("<p/>"), false)
	assert.ErrorContains(t, err, "limit")
	_, _, err = r.CreateApp("bob", "b-0", "", "", []byte("<p/>"), false)
	assert.NoError(t, err, "one owner's apps must not use up another's limit")
}

func TestStoreKeepsOwnersApart(t *testing.T) {
	dir := t.TempDir()
	r := persistentRegistry(t, dir)
	_, _, err := r.CreateApp("", "notes", "Notes", "", []byte("<p>default</p>"), false)
	require.NoError(t, err)
	_, _, err = r.CreateApp("alice@example.com", "notes", "Alice's notes", "", []byte("<p>alice</p>"), false)
	require.NoError(t, err)

	// The default owner keeps the original layout; others get a directory
	// named from their ID, not the ID itself.
	assert.FileExists(t, filepath.Join(dir, "notes.json"))
	aliceFile := filepath.Join(dir, usersDir, ownerKey("alice@example.com"), "notes.json")
	assert.FileExists(t, aliceFile)
	info, err := os.Stat(aliceFile)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	reloaded := persistentRegistry(t, dir)
	html, err := reloaded.AppHTML("alice@example.com", "notes")
	require.NoError(t, err)
	assert.Equal(t, "<p>alice</p>", string(html))
	html, err = reloaded.AppHTML("", "notes")
	require.NoError(t, err)
	assert.Equal(t, "<p>default</p>", string(html))

	require.NoError(t, reloaded.DeleteApp("alice@example.com", "notes"))
	assert.NoFileExists(t, aliceFile)
	assert.FileExists(t, filepath.Join(dir, "notes.json"))
}

// A file moved into another owner's directory is not loaded as theirs.
func TestStoreSkipsFilesInTheWrongOwnersDirectory(t *testing.T) {
	dir := t.TempDir()
	r := persistentRegistry(t, dir)
	_, _, err := r.CreateApp("alice", "notes", "Notes", "", []byte("<p>alice</p>"), false)
	require.NoError(t, err)

	bobDir := filepath.Join(dir, usersDir, ownerKey("bob"))
	require.NoError(t, os.MkdirAll(bobDir, 0o700))
	require.NoError(t, os.Rename(
		filepath.Join(dir, usersDir, ownerKey("alice"), "notes.json"),
		filepath.Join(bobDir, "notes.json"),
	))

	reloaded := NewUIResourceRegistry()
	loaded, skipped, err := reloaded.EnablePersistence(dir)
	require.NoError(t, err)
	assert.Equal(t, 0, loaded)
	assert.Equal(t, []string{filepath.Join(usersDir, ownerKey("bob"), "notes.json")}, skipped)
	_, err = reloaded.AppHTML("bob", "notes")
	assert.Error(t, err)
}

func TestOwnersConcurrentWrites(t *testing.T) {
	r := persistentRegistry(t, t.TempDir())
	embedded := r.Count()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		owner := fmt.Sprintf("user-%d", i%3)
		name := fmt.Sprintf("app-%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := r.CreateApp(owner, name, "", "", []byte("<p/>"), false)
			assert.NoError(t, err)
			_ = r.ListAppInfo(owner)
			_, _, _ = r.GetAppHTML(owner, name)
		}()
	}
	wg.Wait()
	assert.Equal(t, embedded+8, r.Count())
}
