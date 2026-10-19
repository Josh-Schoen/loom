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
	"errors"
	"time"

	"github.com/teradata-labs/loom/pkg/shuttle"
	"github.com/teradata-labs/loom/pkg/types"
)

// Saved-script tool names. An agent that lists any of them in
// tools.builtin gets all four (looms serve).
const (
	SaveToolName    = "save_script"
	RunSavedName    = "run_saved_script"
	ListSavedName   = "list_saved_scripts"
	DeleteSavedName = "delete_saved_script"
)

// SavedToolNames are the saved-script tools.
var SavedToolNames = []string{SaveToolName, RunSavedName, ListSavedName, DeleteSavedName}

// SavedTools returns the tools that keep a person's calculation scripts in
// store and run them through runner. Each person sees only their own
// scripts (the user of the turn, types.UserIDFromContext).
func SavedTools(store *Store, runner Runner) []shuttle.Tool {
	return []shuttle.Tool{
		&saveTool{store: store, runner: runner},
		&runSavedTool{store: store, runner: runner},
		&listSavedTool{store: store},
		&deleteSavedTool{store: store},
	}
}

func owner(ctx context.Context) string {
	if id := types.UserIDFromContext(ctx); id != "" {
		return id
	}
	return types.DefaultUserID
}

func failed(start time.Time, code, msg, suggestion string) *shuttle.Result {
	return &shuttle.Result{
		Success:         false,
		Error:           &shuttle.Error{Code: code, Message: msg, Suggestion: suggestion},
		ExecutionTimeMs: time.Since(start).Milliseconds(),
	}
}

func done(start time.Time, data map[string]interface{}) *shuttle.Result {
	return &shuttle.Result{Success: true, Data: data, ExecutionTimeMs: time.Since(start).Milliseconds()}
}

// ---- save_script ----

type saveTool struct {
	store  *Store
	runner Runner
}

func (t *saveTool) Name() string { return SaveToolName }

func (t *saveTool) Description() string {
	return `Save a calculation script (as run_script takes) under a short name, so it can be run again later with run_saved_script and new inputs. Use it when the person asks you to keep or remember a calculation ("save this as my budget calculator"). The script is run once with example_inputs first and saved only if that works; saving under an existing name replaces it. Scripts are the person's own.`
}

func (t *saveTool) InputSchema() *shuttle.JSONSchema {
	return shuttle.NewObjectSchema("A script to keep",
		map[string]*shuttle.JSONSchema{
			"name":           shuttle.NewStringSchema("Short lowercase name with hyphens, e.g. budget-calculator").WithPattern(scriptName.String()),
			"description":    shuttle.NewStringSchema("What it works out and which inputs it takes, in a sentence"),
			"script":         shuttle.NewStringSchema("Starlark source that reads `inputs` and sets `result`"),
			"example_inputs": shuttle.NewObjectSchema("Inputs for the test run before saving", nil, nil),
		},
		[]string{"name", "description", "script", "example_inputs"},
	)
}

func (t *saveTool) Execute(ctx context.Context, params map[string]interface{}) (*shuttle.Result, error) {
	start := time.Now()
	name, _ := params["name"].(string)
	description, _ := params["description"].(string)
	src, _ := params["script"].(string)
	example, _ := params["example_inputs"].(map[string]interface{})
	if name == "" || src == "" {
		return failed(start, "INVALID_PARAMS", "name and script are required", ""), nil
	}
	if err := CheckSyntax(src); err != nil {
		return failed(start, "SCRIPT_FAILED", err.Error(), "Fix the script and save it again"), nil
	}
	res, err := t.runner.Run(ctx, src, example)
	if err != nil {
		return failed(start, "SCRIPT_FAILED", "the test run failed: "+err.Error(), "Fix the script or the example inputs and save it again"), nil
	}
	if err := t.store.Save(owner(ctx), SavedScript{Name: name, Description: description, Script: src}); err != nil {
		return failed(start, "SAVE_FAILED", err.Error(), ""), nil
	}
	return done(start, map[string]interface{}{"name": name, "saved": true, "test_result": res.Value}), nil
}

func (t *saveTool) Backend() string { return "" }

