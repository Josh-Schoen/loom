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

// Package toolbench is a benchmark harness for tool discovery strategies.
//
// It measures two things offline, without an LLM:
//
//  1. Retrieval quality and latency of the tool registry's FTS5 search
//     (recall@k, MRR, search time) against a labeled synthetic corpus.
//  2. Prompt-prefix stability of different ways of delivering discovered
//     tools to a provider (all tools upfront, append-on-discovery,
//     generic dispatcher, hybrid), using a deterministic prefix-cache model.
//
// It does NOT measure model behavior: tool-selection accuracy, argument
// accuracy and end-to-end latency need a live model and are out of scope.
// The synthetic corpus is generated from templates, so absolute retrieval
// numbers are indicative only; use them to compare strategies and detect
// regressions, not as a claim about real-world accuracy.
package toolbench

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"

	"github.com/teradata-labs/loom/pkg/shuttle"
)

// Entry is one tool in the synthetic corpus.
type Entry struct {
	Name         string
	Description  string
	Domain       string
	Noun         string
	Verb         string
	Schema       *shuttle.JSONSchema
	Capabilities []string
	Keywords     []string
}

// Def returns the serialized provider tool definition for the entry. Its byte
// length is what the tool costs in the prompt prefix.
func (e Entry) Def() string {
	return marshalDef(e.Name, e.Description, e.Schema)
}

// Task is a labeled discovery request: a natural-language query and the one
// tool that satisfies it.
type Task struct {
	Query    string
	Expected string
}

// CorpusConfig controls corpus generation.
type CorpusConfig struct {
	// Tools is the number of tools to generate (max MaxCorpusTools).
	Tools int
	// Seed makes generation deterministic.
	Seed int64
}

type domainSpec struct {
	name  string
	label string
	nouns []string
}

// domains are the service families the corpus draws from. Every domain
// contributes len(nouns) * len(verbs) candidate tools.
var domains = []domainSpec{
	{"slack", "Slack", []string{"message", "channel", "reminder", "user", "file"}},
	{"github", "GitHub", []string{"issue", "pull request", "branch", "release", "comment"}},
	{"postgres", "PostgreSQL", []string{"table", "index", "row", "schema", "backup"}},
	{"jira", "Jira", []string{"ticket", "sprint", "board", "epic", "worklog"}},
	{"gdrive", "Google Drive", []string{"document", "folder", "permission", "revision", "comment"}},
	{"s3", "Amazon S3", []string{"object", "bucket", "policy", "lifecycle rule", "tag"}},
	{"calendar", "Calendar", []string{"event", "invite", "calendar", "attendee", "reminder"}},
	{"email", "Email", []string{"message", "draft", "label", "attachment", "thread"}},
	{"kubernetes", "Kubernetes", []string{"pod", "deployment", "service", "namespace", "secret"}},
	{"stripe", "Stripe", []string{"customer", "invoice", "subscription", "refund", "charge"}},
	{"salesforce", "Salesforce", []string{"lead", "opportunity", "account", "contact", "campaign"}},
	{"datadog", "Datadog", []string{"monitor", "dashboard", "metric", "downtime", "log"}},
	{"pagerduty", "PagerDuty", []string{"incident", "schedule", "service", "escalation policy", "alert"}},
	{"notion", "Notion", []string{"page", "database", "block", "comment", "workspace"}},
	{"linear", "Linear", []string{"issue", "project", "cycle", "label", "team"}},
	{"snowflake", "Snowflake", []string{"warehouse", "stage", "task", "view", "role"}},
}

type verbSpec struct {
	name     string
	phrase   string   // description opener
	plural   bool     // noun is pluralized in descriptions and queries
	synonyms []string // words a caller might use instead of the canonical verb
	params   func(noun string) (map[string]*shuttle.JSONSchema, []string)
}

