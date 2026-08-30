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
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// exampleDocument is the at-risk-revenue shape: three sql cells (one plain,
// one grain-bearing aggregate, one grain-bearing terminal), a prose cell, an
// input cell, and a call cell with no source.
const exampleDocument = `apiVersion: loom/v1
kind: Project
metadata:
  name: at risk revenue
  description: revenue at risk by customer
  variant: default
cells:
  - id: intro
    lang: prose
    source: |
      # At-risk revenue
      One row per customer.
  - id: as_of
    lang: input
    params:
      as_of: "date in [2024-01-01, today]"
  - id: orders
    lang: sql
    source: |
      SELECT order_id, customer_id, amount
      FROM cust_ord_fact
  - id: customer_totals
    lang: sql
    inputs: [orders]
    declared_grain: customer_id
    source: |
      SELECT customer_id, SUM(amount) AS total_amount, COUNT(*) AS order_count
      FROM {{ ref('orders') }}
      GROUP BY customer_id
  - id: customer_report
    lang: sql
    inputs: [customer_totals]
    declared_grain: customer_id
    source: |
      SELECT customer_id, total_amount
      FROM {{ ref('customer_totals') }}
      WHERE total_amount > 0
  - id: shared_metric
    lang: call
    ref: revenue_at_risk@v2
`

// readTree returns every regular file under dir keyed by slash-separated
// path relative to dir.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

func compileExample(t *testing.T) (map[string]string, []string) {
	t.Helper()
	doc, err := Parse([]byte(exampleDocument))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	dir := t.TempDir()
	skipped, err := Compile(doc, filepath.Join(dir, "compiled"))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return readTree(t, filepath.Join(dir, "compiled")), skipped
}

func TestCompileFileSet(t *testing.T) {
	t.Parallel()

	files, skipped := compileExample(t)

	want := []string{
		"dbt_project.yml",
		"macros/loom_checks.sql",
		"models/customer_report.sql",
		"models/customer_totals.sql",
		"models/orders.sql",
		"models/schema.yml",
		"package.yaml",
		"tests/loom_partition_sums_customer_totals.sql",
	}
	got := make([]string, 0, len(files))
	for p := range files {
		got = append(got, p)
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("file set:\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	if strings.Join(skipped, ",") != "as_of,intro,shared_metric" {
		t.Fatalf("skipped = %v", skipped)
	}
}

func TestCompileDBTProject(t *testing.T) {
	t.Parallel()

	files, _ := compileExample(t)
	got := files["dbt_project.yml"]
	for _, want := range []string{
		"name: at_risk_revenue\n",
		"profile: at_risk_revenue\n",
		`model-paths: ["models"]`,
		`test-paths: ["tests"]`,
		`macro-paths: ["macros"]`,
		"  at_risk_revenue:\n    +materialized: table\n    +post-hook: \"{{ loom_row_audit() }}\"\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("dbt_project.yml missing %q:\n%s", want, got)
		}
	}
}

func TestCompileModelsVerbatim(t *testing.T) {
	t.Parallel()

	doc, err := Parse([]byte(exampleDocument))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	files, _ := compileExample(t)
	for _, id := range []string{"orders", "customer_totals", "customer_report"} {
		c, _ := doc.Cell(id)
		if files["models/"+id+".sql"] != c.Source {
			t.Fatalf("models/%s.sql not verbatim:\ngot:\n%s\nwant:\n%s", id, files["models/"+id+".sql"], c.Source)
		}
	}
}

// TestCompileSchemaYMLArgumentsForm pins the non-deprecated generic-test
// format: dbt 1.12 raises MissingArgumentsPropertyInGenericTestDeprecation
// when test args sit at the top level instead of under arguments:.
func TestCompileSchemaYMLArgumentsForm(t *testing.T) {
	t.Parallel()

	files, _ := compileExample(t)
	got := files["models/schema.yml"]
	want := `version: 2
models:
  - name: customer_totals
    data_tests:
      - loom_grain_unique:
          arguments:
            grain: customer_id
  - name: customer_report
    data_tests:
      - loom_grain_unique:
          arguments:
            grain: customer_id
`
	if !strings.Contains(got, want) {
		t.Fatalf("schema.yml:\ngot:\n%s\nwant to contain:\n%s", got, want)
	}
	if strings.Contains(got, "      - loom_grain_unique:\n          grain:") {
		t.Fatalf("schema.yml uses the deprecated top-level argument form:\n%s", got)
	}
	if strings.Contains(got, "name: orders") {
		t.Fatalf("schema.yml emits a test for a cell with no declared grain:\n%s", got)
	}
}