// ---- run_saved_script ----

type runSavedTool struct {
	store  *Store
	runner Runner
}

func (t *runSavedTool) Name() string { return RunSavedName }

func (t *runSavedTool) Description() string {
	return `Run one of the person's saved calculation scripts (see list_saved_scripts) with new inputs and get its exact result.`
}

func (t *runSavedTool) InputSchema() *shuttle.JSONSchema {
	return shuttle.NewObjectSchema("A saved script to run",
		map[string]*shuttle.JSONSchema{
			"name":   shuttle.NewStringSchema("The saved script's name"),
			"inputs": shuttle.NewObjectSchema("Values the script reads from `inputs`", nil, nil),
		},
		[]string{"name"},
	)
}

func (t *runSavedTool) Execute(ctx context.Context, params map[string]interface{}) (*shuttle.Result, error) {
	start := time.Now()
	name, _ := params["name"].(string)
	inputs, _ := params["inputs"].(map[string]interface{})
	sc, err := t.store.Get(owner(ctx), name)
	if errors.Is(err, ErrScriptNotFound) {
		return failed(start, "NOT_FOUND", err.Error(), "Call list_saved_scripts for the names"), nil
	}
	if err != nil {
		return failed(start, "INTERNAL", err.Error(), ""), nil
	}
	res, err := t.runner.Run(ctx, sc.Script, inputs)
	if err != nil {
		data := map[string]interface{}{}
		if res.Output != "" {
			data["output"] = res.Output
		}
		r := failed(start, "SCRIPT_FAILED", err.Error(), "Check the inputs against the script's description")
		r.Data = data
		return r, nil
	}
	data := map[string]interface{}{"name": name, "result": res.Value}
	if res.Output != "" {
		data["output"] = res.Output
	}
	return done(start, data), nil
}

func (t *runSavedTool) Backend() string { return "" }

// ---- list_saved_scripts ----

type listSavedTool struct{ store *Store }

func (t *listSavedTool) Name() string { return ListSavedName }

func (t *listSavedTool) Description() string {
	return `List the person's saved calculation scripts: name, description and when each was last saved.`
}

func (t *listSavedTool) InputSchema() *shuttle.JSONSchema {
	return shuttle.NewObjectSchema("No parameters", map[string]*shuttle.JSONSchema{}, nil)
}

func (t *listSavedTool) Execute(ctx context.Context, _ map[string]interface{}) (*shuttle.Result, error) {
	start := time.Now()
	scripts, err := t.store.List(owner(ctx))
	if err != nil {
		return failed(start, "INTERNAL", err.Error(), ""), nil
	}
	list := make([]interface{}, 0, len(scripts))
	for _, sc := range scripts {
		list = append(list, map[string]interface{}{
			"name":        sc.Name,
			"description": sc.Description,
			"updated_at":  sc.UpdatedAt.Format(time.RFC3339),
		})
	}
	return done(start, map[string]interface{}{"scripts": list}), nil
}

func (t *listSavedTool) Backend() string { return "" }

// ---- delete_saved_script ----

type deleteSavedTool struct{ store *Store }

func (t *deleteSavedTool) Name() string { return DeleteSavedName }

func (t *deleteSavedTool) Description() string {
	return `Delete one of the person's saved calculation scripts, when they ask.`
}

func (t *deleteSavedTool) InputSchema() *shuttle.JSONSchema {
	return shuttle.NewObjectSchema("A saved script to delete",
		map[string]*shuttle.JSONSchema{"name": shuttle.NewStringSchema("The saved script's name")},
		[]string{"name"},
	)
}

func (t *deleteSavedTool) Execute(ctx context.Context, params map[string]interface{}) (*shuttle.Result, error) {
	start := time.Now()
	name, _ := params["name"].(string)
	if err := t.store.Delete(owner(ctx), name); err != nil {
		code := "INTERNAL"
		if errors.Is(err, ErrScriptNotFound) {
			code = "NOT_FOUND"
		}
		return failed(start, code, err.Error(), ""), nil
	}
	return done(start, map[string]interface{}{"name": name, "deleted": true}), nil
}

func (t *deleteSavedTool) Backend() string { return "" }