var verbs = []verbSpec{
	{"create", "Create a new", false, []string{"make", "add", "open", "set up"}, func(n string) (map[string]*shuttle.JSONSchema, []string) {
		return map[string]*shuttle.JSONSchema{
			"title":   shuttle.NewStringSchema("Title or name of the " + n),
			"body":    shuttle.NewStringSchema("Free-form description or content of the " + n),
			"options": shuttle.NewObjectSchema("Optional settings for the "+n, map[string]*shuttle.JSONSchema{"notify": shuttle.NewBooleanSchema("Notify subscribers")}, nil),
		}, []string{"title"}
	}},
	{"get", "Get a single", false, []string{"fetch", "retrieve", "read", "show"}, func(n string) (map[string]*shuttle.JSONSchema, []string) {
		return map[string]*shuttle.JSONSchema{
			"id": shuttle.NewStringSchema("Identifier of the " + n),
		}, []string{"id"}
	}},
	{"list", "List the", true, []string{"enumerate", "browse", "show all", "page through"}, func(n string) (map[string]*shuttle.JSONSchema, []string) {
		return map[string]*shuttle.JSONSchema{
			"limit":  shuttle.NewNumberSchema("Maximum number of " + n + "s to return"),
			"cursor": shuttle.NewStringSchema("Pagination cursor from a previous call"),
			"filter": shuttle.NewStringSchema("Filter expression applied to " + n + "s"),
		}, nil
	}},
	{"update", "Update an existing", false, []string{"modify", "edit", "change", "amend"}, func(n string) (map[string]*shuttle.JSONSchema, []string) {
		return map[string]*shuttle.JSONSchema{
			"id":     shuttle.NewStringSchema("Identifier of the " + n),
			"fields": shuttle.NewObjectSchema("Fields to change on the "+n, map[string]*shuttle.JSONSchema{"title": shuttle.NewStringSchema("New title")}, nil),
		}, []string{"id", "fields"}
	}},
	{"delete", "Delete a", false, []string{"remove", "erase", "drop", "discard"}, func(n string) (map[string]*shuttle.JSONSchema, []string) {
		return map[string]*shuttle.JSONSchema{
			"id":      shuttle.NewStringSchema("Identifier of the " + n),
			"confirm": shuttle.NewBooleanSchema("Must be true to proceed"),
		}, []string{"id", "confirm"}
	}},
	{"search", "Search for", true, []string{"find", "look up", "query", "locate"}, func(n string) (map[string]*shuttle.JSONSchema, []string) {
		return map[string]*shuttle.JSONSchema{
			"query": shuttle.NewStringSchema("Search text matched against " + n + "s"),
			"limit": shuttle.NewNumberSchema("Maximum number of results"),
		}, []string{"query"}
	}},
}

// MaxCorpusTools is the number of distinct tools the generator can produce.
func MaxCorpusTools() int {
	n := 0
	for _, d := range domains {
		n += len(d.nouns) * len(verbs)
	}
	return n
}

// GenerateCorpus produces a deterministic corpus of cfg.Tools entries, sorted
// by name. The subset is chosen by shuffling the full candidate list with
// cfg.Seed, so smaller corpora are not prefixes of larger ones.
func GenerateCorpus(cfg CorpusConfig) ([]Entry, error) {
	if cfg.Tools <= 0 {
		return nil, fmt.Errorf("toolbench: Tools must be positive, got %d", cfg.Tools)
	}
	if cfg.Tools > MaxCorpusTools() {
		return nil, fmt.Errorf("toolbench: Tools %d exceeds corpus maximum %d", cfg.Tools, MaxCorpusTools())
	}

	all := make([]Entry, 0, MaxCorpusTools())
	for _, d := range domains {
		for _, n := range d.nouns {
			for _, v := range verbs {
				all = append(all, buildEntry(d, n, v))
			}
		}
	}

	rng := rand.New(rand.NewSource(cfg.Seed)) // #nosec G404 -- deterministic benchmark data, not security-sensitive
	rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	picked := all[:cfg.Tools]
	sort.Slice(picked, func(i, j int) bool { return picked[i].Name < picked[j].Name })
	return picked, nil
}

