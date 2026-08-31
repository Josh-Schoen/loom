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

package project

// Running a compiled project through dbt.
//
// This is the ONE dbt invocation path. Two callers share it: the run_project
// builtin (the agent's tool) and the desktop's single-cell rerun. Neither
// owns the machinery — a second copy of "which flags dbt gets" would drift
// the moment one side learned something the other did not.
//
// What stays with the callers: how a run is REPORTED. The tool turns an
// outcome into a shuttle.Result with its own failure codes; the desktop turns
// it into a refreshed notebook. Run returns the facts and takes no view.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/teradata-labs/loom/pkg/project/oracle"
)

// Environment variables read at run time (never cached at construction, so a
// daemon that learns its dbt location later still works).
const (
	// EnvDBTBin overrides the dbt binary. Looked up on PATH.
	EnvDBTBin = "LOOM_DBT_BIN"
	// EnvDBTProfilesDir overrides the dbt profiles directory — the file that
	// carries connection details. CONSTRAINT: warehouse credentials reach dbt
	// through that profile's env_var() indirection from the host process's own
	// environment. They are never parameters here and this package never sees
	// them.
	EnvDBTProfilesDir = "LOOM_DBT_PROFILES_DIR"
)

// DefaultDBTProfilesSubdir is the profiles directory inside the Loom data
// directory used when EnvDBTProfilesDir is unset (~/.loom/dbt-spike).
const DefaultDBTProfilesSubdir = "dbt-spike"

const (
	// DefaultRunTimeout bounds one dbt build. dbt builds are minutes, not
	// seconds; a hung warehouse connection must not hold the caller forever.
	DefaultRunTimeout = 15 * time.Minute

	// setupTimeout bounds the best-effort loom_setup run-operation.
	setupTimeout = 2 * time.Minute

	// DefaultOutputCap bounds captured dbt output. dbt is chatty and the
	// output can ride back through an LLM context, so the tail is kept (dbt
	// reports its errors last) and the head is dropped.
	DefaultOutputCap = 64 * 1024

	// BuildCacheDir holds compiled dbt projects. Generated dbt code and dbt's
	// own target/ artifacts live under the Loom data directory, never in the
	// user's repo: the repo holds the project document, nothing derived.
	BuildCacheDir = "projects-cache"

	// BuildSubdir is the compiled project root inside a cache entry.
	BuildSubdir = "build"

	// buildPathHashLen is how much of the document path's sha256 names the
	// cache entry — enough to separate documents, short enough to read.
	buildPathHashLen = 16
)

// BuildDir is the compile destination for a project document: keyed by the
// document's absolute path so two documents never share a build tree, and
// rooted in dataDir so generated dbt code and dbt's target/ artifacts stay out
// of the user's git repo.
//
// CONSTRAINT: the hashed input is the document's Clean-then-Abs path, NOT its
// symlink-resolved real path. Every caller must derive the same directory or a
// run's artifacts become invisible to the reader with nothing to say why, so
// this function is the only place the derivation exists.
func BuildDir(dataDir, docPath string) string {
	abs := filepath.Clean(docPath)
	if p, err := filepath.Abs(abs); err == nil {
		abs = p
	}
	sum := sha256.Sum256([]byte(abs))
	key := hex.EncodeToString(sum[:])[:buildPathHashLen]
	return filepath.Join(dataDir, BuildCacheDir, key, BuildSubdir)
}

// ResolveDBT resolves the dbt binary and the profiles directory from the
// process environment. dataDir is the Loom data directory, which holds the
// default profile when EnvDBTProfilesDir is unset.
//
// The error names the binary that was looked for: it is the whole diagnosis
// for "dbt is not installed", and both callers pass it through to the user.
func ResolveDBT(dataDir string) (bin, profilesDir string, err error) {
	binName := strings.TrimSpace(os.Getenv(EnvDBTBin))
	if binName == "" {
		binName = "dbt"
	}
	binPath, lookErr := exec.LookPath(binName)
	if lookErr != nil {
		return "", "", fmt.Errorf("dbt binary %q not found on PATH: %v", binName, lookErr)
	}
	profilesDir = strings.TrimSpace(os.Getenv(EnvDBTProfilesDir))
	if profilesDir == "" {
		profilesDir = filepath.Join(dataDir, DefaultDBTProfilesSubdir)
	}
	return binPath, profilesDir, nil
}

