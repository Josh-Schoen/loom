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

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// AuditTable is the row-accounting table the generated post-hook writes to.
// loom_setup must create it before dbt run. Column is logged_at, not at —
// at is a Teradata reserved word.
const AuditTable = "loom_audit"

var (
	// refPattern matches the {{ ref('cell_id') }} contract in Cell.Source.
	refPattern = regexp.MustCompile(`\{\{\s*ref\(\s*['"]([^'"]*)['"]\s*\)\s*\}\}`)
	// groupByPattern marks a cell whose output grain is an aggregation.
	groupByPattern = regexp.MustCompile(`(?i)\bgroup\s+by\b`)
	// sumAliasPattern extracts SUM(<col>) AS <alias> — the partition-sums
	// test needs both the upstream column and the aliased output column.
	sumAliasPattern = regexp.MustCompile(`(?i)\bsum\s*\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)\s+as\s+([A-Za-z_][A-Za-z0-9_]*)`)
	// dbtNameIllegal is every character a dbt project name may not carry.
	dbtNameIllegal = regexp.MustCompile(`[^a-z0-9_]+`)
)

// Compile writes a runnable dbt project for doc into dir, creating dir.
// skipped names the cells that produced no dbt model and are recorded in
// package.yaml only. Compile writes files; it does not prune files a
// previous compile left behind.
func Compile(doc *Document, dir string) (skipped []string, err error) {
	if doc == nil {
		return nil, fmt.Errorf("project: compile: nil document")
	}
	if err := doc.Validate(); err != nil {
		return nil, err
	}

	files, skipped, err := renderProject(doc)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, fmt.Errorf("project: compile: %w", err)
		}
		if err := os.WriteFile(abs, []byte(files[rel]), 0o644); err != nil {
			return nil, fmt.Errorf("project: compile: %w", err)
		}
	}
	return skipped, nil
}

// renderProject builds the whole project in memory. Pure: identical input
// yields byte-identical output, and nothing here reads a clock.
func renderProject(doc *Document) (files map[string]string, skipped []string, err error) {
	projectName := dbtProjectName(doc.Metadata.Name)

	order, err := doc.TopoOrder()
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]Cell, len(doc.Cells))
	for _, c := range doc.Cells {
		byID[c.ID] = c
	}

	var models []string // cell IDs that emit a dbt model, in topo order
	for _, id := range order {
		c := byID[id]
		switch {
		case c.Lang == LangSQL:
			if strings.TrimSpace(c.Source) == "" {
				skipped = append(skipped, id)
				continue
			}
			models = append(models, id)
		case c.Lang == LangCall && strings.TrimSpace(c.Source) != "":
			// v1: registry resolution is out of scope — a call cell only
			// compiles when it carries its own source.
			models = append(models, id)
		default:
			skipped = append(skipped, id)
		}
	}

	files = map[string]string{}
	files["dbt_project.yml"] = renderDBTProject(projectName)
	files["macros/loom_checks.sql"] = renderMacros(models)

	for _, id := range models {
		c := byID[id]
		if err := validateRefs(c); err != nil {
			return nil, nil, err
		}
		files["models/"+id+".sql"] = ensureTrailingNewline(c.Source)
	}

	if schema := renderSchemaYML(doc, order, models); schema != "" {
		files["models/schema.yml"] = schema
	}
	for name, sql := range renderPartitionTests(doc, order, models) {
		files["tests/"+name+".sql"] = sql
	}

	pkg, err := renderPackageYAML(doc, order, skipped)
	if err != nil {
		return nil, nil, err
	}
	files["package.yaml"] = pkg

	sort.Strings(skipped)
	return files, skipped, nil
}

// validateRefs enforces the Cell.Source contract: every {{ ref('x') }}
// names a cell listed in Inputs.
func validateRefs(c Cell) error {
	inputs := make(map[string]bool, len(c.Inputs))
	for _, in := range c.Inputs {
		inputs[in] = true
	}
	for _, m := range refPattern.FindAllStringSubmatch(c.Source, -1) {
		if !inputs[m[1]] {
			return fmt.Errorf("project: cell %q: source refs %q which is not listed in inputs", c.ID, m[1])
		}
	}
	return nil
}