func buildEntry(d domainSpec, noun string, v verbSpec) Entry {
	nounWord := noun
	if v.plural {
		nounWord = noun + "s"
	}
	props, required := v.params(noun)
	props["workspace"] = shuttle.NewStringSchema("Workspace or account scope in " + d.label)
	name := fmt.Sprintf("%s_%s_%s", d.name, v.name, strings.ReplaceAll(noun, " ", "_"))
	return Entry{
		Name:         name,
		Description:  fmt.Sprintf("%s %s in %s.", v.phrase, nounWord, d.label),
		Domain:       d.name,
		Noun:         noun,
		Verb:         v.name,
		Schema:       shuttle.NewObjectSchema("Parameters for "+name, props, required),
		Capabilities: []string{d.name},
		Keywords:     []string{d.label, noun},
	}
}

// GenerateTasks produces n labeled discovery tasks for entries. Queries use a
// synonym of the canonical verb, so a task is not a copy of the tool's name.
func GenerateTasks(entries []Entry, n int, seed int64) []Task {
	rng := rand.New(rand.NewSource(seed)) // #nosec G404 -- deterministic benchmark data
	tasks := make([]Task, 0, n)
	for i := 0; i < n; i++ {
		tasks = append(tasks, queryFor(entries[rng.Intn(len(entries))], rng))
	}
	return tasks
}

func queryFor(e Entry, rng *rand.Rand) Task {
	var label string
	for _, d := range domains {
		if d.name == e.Domain {
			label = d.label
			break
		}
	}
	var syn []string
	plural := false
	for _, v := range verbs {
		if v.name == e.Verb {
			syn = v.synonyms
			plural = v.plural
			break
		}
	}
	verb := syn[rng.Intn(len(syn))]
	noun := e.Noun
	if plural {
		noun += "s"
	}
	templates := []string{
		"%[1]s %[2]s in %[3]s",
		"%[3]s: %[1]s %[2]s",
		"I need to %[1]s the %[2]s on %[3]s",
	}
	return Task{
		Query:    fmt.Sprintf(templates[rng.Intn(len(templates))], verb, noun, label),
		Expected: e.Name,
	}
}

// SessionConfig shapes a scripted session: which tools are needed on which turn.
type SessionConfig struct {
	// Turns is the number of user turns; each needs exactly one tool.
	Turns int
	// HotTools is the size of the set of tools that recur through the session.
	HotTools int
	// HotShare is the probability (0-1) that a turn needs a hot tool.
	HotShare float64
	// Seed makes the script deterministic.
	Seed int64
}

// GenerateSession scripts a session of cfg.Turns tasks. HotShare of the turns
// draw from a small recurring set, the rest from the whole corpus, which models
// the common pattern of a few tools doing most of the work.
func GenerateSession(entries []Entry, cfg SessionConfig) ([]Task, error) {
	if cfg.Turns <= 0 || cfg.HotTools <= 0 || cfg.HotTools > len(entries) {
		return nil, fmt.Errorf("toolbench: invalid session config %+v for %d tools", cfg, len(entries))
	}
	if cfg.HotShare < 0 || cfg.HotShare > 1 {
		return nil, fmt.Errorf("toolbench: HotShare must be in [0,1], got %v", cfg.HotShare)
	}
	rng := rand.New(rand.NewSource(cfg.Seed)) // #nosec G404 -- deterministic benchmark data
	hot := make([]Entry, cfg.HotTools)
	for i, idx := range rng.Perm(len(entries))[:cfg.HotTools] {
		hot[i] = entries[idx]
	}
	tasks := make([]Task, 0, cfg.Turns)
	for i := 0; i < cfg.Turns; i++ {
		pool := entries
		if rng.Float64() < cfg.HotShare {
			pool = hot
		}
		tasks = append(tasks, queryFor(pool[rng.Intn(len(pool))], rng))
	}
	return tasks, nil
}

func marshalDef(name, description string, schema *shuttle.JSONSchema) string {
	b, err := json.Marshal(struct {
		Name        string              `json:"name"`
		Description string              `json:"description"`
		InputSchema *shuttle.JSONSchema `json:"input_schema"`
	}{name, description, schema})
	if err != nil {
		// JSONSchema contains only marshalable types; a failure is a programming error.
		panic(fmt.Sprintf("toolbench: marshal tool def %q: %v", name, err))
	}
	return string(b)
}
