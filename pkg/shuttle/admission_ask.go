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
package shuttle

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// summaryMaxLen bounds the rune length of an approval summary digest so display
// carriers and store columns never receive an unbounded string.
const summaryMaxLen = 200

// paramsMaxBytes bounds the JSON-encoded size of the parameter map carried with
// an approval, so store columns and stream events never receive an unbounded
// payload.
const paramsMaxBytes = 8192

// summarizeCall renders a deterministic one-line display digest for a held tool
// call as "<toolName> <digest>". The digest prefers a non-empty "stmt" param
// (the common statement carrier) and otherwise joins params as key=value pairs
// sorted by key for stability, bounded to summaryMaxLen runes. Display text
// only — it never influences the admission decision.
func summarizeCall(toolName string, params map[string]interface{}) string {
	digest := digestParams(params)
	if digest == "" {
		return toolName
	}
	return truncateRunes(toolName+" "+digest, summaryMaxLen)
}

// digestParams renders params to a single stable line: the "stmt" value when
// present, else a key-sorted key=value join. Empty for no params.
func digestParams(params map[string]interface{}) string {
	if len(params) == 0 {
		return ""
	}
	if stmt, ok := params["stmt"].(string); ok && stmt != "" {
		return stmt
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, params[k]))
	}
	return strings.Join(parts, " ")
}

// boundParams caps the JSON-encoded size of params at paramsMaxBytes and
// reports whether anything was cut. A map that encodes within the bound is
// returned whole. Otherwise pairs are visited in key-sorted order and each is
// kept only if the result still encodes within the bound, so the retained map
// is always whole pairs — never a cut JSON value — and is deterministic for a
// given input. A pair that cannot fit (or cannot be encoded at all) is elided
// entirely and sets the flag.
func boundParams(params map[string]interface{}) (map[string]interface{}, bool) {
	if len(params) == 0 {
		return params, false
	}
	if encoded, err := json.Marshal(params); err == nil && len(encoded) <= paramsMaxBytes {
		return params, false
	}

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	kept := make(map[string]interface{}, len(params))
	truncated := false
	for _, k := range keys {
		kept[k] = params[k]
		encoded, err := json.Marshal(kept)
		if err != nil || len(encoded) > paramsMaxBytes {
			delete(kept, k)
			truncated = true
		}
	}
	return kept, truncated
}

// truncateRunes caps s to max runes, cutting on a rune boundary.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// hitlAskResolver turns the chain's Ask verdict into a terminal Allow/Deny by
// raising a human approval request and blocking on the HITL store until a
// separate actor resolves it. It implements AskResolver. A separate actor
// resolves via RespondToRequest — the harness raises the request, the human
// decides; the model cannot self-approve.
type hitlAskResolver struct {
	store    HumanRequestStore
	timeout  time.Duration // how long to block before failing closed
	poll     time.Duration // store poll interval
	notifier Notifier      // pending-emit collaborator; nil disables the emit
}

// NewHITLAskResolver builds the Ask resolver wired into ChainDeps.Ask. timeout
// bounds the turn-blocking wait (default 300s when non-positive); poll is the
// store poll interval (default 1s, mirroring ContactHumanConfig). notifier is
// fired once the pending request is stored so the hold surfaces on the progress
// stream; a nil notifier disables the emit (the hold is unaffected).
func NewHITLAskResolver(store HumanRequestStore, timeout, poll time.Duration, notifier Notifier) AskResolver {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	if poll <= 0 {
		poll = 1 * time.Second
	}
	return &hitlAskResolver{store: store, timeout: timeout, poll: poll, notifier: notifier}
}

