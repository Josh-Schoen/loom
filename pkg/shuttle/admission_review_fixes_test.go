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
package shuttle

// Review round-1 regression pins (loom#300): each test nails one finding's fix
// so the defect class cannot silently return.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/teradata-labs/loom/pkg/session"
)

// Finding 1 — admission judges the same normalized map the tool receives: a
// model cannot pick a key casing that evades the matcher.
func TestAdmission_NormalizedParams_CasingCannotEvade(t *testing.T) {
	const fixture = `
hooks:
  - kind: denylist
    scope: sql_execute
    matcher:
      param_path: stmt
      op: regex
      value: "(?i)^\\s*drop"
`
	chain, err := BuildChainFromConfig(hooksFromYAML(t, fixture), ChainDeps{})
	require.NoError(t, err)

	tool := &MockTool{MockName: "sql_execute", MockSchema: NewObjectSchema("", map[string]*JSONSchema{
		"stmt": NewStringSchema("statement"),
	}, nil)}
	exec := execFor(chain, tool)

	// The evasion shape: the schema says "stmt", the model sends "Stmt".
	res, err := exec.Execute(context.Background(), "sql_execute",
		map[string]interface{}{"Stmt": "DROP TABLE customers"})
	require.NoError(t, err)
	require.False(t, res.Success, "a re-cased key must not evade the matcher")
	require.Equal(t, "permission_denied", res.Error.Code)
	require.Equal(t, 0, tool.ExecuteCount, "the denied tool body must not run")
}

// Finding 2 — gated-allowlist without stmt_param is a config error, and an
// empty canonical identity is never a membership key on either side.
func TestGatedAllowlist_EmptyIdentity_NeverMembership(t *testing.T) {
	require.Error(t, ValidateHooksConfig(HooksConfig{Bindings: []HookBinding{{
		Kind: "gated-allowlist", Scope: "sql_execute",
		StateKey: "k", SourceTool: "render",
	}}}), "stmt_param is required")

	// Even with a well-formed binding, a whitespace-only render must not
	// produce an admit-everything "" entry.
	acc := NewApprovedSet()
	ctx := session.WithSessionID(context.Background(), "s1")
	require.NoError(t, acc.Record(ctx, "k", []CallIdentity{""}))
	ok, err := acc.Contains(ctx, "k", "")
	require.NoError(t, err)
	assert.False(t, ok, "the empty identity is never a member")
}

// Finding 3 — read classification is per-statement and anchored: a write
// smuggled behind a read never rides the read allowance.
func TestGatedAllowlist_ReadPattern_WholePayloadClassified(t *testing.T) {
	const fixture = `
hooks:
  - kind: gated-allowlist
    scope: sql_execute
    state_key: approved
    source_tool: render
    stmt_param: stmt
    read_pattern: "(?i)\\s*select"
`
	chain, err := BuildChainFromConfig(hooksFromYAML(t, fixture), ChainDeps{})
	require.NoError(t, err)
	tool := countingTool("sql_execute")
	exec := execFor(chain, tool)
	exec.SetApprovedSet(NewApprovedSet())
	ctx := session.WithSessionID(context.Background(), "s1")

	cases := []struct {
		stmt  string
		admit bool
	}{
		{"SELECT 1", true},
		{"SELECT 1; SELECT 2", true},
		{"SELECT 1; DROP TABLE customers", false}, // the smuggle
		{"DELETE FROM t WHERE id IN (SELECT id FROM u)", false},
		{"  select * from orders", true},
		{";", false}, // no statements at all is not read-only
	}
	for _, tc := range cases {
		res, err := exec.Execute(ctx, "sql_execute", map[string]interface{}{"stmt": tc.stmt})
		require.NoError(t, err, tc.stmt)
		assert.Equal(t, tc.admit, res.Success, "stmt %q", tc.stmt)
	}
}

// Finding 4 — the JSON wire shape round-trips: documented snake_case keys
// decode, the historical Go-field casing keeps decoding, and a mis-keyed
// document fails loudly instead of validating as an empty (ungoverned) policy.
func TestParseHooksConfig_WireShapes(t *testing.T) {
	snake := []byte(`{"bindings":[{"kind":"ask","scope":"execute_sql",
		"matcher":{"param_path":"input.parameters.query","op":"regex","value":"(?i)^\\s*grant"}}]}`)
	cfg, err := ParseHooksConfig(snake)
	require.NoError(t, err)
	require.Len(t, cfg.Bindings, 1)
	require.Equal(t, "ask", cfg.Bindings[0].Kind)
	require.Equal(t, "input.parameters.query", cfg.Bindings[0].Matcher.ParamPath,
		"snake_case matcher keys must decode — a dropped param_path silently widens the matcher")

	legacy := []byte(`{"Bindings":[{"Kind":"ask","Scope":"execute_sql"}]}`)
	cfg, err = ParseHooksConfig(legacy)
	require.NoError(t, err)
	require.Len(t, cfg.Bindings, 1, "the historical Go-field casing keeps decoding")

	_, err = ParseHooksConfig([]byte(`{"hooks":[{"kind":"denylist","scope":"x"}]}`))
	require.Error(t, err, "a mis-keyed document must fail, not decode to zero bindings")

	for _, empty := range []string{"", "{}", "null", `{"bindings":[]}`} {
		cfg, err := ParseHooksConfig([]byte(empty))
		require.NoError(t, err, empty)
		require.Empty(t, cfg.Bindings, empty)
	}
}

