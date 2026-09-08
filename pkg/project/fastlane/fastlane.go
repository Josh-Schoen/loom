// Package fastlane is the notebook's interactive execution lane (backlog 41).
//
// The two-speed contract: a cell RERUN is fast and unverified — this package
// runs the cell's SQL directly over the warehouse's own wire protocol — while
// "Run verified" stays a dbt build (pkg/project.Run): tests, grain checks,
// verdicts, materialization. dbt is the verifier, not the keystroke engine;
// measured against the same warehouse, a dbt invocation pays ~13s of process
// startup around ~2s of SQL, and this lane pays ~0.2s.
//
// THE CELL ID IS THE VARIABLE. The document's explicit inputs:/ref() DAG is
// the dependency truth (never inferred from code — the yaml is what the
// agent edits and the verifier reads). A session materializes each upstream
// cell as a VOLATILE table named after it, so {{ ref('orders_base') }}
// resolves to session-scoped state: fast, invisible to the schema, gone when
// the connection closes. Staleness is a hash chain — a cell rebuilds when
// its source or any upstream's hash changes, and only then.
//
// CREDENTIALS: this package resolves them (profiles.yml's env_var() names
// against the process environment) and hands them to the driver as a
// marshalled DSN. They are never a log line and never an argv. This is a
// DIFFERENT contract from pkg/project's dbt path (which never touches them);
// the difference is the package boundary.
package fastlane

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	// Registers database/sql driver "teradata" (pure Go, wire protocol).
	_ "github.com/Teradata-TIO/go-teradata"
	"gopkg.in/yaml.v3"

	"github.com/teradata-labs/loom/pkg/project"
)

// DefaultPreviewRows matches the verified lane's sample size, so switching
// lanes never changes how much data the reader sees.
const DefaultPreviewRows = 100

// queryTimeout bounds one statement. Interactive means seconds; a runaway
// query must hand the session back.
const queryTimeout = 120 * time.Second

// envVarPattern matches dbt's env_var() indirection in a profile value.
// Only this form is resolved — profiles are configuration, not templates.
var envVarPattern = regexp.MustCompile(`\{\{\s*env_var\(\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]\s*\)\s*\}\}`)

// Connection is a resolved connection to a dbt profile's active target.
type Connection struct {
	Engine string
	// Params are the profile's connection fields with env_var() resolved.
	// Treat as secret: never log, never put on an argv.
	Params map[string]string
	// ID changes when the connection facts change — callers key cached
	// sessions on it.
	ID string
}

// ResolveConnection reads the active target of the named profile and
// resolves its connection fields against the process environment. An
// env_var() naming an unset variable errors BY NAME — never a silently
// empty credential.
func ResolveConnection(profilesDir, profile string) (*Connection, error) {
	body, err := os.ReadFile(filepath.Join(profilesDir, "profiles.yml")) // #nosec G304 -- the resolved dbt profiles home
	if err != nil {
		return nil, fmt.Errorf("no dbt profiles.yml at %s: %v", profilesDir, err)
	}
	var doc map[string]struct {
		Target  string                       `yaml:"target"`
		Outputs map[string]map[string]string `yaml:"outputs"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("profiles.yml did not parse: %v", err)
	}
	entry, ok := doc[profile]
	if !ok {
		return nil, fmt.Errorf("profiles.yml has no profile %q", profile)
	}
	raw, ok := entry.Outputs[entry.Target]
	if !ok {
		return nil, fmt.Errorf("profile %q names no output %q", profile, entry.Target)
	}

	params := map[string]string{}
	for _, key := range []string{"host", "user", "password", "logmech", "path", "database", "schema"} {
		if v, ok := raw[key]; ok && v != "" {
			resolved, err := resolveValue(v)
			if err != nil {
				return nil, err
			}
			params[key] = resolved
		}
	}
	engine := raw["type"]
	if engine == "" {
		return nil, fmt.Errorf("target %q declares no engine type", entry.Target)
	}
	return &Connection{
		Engine: engine,
		Params: params,
		ID:     engine + "|" + params["host"] + "|" + params["path"] + "|" + params["user"],
	}, nil
}

func resolveValue(v string) (string, error) {
	var missing string
	out := envVarPattern.ReplaceAllStringFunc(v, func(m string) string {
		name := envVarPattern.FindStringSubmatch(m)[1]
		val, ok := os.LookupEnv(name)
		if !ok {
			missing = name
		}
		return val
	})
	if missing != "" {
		return "", fmt.Errorf("profile needs %s, which is not set in the app's environment", missing)
	}
	return out, nil
}

// TeradataDSNParams maps profile fields to the keys go-teradata's JSON DSN
// accepts — its parser REJECTS unknown fields (`json: unknown field
// "schema"`, proven live). dbt-teradata's `schema` means the default
// database, so it becomes `database` when no explicit one is set.
func TeradataDSNParams(p map[string]string) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"host", "user", "password", "logmech", "database"} {
		if v := p[k]; v != "" {
			out[k] = v
		}
	}
	if out["database"] == "" && p["schema"] != "" {
		out["database"] = p["schema"]
	}
	return out
}

