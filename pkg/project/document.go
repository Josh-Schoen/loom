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

// Package project holds the project document: one YAML file of typed cells,
// living in a git repo, that compiles to a dbt project with generated
// verification. Running the compiled project yields per-cell
// oracle.VerificationRecords folded from dbt artifacts.
package project

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/teradata-labs/loom/pkg/project/oracle"
	"gopkg.in/yaml.v3"
)

// Document apiVersion and kind accepted by Parse.
const (
	APIVersionV1 = "loom/v1"
	KindProject  = "Project"
)

// Cell languages.
const (
	LangSQL    = "sql"
	LangPython = "python"
	LangProse  = "prose"
	LangChart  = "chart"
	LangValue  = "value"
	LangCall   = "call"
	LangInput  = "input"
)

// cellIDPattern is the identifier rule for cell IDs: snake_case, leading
// lowercase letter. Cell IDs become dbt model names and file names, so
// nothing outside this set may pass.
var cellIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Document is a project: one document of typed cells, versioned in git.
type Document struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Cells      []Cell   `yaml:"cells"`
}

// Metadata identifies the project. Variant names the sealed-definition
// variant this document compiles as (registry address is name@variant).
type Metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Variant     string `yaml:"variant,omitempty"`
}

// Cell is one unit of the document. Reading order is presentation only;
// execution order derives from Inputs.
type Cell struct {
	// ID is unique within the document and matches cellIDPattern.
	ID string `yaml:"id"`
	// Lang is one of the Lang* constants.
	Lang string `yaml:"lang"`
	// Inputs are upstream cell IDs — the DAG edges. Every entry names a
	// cell in this document; entries are unique within a cell.
	Inputs []string `yaml:"inputs,omitempty"`
	// DeclaredGrain is the unique key of the output (sql cells) — one
	// column, or a comma-joined composite. In YAML it accepts a scalar OR a
	// sequence (agents naturally write `declared_grain: [customer_id]`, and
	// composite grains genuinely are lists); each part must pass
	// oracle.GrainCountSQL's identifier rule.
	DeclaredGrain GrainKey `yaml:"declared_grain,omitempty"`
	// Source is sql text / python / markdown per Lang. CONTRACT: within
	// Source, upstream cells are referenced as {{ ref('<cell_id>') }}, and
	// every such ref must name a cell listed in Inputs — Compile rejects
	// any that does not.
	Source string `yaml:"source,omitempty"`
	// Chart is a declarative chart spec, opaque to this package.
	Chart map[string]any `yaml:"chart,omitempty"`
	// Params is name→domain for input cells, and the argument map for call
	// cells.
	Params map[string]string `yaml:"params,omitempty"`
	// Ref is the registry address name@variant for call cells.
	Ref string `yaml:"ref,omitempty"`
}

// Load reads and validates a project document from path.
func Load(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("project: read %s: %w", path, err)
	}
	doc, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("project: %s: %w", path, err)
	}
	return doc, nil
}

// Parse decodes and validates a project document. Unknown YAML fields are
// ignored so documents carrying fields a later version adds still load.
func Parse(data []byte) (*Document, error) {
	var doc Document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("project: parse: %w", err)
	}
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	return &doc, nil
}

// Validate fails on anything that cannot compile: wrong kind or apiVersion,
// bad or duplicate cell IDs, unknown or duplicated inputs, dependency
// cycles, grain identifiers that cannot reach a warehouse, and grain-bearing
// sql cells with no source.
func (d *Document) Validate() error {
	if d.APIVersion != APIVersionV1 {
		return fmt.Errorf("project: apiVersion %q: want %q", d.APIVersion, APIVersionV1)
	}
	if d.Kind != KindProject {
		return fmt.Errorf("project: kind %q: want %q", d.Kind, KindProject)
	}
	if strings.TrimSpace(d.Metadata.Name) == "" {
		return fmt.Errorf("project: metadata.name is empty")
	}
	if len(d.Cells) == 0 {
		return fmt.Errorf("project: no cells")
	}

	seen := make(map[string]bool, len(d.Cells))
	for _, c := range d.Cells {
		if c.ID == "" {
			return fmt.Errorf("project: cell with empty id")
		}
		if !cellIDPattern.MatchString(c.ID) {
			return fmt.Errorf("project: cell id %q is not snake_case (%s)", c.ID, cellIDPattern)
		}
		if seen[c.ID] {
			return fmt.Errorf("project: duplicate cell id %q", c.ID)
		}
		seen[c.ID] = true
		if !validLang(c.Lang) {
			return fmt.Errorf("project: cell %q: unknown lang %q", c.ID, c.Lang)
		}
	}

	for _, c := range d.Cells {
		inputSeen := make(map[string]bool, len(c.Inputs))
		for _, in := range c.Inputs {
			if !seen[in] {
				return fmt.Errorf("project: cell %q: input %q names no cell", c.ID, in)
			}
			if inputSeen[in] {
				return fmt.Errorf("project: cell %q: duplicate input %q", c.ID, in)
			}
			inputSeen[in] = true
		}
		if c.DeclaredGrain != "" {
			// Same identifier rule as the grain check that runs the SQL:
			// GrainCountSQL returns "" for anything it refuses to emit.
			// Composite grains validate per column.
			for _, part := range strings.Split(string(c.DeclaredGrain), ",") {
				if oracle.GrainCountSQL(strings.TrimSpace(part), "SELECT 1") == "" {
					return fmt.Errorf("project: cell %q: declared_grain %q is not a valid grain identifier", c.ID, c.DeclaredGrain)
				}
			}
			if c.Lang == LangSQL && strings.TrimSpace(c.Source) == "" {
				return fmt.Errorf("project: cell %q: sql cell with declared_grain has no source", c.ID)
			}
		}
	}

	if _, err := d.TopoOrder(); err != nil {
		return err
	}
	return nil
}