// EmitsModel reports whether a cell compiles to a dbt model. A sql cell needs
// source; a call cell compiles only when it carries its own source, because v1
// does not resolve registry addresses. Everything else is presentation.
func EmitsModel(c Cell) bool {
	if strings.TrimSpace(c.Source) == "" {
		return false
	}
	return c.Lang == LangSQL || c.Lang == LangCall
}

// ── Node selection ──────────────────────────────────────────────────────────

// Selection is a dbt node selection derived from the document.
//
// Both halves come from one derivation on purpose: Selector goes to dbt and
// Cells scopes the previews taken afterwards, and a caller assembling those
// separately could refresh a preview for a model dbt was never asked to
// build — showing a stale sample as if the run had just produced it.
type Selection struct {
	// Selector is the dbt --select expression.
	Selector string
	// Cells are the cell IDs the selector reaches, in dependency order.
	Cells []string
}

// SelectDownstream selects one cell and everything downstream of it — the
// selection a single-cell rerun needs, since a cell's consumers are stale the
// moment its SQL changes. The dbt form is "<cell>+".
func (d *Document) SelectDownstream(id string) (Selection, error) {
	if _, ok := d.Cell(id); !ok {
		return Selection{}, fmt.Errorf("project: no cell %q", id)
	}
	order, err := d.TopoOrder()
	if err != nil {
		return Selection{}, err
	}
	reached := map[string]bool{id: true}
	// One forward pass over dependency order suffices: a cell's inputs are
	// always earlier in the order, so reachability is settled before it is
	// read.
	for _, cellID := range order {
		if reached[cellID] {
			continue
		}
		c, ok := d.Cell(cellID)
		if !ok {
			continue
		}
		for _, in := range c.Inputs {
			if reached[in] {
				reached[cellID] = true
				break
			}
		}
	}
	cells := make([]string, 0, len(reached))
	for _, cellID := range order {
		if reached[cellID] {
			cells = append(cells, cellID)
		}
	}
	return Selection{Selector: id + "+", Cells: cells}, nil
}

// ── The run ─────────────────────────────────────────────────────────────────

// RunOptions configures one dbt build. Zero fields take the package defaults;
// DBTBin and ProfilesDir are required (see ResolveDBT).
type RunOptions struct {
	// DBTBin is the dbt executable's path.
	DBTBin string
	// ProfilesDir is handed to dbt as --profiles-dir.
	ProfilesDir string
	// Selection scopes the build to some of the document's cells. Nil builds
	// the whole project.
	Selection *Selection
	// Timeout bounds the dbt build. Zero means DefaultRunTimeout.
	Timeout time.Duration
	// OutputCap bounds the captured dbt output in bytes. Zero means
	// DefaultOutputCap.
	OutputCap int
}

// RunOutcome is what one dbt build produced. It reports; it does not judge —
// a failing verification test is a normal outcome with Err nil, and the
// caller decides what that means for its own surface.
type RunOutcome struct {
	// ExitCode is dbt build's exit status, or -1 when it could not be started.
	ExitCode int
	// Output is dbt build's combined output, capped to OutputCap with the tail
	// kept.
	Output string
	// Err is set ONLY when dbt produced no verdicts at all — a missing or
	// unreadable run_results.json, i.e. dbt never got as far as running
	// anything. Records is then nil and Output is the only evidence.
	Err error
	// Artifact is the raw target/run_results.json, nil when Err is set.
	Artifact []byte
	// Records is the artifact folded per cell ID.
	Records map[string][]oracle.VerificationRecord
	// Previews is how many cell previews were written this run.
	Previews int
}

