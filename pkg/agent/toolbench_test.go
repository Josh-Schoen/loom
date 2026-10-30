// Copyright 2026 Josh Schoen
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

// Tool-context benchmark: how many tool definitions each provider call carries
// when several MCP servers are connected, and what that costs.
//
// A scripted model plays fixed conversations against a real Agent. Every
// provider call is recorded (the advertised tools and the compiled messages),
// so the numbers measure what loom actually sends, not a model of it. Three
// tool-loading setups are compared:
//
//	eager_all  every tool of every MCP server registered up front (an MCP
//	           config with an empty or "*" tool list)
//	on_demand  only tool_search up front; tools are found in the on-disk
//	           FTS5 index and registered on first call
//	mixed      one server eager, the rest on demand
//
// TestToolbench_NoRegression gates the deterministic metrics against
// testdata/toolbench/baseline.json. Regenerate the baseline deliberately with
// LOOM_TOOLBENCH_UPDATE=1 (the diff is the review record). LOOM_TOOLBENCH_OUT
// writes the current report to a file. BenchmarkToolbench* measure speed; compare
// runs with benchstat. See docs/testing/toolbench.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	llmtypes "github.com/teradata-labs/loom/pkg/llm/types"
	"github.com/teradata-labs/loom/pkg/shuttle"
	"github.com/teradata-labs/loom/pkg/shuttle/builtin"
	toolregistry "github.com/teradata-labs/loom/pkg/tools/registry"
)

const (
	toolbenchBaselinePath = "testdata/toolbench/baseline.json"

	// Prompt-cache price multipliers relative to uncached input (Anthropic's
	// published cache-read and 5-minute cache-write rates). Used only for the
	// modeled billed-token metric.
	toolbenchCacheReadMultiplier  = 0.1
	toolbenchCacheWriteMultiplier = 1.25

	// Relative slack on token metrics before the gate calls it a regression.
	toolbenchTokenSlack = 0.02
)

// ---------------------------------------------------------------------------
// Fixture: five MCP servers, 150 tools, realistic description/schema sizes.
// ---------------------------------------------------------------------------

type toolbenchServer struct {
	name   string
	prefix string
	verbs  []string
	nouns  []string
	limit  int
}

var toolbenchServers = []toolbenchServer{
	{"github", "gh", []string{"list", "get", "create", "update", "delete"},
		[]string{"issue", "pull_request", "branch", "commit", "release", "workflow_run", "review_comment", "label", "file"}, 40},
	{"sheets", "sheet", []string{"get", "append", "update", "clear", "insert"},
		[]string{"spreadsheet", "range", "row", "column", "chart"}, 25},
	{"calendar", "cal", []string{"list", "get", "create", "update", "delete"},
		[]string{"event", "calendar", "attendee"}, 15},
	{"drive", "drive", []string{"list", "get", "create", "update", "delete"},
		[]string{"file", "folder", "permission", "comment", "revision", "shared_drive"}, 30},
	{"db", "db", []string{"list", "get", "create", "update", "delete"},
		[]string{"table", "view", "index", "query", "schema", "role", "backup", "migration"}, 40},
}

type toolbenchToolDef struct {
	server string
	name   string // unique across servers, e.g. gh_list_issue
	desc   string
	schema *shuttle.JSONSchema
}

func (d toolbenchToolDef) qualified() string { return d.server + ":" + d.name }

var (
	toolbenchFixtureOnce sync.Once
	toolbenchFixtureDefs []toolbenchToolDef
)

func toolbenchFixture() []toolbenchToolDef {
	toolbenchFixtureOnce.Do(func() {
		for _, s := range toolbenchServers {
			n := 0
			for _, noun := range s.nouns {
				for _, verb := range s.verbs {
					if n == s.limit {
						break
					}
					toolbenchFixtureDefs = append(toolbenchFixtureDefs, toolbenchMakeTool(s, verb, noun))
					n++
				}
			}
		}
	})
	return toolbenchFixtureDefs
}

