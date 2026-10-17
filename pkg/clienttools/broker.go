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

// Package clienttools implements client-executed tools: tools a StreamWeave
// caller declares on its request and runs itself. The server advertises them
// to the agent for that call only, hands each call back to the caller on the
// progress stream, and resumes the turn when the caller submits the result.
// See docs/architecture/client-tools.md.
package clienttools

import (
	"errors"
	"sync"
)

// Result is the caller's outcome for one client tool call.
type Result struct {
	Success      bool
	Output       interface{}
	ErrorCode    string
	ErrorMessage string
}

// ErrDuplicateCall is returned when a call id is already pending in a session.
var ErrDuplicateCall = errors.New("client tool call already pending")

// notPendingReason is the single rejection reason for a submission that does
// not match a pending call. Unknown, already answered, timed out, ended, and
// owned by another user all read the same, so a submitter learns nothing about
// calls it does not own.
const notPendingReason = "no pending client tool call with this id in this session"

type callKey struct {
	sessionID string
	callID    string
}

type pendingCall struct {
	ownerUserID string
	// result is buffered (capacity 1) so Submit never blocks: the entry is
	// removed under the lock before the send, so at most one value is sent.
	result chan Result
}

// Broker matches submitted results to the turns waiting on them. It is safe
// for concurrent use: waiters register and withdraw on the turn goroutine
// while submissions arrive on RPC goroutines.
//
// Exactly-once: an entry leaves the table under the lock by exactly one of
// Submit or withdraw, and only the remover acts on it.
type Broker struct {
	mu      sync.Mutex
	pending map[callKey]*pendingCall
}

// NewBroker returns an empty broker.
func NewBroker() *Broker {
	return &Broker{pending: make(map[callKey]*pendingCall)}
}

// register adds a pending call owned by ownerUserID and returns the channel
// its result arrives on.
func (b *Broker) register(sessionID, callID, ownerUserID string) (<-chan Result, error) {
	key := callKey{sessionID: sessionID, callID: callID}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.pending[key]; exists {
		return nil, ErrDuplicateCall
	}
	call := &pendingCall{ownerUserID: ownerUserID, result: make(chan Result, 1)}
	b.pending[key] = call
	return call.result, nil
}

// withdraw removes a pending call that stopped waiting (timeout or stream
// end). It reports false when Submit already removed it — the result is then
// in the channel and the waiter must use it.
func (b *Broker) withdraw(sessionID, callID string) bool {
	key := callKey{sessionID: sessionID, callID: callID}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.pending[key]; !exists {
		return false
	}
	delete(b.pending, key)
	return true
}

// Submit delivers a result to the call pending under (sessionID, callID) when
// submitterUserID owns it. It reports whether the result was accepted and, if
// not, why.
func (b *Broker) Submit(sessionID, callID, submitterUserID string, result Result) (bool, string) {
	key := callKey{sessionID: sessionID, callID: callID}
	b.mu.Lock()
	call, exists := b.pending[key]
	if !exists || call.ownerUserID != submitterUserID {
		b.mu.Unlock()
		return false, notPendingReason
	}
	delete(b.pending, key)
	b.mu.Unlock()

	call.result <- result
	return true, ""
}

// PendingCount returns the number of calls currently waiting.
func (b *Broker) PendingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}
