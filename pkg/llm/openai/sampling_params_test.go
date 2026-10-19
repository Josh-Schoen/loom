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

package openai

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A Claude model behind the gateway that rejects sampling parameters gets no
// temperature (0 is omitted from the request); other models keep theirs.
func TestTemperatureParam_OmittedForModelsThatRejectIt(t *testing.T) {
	for model, want := range map[string]float64{
		"coding-agent/claude-fable-5-1": 0,
		"claude-opus-5-5":               0,
		"gpt-4o":                        0.2,
		"claude-sonnet-4-5-20250929":    0.2,
	} {
		c := NewClient(Config{Model: model, Temperature: 0.2})
		assert.Equal(t, want, c.temperatureParam(context.Background()), model)
	}
}
