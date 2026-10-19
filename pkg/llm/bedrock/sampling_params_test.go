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

package bedrock

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Models that reject sampling parameters never get a temperature, on either
// Bedrock client; older models keep the configured one.
func TestTemperature_OmittedForModelsThatRejectIt(t *testing.T) {
	for _, tc := range []struct {
		modelID  string
		wantTemp bool
	}{
		{"us.anthropic.claude-opus-5-v1:0", false},
		{"global.anthropic.claude-sonnet-5", false},
		{"us.anthropic.claude-sonnet-4-5-20250929-v1:0", true},
	} {
		sdk := &SDKClient{modelID: tc.modelID, maxTokens: 4096, temperature: 0.2}
		assert.Equal(t, tc.wantTemp, sdk.temperatureParam(context.Background()).Valid(), "SDKClient %s", tc.modelID)

		legacy := &Client{modelID: tc.modelID, temperature: 0.2}
		request := map[string]interface{}{}
		legacy.setTemperature(request)
		_, has := request["temperature"]
		assert.Equal(t, tc.wantTemp, has, "Client %s", tc.modelID)
	}
}
