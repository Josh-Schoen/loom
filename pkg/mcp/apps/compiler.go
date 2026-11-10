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

package apps

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"strings"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

// Structural limits for spec validation.
const (
	MaxComponents          = 50
	MaxNestingDepth        = 10
	MaxComponentPropsBytes = 64 * 1024  // 64 KB per component props
	MaxSpecBytes           = 512 * 1024 // 512 KB total spec

	MaxDataTableRows = 1000 // rows in one data-table
	MaxPlanDepth     = 12   // levels of steps in one query-plan
	MaxPlanSteps     = 100  // steps in one query-plan, all levels
)

// Dangerous keys that are rejected at any nesting depth (prototype pollution prevention).
var dangerousKeys = map[string]bool{
	"__proto__":   true,
	"constructor": true,
	"prototype":   true,
}

// Dangerous string value prefixes.
var dangerousValuePrefixes = []string{
	"javascript:",
	"vbscript:",
	"data:text/html",
}

// Dangerous CSS value patterns.
var dangerousCSSPatterns = []string{
	"url(",
	"expression(",
	"@import",
}

//go:embed html/app-template.html
var appTemplateHTML []byte

//go:embed html/runtime.js
var runtimeJS []byte

//go:embed html/chart-legend.js
var chartLegendJS []byte

// legendInjectMarker is the placeholder inside runtime.js's IIFE where the
// chart-legend.js source is spliced in at build time so it shares the runtime's
// closure scope (THEME, createElement, setSafeAttribute, SLICE_CHART_TYPES).
const legendInjectMarker = "// __LOOM_INJECT_CHART_LEGEND__"

// assembledRuntime returns runtime.js with chart-legend.js injected at the
// marker. Injecting (rather than shipping a second <script>) keeps the legend
// code inside the runtime IIFE where its helpers live.
func assembledRuntime() string {
	src := string(runtimeJS)
	if !strings.Contains(src, legendInjectMarker) {
		// No marker: fall back to the raw runtime so charts still render even if
		// the legend cannot be injected.
		return src
	}
	return strings.Replace(src, legendInjectMarker, string(chartLegendJS), 1)
}

// Compiler validates UIAppSpec and compiles it to standalone HTML.
type Compiler struct {
	catalog *ComponentCatalog
	tmpl    *template.Template
	runtime string
}

// templateData is passed to the HTML template during execution.
type templateData struct {
	Title    string
	SpecJSON template.JS // #nosec -- sanitized protojson output, </ escaped to prevent script tag injection
	Runtime  template.JS // #nosec -- runtime.js is embedded at build time, not user input
}

// NewCompiler creates a new compiler with the embedded template and runtime.
func NewCompiler() (*Compiler, error) {
	tmpl, err := template.New("app").Parse(string(appTemplateHTML))
	if err != nil {
		return nil, fmt.Errorf("parse app template: %w", err)
	}

	return &Compiler{
		catalog: NewComponentCatalog(),
		tmpl:    tmpl,
		runtime: assembledRuntime(),
	}, nil
}

// Compile validates the spec and compiles it to a standalone HTML document.
func (c *Compiler) Compile(spec *loomv1.UIAppSpec) ([]byte, error) {
	if err := c.Validate(spec); err != nil {
		return nil, fmt.Errorf("invalid spec: %w", err)
	}

	// Marshal spec to JSON using protojson for correct field naming
	specJSON, err := protojson.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}

	title := spec.Title
	if title == "" {
		title = "Loom App"
	}

	// Sanitize JSON for safe embedding inside <script type="application/json">.
	// Escape <, >, & to their JSON unicode equivalents (\u003c, \u003e, \u0026)
	// to prevent: (1) premature </script> tag closing, (2) HTML injection in
	// defense-in-depth. JSON.parse handles these unicode escapes transparently.
	// This matches Go's encoding/json.Marshal HTML-safe escaping behavior.
	safeJSON := strings.ReplaceAll(string(specJSON), "<", `\u003c`)
	safeJSON = strings.ReplaceAll(safeJSON, ">", `\u003e`)
	safeJSON = strings.ReplaceAll(safeJSON, "&", `\u0026`)

	data := templateData{
		Title:    title,
		SpecJSON: template.JS(safeJSON),  // #nosec -- sanitized protojson output with </ escaped
		Runtime:  template.JS(c.runtime), // #nosec -- embedded build-time JS, not user content
	}

	var buf bytes.Buffer
	if err := c.tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	return buf.Bytes(), nil
}