func toolbenchMakeTool(s toolbenchServer, verb, noun string) toolbenchToolDef {
	human := strings.ReplaceAll(noun, "_", " ")
	desc := fmt.Sprintf("%s a %s in the connected %s account. Returns the %s as JSON with its id, title, "+
		"timestamps and owner. Use the filter and sort fields to narrow results and page/per_page to paginate. "+
		"Fails with a not-found error when the %s does not exist or the account lacks access.",
		strings.ToUpper(verb[:1])+verb[1:], human, s.name, human, human)
	props := map[string]*shuttle.JSONSchema{
		"owner":    shuttle.NewStringSchema("Account, organization or workspace that owns the " + human + "."),
		"filter":   shuttle.NewStringSchema("Filter expression, e.g. state:open author:me. Unknown keys are ignored."),
		"sort":     shuttle.NewStringSchema("Sort order of the results.").WithEnum("created", "updated", "name", "relevance"),
		"page":     shuttle.NewNumberSchema("Page number, starting at 1.").WithDefault(1),
		"per_page": shuttle.NewNumberSchema("Results per page, 1-100.").WithDefault(30),
		"fields": shuttle.NewArraySchema("Fields to include in the response; all fields when omitted.",
			shuttle.NewStringSchema("Field name")),
	}
	var required []string
	if verb != "list" {
		props["id"] = shuttle.NewStringSchema("Identifier of the " + human + ".")
		required = append(required, "id")
	}
	if verb == "create" || verb == "update" || verb == "append" || verb == "insert" {
		props["body"] = shuttle.NewStringSchema("Content of the " + human + " as Markdown or JSON, depending on the " +
			human + " type. Large bodies are accepted up to 64 KB.")
		props["dry_run"] = shuttle.NewBooleanSchema("Validate the request without saving anything.")
	}
	return toolbenchToolDef{
		server: s.name,
		name:   s.prefix + "_" + verb + "_" + noun,
		desc:   desc,
		schema: shuttle.NewObjectSchema("Parameters for "+verb+" "+human, props, required),
	}
}

func toolbenchFixtureByName() map[string]toolbenchToolDef {
	m := make(map[string]toolbenchToolDef)
	for _, d := range toolbenchFixture() {
		m[d.name] = d
	}
	return m
}

// toolbenchEagerTool is an MCP tool registered up front under its
// server-qualified name, the way RegisterMCPServer names adapter tools.
type toolbenchEagerTool struct{ def toolbenchToolDef }

func (t toolbenchEagerTool) Name() string                     { return t.def.qualified() }
func (t toolbenchEagerTool) Description() string              { return t.def.desc }
func (t toolbenchEagerTool) InputSchema() *shuttle.JSONSchema { return t.def.schema }
func (t toolbenchEagerTool) Backend() string                  { return "" }
func (t toolbenchEagerTool) Execute(context.Context, map[string]interface{}) (*shuttle.Result, error) {
	return &shuttle.Result{Success: true, Data: toolbenchToolOutput(t.def.name)}, nil
}

func toolbenchToolOutput(name string) string {
	return fmt.Sprintf(`{"tool":%q,"items":[{"id":"1","title":"Quarterly plan","owner":"me"},`+
		`{"id":"2","title":"Budget review","owner":"me"}],"next_page":null}`, name)
}

// toolbenchMCPManager / toolbenchMCPClient stand in for the MCP manager the
// executor resolves on-demand tools through. CallTool answers like a server.
type toolbenchMCPManager struct{}

func (toolbenchMCPManager) GetClient(server string) (interface{}, error) {
	return toolbenchMCPClient{}, nil
}

type toolbenchMCPClient struct{}

func (toolbenchMCPClient) CallTool(_ context.Context, name string, _ map[string]interface{}) (interface{}, error) {
	return map[string]interface{}{
		"content": []interface{}{map[string]interface{}{"type": "text", "text": toolbenchToolOutput(name)}},
	}, nil
}

// ---------------------------------------------------------------------------
// Setups
// ---------------------------------------------------------------------------