// Resolve creates a pending "approval" HumanRequest scoped to the call's session
// and user, then blocks polling the store until the request leaves "pending" or
// the timeout elapses. "approved" admits the call (Allow); "rejected", "timeout",
// a deadline, a store failure, or context cancellation all Deny (fail-closed).
// The block is synchronous within the agent turn, bounded by timeout.
func (r *hitlAskResolver) Resolve(req AdmissionRequest, d Decision) Decision {
	if r == nil || r.store == nil {
		return Decision{Kind: Deny, Reason: "approval required but no HITL store is configured"}
	}

	ctx := req.Ctx
	if ctx == nil {
		return Decision{Kind: Deny, Reason: "approval required but request has no context"}
	}

	now := time.Now()
	hr := &HumanRequest{
		ID:        uuid.New().String(),
		SessionID: req.SessionID,
		Question:  fmt.Sprintf("Approve tool call %q?", req.ToolName),
		Context: map[string]interface{}{
			"tool":    req.ToolName,
			"user_id": req.UserID,
			"reason":  d.Reason,
			// Origin discriminator, duplicated from Kind: the kind/summary
			// columns are droppable by a migration rollback, context_json is
			// not — so a restored row remains recognisably an approval instead
			// of silently re-reading as a question.
			"kind": "approval",
		},
		RequestType: "approval",
		Priority:    "normal",
		Kind:        "approval",
		Summary:     summarizeCall(req.ToolName, req.Params),
		Timeout:     r.timeout,
		CreatedAt:   now,
		ExpiresAt:   now.Add(r.timeout),
		Status:      "pending",
	}
	// The held call's full parameters travel with the request; Summary keeps its
	// 200-rune display digest of the same call.
	hr.Params, hr.ParamsTruncated = boundParams(req.Params)

	if err := r.store.Store(ctx, hr); err != nil {
		return Decision{Kind: Deny, Reason: fmt.Sprintf("failed to raise approval request: %v", err)}
	}

	// Surface the pending hold on the progress stream. The emit is best-effort:
	// a notifier error never changes the admission outcome, which is decided
	// solely by the store resolution below.
	if r.notifier != nil {
		_ = r.notifier.Notify(ctx, hr)
	}

	return r.wait(ctx, hr.ID, hr.ExpiresAt)
}

// missingRowDenyAfter bounds how many consecutive absent-row reads the waiter
// tolerates before failing closed: a pending row that vanished (session purge,
// cascade delete) must become a Deny, not an unbounded spin.
const missingRowDenyAfter = 3

// wait polls the store until the request is resolved, the stored expiry passes,
// or the context is canceled. ONE clock judges the hold: the row's ExpiresAt —
// the same instant the store's resolve CAS enforces — so a resolution landing in
// the final poll interval is honored (the deadline check re-reads the row once
// before giving up). On timeout the waiter closes the row it abandons with a
// terminal "timeout" write, so the persisted state and the decision the model
// received cannot disagree. A store that reports the row absent (nil, nil — the
// postgres contract) is retried briefly, then denied (fail-closed). Only an
// explicit "approved" admits; an explicit reject carries the human's note
// verbatim as the deny reason.
func (r *hitlAskResolver) wait(ctx context.Context, requestID string, expiresAt time.Time) Decision {
	deadline := expiresAt
	if deadline.IsZero() {
		deadline = time.Now().Add(r.timeout)
	}
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()

	missing := 0
	for {
		select {
		case <-ctx.Done():
			return Decision{Kind: Deny, Reason: "approval canceled before a response"}
		case <-ticker.C:
			hr, err := r.store.Get(ctx, requestID)
			if err == nil && hr == nil {
				// Absent row (the postgres store's documented (nil, nil)):
				// tolerate a brief race, then fail closed.
				missing++
				if missing >= missingRowDenyAfter {
					return Decision{Kind: Deny, Reason: "approval request no longer exists"}
				}
				continue
			}
			if err == nil {
				missing = 0
				if d, resolved := decisionFor(hr); resolved {
					return d
				}
			}
			// err != nil is a transient read: fall through to the deadline
			// check and otherwise retry.
			if time.Now().After(deadline) {
				return r.expire(ctx, requestID)
			}
		}
	}
}

// expire is the waiter's give-up path: one final read (a resolution that landed
// in the last poll interval still wins), then a terminal CAS "timeout" write on
// the row it abandons, then the fail-closed Deny.
func (r *hitlAskResolver) expire(ctx context.Context, requestID string) Decision {
	if hr, err := r.store.Get(ctx, requestID); err == nil && hr != nil {
		if d, resolved := decisionFor(hr); resolved {
			return d
		}
	}
	// Best-effort close; the CAS refuses if a response won the race, in which
	// case the read above (or the next reader) sees the resolution.
	_ = r.store.RespondToRequest(ctx, requestID, "timeout", "", "system:expiry", nil)
	return Decision{Kind: Deny, Reason: "approval timed out"}
}

// decisionFor maps a read-back row to a terminal decision; resolved is false
// while the row is still pending.
func decisionFor(hr *HumanRequest) (Decision, bool) {
	switch {
	case hr.Status == "pending":
		return Decision{}, false
	case hr.Status == "approved":
		return Decision{Kind: Allow}, true
	case hr.Response != "":
		return Decision{Kind: Deny, Reason: hr.Response}, true
	default:
		return Decision{Kind: Deny, Reason: fmt.Sprintf("approval %s", hr.Status)}, true
	}
}