// Validate checks a spec against structural limits, component type validity,
// and security constraints (dangerous keys/values).
func (c *Compiler) Validate(spec *loomv1.UIAppSpec) error {
	if spec == nil {
		return fmt.Errorf("spec is nil")
	}

	if spec.Version != "1.0" {
		return fmt.Errorf("unsupported spec version %q (expected \"1.0\")", spec.Version)
	}

	// Check total spec size
	specJSON, err := protojson.Marshal(spec)
	if err != nil {
		return fmt.Errorf("marshal spec for size check: %w", err)
	}
	if len(specJSON) > MaxSpecBytes {
		return fmt.Errorf("spec exceeds maximum size (%d bytes > %d bytes)", len(specJSON), MaxSpecBytes)
	}

	// Validate layout
	switch spec.Layout {
	case "", "stack", "grid-2", "grid-3", "grid", "grid-4":
		// valid
	default:
		return fmt.Errorf("invalid layout %q (valid: stack, grid-2, grid-3, grid, grid-4)", spec.Layout)
	}

	// Validate components
	if len(spec.Components) == 0 {
		return fmt.Errorf("spec must have at least one component")
	}

	componentCount := 0
	if err := c.validateComponents(spec.Components, 0, &componentCount); err != nil {
		return err
	}

	return nil
}

// validateComponents recursively validates a list of components.
func (c *Compiler) validateComponents(components []*loomv1.UIComponent, depth int, count *int) error {
	if depth > MaxNestingDepth {
		return fmt.Errorf("component nesting depth exceeds maximum (%d)", MaxNestingDepth)
	}

	for i, comp := range components {
		*count++
		if *count > MaxComponents {
			return fmt.Errorf("component count exceeds maximum (%d)", MaxComponents)
		}

		if comp.Type == "" {
			return fmt.Errorf("component[%d] at depth %d: type is required", i, depth)
		}

		if !c.catalog.IsValidType(comp.Type) {
			return fmt.Errorf("component[%d]: unknown type %q (valid types: %s)",
				i, comp.Type, strings.Join(c.catalog.ValidTypes(), ", "))
		}

		// Check props size
		if comp.Props != nil {
			propsJSON, err := comp.Props.MarshalJSON()
			if err != nil {
				return fmt.Errorf("component[%d] %q: failed to marshal props: %w", i, comp.Type, err)
			}
			if len(propsJSON) > MaxComponentPropsBytes {
				hint := ""
				if comp.Type == "data-table" {
					hint = "; put in fewer rows (a sample) and set total to the full count"
				}
				return fmt.Errorf("component[%d] %q: props exceed maximum size (%d bytes > %d bytes)%s",
					i, comp.Type, len(propsJSON), MaxComponentPropsBytes, hint)
			}

			// Sanitize props for dangerous keys and values
			if err := sanitizeStruct(comp.Props.AsMap()); err != nil {
				return fmt.Errorf("component[%d] %q props: %w", i, comp.Type, err)
			}
		}

		var partErr error
		switch comp.Type {
		case "checklist":
			partErr = validateChecklist(comp)
		case "data-table":
			partErr = validateDataTable(comp)
		case "query-plan":
			partErr = validateQueryPlan(comp)
		}
		if partErr != nil {
			return fmt.Errorf("component[%d] %q: %w", i, comp.Type, partErr)
		}

		// Validate children
		if len(comp.Children) > 0 {
			if !c.catalog.HasChildren(comp.Type) {
				return fmt.Errorf("component[%d] %q does not support children", i, comp.Type)
			}
			if err := c.validateComponents(comp.Children, depth+1, count); err != nil {
				return err
			}
		}
	}

	return nil
}