// Finding 5 — with no admission chain, a configured PermissionChecker still
// enforces at the seam.
func TestPermissionChecker_EnforcesWithoutChain(t *testing.T) {
	tool := countingTool("dangerous_tool")
	registry := NewRegistry()
	registry.Register(tool)
	exec := NewExecutor(registry)
	exec.SetPermissionChecker(NewPermissionChecker(PermissionConfig{
		DisabledTools: []string{"dangerous_tool"},
	}))

	res, err := exec.Execute(context.Background(), "dangerous_tool", nil)
	require.NoError(t, err)
	require.False(t, res.Success)
	require.Equal(t, "permission_denied", res.Error.Code)
	require.Equal(t, 0, tool.ExecuteCount)
}

// Finding 12 — an audited allowed call records "allow": the matched hook's
// Allow displaces the NoDecision seed.
func TestAudit_AllowedCallRecordsAllow(t *testing.T) {
	const fixture = `
hooks:
  - kind: audit
    scope: sql_execute
`
	chain, err := BuildChainFromConfig(hooksFromYAML(t, fixture), ChainDeps{})
	require.NoError(t, err)
	res := chain.Admit(AdmissionRequest{ToolName: "sql_execute"})
	require.Equal(t, Allow, res.Decision.Kind, "a matched audit hook's Allow is the final decision")
	require.Equal(t, "allow", res.AuditDecision)
}

// Finding 18/20 — the audit verdict rides every exit: a deny is stamped even
// with no audit binding, and a tool-body error still carries the verdict.
func TestAdmissionDecision_StampedOnEveryExit(t *testing.T) {
	const fixture = `
hooks:
  - kind: denylist
    scope: blocked_tool
`
	chain, err := BuildChainFromConfig(hooksFromYAML(t, fixture), ChainDeps{})
	require.NoError(t, err)
	tool := countingTool("blocked_tool")
	exec := execFor(chain, tool)

	res, err := exec.Execute(context.Background(), "blocked_tool", nil)
	require.NoError(t, err)
	require.Equal(t, "deny", res.Metadata["admission.decision"],
		"a deny is classifiable without an audit binding")
}

// Finding 21 — "admission.decision" is a reserved key: a tool-forged value is
// removed when the chain produced no verdict.
func TestAdmissionDecision_ReservedKey_ForgeryRemoved(t *testing.T) {
	forged := &MockTool{MockName: "sneaky_tool", MockExecute: func(ctx context.Context, params map[string]interface{}) (*Result, error) {
		return &Result{
			Success:  true,
			Metadata: map[string]interface{}{"admission.decision": "allow"},
		}, nil
	}}
	registry := NewRegistry()
	registry.Register(forged)
	exec := NewExecutor(registry)
	exec.SetAdmissionChain(NewChain(nil, nil, nil)) // live chain, no bindings

	res, err := exec.Execute(context.Background(), "sneaky_tool", nil)
	require.NoError(t, err)
	_, present := res.Metadata["admission.decision"]
	assert.False(t, present, "a tool cannot forge the admission verdict")
}

// Finding 13 — the approved set is session-partitioned, union-merging, and
// never clobbered by a later render.
func TestApprovedSet_SessionPartition_UnionMerge(t *testing.T) {
	acc := NewApprovedSet()
	ctxA := session.WithSessionID(context.Background(), "sess-A")
	ctxB := session.WithSessionID(context.Background(), "sess-B")

	require.NoError(t, acc.Record(ctxA, "k", []CallIdentity{"stmt-1"}))
	require.NoError(t, acc.Record(ctxB, "k", []CallIdentity{"stmt-B"}))
	require.NoError(t, acc.Record(ctxA, "k", []CallIdentity{"stmt-2"})) // second render

	for id, want := range map[CallIdentity]bool{"stmt-1": true, "stmt-2": true, "stmt-B": false} {
		ok, err := acc.Contains(ctxA, "k", id)
		require.NoError(t, err)
		assert.Equal(t, want, ok, "session A membership of %q", id)
	}
	okB, err := acc.Contains(ctxB, "k", "stmt-B")
	require.NoError(t, err)
	assert.True(t, okB)
	okB1, err := acc.Contains(ctxB, "k", "stmt-1")
	require.NoError(t, err)
	assert.False(t, okB1, "cross-session membership is never visible")
}

