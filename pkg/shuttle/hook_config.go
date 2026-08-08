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
package shuttle

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// HooksConfig is the `tools.hooks` block Viper unmarshals: an ordered list of
// library-policy bindings. It lives in package shuttle (not cmd/looms) so a
// library builder can construct an admission chain without importing main.
// The json tags define the wire shape an out-of-process host (e.g. an agent
// profile's hooks_config) transports; encoding/json matches them
// case-insensitively, so the historical Go-field-name casing keeps decoding.
type HooksConfig struct {
	Bindings []HookBinding `mapstructure:"hooks" json:"bindings"`
}

// ParseHooksConfig decodes a JSON hooks config and refuses the silent-empty
// trap: a document whose keys miss the schema fails loudly (unknown fields are
// rejected), so a mis-keyed config errors at the save door instead of building
// a chain that governs nothing. A genuinely empty document (empty input, "{}",
// "null", or an explicitly empty bindings list) parses to an empty config.
func ParseHooksConfig(raw []byte) (HooksConfig, error) {
	var cfg HooksConfig
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("{}")) || bytes.Equal(trimmed, []byte("null")) {
		return cfg, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return HooksConfig{}, fmt.Errorf("hooks config: %w", err)
	}
	return cfg, nil
}

// HookBinding declares one library policy purely from config: which policy
// (Kind), the tools it governs (Scope), the params it selects on (Matcher), and
// the policy-specific parameters. A binding is turned into a live Hook by
// BuildChainFromConfig; a malformed binding fails serve startup (fail-closed).
type HookBinding struct {
	// Kind selects the policy: "gated-allowlist" | "denylist" | "audit" | "ask" | "custom".
	Kind string `mapstructure:"kind" json:"kind"`
	// Scope is the tool selector: an exact tool name or a "<prefix>*" pattern,
	// with the same match semantics as tools.permissions.
	Scope string `mapstructure:"scope" json:"scope"`
	// Matcher selects calls by their params, including a dispatch tool's
	// carried/nested op addressed by path. An absent matcher selects every call
	// to the scoped tools (a scope-only binding).
	Matcher MatcherSpec `mapstructure:"matcher" json:"matcher"`
	// StateKey names the approved-set partition a gated-allowlist reads.
	StateKey string `mapstructure:"state_key" json:"state_key"`
	// ReadPattern classifies a read-only call (gated-allowlist): anchored to
	// each statement's head, and every statement of the payload must match.
	ReadPattern string `mapstructure:"read_pattern" json:"read_pattern"`
	// StmtParam names the param holding the statement whose identity is checked
	// (gated-allowlist, required); shared with the render/record side's
	// canonicalization.
	StmtParam string `mapstructure:"stmt_param" json:"stmt_param"`
	// Pattern is not a valid field: a denylist selects calls with Matcher.
	// Declared only so a config that sets it fails validation loudly instead of
	// being dropped by the decoder.
	Pattern string `mapstructure:"pattern" json:"pattern"`
	// SourceTool names the render/record tool a gated-allowlist trusts as the
	// source of approved-set entries.
	SourceTool string `mapstructure:"source_tool" json:"source_tool"`
	// ResultPath locates the rendered statements within a render Result.
	ResultPath string `mapstructure:"result_path" json:"result_path"`
	// Name is the registry key of a custom hook.
	Name string `mapstructure:"name" json:"name"`
}
