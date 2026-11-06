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
	"encoding/json"
	"fmt"
	"sort"
	"time"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/tools/registry"
)

// Searcher resolves a query to ranked tool names.
type Searcher interface {
	// Search returns up to k tool names, best first, and the wall time spent.
	Search(ctx context.Context, query string, k int) ([]string, time.Duration, error)
}

// RegistrySearcher runs queries against the real tool registry in FAST mode
// (FTS5/BM25 only, no LLM), so it measures the retrieval stage that every
// search mode shares.
type RegistrySearcher struct {
	reg *registry.Registry
}

// NewRegistrySearcher indexes entries into a fresh registry database at dbPath.
func NewRegistrySearcher(ctx context.Context, dbPath string, entries []Entry) (*RegistrySearcher, error) {
	reg, err := registry.New(registry.Config{DBPath: dbPath})
	if err != nil {
		return nil, fmt.Errorf("toolbench: open registry: %w", err)
	}
	now := time.Now().Format(time.RFC3339)
	for _, e := range entries {
		schema, err := json.Marshal(e.Schema)
		if err != nil {
			_ = reg.Close()
			return nil, fmt.Errorf("toolbench: marshal schema for %s: %w", e.Name, err)
		}
		if err := reg.RegisterTool(ctx, &loomv1.IndexedTool{
			Id:           fmt.Sprintf("mcp:%s:%s", e.Domain, e.Name),
			Name:         e.Name,
			Description:  e.Description,
			Source:       loomv1.ToolSource_TOOL_SOURCE_MCP,
			McpServer:    e.Domain,
			InputSchema:  string(schema),
			IndexedAt:    now,
			Capabilities: e.Capabilities,
			Keywords:     e.Keywords,
		}); err != nil {
			_ = reg.Close()
			return nil, fmt.Errorf("toolbench: register %s: %w", e.Name, err)
		}
	}
	return &RegistrySearcher{reg: reg}, nil
}

// Close releases the registry database.
func (s *RegistrySearcher) Close() error { return s.reg.Close() }

// Search implements Searcher.
func (s *RegistrySearcher) Search(ctx context.Context, query string, k int) ([]string, time.Duration, error) {
	start := time.Now()
	resp, err := s.reg.Search(ctx, &loomv1.SearchToolsRequest{
		Query:      query,
		Mode:       loomv1.SearchMode_SEARCH_MODE_FAST,
		MaxResults: int32(k), // #nosec G115 -- k is a small benchmark parameter
	})
	elapsed := time.Since(start)
	if err != nil {
		return nil, elapsed, err
	}
	names := make([]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		names = append(names, r.Tool.Name)
	}
	return names, elapsed, nil
}

// Retrieval summarizes search quality and latency over a task set.
type Retrieval struct {
	Tasks    int
	RecallAt map[int]float64 // k -> fraction of tasks whose expected tool is in the top k
	MRR      float64         // mean reciprocal rank over the top maxK results
	P50      time.Duration
	P95      time.Duration
}

// EvaluateRetrieval runs every task through s and reports recall@k for each
// requested k, mean reciprocal rank, and search latency percentiles.
func EvaluateRetrieval(ctx context.Context, s Searcher, tasks []Task, ks []int) (Retrieval, error) {
	if len(tasks) == 0 || len(ks) == 0 {
		return Retrieval{}, fmt.Errorf("toolbench: need at least one task and one k")
	}
	maxK := 0
	for _, k := range ks {
		if k > maxK {
			maxK = k
		}
	}

	hits := make(map[int]int, len(ks))
	var rrSum float64
	durations := make([]time.Duration, 0, len(tasks))
	for _, t := range tasks {
		names, d, err := s.Search(ctx, t.Query, maxK)
		if err != nil {
			return Retrieval{}, fmt.Errorf("toolbench: search %q: %w", t.Query, err)
		}
		durations = append(durations, d)
		rank := 0
		for i, n := range names {
			if n == t.Expected {
				rank = i + 1
				break
			}
		}
		if rank > 0 {
			rrSum += 1 / float64(rank)
			for _, k := range ks {
				if rank <= k {
					hits[k]++
				}
			}
		}
	}

	out := Retrieval{Tasks: len(tasks), RecallAt: make(map[int]float64, len(ks)), MRR: rrSum / float64(len(tasks))}
	for _, k := range ks {
		out.RecallAt[k] = float64(hits[k]) / float64(len(tasks))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	out.P50 = percentile(durations, 0.50)
	out.P95 = percentile(durations, 0.95)
	return out, nil
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}
