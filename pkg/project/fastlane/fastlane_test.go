package fastlane

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/teradata-labs/loom/pkg/project"
)

// ── A recording fake driver ──────────────────────────────────────────────────
// Just enough of database/sql's driver contract to run a Session: Exec is
// recorded, Query answers a fixed two-column sample.

type fakeDriver struct{ log *[]string }

func (d fakeDriver) Open(string) (driver.Conn, error) { return &fakeConn{log: d.log}, nil }

type fakeConn struct{ log *[]string }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, fmt.Errorf("prepare unused") }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return nil, fmt.Errorf("tx unused") }

func (c *fakeConn) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	*c.log = append(*c.log, q)
	return driver.RowsAffected(0), nil
}

func (c *fakeConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	*c.log = append(*c.log, q)
	return &fakeRows{}, nil
}

type fakeRows struct{ served int }

func (r *fakeRows) Columns() []string { return []string{"customer_id", "total_amount"} }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.served >= 2 {
		return io.EOF
	}
	r.served++
	dest[0] = int64(r.served)
	dest[1] = []byte("42.00")
	return nil
}

// newFakeSession wires a Session onto the fake driver, bypassing Open's
// engine gate and ping.
func newFakeSession(t *testing.T) (*Session, *[]string) {
	t.Helper()
	log := &[]string{}
	name := fmt.Sprintf("fastlane-fake-%s", t.Name())
	sql.Register(name, fakeDriver{log: log})
	db, err := sql.Open(name, "{}")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	py, _ := exec.LookPath("python3")
	scratch := t.TempDir()
	return &Session{db: db, engine: "teradata", venvPython: py, scratch: scratch, scopes: map[string]*scopeState{}}, log
}

// testScope is the document key the single-document tests run under; vt is
// the volatile name a cell takes in it.
const testScope = "/repo/a/project.yaml"

func vt(scope, cell string) string { return volatileName(scope, cell) }

func testDoc(t *testing.T, downstreamSQL string) *project.Document {
	t.Helper()
	body := fmt.Sprintf(`apiVersion: loom/v1
kind: Project
metadata:
  name: fl_test
cells:
  - id: orders_base
    lang: sql
    source: |
      SELECT 1 AS customer_id
  - id: customer_totals
    lang: sql
    inputs: [orders_base]
    source: |
      %s
`, downstreamSQL)
	doc, err := project.Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}

func TestRunCellMaterializesClosureInOrder(t *testing.T) {
	s, log := newFakeSession(t)
	doc := testDoc(t, "SELECT customer_id FROM {{ ref('orders_base') }}")

	res, err := s.RunCell(context.Background(), testScope, doc, "customer_totals", 10)
	if err != nil {
		t.Fatalf("RunCell: %v", err)
	}

	// The statement log tells the whole story: upstream volatile table
	// before downstream, ref resolved to the volatile name, preview last.
	joined := strings.Join(*log, "\n---\n")
	iUp := strings.Index(joined, "CREATE VOLATILE TABLE "+vt(testScope, "orders_base"))
	iDown := strings.Index(joined, "CREATE VOLATILE TABLE "+vt(testScope, "customer_totals"))
	iPrev := strings.Index(joined, "SELECT TOP 11 * FROM "+vt(testScope, "customer_totals"))
	if iUp < 0 || iDown < 0 || iPrev < 0 || !(iUp < iDown && iDown < iPrev) {
		t.Fatalf("statement order wrong:\n%s", joined)
	}
	if !strings.Contains(joined, "FROM "+vt(testScope, "orders_base")) {
		t.Errorf("ref('orders_base') should resolve to its volatile table:\n%s", joined)
	}
	if !strings.Contains(joined, "ON COMMIT PRESERVE ROWS") {
		t.Error("volatile tables must PRESERVE ROWS (ANSI tmode commits after every statement)")
	}

	if got := res.Rebuilt; len(got) != 2 || got[0] != "orders_base" || got[1] != "customer_totals" {
		t.Errorf("Rebuilt = %v, want both cells in topo order", got)
	}
	if res.Preview.Source != "direct" {
		t.Errorf("preview source = %q, want direct", res.Preview.Source)
	}
	if len(res.Preview.Rows) != 2 || res.Preview.Truncated {
		t.Errorf("preview = %d rows truncated=%v, want the fake's 2 rows untruncated", len(res.Preview.Rows), res.Preview.Truncated)
	}
	// Driver []byte values must reach the preview as strings, not base64.
	if res.Preview.Rows[0][1] != "42.00" {
		t.Errorf("byte column = %#v, want the string it holds", res.Preview.Rows[0][1])
	}
}

