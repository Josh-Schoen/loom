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
	"strings"

	"github.com/teradata-labs/loom/pkg/shuttle"
)

// toolSearchName is the tool a session uses to discover deferred tools. Tool
// deferral never activates for an agent that has not registered it, because the
// model would have no way to find the tools that were hidden.
const toolSearchName = "tool_search"

// DefaultDeferralThresholdBytes is the advertised-tool size above which
// DeferralAuto switches deferral on. It is a starting point, not a measured
// optimum: the offline benchmark (pkg/tools/toolbench) puts 100 tools at about
// 47 KB, and the effect on model accuracy has not been measured yet.
const DefaultDeferralThresholdBytes = 40 * 1024

// autoOffPercent is the fraction of the threshold a session's tool list must
// fall below before DeferralAuto switches deferral back off. The gap prevents a
// list hovering near the threshold from flipping on and off, because every flip
// changes the tools array and breaks the provider's cached prompt prefix.
const autoOffPercent = 70

// internalToolNames are agent-internal tools that are never deferred: the
// agent's own prompts and memory machinery reference them directly.
var internalToolNames = map[string]bool{
	"query_tool_result": true,
	"recall":            true,
	"graph_memory":      true,
	"task_board":        true,
	"manage_skills":     true,
	"load_pattern":      true,
}

// DeferralMode controls whether tools are advertised upfront or only after a
// session discovers them with tool_search.
type DeferralMode int

const (
	// DeferralOff advertises every tool upfront. This is the zero value.
	DeferralOff DeferralMode = iota
	// DeferralOn hides every deferrable tool until the session discovers it.
	DeferralOn
	// DeferralAuto defers when the advertised tool list exceeds a size
	// threshold, and keeps the decision stable per session (see autoOffPercent).
	DeferralAuto
)

func (m DeferralMode) String() string {
	switch m {
	case DeferralOff:
		return "off"
	case DeferralOn:
		return "on"
	case DeferralAuto:
		return "auto"
	default:
		return "unknown"
	}
}

// ParseDeferralMode converts "off", "on" or "auto" (case-insensitive; empty
// means off) to a DeferralMode.
func ParseDeferralMode(s string) (DeferralMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "off":
		return DeferralOff, true
	case "on":
		return DeferralOn, true
	case "auto":
		return DeferralAuto, true
	default:
		return DeferralOff, false
	}
}

// ToolDeferralConfig configures tool deferral for an agent.
type ToolDeferralConfig struct {
	// Mode selects off, on or auto. The zero value is off.
	Mode DeferralMode

	// ThresholdBytes is the DeferralAuto trigger: the size of the full
	// advertised tool list (names, descriptions and schemas) at which deferral
	// switches on. Zero means DefaultDeferralThresholdBytes.
	ThresholdBytes int

	// AlwaysOn lists tools that are advertised even while deferral is active.
	// An entry ending in "*" matches by prefix, e.g. "vantage:*" keeps every
	// tool of the "vantage" MCP server upfront.
	AlwaysOn []string
}

func (c ToolDeferralConfig) threshold() int {
	if c.ThresholdBytes > 0 {
		return c.ThresholdBytes
	}
	return DefaultDeferralThresholdBytes
}

func (c ToolDeferralConfig) alwaysOn(name string) bool {
	for _, pattern := range c.AlwaysOn {
		if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		} else if pattern == name {
			return true
		}
	}
	return false
}

// ensureDeferralMapsLocked allocates the per-session maps. NewAgent does this
// already; it keeps a zero-value Agent (as some tests build) from panicking on
// the first write. Caller holds deferralMu for writing.
func (a *Agent) ensureDeferralMapsLocked() {
	if a.sessionDeferral == nil {
		a.sessionDeferral = make(map[string]DeferralMode)
	}
	if a.sessionAutoOn == nil {
		a.sessionAutoOn = make(map[string]bool)
	}
	if a.sessionDiscovered == nil {
		a.sessionDiscovered = make(map[string]map[string]bool)
	}
}

// SetToolDeferral replaces the agent's deferral settings at runtime. The change
// applies to the next provider call of every session, with no restart. It takes
// precedence over Config.ToolDeferral until the agent is rebuilt.
//
// Changing the mode changes the advertised tool list, which breaks the
// provider's cached prompt prefix once for each affected session.
func (a *Agent) SetToolDeferral(cfg ToolDeferralConfig) {
	a.deferralMu.Lock()
	defer a.deferralMu.Unlock()
	a.ensureDeferralMapsLocked()
	a.deferralOverride = &cfg
	// Auto decisions were made against the old threshold.
	a.sessionAutoOn = make(map[string]bool)
}

// ToolDeferral returns the deferral settings currently in force for the agent:
// the runtime setting if SetToolDeferral was called, else Config.ToolDeferral.
func (a *Agent) ToolDeferral() ToolDeferralConfig {
	a.deferralMu.RLock()
	defer a.deferralMu.RUnlock()
	return a.toolDeferralLocked()
}

func (a *Agent) toolDeferralLocked() ToolDeferralConfig {
	if a.deferralOverride != nil {
		return *a.deferralOverride
	}
	if a.config != nil {
		return a.config.ToolDeferral
	}
	return ToolDeferralConfig{}
}

