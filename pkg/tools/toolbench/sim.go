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
package toolbench

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/teradata-labs/loom/pkg/shuttle"
)

// Strategy is a way of getting tools in front of the model.
type Strategy int

const (
	// AllUpfront sends every tool definition on every request.
	AllUpfront Strategy = iota
	// SearchAppend sends core tools plus tool_search; tools found by a search
	// are appended to the tools array for the rest of the session.
	SearchAppend
	// Dispatcher sends core tools, tool_search and a generic call_tool. Found
	// tools' schemas travel in the search result text, and the tools array
	// never changes.
	Dispatcher
	// Hybrid starts as Dispatcher and promotes a tool into the tools array
	// once it has been used PromoteAfter times.
	Hybrid
)

func (s Strategy) String() string {
	switch s {
	case AllUpfront:
		return "all-upfront"
	case SearchAppend:
		return "search-append"
	case Dispatcher:
		return "dispatcher"
	case Hybrid:
		return "hybrid"
	default:
		return fmt.Sprintf("strategy(%d)", int(s))
	}
}

// Strategies lists every strategy in report order.
func Strategies() []Strategy { return []Strategy{AllUpfront, SearchAppend, Dispatcher, Hybrid} }

// SimConfig holds the assumptions of the simulation.
type SimConfig struct {
	// TopK is the number of search hits returned per search.
	TopK int
	// PromoteAfter is the use count at which Hybrid promotes a tool.
	PromoteAfter int
	// ResultBytes is the size of every tool result placed in history.
	ResultBytes int
	// SystemBytes is the size of the system prompt.
	SystemBytes int
	// CacheWriteCost and CacheReadCost are multipliers on the base input
	// price for bytes written to and read from the prefix cache. Defaults
	// follow Anthropic's published ratios (1.25x write, 0.1x read) for the
	// 5-minute cache; adjust them for other providers.
	CacheWriteCost float64
	CacheReadCost  float64
}

// DefaultSimConfig returns the assumptions used by the reports.
func DefaultSimConfig() SimConfig {
	return SimConfig{
		TopK:           5, // matches tool_search's default max_results
		PromoteAfter:   2,
		ResultBytes:    1500,
		SystemBytes:    2000,
		CacheWriteCost: 1.25,
		CacheReadCost:  0.10,
	}
}

func (c SimConfig) validate() error {
	if c.TopK <= 0 || c.PromoteAfter <= 0 || c.ResultBytes < 0 || c.SystemBytes < 0 {
		return fmt.Errorf("toolbench: invalid SimConfig %+v", c)
	}
	return nil
}

// SimResult is the outcome of simulating one strategy over a session.
type SimResult struct {
	Strategy    Strategy
	Turns       int
	Requests    int // provider calls, including search round trips
	SearchCalls int
	Failed      int // turns whose search did not return the needed tool
	Promotions  int // Hybrid only
	// TotalBytes is the sum of request sizes: what a provider without prefix
	// caching would bill as input.
	TotalBytes int
	// ReusedBytes is the part of each request that matches the previous
	// request's prefix; WrittenBytes is the rest.
	ReusedBytes  int
	WrittenBytes int
	// CacheBreaks counts requests whose prefix was shorter than the previous
	// request, i.e. content that was cached before had to be re-sent.
	CacheBreaks int
	// BytesLost is the total size of the previous requests' tails that a
	// break forced to be rewritten.
	BytesLost int
	// FirstToolsBytes is the size of the tools array on the first request.
	FirstToolsBytes int
	// RelCost is 1.0*unit for a request with no caching; here it is
	// CacheWriteCost*WrittenBytes + CacheReadCost*ReusedBytes, in bytes.
	RelCost float64
}

// UncachedCost is the cost of the same traffic with no prefix caching.
func (r SimResult) UncachedCost() float64 { return float64(r.TotalBytes) }

// request is one provider call, split into ordered segments. Prefix caching
// operates on the concatenation tools, system, messages, in that order.
type request struct {
	tools    []string
	system   string
	messages []string
}