func TestRunCellStalenessChain(t *testing.T) {
	s, log := newFakeSession(t)
	doc := testDoc(t, "SELECT customer_id FROM {{ ref('orders_base') }}")

	if _, err := s.RunCell(context.Background(), testScope, doc, "customer_totals", 10); err != nil {
		t.Fatal(err)
	}
	*log = nil

	// Unchanged document: nothing rebuilds, only the preview reads.
	res, err := s.RunCell(context.Background(), testScope, doc, "customer_totals", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rebuilt) != 0 {
		t.Errorf("unchanged doc rebuilt %v, want nothing", res.Rebuilt)
	}
	if creates := strings.Count(strings.Join(*log, ""), "CREATE VOLATILE"); creates != 0 {
		t.Errorf("unchanged doc issued %d CREATEs", creates)
	}

	// An upstream edit invalidates the chain: both rebuild.
	*log = nil
	doc2 := testDoc(t, "SELECT customer_id FROM {{ ref('orders_base') }}")
	doc2.Cells[0].Source = "SELECT 2 AS customer_id"
	res, err = s.RunCell(context.Background(), testScope, doc2, "customer_totals", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rebuilt) != 2 {
		t.Errorf("upstream edit rebuilt %v, want both cells", res.Rebuilt)
	}
}

func TestResolveConnectionAndDSN(t *testing.T) {
	dir := t.TempDir()
	body := `loom_project:
  target: clearscape
  outputs:
    clearscape:
      type: teradata
      host: "{{ env_var('FL_HOST') }}"
      user: u1
      password: "{{ env_var('FL_PASSWORD') }}"
      logmech: TD2
      schema: data_scientist
`
	if err := os.WriteFile(filepath.Join(dir, "profiles.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FL_HOST", "h1")
	t.Setenv("FL_PASSWORD", "p1")

	conn, err := ResolveConnection(dir, "loom_project")
	if err != nil {
		t.Fatalf("ResolveConnection: %v", err)
	}
	if conn.Engine != "teradata" || conn.Params["host"] != "h1" || conn.Params["password"] != "p1" {
		t.Errorf("conn = %+v, want resolved fields", conn)
	}

	dsn := TeradataDSNParams(conn.Params)
	if dsn["database"] != "data_scientist" {
		t.Errorf("dsn database = %q, want schema mapped to database", dsn["database"])
	}
	if _, leak := dsn["schema"]; leak {
		t.Error("dsn must not carry the schema key the driver rejects")
	}

	os.Unsetenv("FL_PASSWORD")
	if _, err := ResolveConnection(dir, "loom_project"); err == nil || !strings.Contains(err.Error(), "FL_PASSWORD") {
		t.Errorf("missing credential: err = %v, want it named", err)
	}
}

func TestOpenRefusesUnsupportedEngine(t *testing.T) {
	_, err := Open(context.Background(), &Connection{Engine: "duckdb"}, nil)
	var unsupported ErrUnsupportedEngine
	if err == nil || !strings.Contains(err.Error(), "duckdb") {
		t.Fatalf("err = %v, want an ErrUnsupportedEngine naming duckdb (%T)", err, unsupported)
	}
}

func TestWritePreview(t *testing.T) {
	dir := t.TempDir()
	p := project.CellPreview{Columns: []string{"a"}, Rows: [][]any{{1}}, Source: "direct"}
	if err := WritePreview(dir, "cell_1", p); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "target", project.PreviewsSubdir, "cell_1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"source":"direct"`) {
		t.Errorf("preview file %s should carry its lane", raw)
	}
	if err := WritePreview(dir, "../escape", p); err == nil {
		t.Error("a path-shaped id must refuse")
	}
}

// A python cell rides the same closure: its SQL input exports as CSV, the
// kernel (system python3, stdlib mode — no pandas required) executes it with
// the input under its cell id, and the published variable becomes the
// preview. Also proves the staleness ledger covers python (unchanged reruns
// peek instead of re-executing).
func TestRunPythonCell(t *testing.T) {
	s, log := newFakeSession(t)
	if s.venvPython == "" {
		t.Skip("no python3 on this machine")
	}
	t.Cleanup(s.Close)

	body := `apiVersion: loom/v1
kind: Project
metadata:
  name: fl_py
cells:
  - id: orders_base
    lang: sql
    source: |
      SELECT 1 AS customer_id
  - id: totals
    lang: python
    inputs: [orders_base]
    source: |
      print("rows:", len(orders_base))
      totals = orders_base
`
	doc, err := project.Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	res, err := s.RunCell(context.Background(), testScope, doc, "totals", 10)
	if err != nil {
		t.Fatalf("RunCell(python): %v", err)
	}
	if res.Preview.Source != "direct" || len(res.Preview.Columns) != 2 || len(res.Preview.Rows) != 2 {
		t.Errorf("preview = %+v, want the exported table back under the cell id", res.Preview)
	}
	if !strings.Contains(res.Preview.Stdout, "rows: 2") {
		t.Errorf("stdout = %q, want the print to arrive", res.Preview.Stdout)
	}
	// The pandas note is environment-dependent (present iff the interpreter
	// lacks pandas); what is invariant is that a note, when present, says so.
	if res.Preview.Note != "" && !strings.Contains(res.Preview.Note, "pandas") {
		t.Errorf("note = %q, want it to name pandas", res.Preview.Note)
	}
	if !strings.Contains(strings.Join(*log, "\n"), "SELECT TOP 100000 * FROM "+vt(testScope, "orders_base")) {
		t.Errorf("the SQL input should export from its volatile table:\n%s", strings.Join(*log, "\n"))
	}

	// Unchanged rerun: nothing rebuilds; the preview comes from a peek.
	res, err = s.RunCell(context.Background(), testScope, doc, "totals", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rebuilt) != 0 {
		t.Errorf("unchanged python rerun rebuilt %v, want nothing", res.Rebuilt)
	}
	if len(res.Preview.Rows) != 2 {
		t.Errorf("peek preview lost the value: %+v", res.Preview)
	}

	// A failing cell surfaces python's own words and stays off the ledger.
	doc.Cells[1].Source = "totals = undefined_name"
	_, err = s.RunCell(context.Background(), testScope, doc, "totals", 10)
	if err == nil || !strings.Contains(err.Error(), "undefined_name") {
		t.Errorf("err = %v, want python's NameError to surface", err)
	}
}