type toolbenchSetup struct {
	name  string
	eager func(server string) bool
}

var toolbenchSetups = []toolbenchSetup{
	{"eager_all", func(string) bool { return true }},
	{"on_demand", func(string) bool { return false }},
	{"mixed", func(s string) bool { return s == "github" }},
}

// toolbenchIndex builds the on-disk FTS5 tool index holding every fixture tool.
func toolbenchIndex(tb testing.TB) *toolregistry.Registry {
	tb.Helper()
	reg, err := toolregistry.New(toolregistry.Config{DBPath: filepath.Join(tb.TempDir(), "tools.db")})
	if err != nil {
		tb.Fatalf("tool index: %v", err)
	}
	tb.Cleanup(func() { _ = reg.Close() })
	ctx := context.Background()
	for _, d := range toolbenchFixture() {
		schema, err := json.Marshal(d.schema)
		if err != nil {
			tb.Fatalf("marshal schema: %v", err)
		}
		if err := reg.RegisterTool(ctx, &loomv1.IndexedTool{
			Id:          "mcp:" + d.server + ":" + d.name,
			Name:        d.name,
			Description: d.desc,
			Source:      loomv1.ToolSource_TOOL_SOURCE_MCP,
			McpServer:   d.server,
			InputSchema: string(schema),
			Keywords:    strings.Split(d.name, "_"),
		}); err != nil {
			tb.Fatalf("index %s: %v", d.name, err)
		}
	}
	return reg
}

func toolbenchAgent(tb testing.TB, setup toolbenchSetup, model *toolbenchModel, index *toolregistry.Registry) *Agent {
	tb.Helper()
	cfg := DefaultConfig()
	cfg.PatternConfig = DefaultPatternConfig()
	cfg.PatternConfig.UseLLMClassifier = false
	ag := NewAgent(&mockBackend{}, model, WithConfig(cfg))

	anyOnDemand := false
	for _, d := range toolbenchFixture() {
		if setup.eager(d.server) {
			ag.RegisterTool(toolbenchEagerTool{def: d})
		} else {
			anyOnDemand = true
		}
	}
	if anyOnDemand {
		ag.RegisterTool(toolregistry.NewSearchTool(index))
		ag.SetToolRegistryForDynamicDiscovery(index, toolbenchMCPManager{})
	}
	return ag
}

// ---------------------------------------------------------------------------
// Scenarios
// ---------------------------------------------------------------------------

type toolbenchTurn struct {
	session string
	tools   []string // fixture tool names the model uses, in order
}

type toolbenchScenario struct {
	name  string
	turns []toolbenchTurn
}

var toolbenchScenarios = []toolbenchScenario{
	{"short", []toolbenchTurn{
		{"a", []string{"gh_list_issue", "gh_get_issue"}},
	}},
	{"long", []toolbenchTurn{
		{"a", []string{"gh_list_pull_request"}},
		{"a", []string{"gh_get_pull_request"}},
		{"a", []string{"sheet_get_range"}},
		{"a", []string{"sheet_append_row"}},
		{"a", []string{"db_list_table", "db_get_schema"}},
		{"a", []string{"gh_list_pull_request"}},
		{"a", []string{"sheet_get_range"}},
		{"a", []string{"db_list_table"}},
		{"a", []string{"cal_list_event"}},
		{"a", []string{"gh_create_issue"}},
	}},
	{"two_sessions", []toolbenchTurn{
		{"a", []string{"gh_list_issue"}},
		{"b", []string{"sheet_get_range"}},
		{"a", []string{"gh_get_issue"}},
		{"b", []string{"cal_list_event"}},
		{"a", []string{"gh_list_issue"}},
	}},
}

// ---------------------------------------------------------------------------
// Scripted model: plays a turn's tool list, searching for any tool it was
// not given (the way a model follows tool_search), and records every call.
// ---------------------------------------------------------------------------

var toolbenchTurnTag = regexp.MustCompile(`\[toolbench s=(\w+) t=(\d+)\]`)

