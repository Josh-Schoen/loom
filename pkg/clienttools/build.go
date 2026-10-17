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
package clienttools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/observability"
	"github.com/teradata-labs/loom/pkg/shuttle"
	"github.com/teradata-labs/loom/pkg/types"
)

// Defaults for the per-call wait. Operators override them through Config
// (looms: server.client_tools.*).
const (
	DefaultTimeout    = 300 * time.Second
	DefaultMaxTimeout = 900 * time.Second
)

// toolNamePattern is the tool-name shape every supported LLM provider accepts
// (the strictest common subset of the providers' tool-name rules).
var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Config is the server's timeout policy for client tool calls.
type Config struct {
	// DefaultTimeout applies when a request sets no timeout.
	DefaultTimeout time.Duration
	// MaxTimeout caps any requested timeout.
	MaxTimeout time.Duration
}

// DefaultConfig returns the built-in timeout policy.
func DefaultConfig() Config {
	return Config{DefaultTimeout: DefaultTimeout, MaxTimeout: DefaultMaxTimeout}
}

// Timeout resolves a request's client_tool_timeout_seconds against the policy:
// non-positive means the default, and anything above the cap is capped.
func (c Config) Timeout(requestedSeconds int32) time.Duration {
	timeout := c.DefaultTimeout
	if requestedSeconds > 0 {
		timeout = time.Duration(requestedSeconds) * time.Second
	}
	if c.MaxTimeout > 0 && timeout > c.MaxTimeout {
		timeout = c.MaxTimeout
	}
	return timeout
}

// Spec is a validated client tool definition, not yet bound to a call.
type Spec struct {
	Name        string
	Description string
	Schema      *shuttle.JSONSchema
}

// Parse validates a request's client tool definitions. isReserved reports
// names the agent can already reach; a client tool may not shadow one. Errors
// describe the first invalid definition and are meant for an INVALID_ARGUMENT
// status.
func Parse(defs []*loomv1.ToolDefinition, isReserved func(name string) bool) ([]Spec, error) {
	if len(defs) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(defs))
	specs := make([]Spec, 0, len(defs))
	for i, def := range defs {
		name := def.GetName()
		if !toolNamePattern.MatchString(name) {
			return nil, fmt.Errorf("client_tools[%d]: name %q must match %s", i, name, toolNamePattern.String())
		}
		if seen[name] {
			return nil, fmt.Errorf("client_tools[%d]: duplicate name %q", i, name)
		}
		seen[name] = true
		if isReserved(name) {
			return nil, fmt.Errorf("client_tools[%d]: name %q is already an agent tool", i, name)
		}
		schema, err := parseInputSchema(def.GetInputSchemaJson())
		if err != nil {
			return nil, fmt.Errorf("client_tools[%d] (%s): %w", i, name, err)
		}
		specs = append(specs, Spec{Name: name, Description: def.GetDescription(), Schema: schema})
	}
	return specs, nil
}

// Call carries what the tools of one StreamWeave call share.
type Call struct {
	SessionID   string
	OwnerUserID string
	Timeout     time.Duration
	Broker      *Broker
	Emit        types.ProgressCallback
	Tracer      observability.Tracer
}

// Bind turns validated specs into tools bound to call.
func Bind(specs []Spec, call Call) []shuttle.Tool {
	tools := make([]shuttle.Tool, 0, len(specs))
	for _, spec := range specs {
		tools = append(tools, &Tool{
			name:        spec.Name,
			description: spec.Description,
			schema:      spec.Schema,
			sessionID:   call.SessionID,
			ownerUserID: call.OwnerUserID,
			timeout:     call.Timeout,
			heartbeat:   shuttle.HoldHeartbeatInterval,
			broker:      call.Broker,
			emit:        call.Emit,
			tracer:      call.Tracer,
		})
	}
	return tools
}

// parseInputSchema decodes input_schema_json. An empty schema means a tool
// with no parameters. The top level must be an object schema, which is what
// every provider's tool-calling API requires.
func parseInputSchema(raw string) (*shuttle.JSONSchema, error) {
	if raw == "" {
		return &shuttle.JSONSchema{Type: "object"}, nil
	}
	var schema shuttle.JSONSchema
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		return nil, fmt.Errorf("input_schema_json is not valid JSON Schema: %w", err)
	}
	if schema.Type != "object" {
		return nil, fmt.Errorf("input_schema_json must have type \"object\", got %q", schema.Type)
	}
	return &schema, nil
}
