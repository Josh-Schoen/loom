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
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
)

func pairingCfg() *Config {
	c := &Config{}
	c.Server.HTTPPort = 5006
	c.Server.DevicePairing = DevicePairingConfig{Enabled: true, LinkScheme: "loompair", CodeTTLSeconds: 600}
	return c
}

func TestValidateDevicePairing(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr bool
	}{
		{"disabled passes", func(c *Config) { c.Server.DevicePairing = DevicePairingConfig{} }, false},
		{"defaults pass", func(*Config) {}, false},
		{"https public url passes", func(c *Config) { c.Server.DevicePairing.PublicURL = "https://assistant.example.com" }, false},
		{"ttl too short", func(c *Config) { c.Server.DevicePairing.CodeTTLSeconds = 10 }, true},
		{"ttl too long", func(c *Config) { c.Server.DevicePairing.CodeTTLSeconds = 7200 }, true},
		{"bad scheme", func(c *Config) { c.Server.DevicePairing.LinkScheme = "1loom" }, true},
		{"empty scheme", func(c *Config) { c.Server.DevicePairing.LinkScheme = "" }, true},
		{"public url without scheme", func(c *Config) { c.Server.DevicePairing.PublicURL = "assistant.example.com" }, true},
		{"public url wrong scheme", func(c *Config) { c.Server.DevicePairing.PublicURL = "ftp://host" }, true},
		{"no gateway and no public url", func(c *Config) { c.Server.HTTPPort = 0 }, true},
		{"no gateway with public url", func(c *Config) {
			c.Server.HTTPPort = 0
			c.Server.DevicePairing.PublicURL = "https://proxy.example.com"
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := pairingCfg()
			tt.mutate(c)
			err := c.validateDevicePairing()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestWriteQRUsesExplicitColors(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeQR(&buf, "loompair://pair?code=ABC&server=http%3A%2F%2F10.0.0.2%3A5006"))
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.NotEmpty(t, lines)
	for _, line := range lines {
		assert.True(t, strings.HasSuffix(line, "\x1b[0m"), "each line resets colors")
		// Only black (30/40) and white (37/47) cells, never the theme's default colors.
		cells := strings.Split(strings.TrimSuffix(line, "\x1b[0m"), "▀")
		for _, cell := range cells[:len(cells)-1] {
			assert.Contains(t, []string{"\x1b[30;40m", "\x1b[30;47m", "\x1b[37;40m", "\x1b[37;47m"}, cell)
		}
	}
	// The quiet zone keeps the first row light.
	assert.Equal(t, strings.Repeat("\x1b[37;47m▀", 4), lines[0][:len("\x1b[37;47m▀")*4])
}

func TestPrintPairingLinkOnly(t *testing.T) {
	resp := &loomv1.CreatePairingCodeResponse{
		PairingLink: "loompair://pair?code=X&server=http%3A%2F%2F10.0.0.2%3A5006",
		ServerUrl:   "http://10.0.0.2:5006",
		ExpiresAt:   timestamppb.Now(),
	}
	var buf bytes.Buffer
	printPairing(&buf, resp, true)
	assert.Equal(t, resp.GetPairingLink()+"\n", buf.String())

	buf.Reset()
	printPairing(&buf, resp, false)
	assert.Contains(t, buf.String(), resp.GetPairingLink())
	assert.Contains(t, buf.String(), "http://10.0.0.2:5006")
}

func TestPrintDevices(t *testing.T) {
	var buf bytes.Buffer
	printDevices(&buf, nil)
	assert.Contains(t, buf.String(), "looms pair")

	buf.Reset()
	printDevices(&buf, []*loomv1.Device{
		{Id: "dev_1", Name: "Phone", Platform: "ios", UserId: "u", CreatedAt: timestamppb.Now(), LastSeenAt: timestamppb.Now()},
		{Id: "dev_2", Name: "Old", Platform: "ios", UserId: "u", CreatedAt: timestamppb.Now(), LastSeenAt: timestamppb.Now(), RevokedAt: timestamppb.Now()},
	})
	out := buf.String()
	assert.Contains(t, out, "dev_1")
	assert.Contains(t, out, "active")
	assert.Contains(t, out, "revoked")
}