func TestCompilePartitionTest(t *testing.T) {
	t.Parallel()

	files, _ := compileExample(t)
	got := files["tests/loom_partition_sums_customer_totals.sql"]
	want := `WITH parts AS (SELECT SUM(total_amount) AS s FROM {{ ref('customer_totals') }}),
     whole AS (SELECT SUM(amount) AS s FROM {{ ref('orders') }})
SELECT parts.s AS part_sum, whole.s AS whole_sum
FROM parts, whole
WHERE ABS(parts.s - whole.s) > 0.001
`
	if !strings.Contains(got, want) {
		t.Fatalf("partition test:\ngot:\n%s\nwant to contain:\n%s", got, want)
	}
	// customer_report aggregates nothing: no test, and that is structural.
	if _, ok := files["tests/loom_partition_sums_customer_report.sql"]; ok {
		t.Fatal("partition test emitted for a non-aggregating cell")
	}
}

func TestCompileMacros(t *testing.T) {
	t.Parallel()

	files, _ := compileExample(t)
	got := files["macros/loom_checks.sql"]
	for _, want := range []string{
		"{% test loom_grain_unique(model, grain) %}",
		"SELECT {{ grain }}, COUNT(*) AS n",
		"HAVING COUNT(*) > 1",
		"{% macro loom_row_audit() %}",
		"INSERT INTO {{ target.schema }}.loom_audit",
		"SELECT '{{ this.identifier }}', COUNT(*), CURRENT_TIMESTAMP FROM {{ this }}",
		"logged_at TIMESTAMP",
		`{% macro loom_setup() %}`,
		`{% macro loom_teardown() %}`,
		`{% for t in ["loom_audit", "orders", "customer_totals", "customer_report"] %}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("macros missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "spike_") {
		t.Fatalf("macros still carry spike names:\n%s", got)
	}
}

func TestCompilePackageYAML(t *testing.T) {
	t.Parallel()

	files, _ := compileExample(t)
	got := files["package.yaml"]
	for _, want := range []string{
		"name: at risk revenue\n",
		"variant: default\n",
		"parameters:\n  - name: as_of\n    domain: date in [2024-01-01, today]\n",
		"grain: customer_id\n",
		"  - id: customer_totals\n    lang: sql\n    grain: customer_id\n",
		"  - id: shared_metric\n    lang: call\n    ref: revenue_at_risk@v2\n",
		"skipped:\n  - as_of\n  - intro\n  - shared_metric\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("package.yaml missing %q:\n%s", want, got)
		}
	}
	// Determinism: no clock in the output.
	if strings.Contains(got, "generated_at") {
		t.Fatalf("package.yaml carries generated_at:\n%s", got)
	}
}

func TestCompileDeterministic(t *testing.T) {
	t.Parallel()

	doc, err := Parse([]byte(exampleDocument))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	first, second := t.TempDir(), t.TempDir()
	if _, err := Compile(doc, first); err != nil {
		t.Fatalf("Compile first: %v", err)
	}
	if _, err := Compile(doc, second); err != nil {
		t.Fatalf("Compile second: %v", err)
	}
	a, b := readTree(t, first), readTree(t, second)
	if len(a) != len(b) {
		t.Fatalf("double compile: %d files vs %d", len(a), len(b))
	}
	for path, content := range a {
		other, ok := b[path]
		if !ok {
			t.Fatalf("double compile: %s missing from second output", path)
		}
		if content != other {
			t.Fatalf("double compile: %s differs:\nfirst:\n%s\nsecond:\n%s", path, content, other)
		}
	}
}

func TestCompileErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		doc     *Document
		wantErr string
	}{
		{
			name:    "nil document",
			doc:     nil,
			wantErr: "nil document",
		},
		{
			name: "ref not in inputs",
			doc: &Document{
				APIVersion: APIVersionV1, Kind: KindProject, Metadata: Metadata{Name: "p"},
				Cells: []Cell{
					{ID: "orders", Lang: LangSQL, Source: "SELECT 1 AS x"},
					{ID: "other", Lang: LangSQL, Source: "SELECT 1 AS x"},
					{ID: "totals", Lang: LangSQL, Inputs: []string{"orders"},
						Source: "SELECT * FROM {{ ref('other') }}"},
				},
			},
			wantErr: `cell "totals": source refs "other" which is not listed in inputs`,
		},
		{
			name: "invalid document reaches compile",
			doc: &Document{
				APIVersion: APIVersionV1, Kind: "Notebook", Metadata: Metadata{Name: "p"},
				Cells: []Cell{{ID: "orders", Lang: LangSQL}},
			},
			wantErr: `kind "Notebook"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			_, err := Compile(tt.doc, filepath.Join(dir, "out"))
			if err == nil {
				t.Fatalf("Compile: want error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Compile: error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestDBTProjectName(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"churn-analysis", "churn_analysis"},
		{"At Risk Revenue", "at_risk_revenue"},
		{"  spaced  ", "spaced"},
		{"2024_revenue", "loom_2024_revenue"},
		{"a.b/c", "a_b_c"},
		{"!!!", "loom_project"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if got := dbtProjectName(tt.in); got != tt.want {
				t.Fatalf("dbtProjectName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
