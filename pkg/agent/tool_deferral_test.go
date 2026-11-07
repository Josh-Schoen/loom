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
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teradata-labs/loom/pkg/shuttle"
)

// bigTool returns a tool whose advertised size is roughly padBytes.
func bigTool(name string, padBytes int) shuttle.Tool {
	return &shuttle.MockTool{MockName: name, MockDescription: strings.Repeat("d", padBytes)}
}

func deferralTestAgent(cfg ToolDeferralConfig, tools ...shuttle.Tool) *Agent {
	a := &Agent{
		tools:             shuttle.NewRegistry(),
		config:            &Config{ToolDeferral: cfg},
		sessionToolLedger: make(map[string]map[string]bool),
		scopedToolNames:   make(map[string]bool),
	}
	for _, t := range tools {
		a.tools.Register(t)
	}
	return a
}

func deferralNames(a *Agent, sessionID string) []string {
	var names []string
	for _, t := range a.advertisedTools(context.Background(), &Session{ID: sessionID}) {
		names = append(names, t.Name())
	}
	sort.Strings(names)
	return names
}

func TestToolDeferral_Off(t *testing.T) {
	a := deferralTestAgent(ToolDeferralConfig{}, mockTool("tool_search"), mockTool("a"), mockTool("b"))
	assert.Equal(t, []string{"a", "b", "tool_search"}, deferralNames(a, "s1"))
}

func TestToolDeferral_OnHidesUndiscovered(t *testing.T) {
	a := deferralTestAgent(ToolDeferralConfig{Mode: DeferralOn},
		mockTool("tool_search"), mockTool("recall"), mockTool("pinned"), mockTool("vantage:query"),
		mockTool("slack:send_message"), mockTool("plain_tool"))
	a.SetToolDeferral(ToolDeferralConfig{Mode: DeferralOn, AlwaysOn: []string{"pinned", "vantage:*"}})

	got := deferralNames(a, "s1")
	assert.Equal(t, []string{"pinned", "recall", "tool_search", "vantage:query"}, got,
		"internal tools, tool_search and always-on entries (exact and prefix) stay advertised")

	// Hidden tools are still registered, so they remain executable.
	assert.True(t, a.tools.IsRegistered("slack:send_message"))
	assert.True(t, a.tools.IsRegistered("plain_tool"))
}

func TestToolDeferral_DiscoveryRevealsPerSession(t *testing.T) {
	a := deferralTestAgent(ToolDeferralConfig{Mode: DeferralOn},
		mockTool("tool_search"), mockTool("slack:send_message"), mockTool("plain_tool"))

	// Plain name matches the server-qualified registered tool.
	a.DiscoverTools("s1", "send_message")
	assert.Equal(t, []string{"slack:send_message", "tool_search"}, deferralNames(a, "s1"))
	assert.Equal(t, []string{"tool_search"}, deferralNames(a, "s2"), "another session must not see s1's discoveries")

	// Server-qualified form matches too.
	a.DiscoverTools("s2", "slack:send_message", "plain_tool")
	assert.Equal(t, []string{"plain_tool", "slack:send_message", "tool_search"}, deferralNames(a, "s2"))
}

func TestToolDeferral_RequiresToolSearch(t *testing.T) {
	a := deferralTestAgent(ToolDeferralConfig{Mode: DeferralOn}, mockTool("a"), mockTool("b"))
	assert.Equal(t, []string{"a", "b"}, deferralNames(a, "s1"),
		"without tool_search nothing is hidden: the model could not find the tools again")
}

func TestToolDeferral_SessionLedgerStaysAdvertised(t *testing.T) {
	a := deferralTestAgent(ToolDeferralConfig{Mode: DeferralOn}, mockTool("tool_search"), mockTool("skill_tool"), mockTool("other"))
	a.captureBaseTools()
	a.registerSessionTool("s1", "skill_tool")

	assert.Equal(t, []string{"skill_tool", "tool_search"}, deferralNames(a, "s1"),
		"a tool a skill advertised to the session is not deferred")
}

func TestToolDeferral_RuntimeToggle(t *testing.T) {
	a := deferralTestAgent(ToolDeferralConfig{}, mockTool("tool_search"), mockTool("a"), mockTool("b"))
	require.Equal(t, []string{"a", "b", "tool_search"}, deferralNames(a, "s1"))

	a.SetToolDeferral(ToolDeferralConfig{Mode: DeferralOn})
	assert.Equal(t, []string{"tool_search"}, deferralNames(a, "s1"), "turning on takes effect on the next call")
	assert.Equal(t, DeferralOn, a.ToolDeferral().Mode)

	a.SetToolDeferral(ToolDeferralConfig{Mode: DeferralOff})
	assert.Equal(t, []string{"a", "b", "tool_search"}, deferralNames(a, "s1"), "turning off restores every tool")
}

