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

package spending

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmtypes "github.com/teradata-labs/loom/pkg/llm/types"
	"github.com/teradata-labs/loom/pkg/shuttle"
)

// fakeProvider answers every call with a fixed cost.
type fakeProvider struct {
	mu    sync.Mutex
	cost  float64
	calls int
}

func (f *fakeProvider) Name() string  { return "fake" }
func (f *fakeProvider) Model() string { return "fake-model" }
func (f *fakeProvider) Chat(context.Context, []llmtypes.Message, []shuttle.Tool) (*llmtypes.LLMResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return &llmtypes.LLMResponse{Content: "ok", Usage: llmtypes.Usage{CostUSD: f.cost}}, nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func TestLedgerCountsAndSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spending.json")
	c := &clock{time.Date(2026, 10, 19, 9, 0, 0, 0, time.Local)}
	l, err := open(path, 1.0, c.now)
	require.NoError(t, err)
	require.NoError(t, l.Record(0.25))
	require.NoError(t, l.Record(0.25))
	require.NoError(t, l.Record(-3)) // ignored

	st := l.Status()
	assert.Equal(t, Status{Month: "2026-10", SpentUSD: 0.5, LimitUSD: 1.0}, st)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var saved Status
	require.NoError(t, json.Unmarshal(data, &saved))
	assert.Equal(t, st, saved)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// Reopening keeps this month's total, with the new limit.
	l2, err := open(path, 2.0, c.now)
	require.NoError(t, err)
	assert.Equal(t, Status{Month: "2026-10", SpentUSD: 0.5, LimitUSD: 2.0}, l2.Status())
}

func TestLedgerStartsOverEachMonth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spending.json")
	c := &clock{time.Date(2026, 10, 31, 23, 0, 0, 0, time.Local)}
	l, err := open(path, 1.0, c.now)
	require.NoError(t, err)
	require.NoError(t, l.Record(1.5))
	assert.True(t, l.Status().Reached)

	c.t = time.Date(2026, 11, 1, 0, 5, 0, 0, time.Local)
	assert.Equal(t, Status{Month: "2026-11", LimitUSD: 1.0}, l.Status())

	// A file saved last month starts over too.
	c.t = time.Date(2026, 10, 31, 23, 0, 0, 0, time.Local)
	old, err := open(path, 1.0, c.now)
	require.NoError(t, err)
	require.NoError(t, old.Record(0.9))
	c.t = time.Date(2026, 12, 2, 8, 0, 0, 0, time.Local)
	reopened, err := open(path, 1.0, c.now)
	require.NoError(t, err)
	assert.Zero(t, reopened.Status().SpentUSD)
}

func TestNoLimitKeepsTotal(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "spending.json"), 0)
	require.NoError(t, err)
	require.NoError(t, l.Record(100))
	assert.False(t, l.Status().Reached)
	_, err = l.Admit(context.Background())
	assert.NoError(t, err)
}

func TestProviderStopsAtLimitButFinishesAdmittedTurns(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "spending.json"), 1.0)
	require.NoError(t, err)
	inner := &fakeProvider{cost: 0.6}
	p := Wrap(inner, l)

	turn, err := l.Admit(context.Background())
	require.NoError(t, err)
	_, err = p.Chat(turn, nil, nil) // 0.6
	require.NoError(t, err)
	_, err = p.Chat(turn, nil, nil) // 1.2: the admitted turn goes on
	require.NoError(t, err)
	_, err = p.(llmtypes.StreamingLLMProvider).ChatStream(turn, nil, nil, nil) // 1.8, through Chat
	require.NoError(t, err)
	assert.InDelta(t, 1.8, l.Status().SpentUSD, 1e-9)

	// A new turn, and calls outside any turn, are refused.
	_, err = l.Admit(context.Background())
	var limitErr *LimitError
	require.ErrorAs(t, err, &limitErr)
	assert.True(t, errors.Is(err, ErrLimitReached))
	assert.Equal(t, 1.0, limitErr.LimitUSD)
	_, err = p.Chat(context.Background(), nil, nil)
	assert.ErrorIs(t, err, ErrLimitReached)
	assert.Equal(t, 3, inner.calls, "a refused call never reaches the provider")

	// Health checks are not limited.
	assert.NoError(t, p.(llmtypes.HealthChecker).HealthCheck(context.Background()))
}

