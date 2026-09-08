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

import (
	"os"
	"testing"
)

func TestNormalizeEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{"empty defers to the caller's default", "", ""},
		{
			// The bug this exists for: requests are POSTed to Endpoint verbatim,
			// so an origin on its own returned 405 Method Not Allowed.
			name:     "bare origin gains the messages path",
			endpoint: "https://gateway.example.com",
			want:     "https://gateway.example.com/v1/messages",
		},
		{"trailing slash trimmed", "https://gateway.example.com/", "https://gateway.example.com/v1/messages"},
		{"surrounding whitespace trimmed", "  https://gateway.example.com  ", "https://gateway.example.com/v1/messages"},
		{"host with port", "http://localhost:4000", "http://localhost:4000/v1/messages"},
		{"explicit messages path preserved", "https://gateway.example.com/v1/messages", "https://gateway.example.com/v1/messages"},
		{"non-standard path preserved", "https://gateway.example.com/proxy/anthropic/v1/messages", "https://gateway.example.com/proxy/anthropic/v1/messages"},
		{"the default is already normalised", DefaultAnthropicEndpoint, DefaultAnthropicEndpoint},
		{"unparseable input passed through", "not a url", "not a url"},
		{"scheme without host passed through", "https://", "https:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeEndpoint(tt.endpoint); got != tt.want {
				t.Errorf("NormalizeEndpoint(%q) = %q, want %q", tt.endpoint, got, tt.want)
			}
		})
	}
}

// TestNewClientNormalizesEndpoint covers both routes into Config.Endpoint: an
// explicit value and the ANTHROPIC_API_ENDPOINT fallback.
func TestNewClientNormalizesEndpoint(t *testing.T) {
	t.Run("explicit config value", func(t *testing.T) {
		c := NewClient(Config{APIKey: "test", Endpoint: "https://gateway.example.com"})
		if c.endpoint != "https://gateway.example.com/v1/messages" {
			t.Errorf("endpoint = %q, want the messages path appended", c.endpoint)
		}
	})

	t.Run("environment fallback", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_ENDPOINT", "https://env-gateway.example.com")
		c := NewClient(Config{APIKey: "test"})
		if c.endpoint != "https://env-gateway.example.com/v1/messages" {
			t.Errorf("endpoint = %q, want the messages path appended", c.endpoint)
		}
	})

	t.Run("unset environment uses the default", func(t *testing.T) {
		if err := os.Unsetenv("ANTHROPIC_API_ENDPOINT"); err != nil {
			t.Fatalf("unsetenv: %v", err)
		}
		c := NewClient(Config{APIKey: "test"})
		if c.endpoint != DefaultAnthropicEndpoint {
			t.Errorf("endpoint = %q, want %q", c.endpoint, DefaultAnthropicEndpoint)
		}
	})
}
