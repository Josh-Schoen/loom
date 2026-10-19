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


package script

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/teradata-labs/loom/pkg/shuttle"
	"github.com/teradata-labs/loom/pkg/types"
)

func savedTools(t *testing.T) (map[string]shuttle.Tool, *Store) {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "scripts"), DefaultLimits)
	tools := map[string]shuttle.Tool{}
	for _, tl := range SavedTools(store, childRunner(DefaultLimits)) {
		tools[tl.Name()] = tl
	}
	return tools, store
}

func run(t *testing.T, tool shuttle.Tool, ctx context.Context, params map[string]interface{}) *shuttle.Result {
	t.Helper()
	res, err := tool.Execute(ctx, params)
	require.NoError(t, err)
	return res
}

const splitScript = `result = round(inputs["total"] * (1 + inputs["tip"]) / inputs["people"], 2)`

func TestSaveRunListDelete(t *testing.T) {
	tools, store := savedTools(t)
	alex := types.ContextWithUserID(context.Background(), "alex")

	res := run(t, tools[SaveToolName], alex, map[string]interface{}{
		"name": "bill-split", "description": "Each person's share of a bill with a tip",
		"script": splitScript, "example_inputs": map[string]interface{}{"total": 84.5, "tip": 0.2, "people": 3.0},
	})
	require.True(t, res.Success, "%+v", res.Error)
	assert.Equal(t, 33.8, res.Data.(map[string]interface{})["test_result"])

	res = run(t, tools[RunSavedName], alex, map[string]interface{}{
		"name": "bill-split", "inputs": map[string]interface{}{"total": 120.0, "tip": 0.18, "people": 4.0},
	})
	require.True(t, res.Success, "%+v", res.Error)
	assert.Equal(t, 35.4, res.Data.(map[string]interface{})["result"])

	res = run(t, tools[ListSavedName], alex, nil)
	list := res.Data.(map[string]interface{})["scripts"].([]interface{})
	require.Len(t, list, 1)
	assert.Equal(t, "bill-split", list[0].(map[string]interface{})["name"])

	// Another person sees none of it.
	sam := types.ContextWithUserID(context.Background(), "sam")
	res = run(t, tools[ListSavedName], sam, nil)
	assert.Empty(t, res.Data.(map[string]interface{})["scripts"])
	res = run(t, tools[RunSavedName], sam, map[string]interface{}{"name": "bill-split"})
	assert.Equal(t, "NOT_FOUND", res.Error.Code)

	res = run(t, tools[DeleteSavedName], alex, map[string]interface{}{"name": "bill-split"})
	require.True(t, res.Success)
	_, err := store.Get("alex", "bill-split")
	assert.ErrorIs(t, err, ErrScriptNotFound)
}

func TestSaveChecksBeforeKeeping(t *testing.T) {
	tools, store := savedTools(t)
	ctx := types.ContextWithUserID(context.Background(), "alex")

	res := run(t, tools[SaveToolName], ctx, map[string]interface{}{
		"name": "broken", "description": "x", "script": "result = (", "example_inputs": map[string]interface{}{},
	})
	assert.Equal(t, "SCRIPT_FAILED", res.Error.Code)
	assert.Contains(t, res.Error.Message, "syntax error")

	res = run(t, tools[SaveToolName], ctx, map[string]interface{}{
		"name": "fails", "description": "x", "script": `result = inputs["missing"]`, "example_inputs": map[string]interface{}{},
	})
	assert.Equal(t, "SCRIPT_FAILED", res.Error.Code)
	assert.Contains(t, res.Error.Message, "the test run failed")

	res = run(t, tools[SaveToolName], ctx, map[string]interface{}{
		"name": "Bad Name", "description": "x", "script": "result = 1", "example_inputs": map[string]interface{}{},
	})
	assert.Equal(t, "SAVE_FAILED", res.Error.Code)

	scripts, err := store.List("alex")
	require.NoError(t, err)
	assert.Empty(t, scripts, "nothing that failed was kept")
}

func TestStoreLimitsAndFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scripts")
	store := NewStore(dir, DefaultLimits)
	for i := 0; i < MaxSavedScripts; i++ {
		require.NoError(t, store.Save("alex", SavedScript{Name: "s" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Script: "result = 1"}))
	}
	assert.ErrorContains(t, store.Save("alex", SavedScript{Name: "one-more", Script: "result = 1"}), "delete one first")
	require.NoError(t, store.Save("alex", SavedScript{Name: "saa", Script: "result = 2"}), "replacing does not count")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.NotContains(t, entries[0].Name(), "alex", "files are named by a hash of the person")
	info, err := entries[0].Info()
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