// Run invokes dbt in buildDir, folds its artifact into per-cell records, and
// refreshes the cell previews.
//
// buildDir must already hold a compiled project (see Compile). The document is
// needed for the preview pass, which walks the cells that became models.
func Run(ctx context.Context, doc *Document, buildDir string, opts RunOptions) RunOutcome {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
	outputCap := opts.OutputCap
	if outputCap <= 0 {
		outputCap = DefaultOutputCap
	}

	// Best-effort audit-table setup first: the row-audit post-hook INSERTs
	// into loom_audit, which a fresh schema (or a teardown) lacks. Failure
	// here is ignored — an already-existing table errors harmlessly, and a
	// real problem resurfaces loudly in the build's own records.
	setupCtx, setupCancel := context.WithTimeout(ctx, setupTimeout)
	setupCmd := exec.CommandContext(setupCtx, opts.DBTBin, // #nosec G204 -- DBTBin comes from the host environment, the arguments are fixed
		"run-operation", "loom_setup", "--no-partial-parse", "--profiles-dir", opts.ProfilesDir)
	setupCmd.Dir = buildDir
	_, _ = setupCmd.CombinedOutput()
	setupCancel()

	// --no-partial-parse: the project is regenerated from the document on
	// every compile, so a cached manifest can only be stale.
	args := []string{"build", "--no-partial-parse", "--profiles-dir", opts.ProfilesDir}
	if opts.Selection != nil && strings.TrimSpace(opts.Selection.Selector) != "" {
		args = append(args, "--select", opts.Selection.Selector)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 -- DBTBin comes from the host process's own environment
	// (EnvDBTBin), and the selector is built from validated cell IDs.
	cmd := exec.CommandContext(runCtx, opts.DBTBin, args...)
	cmd.Dir = buildDir
	// cmd.Env stays nil so the child inherits the host environment: that is
	// how the dbt profile's env_var() indirection reaches its credentials
	// without any secret passing through here.
	combined, runErr := cmd.CombinedOutput()

	out := RunOutcome{Output: capTail(string(combined), outputCap)}
	var exitErr *exec.ExitError
	switch {
	case errors.As(runErr, &exitErr):
		out.ExitCode = exitErr.ExitCode()
	case runErr != nil:
		out.ExitCode = -1
	}

	artifact, readErr := os.ReadFile(filepath.Join(buildDir, "target", "run_results.json")) // #nosec G304 -- buildDir is derived from the data directory, not from input
	if readErr != nil {
		out.Err = fmt.Errorf("dbt produced no run_results.json (%v)", readErr)
		return out
	}
	records, foldErr := RecordsFromRunResults(artifact)
	if foldErr != nil {
		out.Err = fmt.Errorf("dbt run_results.json is unreadable: %v", foldErr)
		return out
	}
	out.Artifact = artifact
	out.Records = records

	// Previews only after the records folded: they are a reading convenience
	// layered on a run that already produced verdicts, never a precondition
	// for one. capturePreviews cannot fail (see its doc comment).
	out.Previews = capturePreviews(ctx, doc, opts, buildDir)
	return out
}

// capTail keeps the last max bytes of s, marking what was dropped. dbt reports
// its errors last, so the tail is the half worth keeping.
func capTail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return fmt.Sprintf("[... %d bytes of earlier dbt output dropped ...]\n%s", len(s)-max, s[len(s)-max:])
}

// ── Result previews ─────────────────────────────────────────────────────────
//
// A run leaves the notebook with verdicts but no DATA, which is the surface's
// biggest legibility gap. After a successful build, the runner asks dbt for a
// sampled preview of every cell that became a model and persists it beside the
// run artifact, where the desktop reads it.
//
// BEST-EFFORT, without exception: preview capture happens AFTER the records
// are folded and can only add to RunOutcome.Previews. Every failure path — dbt
// show missing, a warehouse that refuses the query, unparseable output, an
// unwritable directory — skips that cell and nothing else. Nothing is logged:
// a missing preview file IS the signal, and a chatty fallback would train the
// agent to treat a display gap as an error to fix.

const (
	// PreviewLimit is the row cap handed to dbt show. A preview is a sample
	// for reading, not an extract.
	PreviewLimit = 100

	// previewTimeout bounds ONE dbt show. Previews are a courtesy; a slow
	// warehouse must not extend a completed run by minutes.
	previewTimeout = 60 * time.Second

	// PreviewsSubdir holds the previews inside the build tree's target/, next
	// to dbt's own artifacts. cmd/loom-desktop reads them from there.
	PreviewsSubdir = "loom_previews"

	// previewScanLines bounds the search for dbt show's JSON payload in its
	// log output.
	previewScanLines = 500

	// previewMaxDepth bounds how deep the row array is looked for inside the
	// payload: dbt has wrapped it in one object key across versions
	// ({"show": [...]}, {"<node>": [...]}), never deeper.
	previewMaxDepth = 4
)