type toolbenchCall struct {
	session  string
	tools    []shuttle.Tool
	messages []llmtypes.Message
}

type toolbenchModel struct {
	mu    sync.Mutex
	plans map[string][]string // "session/turn" → tool names
	state map[string]*toolbenchPlanState
	calls []toolbenchCall
	seq   int
}

type toolbenchPlanState struct {
	next     int
	searched bool
}

func newToolbenchModel(sc toolbenchScenario) *toolbenchModel {
	m := &toolbenchModel{plans: map[string][]string{}, state: map[string]*toolbenchPlanState{}}
	for i, t := range sc.turns {
		m.plans[fmt.Sprintf("%s/%d", t.session, i)] = t.tools
	}
	return m
}

func (m *toolbenchModel) Name() string  { return "toolbench" }
func (m *toolbenchModel) Model() string { return "toolbench-model" }

func (m *toolbenchModel) Chat(_ context.Context, messages []llmtypes.Message, tools []shuttle.Tool) (*llmtypes.LLMResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var key, session string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		if g := toolbenchTurnTag.FindStringSubmatch(toolbenchText(messages[i])); g != nil {
			session, key = g[1], g[1]+"/"+g[2]
			break
		}
	}
	m.calls = append(m.calls, toolbenchCall{
		session:  session,
		tools:    append([]shuttle.Tool(nil), tools...),
		messages: append([]llmtypes.Message(nil), messages...),
	})

	plan, ok := m.plans[key]
	if !ok {
		return &llmtypes.LLMResponse{Content: "ok", StopReason: "end_turn"}, nil
	}
	st := m.state[key]
	if st == nil {
		st = &toolbenchPlanState{}
		m.state[key] = st
	}
	if st.next >= len(plan) {
		return &llmtypes.LLMResponse{Content: "Done.", StopReason: "end_turn"}, nil
	}

	want := plan[st.next]
	call := func(name string, input map[string]interface{}) *llmtypes.LLMResponse {
		m.seq++
		return &llmtypes.LLMResponse{StopReason: "tool_use", ToolCalls: []llmtypes.ToolCall{
			{ID: fmt.Sprintf("call_%d", m.seq), Name: name, Input: input},
		}}
	}
	args := map[string]interface{}{"id": "1", "owner": "me"}

	advertised := map[string]bool{}
	for _, t := range tools {
		advertised[t.Name()] = true
	}
	def := toolbenchFixtureByName()[want]
	switch {
	case advertised[def.qualified()]:
		st.next++
		return call(def.qualified(), args), nil
	case advertised[want]:
		st.next++
		return call(want, args), nil
	case !st.searched && advertised[builtin.ToolSearchName]:
		st.searched = true
		return call(builtin.ToolSearchName, map[string]interface{}{
			"query": strings.ReplaceAll(want, "_", " "),
			"mode":  "fast",
		}), nil
	default:
		// Read about it in the search result (or never found it): call it by
		// the name the index gave.
		st.next++
		st.searched = false
		return call(want, args), nil
	}
}