func validLang(lang string) bool {
	switch lang {
	case LangSQL, LangPython, LangProse, LangChart, LangValue, LangCall, LangInput:
		return true
	default:
		return false
	}
}

// TopoOrder returns cell IDs in dependency order. Deterministic: cells are
// emitted level by level, sorted by ID within a level.
func (d *Document) TopoOrder() ([]string, error) {
	indeg := make(map[string]int, len(d.Cells))
	dependents := make(map[string][]string, len(d.Cells))
	ids := make([]string, 0, len(d.Cells))
	for _, c := range d.Cells {
		if _, dup := indeg[c.ID]; dup {
			return nil, fmt.Errorf("project: duplicate cell id %q", c.ID)
		}
		indeg[c.ID] = 0
		ids = append(ids, c.ID)
	}
	for _, c := range d.Cells {
		edge := make(map[string]bool, len(c.Inputs))
		for _, in := range c.Inputs {
			if _, ok := indeg[in]; !ok {
				return nil, fmt.Errorf("project: cell %q: input %q names no cell", c.ID, in)
			}
			if edge[in] {
				continue
			}
			edge[in] = true
			indeg[c.ID]++
			dependents[in] = append(dependents[in], c.ID)
		}
	}

	ready := make([]string, 0, len(ids))
	for _, id := range ids {
		if indeg[id] == 0 {
			ready = append(ready, id)
		}
	}
	order := make([]string, 0, len(ids))
	for len(ready) > 0 {
		sort.Strings(ready)
		var next []string
		for _, id := range ready {
			order = append(order, id)
			for _, dep := range dependents[id] {
				indeg[dep]--
				if indeg[dep] == 0 {
					next = append(next, dep)
				}
			}
		}
		ready = next
	}
	if len(order) != len(ids) {
		var stuck []string
		for _, id := range ids {
			if indeg[id] > 0 {
				stuck = append(stuck, id)
			}
		}
		sort.Strings(stuck)
		return nil, fmt.Errorf("project: dependency cycle among cells %s", strings.Join(stuck, ", "))
	}
	return order, nil
}

// Cell returns the cell with the given ID.
func (d *Document) Cell(id string) (Cell, bool) {
	for _, c := range d.Cells {
		if c.ID == id {
			return c, true
		}
	}
	return Cell{}, false
}

// GrainKey is a grain declaration: one column name, or a comma-joined
// composite. YAML accepts either a scalar or a sequence of scalars —
// agents write both shapes, and composite grains are honestly lists.
type GrainKey string

// UnmarshalYAML accepts `declared_grain: customer_id` and
// `declared_grain: [customer_id, month]` alike.
func (g *GrainKey) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		*g = GrainKey(strings.TrimSpace(value.Value))
		return nil
	case yaml.SequenceNode:
		parts := make([]string, 0, len(value.Content))
		for _, n := range value.Content {
			if n.Kind != yaml.ScalarNode {
				return fmt.Errorf("declared_grain: sequence entries must be column names")
			}
			if v := strings.TrimSpace(n.Value); v != "" {
				parts = append(parts, v)
			}
		}
		*g = GrainKey(strings.Join(parts, ", "))
		return nil
	default:
		return fmt.Errorf("declared_grain: expected a column name or a list of column names")
	}
}
