package project

// catalog.go — browsing the warehouse THROUGH the connection dbt already has
// (backlog 22i). Josh's design change, 2026-08-30: "database browser should
// be what dbt is connected to … no RPC needed". Catalog queries ride
// `dbt show --inline` against the compiled project, so the browser sees
// exactly what a cell run sees — same profile, same credentials path, same
// adapter — and this package still never touches a credential.
//
// The query sets are ADAPTER-AWARE: the profile's engine type picks them.
// v1 ships Teradata and DuckDB (the two on this machine); the set grows per
// adapter. An unknown engine refuses with the engine's name in the error —
// a tree faked from another engine's DBC would be wrong quietly.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultShowTimeout bounds one catalog query. dbt's own startup is ~5s and
// the warehouse round-trip rides on top; a minute is generous without letting
// a hung adapter own the panel forever.
const DefaultShowTimeout = 60 * time.Second

// ShowOptions carries what one `dbt show` invocation needs. Zero-value Limit
// and Timeout take defaults.
type ShowOptions struct {
	DBTBin      string
	ProfilesDir string
	Limit       int
	Timeout     time.Duration
}

// ShowInline runs one read-only query through dbt's own connection:
// `dbt show --inline <query> --quiet --output json` in buildDir. Returns the
// result rows as dbt printed them ({"show": [...]}).
//
// The query text is composed by CatalogFor's builders from validated parts —
// callers must not pass user-typed SQL through this without their own
// authority to run SQL (a cell run has that; a tree expand does not).
func ShowInline(ctx context.Context, buildDir, query string, opts ShowOptions) ([]map[string]any, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultShowTimeout
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 500
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// --no-partial-parse for the same reason Run passes it: the project is
	// regenerated from the document, so a cached manifest can only be stale.
	cmd := exec.CommandContext(runCtx, opts.DBTBin, // #nosec G204 -- DBTBin comes from the host environment, the arguments are fixed flags
		"show", "--inline", query,
		"--limit", fmt.Sprintf("%d", limit),
		"--output", "json", "--quiet", "--no-partial-parse",
		"--profiles-dir", opts.ProfilesDir)
	cmd.Dir = buildDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("catalog query failed: %v\n%s", err, TailLines(string(out), 12))
	}

	// --quiet keeps logs out in current dbt, but a stray warning line ahead
	// of the JSON must not break the panel: parse from the first brace.
	text := string(out)
	if i := strings.Index(text, "{"); i > 0 {
		text = text[i:]
	}
	var payload struct {
		Show []map[string]any `json:"show"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, fmt.Errorf("catalog query returned unparseable output: %v\n%s", err, TailLines(string(out), 8))
	}
	return payload.Show, nil
}

// ── Profile reading ──────────────────────────────────────────────────────────

// ProfileTarget is one output of a dbt profile: a named connection to an
// engine. Only the SHAPE of the connection is read — name, engine type,
// host/database/schema when present. Credentials in profiles.yml are
// env_var() templates and are never resolved here.
type ProfileTarget struct {
	Name   string
	Type   string
	Host   string
	Schema string
	// Active marks the profile's default target.
	Active bool
}

// ProfileTargets reads the named profile from <profilesDir>/profiles.yml and
// lists its outputs. This is also the Environment panel's data: the profile
// IS the registry of connected environments (the 22i note — "database
// configurability rides the dbt profile").
func ProfileTargets(profilesDir, profile string) ([]ProfileTarget, error) {
	body, err := os.ReadFile(filepath.Join(profilesDir, "profiles.yml")) // #nosec G304 -- profilesDir is the resolved dbt profiles home
	if err != nil {
		return nil, fmt.Errorf("no dbt profiles.yml at %s: %v", profilesDir, err)
	}
	var doc map[string]struct {
		Target  string `yaml:"target"`
		Outputs map[string]struct {
			Type     string `yaml:"type"`
			Host     string `yaml:"host"`
			Schema   string `yaml:"schema"`
			Path     string `yaml:"path"`     // duckdb: the database file
			Database string `yaml:"database"` // engines that split database from schema
		} `yaml:"outputs"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("profiles.yml did not parse: %v", err)
	}
	entry, ok := doc[profile]
	if !ok {
		return nil, fmt.Errorf("profiles.yml has no profile %q", profile)
	}
	var out []ProfileTarget
	for name, o := range entry.Outputs {
		host := o.Host
		if host == "" {
			host = o.Path // duckdb's "where": the file stands where a host would
		}
		schema := o.Schema
		if schema == "" {
			schema = o.Database
		}
		out = append(out, ProfileTarget{
			Name:   name,
			Type:   o.Type,
			Host:   host,
			Schema: schema,
			Active: name == entry.Target,
		})
	}
	// Stable order, active first: the map order above is random.
	for i := range out {
		if out[i].Active && i != 0 {
			out[0], out[i] = out[i], out[0]
			break
		}
	}
	return out, nil
}

// DefaultProfileName is the one profiles.yml entry every compiled project
// uses (see compile.go — per-project profile names sent the first live run
// hunting a missing profile).
const DefaultProfileName = "loom_project"

// ProfileEngine returns the engine type of the profile's active target.
func ProfileEngine(profilesDir, profile string) (string, error) {
	targets, err := ProfileTargets(profilesDir, profile)
	if err != nil {
		return "", err
	}
	for _, t := range targets {
		if t.Active {
			return t.Type, nil
		}
	}
	return "", fmt.Errorf("profile %q names no active target", profile)
}

// ── Adapter-aware query sets ─────────────────────────────────────────────────