func toolbenchText(m llmtypes.Message) string {
	if m.Content != "" {
		return m.Content
	}
	var b strings.Builder
	for _, cb := range m.ContentBlocks {
		b.WriteString(cb.Text)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Measurement
// ---------------------------------------------------------------------------

// toolbenchResult holds one scenario × setup. All fields are deterministic.
type toolbenchResult struct {
	Scenario string `json:"scenario"`
	Setup    string `json:"setup"`

	UserTurns     int `json:"user_turns"`
	ProviderCalls int `json:"provider_calls"`
	ToolCalls     int `json:"tool_calls"`
	ToolCallsOK   int `json:"tool_calls_ok"`
	SearchCalls   int `json:"search_calls"`

	AdvertisedToolsMax   int `json:"advertised_tools_max"`
	AdvertisedToolsFirst int `json:"advertised_tools_first_call"`

	ToolTokens    int `json:"tool_tokens_total"`
	MessageTokens int `json:"message_tokens_total"`
	InputTokens   int `json:"input_tokens_total"`
	// BilledTokensModeled prices InputTokens with prompt caching: the prefix
	// identical to the session's previous call (tools first, then messages)
	// is a cache read, the rest a cache write. A tool-list change makes the
	// whole request a write.
	BilledTokensModeled int `json:"billed_tokens_modeled"`

	// ToolSetChanges counts calls whose tool list differs from the same
	// session's previous call (each one resets the prompt cache).
	ToolSetChanges int `json:"tool_set_changes"`
	// CrossSessionLeaks counts distinct tools advertised to a session that
	// are neither in the agent's first tool list nor used by that session.
	CrossSessionLeaks int `json:"cross_session_leaks"`
}

type toolbenchMeter struct {
	tc         *TokenCounter
	toolTokens map[string]int
	msgTokens  map[string]int
}

func newToolbenchMeter() *toolbenchMeter {
	return &toolbenchMeter{tc: GetTokenCounter(), toolTokens: map[string]int{}, msgTokens: map[string]int{}}
}

func toolbenchToolSig(tools []shuttle.Tool) string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name()
	}
	return strings.Join(names, "\x00")
}

// toolsTokens counts the tool definitions as a provider serializes them
// (name, description, JSON schema per tool).
func (m *toolbenchMeter) toolsTokens(tools []shuttle.Tool) int {
	sig := toolbenchToolSig(tools)
	if n, ok := m.toolTokens[sig]; ok {
		return n
	}
	type wire struct {
		Name        string              `json:"name"`
		Description string              `json:"description"`
		InputSchema *shuttle.JSONSchema `json:"input_schema"`
	}
	ws := make([]wire, len(tools))
	for i, t := range tools {
		ws[i] = wire{t.Name(), t.Description(), t.InputSchema()}
	}
	b, _ := json.Marshal(ws)
	n := m.tc.CountTokens(string(b))
	m.toolTokens[sig] = n
	return n
}

func toolbenchMessageWire(msg llmtypes.Message) string {
	var b strings.Builder
	b.WriteString(msg.Role)
	b.WriteString("\n")
	b.WriteString(toolbenchText(msg))
	if len(msg.ToolCalls) > 0 {
		j, _ := json.Marshal(msg.ToolCalls)
		b.Write(j)
	}
	if msg.Content == "" && msg.ToolResult != nil {
		j, _ := json.Marshal(msg.ToolResult.Data)
		b.Write(j)
	}
	return b.String()
}

func (m *toolbenchMeter) messageTokens(wire string) int {
	if n, ok := m.msgTokens[wire]; ok {
		return n
	}
	n := m.tc.CountTokens(wire)
	m.msgTokens[wire] = n
	return n
}