// VenvPython is the loom-managed dbt venv's interpreter under dataDir, or ""
// when there is none — the python kernel's interpreter, derived one way for
// every caller (desktop, daemon).
func VenvPython(dataDir string) string {
	py := filepath.Join(dataDir, project.DefaultDBTVenvSubdir, "bin", "python")
	if runtime.GOOS == "windows" {
		py = filepath.Join(dataDir, project.DefaultDBTVenvSubdir, "Scripts", "python.exe")
	}
	if info, err := os.Stat(py); err != nil || info.IsDir() {
		return ""
	}
	return py
}

// ErrUnsupportedEngine reports an engine this lane ships no driver for —
// the caller's signal to stay on the dbt path rather than fail the run.
type ErrUnsupportedEngine struct{ Engine string }

func (e ErrUnsupportedEngine) Error() string {
	return fmt.Sprintf("fastlane has no in-process driver for engine %q (have: teradata)", e.Engine)
}

// Session is one live connection running one document's cells. Volatile
// tables and the staleness ledger live exactly as long as it does.
//
// One statement at a time by design (SetMaxOpenConns(1)): volatile tables
// are per-CONNECTION state on Teradata, so a pool of two connections would
// be two disjoint notebooks.
type Session struct {
	db     *sql.DB
	engine string

	// venvPython runs the python kernel; "" means python cells refuse with
	// the reason.
	venvPython string
	// scratch holds the kernel script and exported CSVs for this session.
	scratch string

	mu sync.Mutex
	// scopes holds one ledger per document (see Scope): the connection and
	// the kernel are shared for warmth, the STATE never is.
	scopes map[string]*scopeState
	kernel *kernel
}

// scopeState is one document's staleness ledger inside a shared Session.
type scopeState struct {
	// built: cell id → the content hash its volatile table was built from.
	built map[string]string
	// pyBuilt is the same ledger for python cells, whose state lives in the
	// kernel's namespace — cleared whenever the kernel (re)spawns.
	pyBuilt map[string]string
	// exported tracks which hash of a SQL cell's rows the kernel-facing CSV
	// carries, so an unchanged upstream is not re-exported per python run.
	exported map[string]string
}

func newScopeState() *scopeState {
	return &scopeState{built: map[string]string{}, pyBuilt: map[string]string{}, exported: map[string]string{}}
}

// scope returns the ledger for one document key, creating it on first use.
// Callers hold s.mu.
func (s *Session) scope(key string) *scopeState {
	st, ok := s.scopes[key]
	if !ok {
		st = newScopeState()
		s.scopes[key] = st
	}
	return st
}

// scopeTag is the identifier-safe form of a document key: eight hex digits
// of its hash, so two projects on one connection with a cell both call
// orders_base get two volatile tables, two CSVs and two python namespaces.
// The key is whatever the caller uses as the document's stable identity —
// the desktop and the daemon both pass the project.yaml path.
func scopeTag(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}

// Options tune a Session beyond the connection.
type Options struct {
	// VenvPython is the interpreter for python cells (the loom-managed dbt
	// venv's python). Empty disables python cells with a clear refusal.
	VenvPython string
}

// sqlOpen is the driver seam: production opens the registered driver; tests
// substitute a recording fake (the "teradata" name is taken by the real one).
var sqlOpen = sql.Open

// Open connects. Returns ErrUnsupportedEngine for engines without an
// in-process driver. opts may be nil.
func Open(ctx context.Context, conn *Connection, opts *Options) (*Session, error) {
	if conn.Engine != "teradata" {
		return nil, ErrUnsupportedEngine{Engine: conn.Engine}
	}
	dsn, err := json.Marshal(TeradataDSNParams(conn.Params))
	if err != nil {
		return nil, err
	}
	db, err := sqlOpen("teradata", string(dsn))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	pingCtx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, err
	}
	scratch, err := os.MkdirTemp("", "loom-fastlane-")
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Session{db: db, engine: conn.Engine, scratch: scratch, scopes: map[string]*scopeState{}}
	if opts != nil {
		s.venvPython = opts.VenvPython
	}
	return s, nil
}