// dbtProjectName sanitizes a project name to a legal dbt identifier.
func dbtProjectName(name string) string {
	n := dbtNameIllegal.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "_")
	n = strings.Trim(n, "_")
	if n == "" {
		return "loom_project"
	}
	if n[0] >= '0' && n[0] <= '9' {
		return "loom_" + n
	}
	return n
}

func renderDBTProject(projectName string) string {
	// Row accounting rides a documented model post-hook; the audit table
	// must exist first (run-operation loom_setup).
	return fmt.Sprintf(`# Generated by loom from the project document. Do not edit by hand.
name: %[1]s
profile: %[1]s
model-paths: ["models"]
test-paths: ["tests"]
macro-paths: ["macros"]
models:
  %[1]s:
    +materialized: table
    +post-hook: "{{ loom_row_audit() }}"
`, projectName)
}

func renderMacros(models []string) string {
	var b strings.Builder
	b.WriteString("-- Generated by loom from the project document. Do not edit by hand.\n\n")
	b.WriteString(`{% test loom_grain_unique(model, grain) %}
-- Grain-preservation test: zero rows = grain holds.
SELECT {{ grain }}, COUNT(*) AS n
FROM {{ model }}
GROUP BY {{ grain }}
HAVING COUNT(*) > 1
{% endtest %}

{% macro loom_row_audit() %}
-- Node-boundary row accounting: rows-out of the model just built.
INSERT INTO {{ target.schema }}.` + AuditTable + `
SELECT '{{ this.identifier }}', COUNT(*), CURRENT_TIMESTAMP FROM {{ this }}
{% endmacro %}

{% macro loom_setup() %}
-- Must run before dbt run: the post-hook inserts into this table.
{% do run_query("CREATE TABLE " ~ target.schema ~ ".` + AuditTable + ` (model_name VARCHAR(128), rows_out BIGINT, logged_at TIMESTAMP)") %}
{% do log("loom audit table created", info=True) %}
{% endmacro %}

{% macro loom_teardown() %}
`)
	tables := append([]string{AuditTable}, models...)
	quoted := make([]string, 0, len(tables))
	for _, t := range tables {
		quoted = append(quoted, `"`+t+`"`)
	}
	b.WriteString("{% for t in [" + strings.Join(quoted, ", ") + "] %}\n")
	b.WriteString(`  {% do run_query("DROP TABLE " ~ target.schema ~ "." ~ t) %}
  {% do log("dropped " ~ t, info=True) %}
{% endfor %}
{% endmacro %}
`)
	return b.String()
}

// renderSchemaYML emits the grain test for every sql cell with a declared
// grain. The arguments: nesting is required — dbt 1.12 raises
// MissingArgumentsPropertyInGenericTestDeprecation on top-level test args.
func renderSchemaYML(doc *Document, order, models []string) string {
	isModel := make(map[string]bool, len(models))
	for _, id := range models {
		isModel[id] = true
	}
	var b strings.Builder
	b.WriteString("# Generated by loom from the project document. Do not edit by hand.\nversion: 2\nmodels:\n")
	n := 0
	for _, id := range order {
		c, _ := doc.Cell(id)
		if c.Lang != LangSQL || c.DeclaredGrain == "" || !isModel[id] {
			continue
		}
		n++
		fmt.Fprintf(&b, "  - name: %s\n", id)
		b.WriteString("    data_tests:\n      - loom_grain_unique:\n          arguments:\n")
		fmt.Fprintf(&b, "            grain: %s\n", c.DeclaredGrain)
	}
	if n == 0 {
		return ""
	}
	return b.String()
}

