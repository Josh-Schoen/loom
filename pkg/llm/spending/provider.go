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

	llmtypes "github.com/teradata-labs/loom/pkg/llm/types"
	"github.com/teradata-labs/loom/pkg/shuttle"
)

// Provider puts an LLM provider behind a Ledger: each call is refused with a
// *LimitError when the limit is reached (unless its turn was admitted), and
// the cost of each call that returns a response is recorded.
type Provider struct {
	inner  llmtypes.LLMProvider
	ledger *Ledger
}

// unwrapper is implemented by provider wrappers (llm.InstrumentedProvider,
// Provider) so Wrap can tell whether a ledger is already in the chain.
type unwrapper interface {
	Unwrap() llmtypes.LLMProvider
}

// Wrap returns p behind ledger. It returns p unchanged when ledger or p is
// nil, or when p already has a ledger in its wrapper chain, so a provider
// shared by several agents is never counted twice.
func Wrap(p llmtypes.LLMProvider, ledger *Ledger) llmtypes.LLMProvider {
	if p == nil || ledger == nil {
		return p
	}
	for cur := p; cur != nil; {
		if _, ok := cur.(*Provider); ok {
			return p
		}
		u, ok := cur.(unwrapper)
		if !ok {
			break
		}
		cur = u.Unwrap()
	}
	return &Provider{inner: p, ledger: ledger}
}

// Name returns the wrapped provider's name.
func (p *Provider) Name() string { return p.inner.Name() }

// Model returns the wrapped provider's model.
func (p *Provider) Model() string { return p.inner.Model() }

// Unwrap returns the wrapped provider.
func (p *Provider) Unwrap() llmtypes.LLMProvider { return p.inner }

// Chat runs one call when the ledger allows it and records its cost.
func (p *Provider) Chat(ctx context.Context, messages []llmtypes.Message, tools []shuttle.Tool) (*llmtypes.LLMResponse, error) {
	if err := p.ledger.allow(ctx); err != nil {
		return nil, err
	}
	resp, err := p.inner.Chat(ctx, messages, tools)
	p.record(resp)
	return resp, err
}

// ChatStream streams when the wrapped provider can, and otherwise answers
// with Chat; either way the ledger is checked first and the cost recorded.
func (p *Provider) ChatStream(ctx context.Context, messages []llmtypes.Message, tools []shuttle.Tool, tokenCallback llmtypes.TokenCallback) (*llmtypes.LLMResponse, error) {
	if err := p.ledger.allow(ctx); err != nil {
		return nil, err
	}
	var (
		resp *llmtypes.LLMResponse
		err  error
	)
	if sp, ok := p.inner.(llmtypes.StreamingLLMProvider); ok {
		resp, err = sp.ChatStream(ctx, messages, tools, tokenCallback)
	} else {
		resp, err = p.inner.Chat(ctx, messages, tools)
	}
	p.record(resp)
	return resp, err
}

// HealthCheck checks the wrapped provider without the limit: a server over
// its limit is still healthy. A ping sent through Chat is still counted.
func (p *Provider) HealthCheck(ctx context.Context) error {
	if hc, ok := p.inner.(llmtypes.HealthChecker); ok {
		return hc.HealthCheck(ctx)
	}
	resp, err := p.inner.Chat(ctx, []llmtypes.Message{{Role: "user", Content: "ping"}}, nil)
	p.record(resp)
	return err
}

// SchedulerScope forwards the wrapped provider's scheduler scope (see
// llm.InstrumentedProvider.SchedulerScope).
func (p *Provider) SchedulerScope() string {
	if sp, ok := p.inner.(interface{ SchedulerScope() string }); ok {
		return sp.SchedulerScope()
	}
	return ""
}

func (p *Provider) record(resp *llmtypes.LLMResponse) {
	if resp != nil {
		// A failed save keeps the total in memory; the next save retries.
		_ = p.ledger.Record(resp.Usage.CostUSD)
	}
}

var (
	_ llmtypes.LLMProvider          = (*Provider)(nil)
	_ llmtypes.StreamingLLMProvider = (*Provider)(nil)
	_ llmtypes.HealthChecker        = (*Provider)(nil)
)
