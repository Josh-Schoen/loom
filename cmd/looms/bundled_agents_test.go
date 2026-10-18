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
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Existing servers keep getting the weaver and guide unless they opt out.
func TestInstallBundled_DefaultTrue(t *testing.T) {
	viper.Reset()
	setDefaults()
	assert.True(t, viper.GetBool("agents.install_bundled"))
}

func TestInstallBundledAgents(t *testing.T) {
	dir := t.TempDir()
	installBundledAgents(dir, zap.NewNop())
	for _, file := range []string{"agents/weaver.yaml", "agents/guide.yaml", "skills/weaver-creation.yaml"} {
		assert.FileExists(t, filepath.Join(dir, file))
	}

	// An edited copy is kept.
	weaver := filepath.Join(dir, "agents", "weaver.yaml")
	require.NoError(t, os.WriteFile(weaver, []byte("edited"), 0o600))
	installBundledAgents(dir, zap.NewNop())
	data, err := os.ReadFile(weaver)
	require.NoError(t, err)
	assert.Equal(t, "edited", string(data))
}