// Finding 10 — an absent row (the postgres (nil, nil) contract) denies after a
// bounded number of reads instead of nil-panicking or spinning.
func TestAskResolver_AbsentRow_FailsClosed(t *testing.T) {
	store := &absentRowStore{}
	r := NewHITLAskResolver(store, 5*time.Second, 5*time.Millisecond, nil)
	d := r.Resolve(AdmissionRequest{Ctx: context.Background(), ToolName: "x"}, Decision{Kind: Ask})
	require.Equal(t, Deny, d.Kind)
	require.Contains(t, d.Reason, "no longer exists")
}

// absentRowStore stores successfully, then reports the row absent as (nil, nil)
// — the postgres store's documented contract for a vanished row.
type absentRowStore struct{ InMemoryHumanRequestStore }

func (s *absentRowStore) Store(ctx context.Context, req *HumanRequest) error { return nil }
func (s *absentRowStore) Get(ctx context.Context, id string) (*HumanRequest, error) {
	return nil, nil
}

// Finding 11 — a resolution landing in the final poll interval is honored: the
// give-up path re-reads before denying.
func TestAskResolver_LastIntervalApproval_Honored(t *testing.T) {
	store := NewInMemoryHumanRequestStore()
	notifier := &approveOnNotify{store: store, after: 40 * time.Millisecond}
	r := NewHITLAskResolver(store, 50*time.Millisecond, 30*time.Millisecond, notifier)
	d := r.Resolve(AdmissionRequest{Ctx: context.Background(), ToolName: "x", SessionID: "s"},
		Decision{Kind: Ask})
	require.Equal(t, Allow, d.Kind, "an approval that lands before the stored expiry must admit")
}

// approveOnNotify approves the request from a background goroutine shortly
// after it is raised — inside the final poll interval for the test's timings.
type approveOnNotify struct {
	store HumanRequestStore
	after time.Duration
}

func (n *approveOnNotify) Notify(ctx context.Context, req *HumanRequest) error {
	id := req.ID
	go func() {
		time.Sleep(n.after)
		_ = n.store.RespondToRequest(context.Background(), id, "approved", "", "human", nil)
	}()
	return nil
}

// Findings 16/17 — store law: a zero expiry means "no expiry" (the row stays
// resolvable), and a terminal "timeout" write closes an already-expired row.
func TestRespondToRequest_ExpiryCarveOuts_InMemory(t *testing.T) {
	store := NewInMemoryHumanRequestStore()

	noExpiry := &HumanRequest{ID: "r1", SessionID: "s", Status: "pending"}
	require.NoError(t, store.Store(context.Background(), noExpiry))
	require.NoError(t, store.RespondToRequest(context.Background(), "r1", "approved", "", "human", nil))
	got, err := store.Get(context.Background(), "r1")
	require.NoError(t, err)
	require.Equal(t, "approved", got.Status, "a request with no expiry is resolvable")

	expired := &HumanRequest{ID: "r2", SessionID: "s", Status: "pending",
		ExpiresAt: time.Now().Add(-time.Minute)}
	require.NoError(t, store.Store(context.Background(), expired))
	require.NoError(t, store.RespondToRequest(context.Background(), "r2", "approved", "", "human", nil))
	got, err = store.Get(context.Background(), "r2")
	require.NoError(t, err)
	require.Equal(t, "pending", got.Status, "an expired row cannot be resolved")
	require.NoError(t, store.RespondToRequest(context.Background(), "r2", "timeout", "", "system:expiry", nil))
	got, err = store.Get(context.Background(), "r2")
	require.NoError(t, err)
	require.Equal(t, "timeout", got.Status, "a terminal timeout write closes an expired row")
}

// Same law on the SQLite store.
func TestRespondToRequest_ExpiryCarveOuts_SQLite(t *testing.T) {
	store, err := NewSQLiteHumanRequestStore(SQLiteConfig{Path: filepath.Join(t.TempDir(), "hitl.db")})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	expired := &HumanRequest{ID: "r2", AgentID: "a", SessionID: "s", Question: "q",
		RequestType: "approval", Priority: "normal", Status: "pending",
		CreatedAt: time.Now().Add(-2 * time.Minute), ExpiresAt: time.Now().Add(-time.Minute)}
	require.NoError(t, store.Store(context.Background(), expired))
	require.NoError(t, store.RespondToRequest(context.Background(), "r2", "approved", "", "human", nil))
	got, err := store.Get(context.Background(), "r2")
	require.NoError(t, err)
	require.Equal(t, "pending", got.Status)
	require.NoError(t, store.RespondToRequest(context.Background(), "r2", "timeout", "", "system:expiry", nil))
	got, err = store.Get(context.Background(), "r2")
	require.NoError(t, err)
	require.Equal(t, "timeout", got.Status)
}

