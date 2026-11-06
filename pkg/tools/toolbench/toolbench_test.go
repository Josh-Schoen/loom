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
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "github.com/teradata-labs/loom/internal/sqlitedriver"
)

// fixedSearcher returns a scripted hit list per query so simulator behavior
// can be tested without the registry.
type fixedSearcher map[string][]string

func (f fixedSearcher) Search(_ context.Context, q string, k int) ([]string, time.Duration, error) {
	hits := f[q]
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, 0, nil
}

func newRegistrySearcher(t testing.TB, entries []Entry) *RegistrySearcher {
	t.Helper()
	s, err := NewRegistrySearcher(context.Background(), filepath.Join(t.TempDir(), "tools.db"), entries)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestPrefixBytes(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want int
	}{
		{"identical", []string{"aa", "bb"}, []string{"aa", "bb"}, 4},
		{"extended", []string{"aa"}, []string{"aa", "bb"}, 2},
		{"diverges in middle", []string{"aa", "bb", "cc"}, []string{"aa", "xx", "cc"}, 2},
		{"insert at front shifts everything", []string{"aa", "bb"}, []string{"zz", "aa", "bb"}, 0},
		{"empty", nil, []string{"aa"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, prefixBytes(tt.a, tt.b))
		})
	}
}

func TestGenerateCorpus(t *testing.T) {
	a, err := GenerateCorpus(CorpusConfig{Tools: 100, Seed: 1})
	require.NoError(t, err)
	b, err := GenerateCorpus(CorpusConfig{Tools: 100, Seed: 1})
	require.NoError(t, err)
	assert.Equal(t, a, b, "same seed must give the same corpus")

	names := map[string]bool{}
	for _, e := range a {
		assert.False(t, names[e.Name], "duplicate tool name %s", e.Name)
		names[e.Name] = true
		assert.NotEmpty(t, e.Description)
	}
	assert.Len(t, a, 100)

	_, err = GenerateCorpus(CorpusConfig{Tools: MaxCorpusTools() + 1})
	assert.Error(t, err)
	_, err = GenerateCorpus(CorpusConfig{Tools: 0})
	assert.Error(t, err)
}

func TestGenerateSession(t *testing.T) {
	entries, err := GenerateCorpus(CorpusConfig{Tools: 100, Seed: 1})
	require.NoError(t, err)

	s, err := GenerateSession(entries, SessionConfig{Turns: 200, HotTools: 4, HotShare: 0.8, Seed: 3})
	require.NoError(t, err)
	require.Len(t, s, 200)

	counts := map[string]int{}
	for _, task := range s {
		counts[task.Expected]++
	}
	assert.Less(t, len(counts), 100, "hot set should concentrate usage on fewer distinct tools")

	_, err = GenerateSession(entries, SessionConfig{Turns: 10, HotTools: 4, HotShare: 1.5})
	assert.Error(t, err)
}

func TestEvaluateRetrieval(t *testing.T) {
	entries, err := GenerateCorpus(CorpusConfig{Tools: 100, Seed: 1})
	require.NoError(t, err)
	tasks := GenerateTasks(entries, 200, 2)
	s := newRegistrySearcher(t, entries)

	got, err := EvaluateRetrieval(context.Background(), s, tasks, []int{1, 3, 5, 10})
	require.NoError(t, err)

	t.Logf("retrieval over %d tools, %d tasks: recall@1=%.2f @3=%.2f @5=%.2f @10=%.2f MRR=%.2f p50=%v p95=%v",
		len(entries), got.Tasks, got.RecallAt[1], got.RecallAt[3], got.RecallAt[5], got.RecallAt[10], got.MRR, got.P50, got.P95)

	// Recall can only grow with k.
	assert.LessOrEqual(t, got.RecallAt[1], got.RecallAt[3])
	assert.LessOrEqual(t, got.RecallAt[3], got.RecallAt[5])
	assert.LessOrEqual(t, got.RecallAt[5], got.RecallAt[10])
	assert.Greater(t, got.MRR, 0.0)
	assert.Greater(t, got.RecallAt[10], 0.0)
}

func TestSimulateInvariants(t *testing.T) {
	ctx := context.Background()
	entries, err := GenerateCorpus(CorpusConfig{Tools: 100, Seed: 1})
	require.NoError(t, err)
	session, err := GenerateSession(entries, SessionConfig{Turns: 30, HotTools: 4, HotShare: 0.7, Seed: 3})
	require.NoError(t, err)
	searcher := newRegistrySearcher(t, entries)
	cfg := DefaultSimConfig()

	results := map[Strategy]SimResult{}
	for _, s := range Strategies() {
		r, err := Simulate(ctx, s, entries, session, searcher, cfg)
		require.NoError(t, err)
		results[s] = r
		assert.Equal(t, r.TotalBytes, r.ReusedBytes+r.WrittenBytes, "%s: reused+written must equal total", s)
	}
	t.Logf("\n%s", FormatSimResults([]SimResult{results[AllUpfront], results[SearchAppend], results[Dispatcher], results[Hybrid]}))

	up := results[AllUpfront]
	assert.Equal(t, 2*len(session), up.Requests, "all-upfront: two requests per turn")
	assert.Zero(t, up.CacheBreaks, "all-upfront: tools never change")
	assert.Zero(t, up.Failed)
	assert.Zero(t, up.SearchCalls)

	disp := results[Dispatcher]
	assert.Zero(t, disp.CacheBreaks, "dispatcher: tools array never changes, so the prefix only grows")
	assert.Greater(t, disp.SearchCalls, 0)

	app := results[SearchAppend]
	assert.Greater(t, app.CacheBreaks, 0, "append: adding a tool mid-session breaks the cached prefix after it")
	assert.Greater(t, app.BytesLost, 0)

	// Deferred strategies must send far fewer tool bytes on the first request.
	for _, s := range []Strategy{SearchAppend, Dispatcher, Hybrid} {
		assert.Less(t, results[s].FirstToolsBytes, up.FirstToolsBytes/5, "%s: first-request tools size", s)
	}
}