func TestWrapIsIdempotent(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "spending.json"), 0)
	require.NoError(t, err)
	inner := &fakeProvider{cost: 0.5}
	once := Wrap(inner, l)
	twice := Wrap(once, l)
	assert.Same(t, once, twice)

	// A ledger under another wrapper is found through Unwrap.
	outer := &passthrough{inner: once}
	assert.Same(t, outer, Wrap(outer, l))
	_, err = Wrap(outer, l).Chat(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 0.5, l.Status().SpentUSD, "counted once")

	assert.Equal(t, llmtypes.LLMProvider(inner), Wrap(inner, nil))
}

type passthrough struct{ inner llmtypes.LLMProvider }

func (p *passthrough) Name() string                 { return p.inner.Name() }
func (p *passthrough) Model() string                { return p.inner.Model() }
func (p *passthrough) Unwrap() llmtypes.LLMProvider { return p.inner }
func (p *passthrough) Chat(ctx context.Context, m []llmtypes.Message, t []shuttle.Tool) (*llmtypes.LLMResponse, error) {
	return p.inner.Chat(ctx, m, t)
}

func TestConcurrentRecords(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "spending.json"), 0)
	require.NoError(t, err)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, l.Record(0.01))
		}()
	}
	wg.Wait()
	assert.InDelta(t, 0.5, l.Status().SpentUSD, 1e-9)
}

// freeProvider costs nothing to call, like a local Ollama model.
type freeProvider struct{ fakeProvider }

func (*freeProvider) CostsNothing() bool { return true }

// Over the limit a provider that costs nothing still answers, outside an
// admitted turn too; a paid one is refused.
func TestFreeProviderRunsOverLimit(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "spending.json"), 1.0)
	require.NoError(t, err)
	require.NoError(t, l.Record(1.0))

	free := &freeProvider{}
	assert.True(t, IsFree(free))
	_, err = Wrap(free, l).Chat(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, free.calls)

	paid := &fakeProvider{cost: 0.1}
	assert.False(t, IsFree(paid))
	_, err = Wrap(paid, l).Chat(context.Background(), nil, nil)
	assert.ErrorIs(t, err, ErrLimitReached)
	assert.Zero(t, paid.calls)
}

// With a fallback, over the limit turns are not refused and paid calls are
// answered by the fallback; under the limit the paid provider answers.
func TestFallbackAnswersOverLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spending.json")
	l, err := Open(path, 1.0)
	require.NoError(t, err)
	paid := &fakeProvider{cost: 0.6}
	assert.ErrorIs(t, l.SetFallback(paid), ErrFallbackNotFree, "a paid fallback would spend past the limit")
	free := &freeProvider{}
	require.NoError(t, l.SetFallback(free))
	p := Wrap(paid, l)

	ctx, err := l.Admit(context.Background())
	require.NoError(t, err)
	_, err = p.Chat(ctx, nil, nil)
	require.NoError(t, err)
	_, err = p.(llmtypes.StreamingLLMProvider).ChatStream(ctx, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, paid.calls, "under the limit the paid provider answers")
	assert.True(t, l.Status().Reached)
	assert.True(t, l.Status().FallingBack)

	ctx, err = l.Admit(context.Background())
	require.NoError(t, err, "over the limit with a fallback the turn runs")
	_, err = p.Chat(ctx, nil, nil)
	require.NoError(t, err)
	_, err = p.(llmtypes.StreamingLLMProvider).ChatStream(ctx, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, paid.calls, "over the limit the paid provider is not called")
	assert.Equal(t, 2, free.calls, "the fallback answers")
	assert.InDelta(t, 1.2, l.Status().SpentUSD, 1e-9)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var saved Status
	require.NoError(t, json.Unmarshal(data, &saved))
	assert.True(t, saved.FallingBack, "the file says the fallback is answering")

	require.NoError(t, l.SetFallback(nil))
	_, err = l.Admit(context.Background())
	assert.ErrorIs(t, err, ErrLimitReached, "without the fallback the limit stops turns again")
}