// CellPreview is a sampled result for one cell: the shape the desktop renders
// as a table or a chart.
//
// Columns is positional and Rows are aligned to it, so a preview survives the
// JSON round-trip with its column ORDER intact — a map would not.
type CellPreview struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	// Truncated is whether the sample hit the row cap, i.e. the real result
	// is larger. The UI says so rather than implying it read everything.
	Truncated bool `json:"truncated"`
}

// capturePreviews writes one preview per model-emitting cell in scope and
// returns how many it wrote. It never fails: the count is the whole report.
func capturePreviews(ctx context.Context, doc *Document, opts RunOptions, buildDir string) int {
	dir := filepath.Join(buildDir, "target", PreviewsSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0
	}

	// A selected build rebuilt only some models; the others' previews are
	// still on disk and still current, so they are left alone.
	var inScope map[string]bool
	if opts.Selection != nil {
		inScope = make(map[string]bool, len(opts.Selection.Cells))
		for _, id := range opts.Selection.Cells {
			inScope[id] = true
		}
	}

	written := 0
	for _, c := range doc.Cells {
		if ctx.Err() != nil {
			// The caller's context is gone (the agent's turn ended, the app
			// quit); the run itself already succeeded, so stop quietly.
			return written
		}
		if !EmitsModel(c) || (inScope != nil && !inScope[c.ID]) {
			continue
		}
		// Defence in depth: Validate already rejects an id that could escape
		// the directory, and this is the only place an id becomes a path.
		if c.ID != filepath.Base(c.ID) || strings.Contains(c.ID, string(filepath.Separator)) {
			continue
		}
		preview, ok := previewOneCell(ctx, opts, buildDir, c.ID)
		if !ok {
			continue
		}
		body, err := json.Marshal(preview)
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, c.ID+".json"), body, 0o644); err != nil { // #nosec G306 -- a preview is display data, read by the desktop UI
			continue
		}
		written++
	}
	return written
}

// previewOneCell runs dbt show for one model and normalizes its output.
func previewOneCell(ctx context.Context, opts RunOptions, buildDir, cellID string) (CellPreview, bool) {
	showCtx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	// #nosec G204 -- DBTBin comes from the host environment (EnvDBTBin),
	// cellID is a validated cell id from the document, and the rest is fixed.
	cmd := exec.CommandContext(showCtx, opts.DBTBin, "show",
		"--select", cellID,
		"--limit", fmt.Sprintf("%d", PreviewLimit),
		"--output", "json",
		"--no-partial-parse",
		"--profiles-dir", opts.ProfilesDir)
	cmd.Dir = buildDir
	// Env stays nil for the same reason as the build: credentials reach dbt
	// through the profile's env_var() indirection, never through here.
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return CellPreview{}, false
	}
	// A nonzero exit that still printed the payload is usable — dbt warns
	// about plenty of things while succeeding.
	return ParseDBTShow(out)
}

// ParseDBTShow normalizes `dbt show --output json` output into a CellPreview.
//
// THE SHAPE, and why the parser is tolerant rather than typed: dbt prints the
// preview as a LOG EVENT, so the JSON payload arrives wrapped in whatever the
// logger and the version put around it. Across dbt versions the payload has
// been the object {"show": [ {row}, ... ]} (inline queries) and the object
// {"<node name>": [ {row}, ... ]} (a --select run), each pretty-printed and
// preceded by ordinary log lines — and the log formatter stamps the first line
// with a timestamp, so the payload does not even start at a line boundary.
//
// So instead of binding to one of those shapes: scan the output line by line
// for a position where a JSON value decodes, then walk that value for the
// first array of objects. Every shape above reduces to the same rows, and a
// future rewrapping does too.
func ParseDBTShow(output []byte) (CellPreview, bool) {
	rows, ok := findShowRows(output)
	if !ok || len(rows) == 0 {
		return CellPreview{}, false
	}
	return previewFromRows(rows), true
}

