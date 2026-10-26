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

// Package spending keeps a server's AI spending under a monthly limit. A
// Ledger adds up the estimated cost of every LLM call (llmtypes.Usage.CostUSD)
// for the current calendar month and saves it to a file; Wrap puts a provider
// behind it. Once the month's total reaches the limit, new turns are refused
// (Admit) and calls outside an admitted turn fail with a *LimitError, until
// the month changes or the limit is raised. Providers that cost nothing (see
// IsFree), such as a model on the server's own computer, are never refused.
// With a fallback (SetFallback), refused calls are answered by it instead, so
// the server keeps answering past the limit at no cost.
//
// A turn admitted while under the limit runs to the end, so the total can
// pass the limit by the cost of the turns in progress when it was reached.
// Costs are the providers' estimates from published prices, not the
// provider's bill.
package spending

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	llmtypes "github.com/teradata-labs/loom/pkg/llm/types"
)

// ErrLimitReached is matched (errors.Is) by every *LimitError.
var ErrLimitReached = errors.New("monthly spending limit reached")

// LimitError reports that this month's spending reached the limit.
type LimitError struct {
	// Month is the calendar month, "2006-01", in the server's time zone.
	Month    string
	SpentUSD float64
	LimitUSD float64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("this month's AI spending limit of $%.2f has been reached ($%.2f spent in %s); it resets next month or when the limit is raised",
		e.LimitUSD, e.SpentUSD, e.Month)
}

// Is makes errors.Is(err, ErrLimitReached) true.
func (e *LimitError) Is(target error) bool { return target == ErrLimitReached }

// Status is a ledger's state, as saved in its file.
type Status struct {
	// Month is the calendar month the total covers, "2006-01".
	Month string `json:"month"`
	// SpentUSD is the estimated cost of this month's calls.
	SpentUSD float64 `json:"spent_usd"`
	// LimitUSD is the monthly limit; 0 means no limit.
	LimitUSD float64 `json:"limit_usd"`
	// Reached is true when there is a limit and SpentUSD is at or over it.
	Reached bool `json:"reached"`
	// FallingBack is true when Reached and a fallback answers the calls the
	// limit refuses (SetFallback).
	FallingBack bool `json:"falling_back"`
}

// Ledger adds up this month's spending. Safe for concurrent use.
type Ledger struct {
	mu    sync.Mutex
	path  string
	limit float64
	now   func() time.Time
	month string
	spent float64
	// fallback answers calls refused over the limit; nil means none.
	fallback llmtypes.LLMProvider
}

// Open loads the ledger saved at path (a missing file starts at zero) with
// the given monthly limit in US dollars; limitUSD <= 0 means no limit (the
// total is still kept). A total saved for an earlier month starts over.
func Open(path string, limitUSD float64) (*Ledger, error) {
	return open(path, limitUSD, time.Now)
}

func open(path string, limitUSD float64, now func() time.Time) (*Ledger, error) {
	if limitUSD < 0 {
		limitUSD = 0
	}
	l := &Ledger{path: path, limit: limitUSD, now: now, month: monthOf(now())}
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from server configuration
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("read spending ledger: %w", err)
	default:
		var saved Status
		if err := json.Unmarshal(data, &saved); err != nil {
			return nil, fmt.Errorf("read spending ledger %s: %w", path, err)
		}
		if saved.Month == l.month && saved.SpentUSD > 0 {
			l.spent = saved.SpentUSD
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.saveLocked(); err != nil {
		return nil, err
	}
	return l, nil
}

func monthOf(t time.Time) string { return t.Format("2006-01") }

// rollLocked starts a new total when the month has changed.
func (l *Ledger) rollLocked() {
	if m := monthOf(l.now()); m != l.month {
		l.month = m
		l.spent = 0
	}
}

func (l *Ledger) statusLocked() Status {
	reached := l.limit > 0 && l.spent >= l.limit
	return Status{
		Month:       l.month,
		SpentUSD:    l.spent,
		LimitUSD:    l.limit,
		Reached:     reached,
		FallingBack: reached && l.fallback != nil,
	}
}

// ErrFallbackNotFree is returned by SetFallback for a provider that is not
// free (IsFree): a paid fallback would spend past the limit.
var ErrFallbackNotFree = errors.New("the spending fallback must be a provider that costs nothing")

// SetFallback makes p answer the calls the limit refuses, so turns keep
// running past the limit instead of being refused. p must cost nothing
// (IsFree), or ErrFallbackNotFree is returned. nil removes the fallback.
// The new status is saved.
func (l *Ledger) SetFallback(p llmtypes.LLMProvider) error {
	if p != nil && !IsFree(p) {
		return ErrFallbackNotFree
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fallback = p
	return l.saveLocked()
}

// fallbackProvider returns the fallback, or nil.
func (l *Ledger) fallbackProvider() llmtypes.LLMProvider {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fallback
}

// Status returns this month's total and limit.
func (l *Ledger) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollLocked()
	return l.statusLocked()
}

// Record adds the cost of one call. Negative and zero costs are ignored. The
// new total is saved; a failed save keeps the total in memory and is
// returned.
func (l *Ledger) Record(costUSD float64) error {
	if costUSD <= 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollLocked()
	l.spent += costUSD
	return l.saveLocked()
}

// check returns a *LimitError when the limit is reached.
func (l *Ledger) check() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollLocked()
	if st := l.statusLocked(); st.Reached {
		return &LimitError{Month: st.Month, SpentUSD: st.SpentUSD, LimitUSD: st.LimitUSD}
	}
	return nil
}

// Admit starts a turn: it returns a *LimitError when the limit is reached,
// and otherwise a context whose LLM calls run even if the limit is reached
// during the turn, so an answer is not cut off halfway. With a fallback, a
// turn over the limit is not refused: ctx is returned as it is, so the
// turn's calls go to the fallback.
func (l *Ledger) Admit(ctx context.Context) (context.Context, error) {
	if l == nil {
		return ctx, nil
	}
	if err := l.check(); err != nil {
		if l.fallbackProvider() != nil {
			return ctx, nil
		}
		return ctx, err
	}
	return context.WithValue(ctx, admittedKey{}, true), nil
}

// allow reports whether a call with ctx may run.
func (l *Ledger) allow(ctx context.Context) error {
	if admitted, _ := ctx.Value(admittedKey{}).(bool); admitted {
		return nil
	}
	return l.check()
}

type admittedKey struct{}

// saveLocked writes the status to path atomically, readable only by the
// owner.
func (l *Ledger) saveLocked() error {
	data, err := json.MarshalIndent(l.statusLocked(), "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("save spending ledger: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".spending-*.json")
	if err != nil {
		return fmt.Errorf("save spending ledger: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save spending ledger: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save spending ledger: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save spending ledger: %w", err)
	}
	if err := os.Rename(tmp.Name(), l.path); err != nil {
		return fmt.Errorf("save spending ledger: %w", err)
	}
	return nil
}
