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

package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/agent"
	"github.com/teradata-labs/loom/pkg/llm/scripted"
	"github.com/teradata-labs/loom/pkg/llm/spending"
	"github.com/teradata-labs/loom/pkg/observability"
)

// An agent's own models come from its YAML on every build (startup and hot
// reload), and building one never changes the shared server provider.
func TestBuildAgentLLMs(t *testing.T) {
	ledger, err := spending.Open(filepath.Join(t.TempDir(), "spending.json"), 0)
	require.NoError(t, err)
	guard := func(p agent.LLMProvider) agent.LLMProvider { return spending.Wrap(p, ledger) }
	serverDefault := guard(scripted.NewProvider(&scripted.Script{}, "server-model"))
	config := &Config{LLM: LLMConfig{AnthropicAPIKey: "sk-test", MaxTokens: 1000}}
	tracer := observability.NewNoOpTracer()

	plain := &loomv1.AgentConfig{Name: "plain"}
	roles := buildAgentLLMs(plain, serverDefault, config, tracer, guard, zap.NewNop())
	assert.Equal(t, "server-model", roles.primary.Model())
	assert.Empty(t, roles.opts)
	again := buildAgentLLMs(plain, serverDefault, config, tracer, guard, zap.NewNop())
	assert.Equal(t, "server-model", again.primary.Model())
	assert.Equal(t, "server-model", serverDefault.Model(), "the shared provider is left as it was")

	own := &loomv1.AgentConfig{
		Name:          "assistant",
		Llm:           &loomv1.LLMConfig{Provider: "anthropic", Model: "claude-opus-5-5"},
		CompressorLlm: &loomv1.LLMConfig{Provider: "anthropic", Model: "claude-haiku-5-5"},
	}
	roles = buildAgentLLMs(own, serverDefault, config, tracer, guard, zap.NewNop())
	assert.Equal(t, "claude-opus-5-5", roles.primary.Model())
	assert.Len(t, roles.opts, 1, "the compressor model is wired")
	assert.Nil(t, roles.classifier)
}
