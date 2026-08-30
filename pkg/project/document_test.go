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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		wantErr string // substring; empty means the document must load
	}{
		{
			name: "minimal valid",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {name: churn}
cells:
  - id: orders
    lang: sql
    source: SELECT 1 AS x
`,
		},
		{
			name: "bad kind",
			yaml: `apiVersion: loom/v1
kind: Notebook
metadata: {name: churn}
cells:
  - id: orders
    lang: sql
`,
			wantErr: `kind "Notebook"`,
		},
		{
			name: "bad apiVersion",
			yaml: `apiVersion: loom/v2
kind: Project
metadata: {name: churn}
cells:
  - id: orders
    lang: sql
`,
			wantErr: `apiVersion "loom/v2"`,
		},
		{
			name: "empty project name",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {}
cells:
  - id: orders
    lang: sql
`,
			wantErr: "metadata.name is empty",
		},
		{
			name: "no cells",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {name: churn}
cells: []
`,
			wantErr: "no cells",
		},
		{
			name: "duplicate cell ids",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {name: churn}
cells:
  - id: orders
    lang: sql
  - id: orders
    lang: sql
`,
			wantErr: `duplicate cell id "orders"`,
		},
		{
			name: "empty cell id",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {name: churn}
cells:
  - id: ""
    lang: sql
`,
			wantErr: "empty id",
		},
		{
			name: "cell id not snake_case",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {name: churn}
cells:
  - id: CustomerTotals
    lang: sql
`,
			wantErr: `cell id "CustomerTotals" is not snake_case`,
		},
		{
			name: "unknown lang",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {name: churn}
cells:
  - id: orders
    lang: rust
`,
			wantErr: `unknown lang "rust"`,
		},
		{
			name: "input names no cell",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {name: churn}
cells:
  - id: totals
    lang: sql
    inputs: [orders]
`,
			wantErr: `input "orders" names no cell`,
		},
		{
			name: "duplicate input",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {name: churn}
cells:
  - id: orders
    lang: sql
    source: SELECT 1 AS x
  - id: totals
    lang: sql
    inputs: [orders, orders]
    source: SELECT 1 AS x
`,
			wantErr: `duplicate input "orders"`,
		},
		{
			name: "dependency cycle",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {name: churn}
cells:
  - id: a
    lang: sql
    inputs: [b]
    source: SELECT 1 AS x
  - id: b
    lang: sql
    inputs: [a]
    source: SELECT 1 AS x
`,
			wantErr: "dependency cycle among cells a, b",
		},
		{
			name: "self cycle",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {name: churn}
cells:
  - id: a
    lang: sql
    inputs: [a]
    source: SELECT 1 AS x
`,
			wantErr: "dependency cycle among cells a",
		},
		{
			name: "grain with injection",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {name: churn}
cells:
  - id: totals
    lang: sql
    declared_grain: "customer_id; DROP TABLE t"
    source: SELECT 1 AS x
`,
			wantErr: "is not a valid grain identifier",
		},
		{
			name: "grain with space",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {name: churn}
cells:
  - id: totals
    lang: sql
    declared_grain: "customer id"
    source: SELECT 1 AS x
`,
			wantErr: "is not a valid grain identifier",
		},
		{
			name: "qualified grain accepted",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {name: churn}
cells:
  - id: totals
    lang: sql
    declared_grain: t.customer_id
    source: SELECT 1 AS x
`,
		},
		{
			name: "grain without source",
			yaml: `apiVersion: loom/v1
kind: Project
metadata: {name: churn}
cells:
  - id: totals
    lang: sql
    declared_grain: customer_id
`,
			wantErr: "sql cell with declared_grain has no source",
		},
		{
			name:    "malformed yaml",
			yaml:    "apiVersion: loom/v1\nkind: [Project\n",
			wantErr: "parse:",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, err := Parse([]byte(tt.yaml))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Parse: unexpected error: %v", err)
				}
				if doc == nil {
					t.Fatal("Parse: nil document with nil error")
				}
				return
			}
			if err == nil {
				t.Fatalf("Parse: want error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Parse: error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "project.yaml")
	if err := os.WriteFile(path, []byte(exampleDocument), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if doc.Metadata.Name != "at risk revenue" {
		t.Fatalf("Load: name = %q", doc.Metadata.Name)
	}
	if len(doc.Cells) != 6 {
		t.Fatalf("Load: %d cells, want 6", len(doc.Cells))
	}
	if _, err := Load(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Fatal("Load: want error for missing file")
	}
}

func TestTopoOrderDeterminism(t *testing.T) {
	t.Parallel()

	// Two roots and a diamond: level order with ID sorting must pin the
	// result regardless of document order.
	doc := &Document{
		APIVersion: APIVersionV1,
		Kind:       KindProject,
		Metadata:   Metadata{Name: "topo"},
		Cells: []Cell{
			{ID: "zeta", Lang: LangSQL, Inputs: []string{"beta", "alpha"}, Source: "SELECT 1 AS x"},
			{ID: "beta", Lang: LangSQL, Inputs: []string{"alpha"}, Source: "SELECT 1 AS x"},
			{ID: "alpha", Lang: LangSQL, Source: "SELECT 1 AS x"},
			{ID: "gamma", Lang: LangProse, Source: "notes"},
		},
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	want := []string{"alpha", "gamma", "beta", "zeta"}
	for i := 0; i < 25; i++ {
		got, err := doc.TopoOrder()
		if err != nil {
			t.Fatalf("TopoOrder: %v", err)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("TopoOrder iteration %d = %v, want %v", i, got, want)
		}
	}
}

func TestCellLookup(t *testing.T) {
	t.Parallel()

	doc, err := Parse([]byte(exampleDocument))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	c, ok := doc.Cell("customer_totals")
	if !ok {
		t.Fatal("Cell: customer_totals not found")
	}
	if c.DeclaredGrain != "customer_id" {
		t.Fatalf("Cell: grain = %q", c.DeclaredGrain)
	}
	if _, ok := doc.Cell("nope"); ok {
		t.Fatal("Cell: want miss for unknown id")
	}
}