func runToolbench(tb testing.TB, sc toolbenchScenario, setup toolbenchSetup, index *toolregistry.Registry, meter *toolbenchMeter) toolbenchResult {
	tb.Helper()
	model := newToolbenchModel(sc)
	ag := toolbenchAgent(tb, setup, model, index)
	ctx := context.Background()

	res := toolbenchResult{Scenario: sc.name, Setup: setup.name, UserTurns: len(sc.turns)}
	used := map[string]map[string]bool{} // session → tool names it used or searched for
	for i, turn := range sc.turns {
		if used[turn.session] == nil {
			used[turn.session] = map[string]bool{}
		}
		for _, n := range turn.tools {
			used[turn.session][n] = true
			used[turn.session][toolbenchFixtureByName()[n].qualified()] = true
		}
		msg := fmt.Sprintf("[toolbench s=%s t=%d] Please use %s.", turn.session, i, strings.Join(turn.tools, " and "))
		resp, err := ag.Chat(ctx, "toolbench-"+sc.name+"-"+setup.name+"-"+turn.session, msg)
		if err != nil {
			tb.Fatalf("%s/%s turn %d: %v", sc.name, setup.name, i, err)
		}
		for _, ex := range resp.ToolExecutions {
			if ex.ToolName == builtin.ToolSearchName {
				res.SearchCalls++
				continue
			}
			res.ToolCalls++
			if ex.Result != nil && ex.Result.Success {
				res.ToolCallsOK++
			}
		}
	}

	model.mu.Lock()
	calls := model.calls
	model.mu.Unlock()
	res.ProviderCalls = len(calls)

	base := map[string]bool{}
	if len(calls) > 0 {
		res.AdvertisedToolsFirst = len(calls[0].tools)
		for _, t := range calls[0].tools {
			base[t.Name()] = true
		}
	}

	type prev struct {
		sig  string
		msgs []string
	}
	last := map[string]*prev{}
	leaks := map[string]bool{}
	for _, c := range calls {
		if len(c.tools) > res.AdvertisedToolsMax {
			res.AdvertisedToolsMax = len(c.tools)
		}
		toolTok := meter.toolsTokens(c.tools)
		sig := toolbenchToolSig(c.tools)
		wires := make([]string, len(c.messages))
		msgTok := 0
		for i, msg := range c.messages {
			wires[i] = toolbenchMessageWire(msg)
			msgTok += meter.messageTokens(wires[i])
		}
		res.ToolTokens += toolTok
		res.MessageTokens += msgTok
		res.InputTokens += toolTok + msgTok

		cached := 0
		if p := last[c.session]; p != nil {
			if p.sig != sig {
				res.ToolSetChanges++
			} else {
				cached = toolTok
				for i := 0; i < len(wires) && i < len(p.msgs) && wires[i] == p.msgs[i]; i++ {
					cached += meter.messageTokens(wires[i])
				}
			}
		}
		fresh := toolTok + msgTok - cached
		res.BilledTokensModeled += int(math.Round(toolbenchCacheReadMultiplier*float64(cached) +
			toolbenchCacheWriteMultiplier*float64(fresh)))
		last[c.session] = &prev{sig: sig, msgs: wires}

		for _, t := range c.tools {
			n := t.Name()
			if !base[n] && !used[c.session][n] {
				leaks[c.session+"\x00"+n] = true
			}
		}
	}
	res.CrossSessionLeaks = len(leaks)
	return res
}