// validateChecklist requires what saving a tick needs: a component id, and
// item ids (explicit, or the label) that are unique within the checklist.
func validateChecklist(comp *loomv1.UIComponent) error {
	if comp.Id == "" {
		return fmt.Errorf("id is required so ticks can be saved")
	}
	seen := make(map[string]bool)
	for n, item := range ChecklistItems(comp) {
		id := ChecklistItemID(item)
		if id == "" {
			return fmt.Errorf("item %d needs a label or id", n)
		}
		if seen[id] {
			return fmt.Errorf("duplicate item id %q", id)
		}
		seen[id] = true
	}
	return nil
}

// dataColumnTypes are the column types a data-table (and a schema) knows.
var dataColumnTypes = map[string]bool{"text": true, "number": true, "money": true, "date": true, "yes-no": true}

// validateDataTable requires what drawing the rows needs: columns, at most
// MaxDataTableRows rows, each no longer than the columns, and relations that
// name one of the table's own columns.
func validateDataTable(comp *loomv1.UIComponent) error {
	fields := comp.GetProps().GetFields()
	columns := fields["columns"].GetListValue().GetValues()
	if len(columns) == 0 {
		return fmt.Errorf(`columns is required: list the column names, or objects like {"name":"price","label":"Price","type":"money"}`)
	}
	names := make(map[string]bool, len(columns))
	for n, col := range columns {
		name := col.GetStringValue()
		if obj := col.GetStructValue(); obj != nil {
			name = obj.GetFields()["name"].GetStringValue()
			if typ, ok := obj.GetFields()["type"]; ok && !dataColumnTypes[typ.GetStringValue()] {
				return fmt.Errorf("column %d (%q) has type %q: use text, number, money, date or yes-no", n, name, typ.GetStringValue())
			}
		}
		if name == "" {
			return fmt.Errorf(`column %d needs a name: give a string, or an object with "name"`, n)
		}
		names[name] = true
	}
	rowsVal, ok := fields["rows"]
	if !ok {
		return fmt.Errorf("rows is required: one list of values per row, in column order (it may be empty)")
	}
	if rowsVal.GetListValue() == nil {
		return fmt.Errorf("rows must be a list of rows, each a list of values in column order")
	}
	rows := rowsVal.GetListValue().GetValues()
	if len(rows) > MaxDataTableRows {
		return fmt.Errorf("has %d rows, more than the %d a data-table holds: put in a sample of at most %d rows and set total to the full count", len(rows), MaxDataTableRows, MaxDataTableRows)
	}
	for n, row := range rows {
		cells := row.GetListValue()
		if cells == nil {
			return fmt.Errorf("row %d must be a list of values in column order, not an object", n)
		}
		if len(cells.GetValues()) > len(columns) {
			return fmt.Errorf("row %d has %d values but there are %d columns: give one value per column, in column order", n, len(cells.GetValues()), len(columns))
		}
	}
	for n, rel := range fields["relations"].GetListValue().GetValues() {
		rf := rel.GetStructValue().GetFields()
		column, table, to := rf["column"].GetStringValue(), rf["table"].GetStringValue(), rf["to"].GetStringValue()
		if column == "" || table == "" || to == "" {
			return fmt.Errorf(`relation %d needs column, table and to, like {"column":"make","table":"makes","to":"make"}`, n)
		}
		if !names[column] {
			return fmt.Errorf("relation %d names column %q, which this data-table does not have", n, column)
		}
	}
	return nil
}

// validateQueryPlan requires steps, each with a label, nested at most
// MaxPlanDepth deep and at most MaxPlanSteps in all.
func validateQueryPlan(comp *loomv1.UIComponent) error {
	steps := comp.GetProps().GetFields()["steps"].GetListValue().GetValues()
	if len(steps) == 0 {
		return fmt.Errorf(`steps is required: the top step (usually one), like {"label":"Count cars by country","rows":3,"time":1.2,"children":[...]}`)
	}
	count := 0
	return validatePlanSteps(steps, 1, &count)
}

