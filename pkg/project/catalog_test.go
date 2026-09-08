package project

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProfileTargets(t *testing.T) {
	dir := t.TempDir()
	body := `loom_project:
  target: clearscape
  outputs:
    clearscape:
      type: teradata
      host: "{{ env_var('TD_SPIKE_HOST') }}"
      schema: data_scientist
    local:
      type: duckdb
      path: /tmp/dev.duckdb
      schema: main
`
	if err := os.WriteFile(filepath.Join(dir, "profiles.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	targets, err := ProfileTargets(dir, "loom_project")
	if err != nil {
		t.Fatalf("ProfileTargets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(targets))
	}
	// Active first, regardless of map order.
	if !targets[0].Active || targets[0].Name != "clearscape" || targets[0].Type != "teradata" {
		t.Errorf("first target = %+v, want active clearscape/teradata", targets[0])
	}
	// DuckDB's path stands where a host would.
	var local ProfileTarget
	for _, tt := range targets {
		if tt.Name == "local" {
			local = tt
		}
	}
	if local.Host != "/tmp/dev.duckdb" || local.Active {
		t.Errorf("local = %+v, want path-as-host and inactive", local)
	}

	if _, err := ProfileTargets(dir, "nope"); err == nil {
		t.Error("unknown profile: want an error")
	}

	engine, err := ProfileEngine(dir, "loom_project")
	if err != nil || engine != "teradata" {
		t.Errorf("ProfileEngine = %q, %v; want teradata", engine, err)
	}
}

func TestCatalogQueries(t *testing.T) {
	td, err := CatalogFor("teradata")
	if err != nil {
		t.Fatal(err)
	}
	// A quote in a clicked name must not escape the literal.
	sql := td.RelationsSQL("o'brien")
	if !strings.Contains(sql, "'o''brien'") {
		t.Errorf("RelationsSQL did not escape the quote: %s", sql)
	}
	if got := td.RelationKindLabel("T "); got != "table" {
		t.Errorf("teradata kind %q, want table (trailing-space code)", got)
	}
	if got := td.RelationKindLabel("V"); got != "view" {
		t.Errorf("teradata kind %q, want view", got)
	}
	if got := td.ColumnTypeLabel("CV"); got != "VARCHAR" {
		t.Errorf("teradata type %q, want VARCHAR", got)
	}

	dd, err := CatalogFor("duckdb")
	if err != nil {
		t.Fatal(err)
	}
	if got := dd.RelationKindLabel("BASE TABLE"); got != "table" {
		t.Errorf("duckdb kind %q, want table", got)
	}
	if !strings.Contains(dd.DDLSQL("main", "orders"), "duckdb_views()") {
		t.Error("duckdb DDL must consult views too")
	}

	if _, err := CatalogFor("snowflake"); err == nil {
		t.Error("unshipped engine: want a refusal naming it, not a guess")
	}
}

func TestRowStringCaseGames(t *testing.T) {
	row := map[string]any{"NAME": "DBC", "kind": "V"}
	if got := RowString(row, "name"); got != "DBC" {
		t.Errorf("RowString(name) = %q, want the case-insensitive hit", got)
	}
	if got := RowString(row, "missing"); got != "" {
		t.Errorf("RowString(missing) = %q, want empty", got)
	}
}

// ShowInline against a fake dbt: proves the flag set, the parse, and the
// tolerance for a stray line ahead of the JSON.
func TestShowInline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake dbt is a shell script")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "dbt")
	script := `#!/bin/sh
echo "warning: something harmless"
echo '{"show": [{"name": "DBC"}, {"name": "data_scientist"}]}'
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	rows, err := ShowInline(context.Background(), dir, "SELECT 1", ShowOptions{DBTBin: fake, ProfilesDir: dir})
	if err != nil {
		t.Fatalf("ShowInline: %v", err)
	}
	if len(rows) != 2 || RowString(rows[0], "name") != "DBC" {
		t.Errorf("rows = %+v, want the two fixture rows", rows)
	}

	// A failing dbt surfaces its own tail, not a bare exit status.
	failing := filepath.Join(dir, "dbt-fail")
	if err := os.WriteFile(failing, []byte("#!/bin/sh\necho 'Database said no'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = ShowInline(context.Background(), dir, "SELECT 1", ShowOptions{DBTBin: failing, ProfilesDir: dir})
	if err == nil || !strings.Contains(err.Error(), "Database said no") {
		t.Errorf("error %v should carry dbt's own words", err)
	}
}