func runToolbenchAll(tb testing.TB) []toolbenchResult {
	tb.Helper()
	meter := newToolbenchMeter()
	var out []toolbenchResult
	for _, sc := range toolbenchScenarios {
		for _, setup := range toolbenchSetups {
			out = append(out, runToolbench(tb, sc, setup, toolbenchIndex(tb), meter))
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Gate
// ---------------------------------------------------------------------------

type toolbenchReport struct {
	Note    string            `json:"note"`
	Results []toolbenchResult `json:"results"`
}

const toolbenchNote = "Generated by TestToolbench_NoRegression (pkg/agent/toolbench_test.go). " +
	"Regenerate with LOOM_TOOLBENCH_UPDATE=1 only for an intended change; see docs/testing/toolbench.md."

func TestToolbench_NoRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("toolbench plays full conversations; skipped in -short")
	}
	results := runToolbenchAll(t)
	report := toolbenchReport{Note: toolbenchNote, Results: results}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, '\n')

	if out := os.Getenv("LOOM_TOOLBENCH_OUT"); out != "" {
		if err := os.WriteFile(out, body, 0o600); err != nil {
			t.Fatalf("write report: %v", err)
		}
	}
	for _, r := range results {
		t.Logf("%-12s %-9s calls=%-3d tools_max=%-3d tool_tok=%-7d input_tok=%-7d billed=%-7d changes=%d leaks=%d ok=%d/%d search=%d",
			r.Scenario, r.Setup, r.ProviderCalls, r.AdvertisedToolsMax, r.ToolTokens, r.InputTokens,
			r.BilledTokensModeled, r.ToolSetChanges, r.CrossSessionLeaks, r.ToolCallsOK, r.ToolCalls, r.SearchCalls)
	}

	if os.Getenv("LOOM_TOOLBENCH_UPDATE") == "1" {
		if err := os.MkdirAll(filepath.Dir(toolbenchBaselinePath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(toolbenchBaselinePath, body, 0o600); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
		t.Logf("baseline written to %s", toolbenchBaselinePath)
		return
	}

	raw, err := os.ReadFile(toolbenchBaselinePath)
	if err != nil {
		t.Fatalf("read baseline (create it with LOOM_TOOLBENCH_UPDATE=1): %v", err)
	}
	var baseline toolbenchReport
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatalf("parse baseline: %v", err)
	}
	want := map[string]toolbenchResult{}
	for _, b := range baseline.Results {
		want[b.Scenario+"/"+b.Setup] = b
	}

	for _, r := range results {
		key := r.Scenario + "/" + r.Setup
		b, ok := want[key]
		if !ok {
			t.Errorf("%s: no baseline entry; regenerate the baseline", key)
			continue
		}
		if r.ToolCallsOK != r.ToolCalls {
			t.Errorf("%s: %d of %d tool calls failed", key, r.ToolCalls-r.ToolCallsOK, r.ToolCalls)
		}
		if r.ToolCallsOK < b.ToolCallsOK {
			t.Errorf("%s: successful tool calls %d < baseline %d", key, r.ToolCallsOK, b.ToolCallsOK)
		}
		if r.ProviderCalls > b.ProviderCalls {
			t.Errorf("%s: provider calls %d > baseline %d (extra model round trips)", key, r.ProviderCalls, b.ProviderCalls)
		}
		if r.ToolSetChanges > b.ToolSetChanges {
			t.Errorf("%s: tool-list changes %d > baseline %d (extra prompt-cache resets)", key, r.ToolSetChanges, b.ToolSetChanges)
		}
		if r.CrossSessionLeaks > b.CrossSessionLeaks {
			t.Errorf("%s: cross-session leaks %d > baseline %d", key, r.CrossSessionLeaks, b.CrossSessionLeaks)
		}
		toolbenchCheckTokens(t, key, "input tokens", r.InputTokens, b.InputTokens)
		toolbenchCheckTokens(t, key, "modeled billed tokens", r.BilledTokensModeled, b.BilledTokensModeled)
	}
}

func toolbenchCheckTokens(t *testing.T, key, what string, got, base int) {
	t.Helper()
	if limit := float64(base) * (1 + toolbenchTokenSlack); float64(got) > limit {
		t.Errorf("%s: %s %d > baseline %d (+%.0f%% slack)", key, what, got, base, toolbenchTokenSlack*100)
	}
}

// ---------------------------------------------------------------------------
// Speed benchmarks (compare with benchstat; not gated)
// ---------------------------------------------------------------------------

func BenchmarkToolbenchConversation(b *testing.B) {
	meter := newToolbenchMeter()
	for _, sc := range toolbenchScenarios {
		for _, setup := range toolbenchSetups {
			b.Run(sc.name+"/"+setup.name, func(b *testing.B) {
				index := toolbenchIndex(b)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = runToolbench(b, sc, setup, index, meter)
				}
			})
		}
	}
}

func BenchmarkToolbenchAdvertisedTools(b *testing.B) {
	for _, setup := range toolbenchSetups {
		b.Run(setup.name, func(b *testing.B) {
			ag := toolbenchAgent(b, setup, newToolbenchModel(toolbenchScenario{}), toolbenchIndex(b))
			ag.captureBaseTools()
			session := &Session{ID: "bench"}
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = ag.advertisedTools(ctx, session)
			}
		})
	}
}

func BenchmarkToolbenchSearch(b *testing.B) {
	index := toolbenchIndex(b)
	st := toolregistry.NewSearchTool(index)
	ctx := context.Background()
	queries := []string{"list pull request", "append row spreadsheet", "get schema table", "list event calendar"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := st.Execute(ctx, map[string]interface{}{"query": queries[i%len(queries)], "mode": "fast"})
		if err != nil || !res.Success {
			b.Fatalf("search failed: %v %+v", err, res)
		}
	}
}