func TestSimulateScriptedMiss(t *testing.T) {
	entries, err := GenerateCorpus(CorpusConfig{Tools: 20, Seed: 1})
	require.NoError(t, err)
	target := entries[0]
	session := []Task{{Query: "q1", Expected: target.Name}}

	// The search returns the wrong tool, so the turn fails after two requests.
	miss := fixedSearcher{"q1": {entries[1].Name}}
	r, err := Simulate(context.Background(), Dispatcher, entries, session, miss, DefaultSimConfig())
	require.NoError(t, err)
	assert.Equal(t, 1, r.Failed)
	assert.Equal(t, 2, r.Requests)

	// The search returns the right tool: search, read result, final answer.
	hit := fixedSearcher{"q1": {target.Name}}
	r, err = Simulate(context.Background(), Dispatcher, entries, session, hit, DefaultSimConfig())
	require.NoError(t, err)
	assert.Zero(t, r.Failed)
	assert.Equal(t, 3, r.Requests)
	assert.Equal(t, 1, r.SearchCalls)
}

func TestSimulateHybridPromotes(t *testing.T) {
	entries, err := GenerateCorpus(CorpusConfig{Tools: 20, Seed: 1})
	require.NoError(t, err)
	target := entries[0]
	session := []Task{
		{Query: "q1", Expected: target.Name},
		{Query: "q1", Expected: target.Name},
		{Query: "q1", Expected: target.Name},
	}
	searcher := fixedSearcher{"q1": {target.Name}}
	cfg := DefaultSimConfig()
	cfg.PromoteAfter = 2

	r, err := Simulate(context.Background(), Hybrid, entries, session, searcher, cfg)
	require.NoError(t, err)
	assert.Equal(t, 1, r.Promotions)
	assert.Equal(t, 1, r.SearchCalls, "later turns reuse the discovered tool without searching")
	assert.Equal(t, 1, r.CacheBreaks, "promotion inserts into the tools array exactly once")
}

func TestSimulateValidation(t *testing.T) {
	entries, err := GenerateCorpus(CorpusConfig{Tools: 10, Seed: 1})
	require.NoError(t, err)
	_, err = Simulate(context.Background(), Dispatcher, entries, nil, nil, DefaultSimConfig())
	assert.Error(t, err, "search strategies need a searcher")

	bad := DefaultSimConfig()
	bad.TopK = 0
	_, err = Simulate(context.Background(), AllUpfront, entries, nil, nil, bad)
	assert.Error(t, err)
}

// BenchmarkSearchFast measures FTS5/BM25 retrieval latency as the tool count
// grows. This is the floor under every search mode; balanced and accurate add
// LLM calls on top.
func BenchmarkSearchFast(b *testing.B) {
	for _, n := range []int{50, 100, 200, 400} {
		b.Run(fmt.Sprintf("tools=%d", n), func(b *testing.B) {
			entries, err := GenerateCorpus(CorpusConfig{Tools: n, Seed: 1})
			require.NoError(b, err)
			tasks := GenerateTasks(entries, 64, 2)
			s := newRegistrySearcher(b, entries)
			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := s.Search(ctx, tasks[i%len(tasks)].Query, 5); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSimulate measures the cost of the simulator itself.
func BenchmarkSimulate(b *testing.B) {
	entries, err := GenerateCorpus(CorpusConfig{Tools: 200, Seed: 1})
	require.NoError(b, err)
	session, err := GenerateSession(entries, SessionConfig{Turns: 30, HotTools: 4, HotShare: 0.7, Seed: 3})
	require.NoError(b, err)
	s := newRegistrySearcher(b, entries)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Simulate(ctx, Hybrid, entries, session, s, DefaultSimConfig()); err != nil {
			b.Fatal(err)
		}
	}
}

// TestReportScaling logs how each strategy compares as the corpus grows. Run
// with -v to see the tables.
func TestReportScaling(t *testing.T) {
	ctx := context.Background()
	for _, n := range []int{50, 100, 200, 400} {
		entries, err := GenerateCorpus(CorpusConfig{Tools: n, Seed: 1})
		require.NoError(t, err)
		session, err := GenerateSession(entries, SessionConfig{Turns: 30, HotTools: 4, HotShare: 0.7, Seed: 3})
		require.NoError(t, err)
		searcher := newRegistrySearcher(t, entries)

		ret, err := EvaluateRetrieval(ctx, searcher, GenerateTasks(entries, 300, 2), []int{1, 3, 5, 10})
		require.NoError(t, err)
		t.Logf("tools=%d retrieval: recall@1=%.2f @3=%.2f @5=%.2f @10=%.2f MRR=%.2f p50=%v p95=%v",
			n, ret.RecallAt[1], ret.RecallAt[3], ret.RecallAt[5], ret.RecallAt[10], ret.MRR, ret.P50, ret.P95)

		var results []SimResult
		for _, s := range Strategies() {
			r, err := Simulate(ctx, s, entries, session, searcher, DefaultSimConfig())
			require.NoError(t, err)
			results = append(results, r)
		}
		t.Logf("tools=%d turns=%d\n%s", n, len(session), FormatSimResults(results))
	}
}