// Close drops the connection (the warehouse discards the volatile tables
// with it), the kernel, and the scratch space.
func (s *Session) Close() {
	if s.kernel != nil {
		s.kernel.stop()
		s.kernel = nil
	}
	if s.db != nil {
		_ = s.db.Close()
	}
	if s.scratch != "" {
		_ = os.RemoveAll(s.scratch)
	}
}

// Ping reports whether the session's connection is still alive — the
// caller's tell between "the warehouse refused the query" (an answer) and
// "the session died" (reconnect and retry).
func (s *Session) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Warm pays the lane's one-time costs ahead of the first click: the
// connection is already open (Open pinged), and needPython spawns the kernel
// now — its python + pandas import is the bulk of a cold run's ~18s, and a
// reader opening a notebook with python cells is about to need it.
func (s *Session) Warm(ctx context.Context, needPython bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !needPython {
		return nil
	}
	return s.ensureKernel(ctx)
}

// Result is one fast run's outcome.
type Result struct {
	Preview project.CellPreview
	// Rebuilt lists the cells whose volatile tables this run had to
	// (re)materialize, in execution order — the honest cost report.
	Rebuilt  []string
	Duration time.Duration
}

// volatileName is the session-scoped table a cell materializes as. The
// prefix keeps cell names from shadowing real tables in the default
// database, the scope tag keeps two documents' same-named cells apart;
// document validation already constrains ids to identifier-safe characters.
func volatileName(scope, cellID string) string {
	return "loomfl_" + scopeTag(scope) + "_" + cellID
}

// RunCell materializes the cell's upstream closure (stale parts only), then
// the cell itself, and returns a sampled preview read back from its volatile
// table. previewRows <= 0 takes DefaultPreviewRows.
//
// scope is the document's stable identity (the project.yaml path). A
// Session is shared per CONNECTION, so without it two projects whose cells
// share an id would share one volatile table and one python variable —
// silently reading each other's data and thrashing each other's ledger.
// Every table, export and namespace this run touches is tagged with it.
//
// SELF-HEALING, once: volatile tables are per-CONNECTION state, and
// database/sql silently replaces a dead connection — leaving the ledger
// claiming tables the fresh connection does not have. The tell is the
// warehouse refusing a loomfl_ name as nonexistent; on that, the ledger
// resets and the run replays from scratch, exactly once.
func (s *Session) RunCell(ctx context.Context, scope string, doc *project.Document, cellID string, previewRows int) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.runCellLocked(ctx, scope, doc, cellID, previewRows)
	if err != nil && lostVolatileState(err) {
		// The CONNECTION lost its volatile tables, so every scope's did.
		for _, st := range s.scopes {
			st.built = map[string]string{}
		}
		res, err = s.runCellLocked(ctx, scope, doc, cellID, previewRows)
	}
	return res, err
}

// lostVolatileState recognizes "the session's volatile tables are gone" —
// Teradata error 3807 (object does not exist) against a loomfl_ name.
func lostVolatileState(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "loomfl_") &&
		(strings.Contains(msg, "3807") || strings.Contains(strings.ToLower(msg), "does not exist"))
}

// fastRunnable is what this lane can execute: SQL/call cells (volatile
// tables) and python cells (the kernel). Everything else is presentation.
func fastRunnable(c project.Cell) bool {
	if c.Lang == project.LangPython {
		return strings.TrimSpace(c.Source) != ""
	}
	return project.EmitsModel(c)
}