// Catalog builds the catalog queries for one engine. Every query aliases its
// projection to the SAME column names — name, kind, col_type, nullable, ddl —
// so the caller never branches on engine to read a row.
type Catalog struct {
	engine string
}

// CatalogFor returns the query set for an engine type as named in the dbt
// profile ("teradata", "duckdb"). Unknown engines are a refusal, not a guess.
func CatalogFor(engine string) (*Catalog, error) {
	switch engine {
	case "teradata", "duckdb":
		return &Catalog{engine: engine}, nil
	default:
		return nil, fmt.Errorf("no catalog query set for engine %q yet (have: teradata, duckdb)", engine)
	}
}

// quoteLiteral makes a SQL string literal from an identifier the tree
// clicked. Doubling the quote is the whole escape — these are catalog names,
// not documents.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// DatabasesSQL lists databases (Teradata) / schemata (DuckDB) — the tree's
// top level.
func (c *Catalog) DatabasesSQL() string {
	switch c.engine {
	case "teradata":
		return `SELECT DatabaseName AS name FROM DBC.DatabasesV ORDER BY 1`
	default: // duckdb
		return `SELECT schema_name AS name FROM information_schema.schemata ORDER BY 1`
	}
}

// RelationsSQL lists tables and views inside one database.
func (c *Catalog) RelationsSQL(db string) string {
	switch c.engine {
	case "teradata":
		// T = table, O = NoPI table, V = view.
		return `SELECT TableName AS name, TableKind AS kind FROM DBC.TablesV WHERE DatabaseName = ` +
			quoteLiteral(db) + ` AND TableKind IN ('T','O','V') ORDER BY 1`
	default:
		return `SELECT table_name AS name, table_type AS kind FROM information_schema.tables WHERE table_schema = ` +
			quoteLiteral(db) + ` ORDER BY 1`
	}
}

// ColumnsSQL lists one relation's columns in declared order.
func (c *Catalog) ColumnsSQL(db, rel string) string {
	switch c.engine {
	case "teradata":
		return `SELECT ColumnName AS name, ColumnType AS col_type, Nullable AS nullable FROM DBC.ColumnsV WHERE DatabaseName = ` +
			quoteLiteral(db) + ` AND TableName = ` + quoteLiteral(rel) + ` ORDER BY ColumnId`
	default:
		return `SELECT column_name AS name, data_type AS col_type, is_nullable AS nullable FROM information_schema.columns WHERE table_schema = ` +
			quoteLiteral(db) + ` AND table_name = ` + quoteLiteral(rel) + ` ORDER BY ordinal_position`
	}
}

// DDLSQL fetches one relation's definition text.
func (c *Catalog) DDLSQL(db, rel string) string {
	switch c.engine {
	case "teradata":
		// RequestText holds the CREATE statement as last submitted.
		return `SELECT RequestText AS ddl FROM DBC.TablesV WHERE DatabaseName = ` +
			quoteLiteral(db) + ` AND TableName = ` + quoteLiteral(rel)
	default:
		return `SELECT sql AS ddl FROM duckdb_tables() WHERE schema_name = ` +
			quoteLiteral(db) + ` AND table_name = ` + quoteLiteral(rel) +
			` UNION ALL SELECT sql AS ddl FROM duckdb_views() WHERE schema_name = ` +
			quoteLiteral(db) + ` AND view_name = ` + quoteLiteral(rel)
	}
}

// RelationKindLabel normalizes an engine's relation-kind code to "table" or
// "view" for display; anything unknown passes through as itself, lowercased —
// honest beats smoothed.
func (c *Catalog) RelationKindLabel(kind string) string {
	k := strings.ToUpper(strings.TrimSpace(kind))
	switch c.engine {
	case "teradata":
		switch k {
		case "T", "O":
			return "table"
		case "V":
			return "view"
		}
	default:
		switch k {
		case "BASE TABLE", "LOCAL TEMPORARY":
			return "table"
		case "VIEW":
			return "view"
		}
	}
	return strings.ToLower(kind)
}

// teradataTypeNames decodes DBC.ColumnsV's two-letter type codes for the
// common types; an unlisted code passes through unchanged.
var teradataTypeNames = map[string]string{
	"CF": "CHAR", "CV": "VARCHAR", "CO": "CLOB",
	"I": "INTEGER", "I1": "BYTEINT", "I2": "SMALLINT", "I8": "BIGINT",
	"D": "DECIMAL", "N": "NUMBER", "F": "FLOAT",
	"DA": "DATE", "TS": "TIMESTAMP", "AT": "TIME",
	"SZ": "TIMESTAMP WITH TIME ZONE", "TZ": "TIME WITH TIME ZONE",
	"BO": "BLOB", "BF": "BYTE", "BV": "VARBYTE",
	"JN": "JSON", "XM": "XML", "PD": "PERIOD(DATE)", "PS": "PERIOD(TIMESTAMP)",
}

// ColumnTypeLabel normalizes an engine's column-type value for display.
func (c *Catalog) ColumnTypeLabel(colType string) string {
	t := strings.TrimSpace(colType)
	if c.engine == "teradata" {
		if name, ok := teradataTypeNames[strings.ToUpper(t)]; ok {
			return name
		}
	}
	return t
}

// RowString reads one aliased column from a dbt show row, tolerating the
// case games engines play with unquoted aliases (Teradata may answer "name"
// or "NAME" depending on session settings).
func RowString(row map[string]any, key string) string {
	if v, ok := row[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	for k, v := range row {
		if strings.EqualFold(k, key) {
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}
