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
package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/teradata-labs/loom/pkg/shuttle"
)

// Error codes reported to the model when a host tool does not succeed.
const (
	codeHostToolFailed      = "HOST_TOOL_FAILED"
	codeHostToolInvalidJSON = "HOST_TOOL_INVALID_JSON"
)

// HostTool is implemented by the host to give the agent a capability, such
// as a lookup in the application's own data.
type HostTool interface {
	// ToolName is the name the model uses to call the tool. It must be
	// unique within an engine.
	ToolName() string

	// ToolDescription tells the model what the tool does and when to use it.
	ToolDescription() string

	// InputSchemaJSON is a JSON Schema of type "object" describing the tool
	// arguments.
	InputSchemaJSON() string

	// Execute runs the tool. inputJSON is a JSON object of arguments. The
	// return value is any JSON value, or the empty string for no data. A
	// returned error is reported to the model as a failed tool call; it does
	// not abort the turn.
	Execute(inputJSON string) (string, error)
}

// toolAdapter presents a HostTool as a Loom tool. Name, description and
// schema are read once at registration so they cannot change under the
// agent and are not fetched across the language boundary on every turn.
type toolAdapter struct {
	host        HostTool
	name        string
	description string
	schema      *shuttle.JSONSchema
}

func newToolAdapter(host HostTool) (*toolAdapter, error) {
	if host == nil {
		return nil, errors.New("tool is required")
	}
	name := host.ToolName()
	if name == "" {
		return nil, errors.New("tool name is required")
	}
	description := host.ToolDescription()
	if description == "" {
		return nil, fmt.Errorf("tool %q: description is required", name)
	}
	schema := &shuttle.JSONSchema{}
	if err := json.Unmarshal([]byte(host.InputSchemaJSON()), schema); err != nil {
		return nil, fmt.Errorf("tool %q: input schema is not valid JSON: %w", name, err)
	}
	if schema.Type != "object" {
		return nil, fmt.Errorf("tool %q: input schema type must be \"object\", got %q", name, schema.Type)
	}
	return &toolAdapter{host: host, name: name, description: description, schema: schema}, nil
}

func (t *toolAdapter) Name() string                     { return t.name }
func (t *toolAdapter) Description() string              { return t.description }
func (t *toolAdapter) InputSchema() *shuttle.JSONSchema { return t.schema }
func (t *toolAdapter) Backend() string                  { return "" }

func (t *toolAdapter) Execute(ctx context.Context, params map[string]interface{}) (*shuttle.Result, error) {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input, err := json.Marshal(nonNilMap(params))
	if err != nil {
		return nil, fmt.Errorf("tool %q: encode input: %w", t.name, err)
	}
	out, err := t.host.Execute(string(input))
	if err != nil {
		return &shuttle.Result{
			Success:         false,
			Error:           &shuttle.Error{Code: codeHostToolFailed, Message: err.Error()},
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}, nil
	}
	var data interface{}
	if out != "" {
		if err := json.Unmarshal([]byte(out), &data); err != nil {
			return &shuttle.Result{
				Success: false,
				Error: &shuttle.Error{
					Code:    codeHostToolInvalidJSON,
					Message: fmt.Sprintf("tool %q returned a value that is not valid JSON: %v", t.name, err),
				},
				ExecutionTimeMs: time.Since(start).Milliseconds(),
			}, nil
		}
	}
	return &shuttle.Result{Success: true, Data: data, ExecutionTimeMs: time.Since(start).Milliseconds()}, nil
}