func (s *Session) runCellLocked(ctx context.Context, scope string, doc *project.Document, cellID string, previewRows int) (*Result, error) {
	if previewRows <= 0 {
		previewRows = DefaultPreviewRows
	}
	st := s.scope(scope)
	cell, ok := doc.Cell(cellID)
	if !ok {
		return nil, fmt.Errorf("no cell %q in this project", cellID)
	}
	if !fastRunnable(cell) {
		return nil, fmt.Errorf("cell %q has no code to run", cellID)
	}

	closure, err := upstreamClosure(doc, cellID)
	if err != nil {
		return nil, err
	}

	started := time.Now()
	var rebuilt []string
	var targetReply *kernelReply
	hashes := map[string]string{}
	for _, id := range closure {
		c, _ := doc.Cell(id)
		h := contentHash(c, hashes)
		hashes[id] = h

		if c.Lang == project.LangPython {
			if st.pyBuilt[id] == h {
				if id != cellID {
					continue
				}
				// Unchanged target: preview the published value without
				// re-executing. A miss (fresh kernel) falls through to run.
				if reply, err := s.peekPython(ctx, scope, id, previewRows); err == nil && reply.OK {
					targetReply = reply
					continue
				}
			}
			reply, err := s.runPython(ctx, scope, doc, c, hashes, previewRows)
			if err != nil {
				delete(st.pyBuilt, id)
				return nil, fmt.Errorf("cell %q: %w", id, err)
			}
			st.pyBuilt[id] = h
			rebuilt = append(rebuilt, id)
			if id == cellID {
				targetReply = reply
			}
			continue
		}

		if st.built[id] == h {
			continue
		}
		if err := s.materialize(ctx, scope, c); err != nil {
			// A failed build means the ledger must not claim the table.
			delete(st.built, id)
			return nil, fmt.Errorf("cell %q: %w", id, err)
		}
		st.built[id] = h
		rebuilt = append(rebuilt, id)
	}

	var preview project.CellPreview
	if cell.Lang == project.LangPython {
		preview = previewFromKernel(targetReply)
	} else {
		preview, err = s.preview(ctx, scope, cellID, previewRows)
		if err != nil {
			return nil, err
		}
	}
	elapsed := time.Since(started)
	preview.RanAtUnix = time.Now().Unix()
	preview.DurationMs = elapsed.Milliseconds()
	return &Result{Preview: preview, Rebuilt: rebuilt, Duration: elapsed}, nil
}

// previewFromKernel shapes a kernel reply as the standard preview. A cell
// with no tabular value is stdout-only — a result, not an absence.
func previewFromKernel(reply *kernelReply) project.CellPreview {
	out := project.CellPreview{Source: "direct"}
	if reply == nil {
		return out
	}
	out.Stdout = reply.Stdout
	if reply.Preview != nil {
		out.Columns = reply.Preview.Columns
		out.Rows = reply.Preview.Rows
		out.Truncated = reply.Preview.Truncated
	}
	if !reply.Pandas {
		out.Note = "the venv has no pandas, so inputs arrived as plain rows — ask the agent to install pandas"
	}
	return out
}

// ensureKernel spawns the python kernel on demand. A fresh kernel is an
// empty namespace, so the python ledger resets with it.
func (s *Session) ensureKernel(ctx context.Context) error {
	if s.kernel != nil {
		return nil
	}
	if s.venvPython == "" {
		return fmt.Errorf("python cells need the loom-managed dbt venv's python, and none was found")
	}
	k, err := startKernel(ctx, s.venvPython, s.scratch)
	if err != nil {
		return err
	}
	s.kernel = k
	s.resetPython()
	return nil
}

// resetPython forgets every scope's python ledger: a (re)spawned kernel is
// an empty namespace for all of them.
func (s *Session) resetPython() {
	for _, st := range s.scopes {
		st.pyBuilt = map[string]string{}
	}
}

// dropKernel discards a kernel whose stream broke, with its namespace ledger.
func (s *Session) dropKernel() {
	if s.kernel != nil {
		s.kernel.stop()
		s.kernel = nil
	}
	s.resetPython()
}

// runPython executes one python cell in the kernel: its SQL inputs export as
// CSV (once per content hash), its python inputs are already in the
// namespace (the closure ran them first), and its published value is the
// variable named after the cell.
func (s *Session) runPython(ctx context.Context, scope string, doc *project.Document, c project.Cell, hashes map[string]string, previewRows int) (*kernelReply, error) {
	if err := s.ensureKernel(ctx); err != nil {
		return nil, err
	}
	st := s.scope(scope)
	tables := map[string]string{}
	for _, in := range c.Inputs {
		inCell, ok := doc.Cell(in)
		if !ok || !project.EmitsModel(inCell) {
			continue // python inputs live in the namespace; others are not data
		}
		if st.exported[in] != hashes[in] {
			if _, err := s.exportTable(ctx, scope, in); err != nil {
				return nil, fmt.Errorf("exporting input %q: %w", in, err)
			}
			st.exported[in] = hashes[in]
		}
		tables[in] = s.exportPath(scope, in)
	}

	reply, err := s.kernel.roundTrip(map[string]any{
		"op": "run", "scope": scopeTag(scope), "cell": c.ID, "code": c.Source,
		"tables": tables, "preview_rows": previewRows,
	}, pythonRunTimeout)
	if err != nil {
		s.dropKernel()
		return nil, err
	}
	if !reply.OK {
		return nil, fmt.Errorf("%s", strings.TrimSpace(reply.Error))
	}
	return reply, nil
}