func (r request) segments() []string {
	segs := make([]string, 0, len(r.tools)+1+len(r.messages))
	segs = append(segs, r.tools...)
	segs = append(segs, "SYSTEM:"+r.system)
	segs = append(segs, r.messages...)
	return segs
}

// prefixBytes returns the number of leading bytes shared by a and b, counted
// in whole segments: a segment only counts if it and everything before it
// are identical.
func prefixBytes(a, b []string) int {
	n := 0
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			break
		}
		n += len(a[i])
	}
	return n
}

func segBytes(segs []string) int {
	n := 0
	for _, s := range segs {
		n += len(s)
	}
	return n
}

var (
	coreToolDefs = []string{
		marshalDef("core_read_file", "Read a file from the workspace.", shuttle.NewObjectSchema("Parameters", map[string]*shuttle.JSONSchema{"path": shuttle.NewStringSchema("File path")}, []string{"path"})),
		marshalDef("core_write_file", "Write a file in the workspace.", shuttle.NewObjectSchema("Parameters", map[string]*shuttle.JSONSchema{"path": shuttle.NewStringSchema("File path"), "content": shuttle.NewStringSchema("File content")}, []string{"path", "content"})),
		marshalDef("core_run_command", "Run a shell command in the workspace.", shuttle.NewObjectSchema("Parameters", map[string]*shuttle.JSONSchema{"command": shuttle.NewStringSchema("Command line")}, []string{"command"})),
	}
	toolSearchDef = marshalDef("tool_search", "Search for available tools by natural-language query.", shuttle.NewObjectSchema("Parameters", map[string]*shuttle.JSONSchema{"query": shuttle.NewStringSchema("What you want to do"), "max_results": shuttle.NewNumberSchema("Maximum results")}, []string{"query"}))
	callToolDef   = marshalDef("call_tool", "Call a tool found with tool_search by name.", shuttle.NewObjectSchema("Parameters", map[string]*shuttle.JSONSchema{"name": shuttle.NewStringSchema("Tool name"), "arguments": shuttle.NewObjectSchema("Tool arguments", nil, nil)}, []string{"name", "arguments"}))
)