// findShowRows locates the payload and returns its row objects.
func findShowRows(output []byte) ([]jsonRow, bool) {
	rest := output
	for line := 0; line < previewScanLines && len(rest) > 0; line++ {
		nl := bytes.IndexByte(rest, '\n')
		current := rest
		if nl >= 0 {
			current = rest[:nl]
		}
		// The payload's first character is the first brace or bracket on its
		// line: a log prefix ("12:00:01  ") carries none.
		if j := bytes.IndexAny(current, "{["); j >= 0 {
			offset := len(output) - len(rest) + j
			var raw json.RawMessage
			if err := json.NewDecoder(bytes.NewReader(output[offset:])).Decode(&raw); err == nil {
				if rows, found := rowsWithin(raw, 0); found {
					return rows, true
				}
			}
		}
		if nl < 0 {
			break
		}
		rest = rest[nl+1:]
	}
	return nil, false
}

// rowsWithin returns the first array of objects inside value, searching
// object values in document order.
func rowsWithin(value json.RawMessage, depth int) ([]jsonRow, bool) {
	if depth > previewMaxDepth {
		return nil, false
	}
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 {
		return nil, false
	}
	switch trimmed[0] {
	case '[':
		var rows []jsonRow
		if err := json.Unmarshal(trimmed, &rows); err == nil && len(rows) > 0 {
			return rows, true
		}
		// An array of something else may still contain the rows.
		var elements []json.RawMessage
		if err := json.Unmarshal(trimmed, &elements); err != nil {
			return nil, false
		}
		for _, el := range elements {
			if rows, ok := rowsWithin(el, depth+1); ok {
				return rows, true
			}
		}
	case '{':
		// Document order, not map order: which key holds the rows differs by
		// dbt version, and the answer must not depend on Go's map iteration.
		for _, v := range objectValuesInOrder(trimmed) {
			if rows, ok := rowsWithin(v, depth+1); ok {
				return rows, true
			}
		}
	}
	return nil, false
}

// objectValuesInOrder returns a JSON object's values in the order they appear
// in the document.
func objectValuesInOrder(object []byte) []json.RawMessage {
	dec := json.NewDecoder(bytes.NewReader(object))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil
	}
	var out []json.RawMessage
	for dec.More() {
		if _, err := dec.Token(); err != nil { // the key
			return out
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return out
		}
		out = append(out, raw)
	}
	return out
}

// jsonRow is one row object with its KEY ORDER preserved. dbt serializes each
// row from an ordered mapping, so the key order in the JSON text is the
// warehouse's column order — the only column-order metadata the payload
// carries, and decoding into a map would throw it away.
type jsonRow struct {
	keys   []string
	values map[string]any
}

// UnmarshalJSON reads the object key by key, recording order.
func (r *jsonRow) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	// Numbers stay literal: a warehouse id larger than 2^53 must not come
	// back rounded through float64.
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("row is not a JSON object")
	}
	r.keys = nil
	r.values = map[string]any{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("row key is not a string")
		}
		var v any
		if err := dec.Decode(&v); err != nil {
			return err
		}
		if _, seen := r.values[key]; !seen {
			r.keys = append(r.keys, key)
		}
		r.values[key] = v
	}
	_, err = dec.Token() // closing brace
	return err
}

// previewFromRows aligns rows onto one column list.
//
// COLUMN ORDER: the first row's key order, which is dbt's column order (see
// jsonRow). A key that only later rows carry — a ragged payload, which dbt
// does not produce but a future one might — is appended, sorted, so the
// result is deterministic either way. A row missing a column gets null there.
func previewFromRows(rows []jsonRow) CellPreview {
	seen := make(map[string]bool, len(rows[0].keys))
	columns := make([]string, 0, len(rows[0].keys))
	for _, k := range rows[0].keys {
		if !seen[k] {
			seen[k] = true
			columns = append(columns, k)
		}
	}
	var extra []string
	for _, r := range rows[1:] {
		for _, k := range r.keys {
			if !seen[k] {
				seen[k] = true
				extra = append(extra, k)
			}
		}
	}
	sort.Strings(extra)
	columns = append(columns, extra...)

	out := CellPreview{
		Columns: columns,
		Rows:    make([][]any, 0, len(rows)),
		// dbt stopped at the cap, so the real result is at least this large.
		Truncated: len(rows) >= PreviewLimit,
	}
	for _, r := range rows {
		row := make([]any, len(columns))
		for i, c := range columns {
			row[i] = r.values[c] // absent → nil, which marshals to null
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}

// TailLines returns the last n non-empty lines of s — the readable half of a
// dbt failure, which reports its errors last. Shared so the tool's result and
// the desktop's error message quote the same thing.
func TailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return strings.Join(kept, "\n")
}