// peekPython previews an already-published value without re-executing.
func (s *Session) peekPython(ctx context.Context, scope, cellID string, previewRows int) (*kernelReply, error) {
	if err := s.ensureKernel(ctx); err != nil {
		return nil, err
	}
	reply, err := s.kernel.roundTrip(map[string]any{
		"op": "peek", "scope": scopeTag(scope), "cell": cellID, "preview_rows": previewRows,
	}, queryTimeout)
	if err != nil {
		s.dropKernel()
		return nil, err
	}
	return reply, nil
}

// upstreamClosure is the cell and its transitive fast-runnable inputs (SQL
// and python), in the document's topological order.
func upstreamClosure(doc *project.Document, cellID string) ([]string, error) {
	order, err := doc.TopoOrder()
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{cellID: true}
	// Topo order guarantees inputs precede dependents, so one reverse pass
	// closes the set.
	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]
		if !wanted[id] {
			continue
		}
		c, _ := doc.Cell(id)
		for _, in := range c.Inputs {
			if inCell, ok := doc.Cell(in); ok && fastRunnable(inCell) {
				wanted[in] = true
			}
		}
	}
	var closure []string
	for _, id := range order {
		if wanted[id] {
			if c, ok := doc.Cell(id); ok && fastRunnable(c) {
				closure = append(closure, id)
			}
		}
	}
	return closure, nil
}

// contentHash chains a cell's source with its inputs' hashes, so an upstream
// edit invalidates everything downstream of it and nothing else.
func contentHash(c project.Cell, upstream map[string]string) string {
	h := sha256.New()
	h.Write([]byte(c.Source))
	ins := append([]string(nil), c.Inputs...)
	sort.Strings(ins)
	for _, in := range ins {
		h.Write([]byte("\x00" + in + "=" + upstream[in]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// materialize (re)creates one cell's volatile table from its ref-resolved
// source.
func (s *Session) materialize(ctx context.Context, scope string, c project.Cell) error {
	name := volatileName(scope, c.ID)
	resolved := resolveRefs(scope, c.Source)

	dropCtx, cancel := context.WithTimeout(ctx, queryTimeout)
	// A missing table errors harmlessly; a real problem resurfaces on CREATE.
	_, _ = s.db.ExecContext(dropCtx, "DROP TABLE "+name)
	cancel()

	// ON COMMIT PRESERVE ROWS: in ANSI mode (the profile's tmode) a volatile
	// table's rows would otherwise vanish at the implicit commit.
	stmt := "CREATE VOLATILE TABLE " + name + " AS (\n" + resolved + "\n) WITH DATA ON COMMIT PRESERVE ROWS"
	execCtx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	if _, err := s.db.ExecContext(execCtx, stmt); err != nil {
		return err
	}
	return nil
}

// resolveRefs rewrites {{ ref('x') }} to x's volatile table in this scope.
func resolveRefs(scope, source string) string {
	return project.RefPattern.ReplaceAllStringFunc(source, func(m string) string {
		id := project.RefPattern.FindStringSubmatch(m)[1]
		return volatileName(scope, id)
	})
}

// preview samples the cell's volatile table. TOP n+1 lets Truncated be a
// fact rather than a guess, without pulling the whole result into memory.
func (s *Session) preview(ctx context.Context, scope, cellID string, previewRows int) (project.CellPreview, error) {
	var out project.CellPreview
	qctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	q := fmt.Sprintf("SELECT TOP %d * FROM %s", previewRows+1, volatileName(scope, cellID))
	rows, err := s.db.QueryContext(qctx, q)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return out, err
	}
	out.Columns = cols
	out.Source = "direct"
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return out, err
		}
		row := make([]any, len(cols))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				row[i] = string(b)
			} else {
				row[i] = v
			}
		}
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out.Rows) > previewRows {
		out.Rows = out.Rows[:previewRows]
		out.Truncated = true
	}
	return out, nil
}

// WritePreview persists a preview where the desktop's document loader reads
// them — the same file, the same shape, as the verified lane's captures
// (pkg/project.capturePreviews), so which lane ran last is carried by
// CellPreview.Source and nothing else has to change.
func WritePreview(buildDir, cellID string, p project.CellPreview) error {
	// The id-becomes-a-path guard, same as the dbt writer's.
	if cellID == "" || cellID != filepath.Base(cellID) || strings.ContainsAny(cellID, `/\`) {
		return fmt.Errorf("cell id %q cannot name a preview file", cellID)
	}
	dir := filepath.Join(buildDir, "target", project.PreviewsSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, cellID+".json"), body, 0o644) // #nosec G306 -- display data, read by the desktop UI
}