// Simulate replays session under strategy and returns prefix-cache metrics.
//
// Model assumptions (all deliberate simplifications):
//   - Each request is the concatenation tools, system, messages; the provider
//     reuses the longest prefix shared with the immediately preceding request
//     and breakpoints sit at the end of the request (ideal placement).
//   - A turn that needs an unavailable tool costs one extra request for the
//     search; a search that misses the needed tool fails the turn.
//   - Tool results, the system prompt and assistant messages have fixed sizes.
//   - Cache TTL and the provider's limit on cache breakpoints are ignored.
func Simulate(ctx context.Context, strategy Strategy, entries []Entry, session []Task, searcher Searcher, cfg SimConfig) (SimResult, error) {
	if err := cfg.validate(); err != nil {
		return SimResult{}, err
	}
	if strategy != AllUpfront && searcher == nil {
		return SimResult{}, fmt.Errorf("toolbench: %s needs a Searcher", strategy)
	}
	byName := make(map[string]Entry, len(entries))
	for _, e := range entries {
		byName[e.Name] = e
	}

	res := SimResult{Strategy: strategy, Turns: len(session)}
	system := strings.Repeat("s", cfg.SystemBytes)
	payload := strings.Repeat("r", cfg.ResultBytes)

	var tools []string           // tools array contents, in order
	inTools := map[string]bool{} // tool names present in the tools array
	known := map[string]bool{}   // tools whose schema is in history (dispatcher)
	uses := map[string]int{}
	var msgs []string
	var prev []string

	switch strategy {
	case AllUpfront:
		tools = append(tools, coreToolDefs...)
		for _, e := range entries {
			tools = append(tools, e.Def())
			inTools[e.Name] = true
		}
	case SearchAppend:
		tools = append(append(tools, coreToolDefs...), toolSearchDef)
	case Dispatcher, Hybrid:
		tools = append(append(tools, coreToolDefs...), toolSearchDef, callToolDef)
	}

	send := func() {
		req := request{tools: tools, system: system, messages: msgs}
		cur := req.segments()
		total := segBytes(cur)
		res.Requests++
		res.TotalBytes += total
		if prev == nil {
			res.FirstToolsBytes = segBytes(tools)
			res.WrittenBytes += total
		} else {
			reused := prefixBytes(prev, cur)
			res.ReusedBytes += reused
			res.WrittenBytes += total - reused
			if prevTotal := segBytes(prev); reused < prevTotal {
				res.CacheBreaks++
				res.BytesLost += prevTotal - reused
			}
		}
		// Copy so later appends to tools/msgs do not alias the stored request.
		prev = append([]string(nil), cur...)
	}

	available := func(name string) bool {
		if strategy == AllUpfront || strategy == SearchAppend {
			return inTools[name]
		}
		return known[name]
	}

	for _, task := range session {
		msgs = append(msgs, "user:"+task.Query)

		if !available(task.Expected) {
			send() // model decides to search
			hits, _, err := searcher.Search(ctx, task.Query, cfg.TopK)
			if err != nil {
				return SimResult{}, fmt.Errorf("toolbench: search %q: %w", task.Query, err)
			}
			res.SearchCalls++
			msgs = append(msgs, "assistant:tool_use tool_search "+task.Query)

			var result strings.Builder
			for _, h := range hits {
				e, ok := byName[h]
				if !ok {
					continue
				}
				switch strategy {
				case SearchAppend:
					result.WriteString(e.Name + ": " + e.Description + "\n")
					if !inTools[e.Name] {
						tools = append(tools, e.Def())
						inTools[e.Name] = true
					}
				default:
					result.WriteString(e.Def() + "\n")
					known[e.Name] = true
				}
			}
			msgs = append(msgs, "tool_result:"+result.String())

			send() // model reads the result
			if !available(task.Expected) {
				msgs = append(msgs, "assistant:no matching tool")
				res.Failed++
				continue
			}
		} else {
			send() // model calls the tool it already has
		}

		call := task.Expected
		if strategy == Dispatcher || (strategy == Hybrid && !inTools[task.Expected]) {
			call = "call_tool " + task.Expected
		}
		msgs = append(msgs, "assistant:tool_use "+call)
		msgs = append(msgs, "tool_result:"+payload)
		send() // model writes the final answer
		msgs = append(msgs, "assistant:done")

		uses[task.Expected]++
		if strategy == Hybrid && !inTools[task.Expected] && uses[task.Expected] >= cfg.PromoteAfter {
			tools = append(tools, byName[task.Expected].Def())
			inTools[task.Expected] = true
			res.Promotions++
		}
	}

	res.RelCost = cfg.CacheWriteCost*float64(res.WrittenBytes) + cfg.CacheReadCost*float64(res.ReusedBytes)
	return res, nil
}

// FormatSimResults renders results as an aligned table. Cost columns are in
// bytes, relative to sending every request uncached (100%). Bytes are a proxy
// for tokens (about 4 bytes per token for English and JSON).
func FormatSimResults(results []SimResult) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "strategy\trequests\tsearches\tfailed\tpromoted\ttools@first\ttotal KB\tcache breaks\tKB lost\tcached cost vs uncached")
	for _, r := range results {
		_, _ = fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%.1f KB\t%.0f\t%d\t%.1f\t%.0f%%\n",
			r.Strategy, r.Requests, r.SearchCalls, r.Failed, r.Promotions,
			float64(r.FirstToolsBytes)/1024, float64(r.TotalBytes)/1024,
			r.CacheBreaks, float64(r.BytesLost)/1024, 100*r.RelCost/r.UncachedCost())
	}
	_ = w.Flush()
	return b.String()
}