// SetSessionToolDeferral overrides the deferral mode for one session. The
// override wins over the agent-wide mode; thresholds and the always-on list
// still come from the agent-wide settings.
func (a *Agent) SetSessionToolDeferral(sessionID string, mode DeferralMode) {
	if sessionID == "" {
		sessionID = a.GetID()
	}
	a.deferralMu.Lock()
	defer a.deferralMu.Unlock()
	a.ensureDeferralMapsLocked()
	a.sessionDeferral[sessionID] = mode
	delete(a.sessionAutoOn, sessionID)
}

// ClearSessionToolDeferral removes a session's override so it follows the
// agent-wide mode again.
func (a *Agent) ClearSessionToolDeferral(sessionID string) {
	if sessionID == "" {
		sessionID = a.GetID()
	}
	a.deferralMu.Lock()
	defer a.deferralMu.Unlock()
	delete(a.sessionDeferral, sessionID)
	delete(a.sessionAutoOn, sessionID)
}

// DiscoverTools records that sessionID has discovered the named tools, so a
// deferred tool is advertised to that session from its next provider call on.
// Names may be plain ("send_message") or server-qualified ("slack:send_message");
// a registered tool matches either form. tool_search calls this for the tools it
// returns. It affects only deferral; it never marks a tool session-scoped.
func (a *Agent) DiscoverTools(sessionID string, names ...string) {
	if len(names) == 0 {
		return
	}
	if sessionID == "" {
		sessionID = a.GetID()
	}
	a.deferralMu.Lock()
	defer a.deferralMu.Unlock()
	a.ensureDeferralMapsLocked()
	set := a.sessionDiscovered[sessionID]
	if set == nil {
		set = make(map[string]bool, len(names))
		a.sessionDiscovered[sessionID] = set
	}
	for _, n := range names {
		if n != "" {
			set[n] = true
		}
	}
}

// forgetSessionDeferral drops every per-session deferral record. Called when a
// session is deleted so a long-running server does not accumulate them.
func (a *Agent) forgetSessionDeferral(sessionID string) {
	a.deferralMu.Lock()
	defer a.deferralMu.Unlock()
	delete(a.sessionDeferral, sessionID)
	delete(a.sessionAutoOn, sessionID)
	delete(a.sessionDiscovered, sessionID)
}

// resetDeferralState drops per-session deferral state for every session.
func (a *Agent) resetDeferralState() {
	a.deferralMu.Lock()
	defer a.deferralMu.Unlock()
	a.sessionDeferral = make(map[string]DeferralMode)
	a.sessionAutoOn = make(map[string]bool)
	a.sessionDiscovered = make(map[string]map[string]bool)
}

// deferralActive reports whether deferral applies to sessionID given the full
// tool list it would otherwise be offered.
func (a *Agent) deferralActive(sessionID string, all []shuttle.Tool) bool {
	// Without tool_search the model cannot find a hidden tool, so deferring
	// would strand it.
	if !a.tools.IsRegistered(toolSearchName) {
		return false
	}

	a.deferralMu.RLock()
	cfg := a.toolDeferralLocked()
	mode := cfg.Mode
	if override, ok := a.sessionDeferral[sessionID]; ok {
		mode = override
	}
	a.deferralMu.RUnlock()

	switch mode {
	case DeferralOn:
		return true
	case DeferralAuto:
		size := advertisedToolsBytes(all)
		threshold := cfg.threshold()
		a.deferralMu.Lock()
		defer a.deferralMu.Unlock()
		a.ensureDeferralMapsLocked()
		on := a.sessionAutoOn[sessionID]
		switch {
		case !on && size >= threshold:
			on = true
		case on && size*100 < threshold*autoOffPercent:
			on = false
		}
		a.sessionAutoOn[sessionID] = on
		return on
	default:
		return false
	}
}

// applyToolDeferral removes tools sessionID has not discovered from all, when
// deferral is active for the session. A tool stays advertised if it is internal,
// is tool_search itself, is on the always-on list, was advertised to the session
// by a skill (the session ledger), or was discovered with tool_search.
//
// Hidden tools stay registered and executable; only their advertisement is
// withheld.
func (a *Agent) applyToolDeferral(sessionID string, all []shuttle.Tool) []shuttle.Tool {
	if !a.deferralActive(sessionID, all) {
		return all
	}

	// Lock order is deferralMu, then mu. Nothing takes them the other way round.
	a.deferralMu.RLock()
	defer a.deferralMu.RUnlock()
	a.mu.RLock()
	defer a.mu.RUnlock()
	cfg := a.toolDeferralLocked()
	discovered := a.sessionDiscovered[sessionID]
	ledger := a.sessionToolLedger[sessionID]

	out := make([]shuttle.Tool, 0, len(all))
	for _, t := range all {
		name := t.Name()
		if internalToolNames[name] || name == toolSearchName || ledger[name] || cfg.alwaysOn(name) ||
			discovered[name] || discovered[unqualifiedToolName(name)] {
			out = append(out, t)
		}
	}
	return out
}

// unqualifiedToolName strips the "server:" prefix of an MCP tool name.
func unqualifiedToolName(name string) string {
	if i := strings.LastIndex(name, ":"); i >= 0 {
		return name[i+1:]
	}
	return name
}