// renderPartitionTests emits a Tier-A metamorphic relation — partition sums
// equal the whole — for each sql cell that aggregates a single upstream sql
// cell with SUM(<col>) AS <alias>. Cells that do not match the shape get no
// test; the omission is structural, not an error.
func renderPartitionTests(doc *Document, order, models []string) map[string]string {
	isModel := make(map[string]bool, len(models))
	for _, id := range models {
		isModel[id] = true
	}
	out := map[string]string{}
	for _, id := range order {
		c, _ := doc.Cell(id)
		if c.Lang != LangSQL || !isModel[id] || len(c.Inputs) != 1 {
			continue
		}
		if !groupByPattern.MatchString(c.Source) {
			continue
		}
		up, ok := doc.Cell(c.Inputs[0])
		if !ok || up.Lang != LangSQL || !isModel[up.ID] {
			continue
		}
		m := sumAliasPattern.FindStringSubmatch(c.Source)
		if m == nil {
			continue
		}
		upstreamCol, outputCol := m[1], m[2]
		// Output columns are aliased: Teradata rejects duplicate names.
		out["loom_partition_sums_"+id] = fmt.Sprintf(
			`-- Generated by loom: partition sums must equal the whole. Zero rows = relation holds.
WITH parts AS (SELECT SUM(%[1]s) AS s FROM {{ ref('%[2]s') }}),
     whole AS (SELECT SUM(%[3]s) AS s FROM {{ ref('%[4]s') }})
SELECT parts.s AS part_sum, whole.s AS whole_sum
FROM parts, whole
WHERE ABS(parts.s - whole.s) > 0.001
`, outputCol, id, upstreamCol, up.ID)
	}
	return out
}

// packageSignature is the package.yaml sidecar: the project's signature as
// the App form and registry index read it. generated_at is deliberately
// absent — the output must be a pure function of the document.
type packageSignature struct {
	Name       string             `yaml:"name"`
	Variant    string             `yaml:"variant,omitempty"`
	Parameters []packageParameter `yaml:"parameters,omitempty"`
	Grain      string             `yaml:"grain,omitempty"`
	Cells      []packageCell      `yaml:"cells"`
	Skipped    []string           `yaml:"skipped,omitempty"`
}

type packageParameter struct {
	Name   string `yaml:"name"`
	Domain string `yaml:"domain"`
}

type packageCell struct {
	ID    string `yaml:"id"`
	Lang  string `yaml:"lang"`
	Grain string `yaml:"grain,omitempty"`
	Ref   string `yaml:"ref,omitempty"`
}

func renderPackageYAML(doc *Document, order, skipped []string) (string, error) {
	sig := packageSignature{
		Name:    doc.Metadata.Name,
		Variant: doc.Metadata.Variant,
		Grain:   terminalGrain(doc, order),
		Cells:   make([]packageCell, 0, len(order)),
	}
	for _, id := range order {
		c, _ := doc.Cell(id)
		if c.Lang == LangInput {
			names := make([]string, 0, len(c.Params))
			for name := range c.Params {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				sig.Parameters = append(sig.Parameters, packageParameter{Name: name, Domain: c.Params[name]})
			}
		}
		sig.Cells = append(sig.Cells, packageCell{ID: c.ID, Lang: c.Lang, Grain: string(c.DeclaredGrain), Ref: c.Ref})
	}
	sorted := append([]string(nil), skipped...)
	sort.Strings(sorted)
	sig.Skipped = sorted

	var buf bytes.Buffer
	buf.WriteString("# Generated by loom from the project document. Do not edit by hand.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(sig); err != nil {
		return "", fmt.Errorf("project: compile: package.yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("project: compile: package.yaml: %w", err)
	}
	return buf.String(), nil
}

// terminalGrain is the declared grain of the project's single terminal sql
// cell — the one nothing else consumes. Ambiguous topologies (none, or more
// than one terminal) carry no project grain.
func terminalGrain(doc *Document, order []string) string {
	consumed := map[string]bool{}
	for _, c := range doc.Cells {
		for _, in := range c.Inputs {
			consumed[in] = true
		}
	}
	var terminals []string
	for _, id := range order {
		c, _ := doc.Cell(id)
		if c.Lang == LangSQL && !consumed[id] {
			terminals = append(terminals, id)
		}
	}
	if len(terminals) != 1 {
		return ""
	}
	c, _ := doc.Cell(terminals[0])
	return string(c.DeclaredGrain)
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