// Two projects on one connection with a cell both call orders_base must not
// share a volatile table, and running one must not mark the other stale:
// the scope tag keeps their tables, ledgers and exports apart.
func TestScopesIsolateSameNamedCells(t *testing.T) {
	s, log := newFakeSession(t)
	scopeA, scopeB := "/repo/a/project.yaml", "/repo/b/project.yaml"
	docA := testDoc(t, "SELECT customer_id FROM {{ ref('orders_base') }}")
	docB := testDoc(t, "SELECT customer_id, 2 AS n FROM {{ ref('orders_base') }}")

	if vt(scopeA, "orders_base") == vt(scopeB, "orders_base") {
		t.Fatal("two documents resolved the same volatile name")
	}
	if got := vt(scopeA, "orders_base"); !regexp.MustCompile(`^loomfl_[0-9a-f]{8}_orders_base$`).MatchString(got) {
		t.Fatalf("volatile name %q is not identifier-shaped", got)
	}

	if _, err := s.RunCell(context.Background(), scopeA, docA, "customer_totals", 10); err != nil {
		t.Fatal(err)
	}
	*log = nil
	resB, err := s.RunCell(context.Background(), scopeB, docB, "customer_totals", 10)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*log, "\n")
	// B builds its OWN tables (both of them — a fresh scope has no ledger)
	// and its ref resolves inside its own scope, never into A's.
	if len(resB.Rebuilt) != 2 {
		t.Errorf("B rebuilt %v, want both of its cells", resB.Rebuilt)
	}
	if !strings.Contains(joined, "FROM "+vt(scopeB, "orders_base")) {
		t.Errorf("B's ref should resolve to B's table:\n%s", joined)
	}
	if strings.Contains(joined, vt(scopeA, "orders_base")) {
		t.Errorf("B's run touched A's table:\n%s", joined)
	}

	// A is untouched by B's run: an unchanged rerun rebuilds nothing.
	*log = nil
	resA, err := s.RunCell(context.Background(), scopeA, docA, "customer_totals", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resA.Rebuilt) != 0 {
		t.Errorf("A rebuilt %v after B ran, want nothing — B's ledger leaked into A's", resA.Rebuilt)
	}
}

// The python kernel is shared per connection too, so its namespace is
// scoped the same way: a variable published in project A is not visible
// from project B, and each scope's ref() reaches only its own cells.
func TestScopesIsolatePythonNamespaces(t *testing.T) {
	s, _ := newFakeSession(t)
	if s.venvPython == "" {
		t.Skip("no python3 on this machine")
	}
	t.Cleanup(s.Close)

	parse := func(cellSrc string) *project.Document {
		body := `apiVersion: loom/v1
kind: Project
metadata:
  name: fl_scope
cells:
  - id: orders_base
    lang: sql
    source: |
      SELECT 1 AS customer_id
  - id: totals
    lang: python
    inputs: [orders_base]
    source: |
` + cellSrc
		doc, err := project.Parse([]byte(body))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		return doc
	}
	scopeA, scopeB := "/repo/a/project.yaml", "/repo/b/project.yaml"
	docA := parse("      secret = 41\n      totals = orders_base\n")
	if _, err := s.RunCell(context.Background(), scopeA, docA, "totals", 10); err != nil {
		t.Fatalf("A: %v", err)
	}

	// B's cell reaches for a name only A published: python's own NameError.
	docB := parse("      totals = secret + 1\n")
	_, err := s.RunCell(context.Background(), scopeB, docB, "totals", 10)
	if err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("B saw A's variable (err = %v); namespaces must be per scope", err)
	}

	// B's own ref() sees B's inputs and nothing of A's other cells.
	docB2 := parse("      totals = ref('orders_base')\n")
	res, err := s.RunCell(context.Background(), scopeB, docB2, "totals", 10)
	if err != nil {
		t.Fatalf("B ref: %v", err)
	}
	if len(res.Preview.Rows) != 2 {
		t.Errorf("B's preview = %+v, want its own exported table", res.Preview)
	}
	// Each scope exported under its own directory.
	if s.exportPath(scopeA, "orders_base") == s.exportPath(scopeB, "orders_base") {
		t.Error("two scopes share one export path")
	}
}
