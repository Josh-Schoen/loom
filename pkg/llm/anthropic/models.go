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

package anthropic

import "strings"

// Request rules that depend on the model. Newer Claude models reject request
// fields older ones accepted, so the client adjusts each request to the
// model it names.

// noSamplingPrefixes are model families that reject `temperature`, `top_p`,
// and `top_k`: removed on Claude Opus 4.7 and later Opus, Fable, and Mythos
// (any value is a 400), and limited to the default value on Claude Sonnet 5.x
// and Haiku 5.x (a non-default value is a 400). The client omits sampling
// parameters for all of them.
var noSamplingPrefixes = []string{
	"claude-opus-4-7",
	"claude-opus-4-8",
	"claude-opus-5",
	"claude-fable-",
	"claude-mythos-",
	"claude-sonnet-5",
	"claude-haiku-5",
}

// acceptsSamplingParams reports whether model accepts a temperature.
func acceptsSamplingParams(model string) bool {
	for _, prefix := range noSamplingPrefixes {
		if strings.HasPrefix(model, prefix) {
			return false
		}
	}
	return true
}

// defaultFallbackModels support the server-side refusal fallback
// ("fallbacks": "default", beta server-side-fallback-2026-07-01) on the
// Claude API: when a safety classifier declines a request, the API retries
// it on a model chosen for the refusal category instead of returning an
// empty refusal.
var defaultFallbackModels = []string{
	"claude-opus-5-5",
	"claude-opus-5",
	"claude-fable-5-1",
	"claude-sonnet-5-5",
}

// serverSideFallbackBeta is the beta that enables "fallbacks": "default".
const serverSideFallbackBeta = "server-side-fallback-2026-07-01"

// promptCachingBeta is sent on every request (cached tokens do not count
// against the input-tokens-per-minute rate limit).
const promptCachingBeta = "prompt-caching-2024-07-31"

func supportsDefaultFallback(model, endpoint string) bool {
	// The "default" form is a Claude API feature; other endpoints (proxies,
	// cloud platforms) may reject the field.
	if !strings.HasPrefix(endpoint, "https://api.anthropic.com/") {
		return false
	}
	for _, m := range defaultFallbackModels {
		if model == m {
			return true
		}
	}
	return false
}

// applyModelRules adjusts req for its model and returns the anthropic-beta
// header value to send with it.
func applyModelRules(req *MessagesRequest, endpoint string) string {
	if !acceptsSamplingParams(req.Model) {
		req.Temperature = 0 // omitted (omitempty)
	}
	betas := []string{promptCachingBeta}
	if supportsDefaultFallback(req.Model, endpoint) {
		req.Fallbacks = "default"
		betas = append(betas, serverSideFallbackBeta)
	}
	return strings.Join(betas, ",")
}