func TestToolDeferral_SessionOverride(t *testing.T) {
	a := deferralTestAgent(ToolDeferralConfig{Mode: DeferralOn}, mockTool("tool_search"), mockTool("a"))

	a.SetSessionToolDeferral("s1", DeferralOff)
	assert.Equal(t, []string{"a", "tool_search"}, deferralNames(a, "s1"), "session override beats the agent mode")
	assert.Equal(t, []string{"tool_search"}, deferralNames(a, "s2"), "other sessions follow the agent mode")

	a.ClearSessionToolDeferral("s1")
	assert.Equal(t, []string{"tool_search"}, deferralNames(a, "s1"))

	a.SetToolDeferral(ToolDeferralConfig{Mode: DeferralOff})
	a.SetSessionToolDeferral("s3", DeferralOn)
	assert.Equal(t, []string{"tool_search"}, deferralNames(a, "s3"), "a session can turn deferral on while the agent default is off")
}

func TestToolDeferral_AutoThresholdAndHysteresis(t *testing.T) {
	a := deferralTestAgent(ToolDeferralConfig{Mode: DeferralAuto, ThresholdBytes: 10_000},
		mockTool("tool_search"), bigTool("t1", 3000), bigTool("t2", 3000))

	assert.Len(t, deferralNames(a, "s1"), 3, "below the threshold everything is advertised")

	a.tools.Register(bigTool("t3", 3000))
	a.tools.Register(bigTool("t4", 3000))
	assert.Equal(t, []string{"tool_search"}, deferralNames(a, "s1"), "above the threshold deferral switches on")

	// Shrink to between the off-line (70%) and the threshold: the decision holds.
	a.tools.Unregister("t4")
	a.tools.Unregister("t3")
	a.tools.Register(bigTool("t3", 2000)) // ~7.1 KB total is above 70% of 10 KB
	a.DiscoverTools("s1")                 // no-op, keeps the call set small
	assert.Equal(t, []string{"tool_search"}, deferralNames(a, "s1"), "inside the hysteresis band the decision is sticky")

	// The decision is per session: s2 has not crossed the threshold, and the
	// list (about 8 KB) is under it, so s2 still sees everything.
	assert.Len(t, deferralNames(a, "s2"), 4, "auto decisions are independent per session")

	// Fall well under 70% and it switches back off.
	a.tools.Unregister("t3")
	assert.Len(t, deferralNames(a, "s1"), 3, "below 70% of the threshold deferral switches off again")
}

func TestToolDeferral_AutoEvaluatesFullListNotTrimmedList(t *testing.T) {
	// If auto measured the already-deferred list it would switch itself off on
	// the second call. It must keep measuring the full list.
	a := deferralTestAgent(ToolDeferralConfig{Mode: DeferralAuto, ThresholdBytes: 5_000},
		mockTool("tool_search"), bigTool("t1", 3000), bigTool("t2", 3000))
	for i := 0; i < 3; i++ {
		assert.Equal(t, []string{"tool_search"}, deferralNames(a, "s1"), "call %d", i)
	}
}

func TestToolDeferral_ForgetAndReset(t *testing.T) {
	a := deferralTestAgent(ToolDeferralConfig{Mode: DeferralOn}, mockTool("tool_search"), mockTool("a"))
	a.DiscoverTools("s1", "a")
	a.SetSessionToolDeferral("s1", DeferralOn)
	require.Equal(t, []string{"a", "tool_search"}, deferralNames(a, "s1"))

	a.forgetSessionDeferral("s1")
	assert.Equal(t, []string{"tool_search"}, deferralNames(a, "s1"), "discoveries are dropped with the session")

	a.DiscoverTools("s2", "a")
	a.resetDeferralState()
	assert.Equal(t, []string{"tool_search"}, deferralNames(a, "s2"))
}

func TestParseDeferralMode(t *testing.T) {
	tests := []struct {
		in   string
		want DeferralMode
		ok   bool
	}{
		{"", DeferralOff, true}, {"off", DeferralOff, true}, {"ON", DeferralOn, true},
		{" auto ", DeferralAuto, true}, {"sometimes", DeferralOff, false},
	}
	for _, tt := range tests {
		got, ok := ParseDeferralMode(tt.in)
		assert.Equal(t, tt.want, got, tt.in)
		assert.Equal(t, tt.ok, ok, tt.in)
	}
}

func TestToolDeferral_Race(t *testing.T) {
	a := deferralTestAgent(ToolDeferralConfig{Mode: DeferralAuto, ThresholdBytes: 2_000},
		mockTool("tool_search"), bigTool("t1", 3000), mockTool("slack:send_message"))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sid := fmt.Sprintf("s%d", i%3)
			for j := 0; j < 50; j++ {
				switch (i + j) % 5 {
				case 0:
					a.DiscoverTools(sid, "send_message")
				case 1:
					a.SetSessionToolDeferral(sid, DeferralMode(j%3))
				case 2:
					a.SetToolDeferral(ToolDeferralConfig{Mode: DeferralMode(j % 3), ThresholdBytes: 2_000})
				case 3:
					a.ClearSessionToolDeferral(sid)
				default:
					_ = deferralNames(a, sid)
				}
			}
		}(i)
	}
	wg.Wait()
}
