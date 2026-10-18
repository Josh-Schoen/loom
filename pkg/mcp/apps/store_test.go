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
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func persistentRegistry(t *testing.T, dir string) *UIResourceRegistry {
	t.Helper()
	r := NewUIResourceRegistry()
	require.NoError(t, RegisterEmbeddedApps(r))
	_, skipped, err := r.EnablePersistence(dir)
	require.NoError(t, err)
	require.Empty(t, skipped)
	return r
}

func TestPersistenceSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	r := persistentRegistry(t, dir)

	info, _, err := r.CreateApp("meal-plan", "Meal Plan", "This week", []byte("<html>v1</html>"), false)
	require.NoError(t, err)
	assert.False(t, info.UpdatedAt.IsZero())

	_, err = r.UpdateApp("meal-plan", "", "", []byte("<html>v2</html>"))
	require.NoError(t, err)
	_, _, err = r.CreateApp("budget", "Budget", "", []byte("<html>b</html>"), false)
	require.NoError(t, err)

	fi, err := os.Stat(filepath.Join(dir, "meal-plan.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	restarted := NewUIResourceRegistry()
	require.NoError(t, RegisterEmbeddedApps(restarted))
	loaded, skipped, err := restarted.EnablePersistence(dir)
	require.NoError(t, err)
	assert.Equal(t, 2, loaded)
	assert.Empty(t, skipped)

	html, got, err := restarted.GetAppHTML("meal-plan")
	require.NoError(t, err)
	assert.Equal(t, "<html>v2</html>", string(html))
	assert.Equal(t, "Meal Plan", got.DisplayName)
	assert.Equal(t, "This week", got.Description)
	assert.True(t, got.Dynamic)
	assert.False(t, got.UpdatedAt.IsZero())

	require.NoError(t, restarted.DeleteApp("budget"))
	_, err = os.Stat(filepath.Join(dir, "budget.json"))
	assert.True(t, os.IsNotExist(err))
}

func TestPersistenceSkipsBadFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "renamed.json"),
		[]byte(`{"name":"other","html":"<p>x</p>"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o600))

	r := NewUIResourceRegistry()
	loaded, skipped, err := r.EnablePersistence(dir)
	require.NoError(t, err)
	assert.Equal(t, 0, loaded)
	assert.ElementsMatch(t, []string{"broken.json", "renamed.json"}, skipped)
	_, err = os.Stat(filepath.Join(dir, "broken.json"))
	assert.NoError(t, err, "bad files stay for inspection")
}

func TestPersistenceFailureLeavesRegistryUnchanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "apps")
	r := persistentRegistry(t, dir)
	_, _, err := r.CreateApp("kept", "Kept", "", []byte("<html>v1</html>"), false)
	require.NoError(t, err)

	// Make the directory unusable so every write fails.
	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, os.WriteFile(dir, []byte("not a dir"), 0o600))

	_, _, err = r.CreateApp("lost", "Lost", "", []byte("<html/>"), false)
	assert.Error(t, err)
	_, _, err = r.GetAppHTML("lost")
	assert.Error(t, err, "a create that was not saved must not be visible")

	_, err = r.UpdateApp("kept", "", "", []byte("<html>v2</html>"))
	assert.Error(t, err)
	html, _, err := r.GetAppHTML("kept")
	require.NoError(t, err)
	assert.Equal(t, "<html>v1</html>", string(html), "a failed update keeps the old version")
}

func TestStoreRejectsUnsafeNames(t *testing.T) {
	s, err := newFileStore(t.TempDir())
	require.NoError(t, err)
	for _, name := range []string{"../escape", "UPPER", "", "a/b"} {
		_, err := s.path(name)
		assert.Error(t, err, name)
	}
}

func TestPersistenceConcurrentWrites(t *testing.T) {
	r := persistentRegistry(t, t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := []string{"a", "b", "c"}[i%3]
			_, _, _ = r.CreateApp(name, name, "", []byte("<p>x</p>"), true)
			_ = r.ListAppInfo()
		}(i)
	}
	wg.Wait()
	for _, name := range []string{"a", "b", "c"} {
		_, info, err := r.GetAppHTML(name)
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now(), info.UpdatedAt, time.Minute)
	}
}
