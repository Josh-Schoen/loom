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

package agent

import (
	"path/filepath"
	"strings"
	"testing"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
)

// A per-agent inline provider "anthropic" must accept the key however the
// deployment carries it: raw ANTHROPIC_API_KEY, or Loom's own config env
// LOOM_LLM_ANTHROPIC_API_KEY (the only one a config-keyed server sets).
// Verified live: a gateway-keyed daemon built its default provider fine
// while every pack agent naming provider "anthropic" inline failed.
func TestCreateLLMProviderAnthropicKeyFallback(t *testing.T) {
	dir := t.TempDir()
	registry, err := NewRegistry(RegistryConfig{
		ConfigDir: dir,
		DBPath:    filepath.Join(dir, "agents.db"),
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close() })

	cfg := &loomv1.LLMConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"}

	t.Run("raw env key", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "sk-raw")
		t.Setenv("LOOM_LLM_ANTHROPIC_API_KEY", "")
		if _, err := registry.createLLMProvider(cfg); err != nil {
			t.Errorf("provider should build from ANTHROPIC_API_KEY: %v", err)
		}
	})

	t.Run("loom config env key", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "")
		t.Setenv("LOOM_LLM_ANTHROPIC_API_KEY", "sk-config")
		if _, err := registry.createLLMProvider(cfg); err != nil {
			t.Errorf("provider should fall back to LOOM_LLM_ANTHROPIC_API_KEY: %v", err)
		}
	})

	t.Run("neither key names both", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "")
		t.Setenv("LOOM_LLM_ANTHROPIC_API_KEY", "")
		_, err := registry.createLLMProvider(cfg)
		if err == nil {
			t.Fatal("expected an error with no key set")
		}
		if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") ||
			!strings.Contains(err.Error(), "LOOM_LLM_ANTHROPIC_API_KEY") {
			t.Errorf("error should name both env vars: %v", err)
		}
	})
}