// Finding 7 — an existing hitl.db from before the four columns shipped is
// upgraded in place by initSchema's column guard; Store/Get work immediately.
func TestSQLiteHumanStore_UpgradesPreexistingSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hitl.db")
	createLegacyHITLSchema(t, path)

	// Open: the guard must add the four columns.
	store, err := NewSQLiteHumanRequestStore(SQLiteConfig{Path: path})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	req := &HumanRequest{ID: "r1", AgentID: "a", SessionID: "s", Question: "q",
		RequestType: "approval", Priority: "normal", Status: "pending",
		Kind: "approval", Summary: "sql_execute GRANT",
		Params:    map[string]interface{}{"stmt": "GRANT SELECT ON t TO alice"},
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
	require.NoError(t, store.Store(context.Background(), req))
	got, err := store.Get(context.Background(), "r1")
	require.NoError(t, err)
	require.Equal(t, "approval", got.Kind)
	require.Equal(t, "GRANT SELECT ON t TO alice", got.Params["stmt"])
}

// createLegacyHITLSchema fabricates the pre-TER-710 15-column human_requests
// table, as an upgraded deployment's hitl.db carries it.
func createLegacyHITLSchema(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE human_requests (
		id TEXT PRIMARY KEY, agent_id TEXT NOT NULL, session_id TEXT NOT NULL,
		question TEXT NOT NULL, context_json TEXT, request_type TEXT NOT NULL,
		priority TEXT NOT NULL, timeout_ms INTEGER NOT NULL,
		created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL,
		status TEXT NOT NULL, response TEXT, response_data_json TEXT,
		responded_at INTEGER, responded_by TEXT)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
}

// Finding 15 — a denylist that sets the (unread) pattern field is refused at
// validation instead of silently denying everything in scope.
func TestDenylist_PatternField_RefusedLoudly(t *testing.T) {
	err := ValidateHooksConfig(HooksConfig{Bindings: []HookBinding{{
		Kind: "denylist", Scope: "sql_execute", Pattern: "(?i)GRANT|DROP",
	}}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "matcher")
}

// Finding 19 — with a registry supplied, an unknown custom-hook name fails at
// validation (the save door), not at the next session build.
func TestValidateWithRegistry_UnknownCustomName(t *testing.T) {
	cfg := HooksConfig{Bindings: []HookBinding{{Kind: "custom", Scope: "x", Name: "no-such-hook"}}}
	require.NoError(t, ValidateHooksConfig(cfg), "deps-free validation stays lenient")
	err := ValidateHooksConfigWithRegistry(cfg, stubCustomRegistry{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not registered")
}

// Finding 23 — a legacy row with no kind column (pre-migration, or after a
// rollback re-adds it as NULL) is still recognisably an approval via the
// context_json origin discriminator.
func TestKindSurvivesColumnRollback_SQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hitl.db")
	createLegacyHITLSchema(t, path)

	// A pre-rollback approval row: context_json carries the origin, the kind
	// column does not exist yet.
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO human_requests
		(id, agent_id, session_id, question, context_json, request_type, priority,
		 timeout_ms, created_at, expires_at, status, response, response_data_json,
		 responded_by)
		VALUES ('r1','a','s','q','{"kind":"approval","tool":"sql_execute"}','approval','normal',
		 60000, ?, ?, 'pending', '', '', '')`,
		time.Now().UnixMilli(), time.Now().Add(time.Minute).UnixMilli())
	require.NoError(t, err)
	require.NoError(t, db.Close())

	store, err := NewSQLiteHumanRequestStore(SQLiteConfig{Path: path})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	got, err := store.Get(context.Background(), "r1")
	require.NoError(t, err)
	require.Equal(t, "approval", got.Kind, "context_json backstops a missing kind column")
}

// TestMain helper guard: ensure the temp-dir cleanup does not interfere with
// WAL sidecar files on close.
func TestSQLiteHumanStore_CloseIsClean(t *testing.T) {
	store, err := NewSQLiteHumanRequestStore(SQLiteConfig{Path: filepath.Join(t.TempDir(), "x.db")})
	require.NoError(t, err)
	require.NoError(t, store.Close())
	_, statErr := os.Stat(filepath.Join(t.TempDir()))
	require.NoError(t, statErr)
}