func validatePlanSteps(steps []*structpb.Value, depth int, count *int) error {
	if depth > MaxPlanDepth {
		return fmt.Errorf("steps are nested more than %d deep: leave out the less important inner steps, or join them into one step", MaxPlanDepth)
	}
	for _, step := range steps {
		*count++
		if *count > MaxPlanSteps {
			return fmt.Errorf("has more than %d steps: show the main steps and join the small ones", MaxPlanSteps)
		}
		obj := step.GetStructValue()
		if obj == nil {
			return fmt.Errorf(`each step must be an object like {"label":"Read makes","rows":8}`)
		}
		if obj.GetFields()["label"].GetStringValue() == "" {
			return fmt.Errorf("step %d needs a label: say what the step does in plain words", *count)
		}
		if children := obj.GetFields()["children"].GetListValue().GetValues(); len(children) > 0 {
			if err := validatePlanSteps(children, depth+1, count); err != nil {
				return err
			}
		}
	}
	return nil
}

// ChecklistItems returns the item objects of a checklist component.
func ChecklistItems(comp *loomv1.UIComponent) []*structpb.Struct {
	itemsVal, ok := comp.GetProps().GetFields()["items"]
	if !ok {
		return nil
	}
	var items []*structpb.Struct
	for _, v := range itemsVal.GetListValue().GetValues() {
		if obj := v.GetStructValue(); obj != nil {
			items = append(items, obj)
		}
	}
	return items
}

// ChecklistItemID is an item's id, or its label when it has none (the
// runtime uses the same rule).
func ChecklistItemID(item *structpb.Struct) string {
	if id := item.GetFields()["id"].GetStringValue(); id != "" {
		return id
	}
	return item.GetFields()["label"].GetStringValue()
}

// sanitizeStruct recursively checks a map for dangerous keys and values.
func sanitizeStruct(m map[string]interface{}) error {
	for key, val := range m {
		if dangerousKeys[key] {
			return fmt.Errorf("dangerous key %q rejected (prototype pollution prevention)", key)
		}

		switch v := val.(type) {
		case string:
			lower := strings.ToLower(v)
			for _, prefix := range dangerousValuePrefixes {
				if strings.HasPrefix(lower, prefix) {
					return fmt.Errorf("dangerous value rejected in key %q: %q", key, prefix+"...")
				}
			}
			for _, pattern := range dangerousCSSPatterns {
				if strings.Contains(lower, pattern) {
					return fmt.Errorf("dangerous CSS pattern rejected in key %q: %q", key, pattern)
				}
			}
		case map[string]interface{}:
			if err := sanitizeStruct(v); err != nil {
				return err
			}
		case []interface{}:
			if err := sanitizeSlice(v); err != nil {
				return err
			}
		}
	}
	return nil
}

// sanitizeSlice recursively checks a slice for dangerous keys and values.
func sanitizeSlice(s []interface{}) error {
	for _, val := range s {
		switch v := val.(type) {
		case string:
			lower := strings.ToLower(v)
			for _, prefix := range dangerousValuePrefixes {
				if strings.HasPrefix(lower, prefix) {
					return fmt.Errorf("dangerous value rejected: %q", prefix+"...")
				}
			}
			for _, pattern := range dangerousCSSPatterns {
				if strings.Contains(lower, pattern) {
					return fmt.Errorf("dangerous CSS pattern rejected in array element: %q", pattern)
				}
			}
		case map[string]interface{}:
			if err := sanitizeStruct(v); err != nil {
				return err
			}
		case []interface{}:
			if err := sanitizeSlice(v); err != nil {
				return err
			}
		}
	}
	return nil
}

// Catalog returns the compiler's component catalog.
func (c *Compiler) Catalog() *ComponentCatalog {
	return c.catalog
}

// ListComponentTypes returns proto ComponentType messages for the discovery RPC.
func (c *Compiler) ListComponentTypes() []*loomv1.ComponentType {
	return c.catalog.ToProto()
}
