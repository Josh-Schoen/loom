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
package server

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/agent"
	"github.com/teradata-labs/loom/pkg/observability"
)

// newPersistedTestStore returns an in-memory SQLite session store.
func newPersistedTestStore(t *testing.T) *agent.SessionStore {
	t.Helper()
	store, err := agent.NewSessionStore(":memory:", observability.NewNoOpTracer())
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// savePersisted writes a session row (and optional messages) directly to the
// store, standing in for a session left behind by an earlier daemon process.
func savePersisted(t *testing.T, store *agent.SessionStore, id, name string, updated time.Time, messages ...string) {
	t.Helper()
	ctx := context.Background()
	session := &agent.Session{
		ID:        id,
		Name:      name,
		CreatedAt: updated.Add(-time.Hour),
		UpdatedAt: updated,
		Context:   map[string]interface{}{},
	}
	if err := store.SaveSession(ctx, session); err != nil {
		t.Fatalf("SaveSession(%s): %v", id, err)
	}
	for _, content := range messages {
		msg := agent.Message{Role: "user", Content: content, Timestamp: updated}
		if err := store.SaveMessage(ctx, id, msg); err != nil {
			t.Fatalf("SaveMessage(%s): %v", id, err)
		}
	}
}

func sessionByID(sessions []*loomv1.Session, id string) *loomv1.Session {
	for _, sess := range sessions {
		if sess.GetId() == id {
			return sess
		}
	}
	return nil
}

func TestServer_ListSessions_IncludesPersisted(t *testing.T) {
	ctx := context.Background()
	store := newPersistedTestStore(t)
	ag := createTestAgent()
	srv := NewServer(ag, store)

	// Live, in-process session.
	live := ag.CreateSession(ctx, "sess-live", "live thread")
	if live == nil {
		t.Fatal("CreateSession returned nil")
	}

	// Store-only session, newer than the live one.
	savePersisted(t, store, "sess-persisted", "", time.Now().Add(time.Hour), "hello", "world")

	resp, err := srv.ListSessions(ctx, &loomv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	liveSess := sessionByID(resp.GetSessions(), "sess-live")
	if liveSess == nil {
		t.Fatal("live session missing from the listing")
	}
	if liveSess.GetState() != "active" {
		t.Errorf("live state = %q, want active", liveSess.GetState())
	}

	persisted := sessionByID(resp.GetSessions(), "sess-persisted")
	if persisted == nil {
		t.Fatal("persisted session missing from the listing")
	}
	if persisted.GetState() != persistedSessionState {
		t.Errorf("persisted state = %q, want %q", persisted.GetState(), persistedSessionState)
	}
	if persisted.GetConversationCount() != 2 {
		t.Errorf("persisted conversation_count = %d, want 2", persisted.GetConversationCount())
	}
	if persisted.GetName() != "" {
		t.Errorf("persisted name = %q, want empty (no fabricated name)", persisted.GetName())
	}

	// Newest-updated first: the persisted row was written an hour ahead.
	if resp.GetSessions()[0].GetId() != "sess-persisted" {
		t.Errorf("first listed session = %q, want sess-persisted (newest first)",
			resp.GetSessions()[0].GetId())
	}
}

func TestServer_ListSessions_LiveWinsOnIDCollision(t *testing.T) {
	ctx := context.Background()
	store := newPersistedTestStore(t)
	ag := createTestAgent()
	srv := NewServer(ag, store)

	const id = "sess-both"
	if sess := ag.CreateSession(ctx, id, "live name"); sess == nil {
		t.Fatal("CreateSession returned nil")
	}
	// Same ID in the store with different metadata; the live entry must win.
	savePersisted(t, store, id, "stale persisted name", time.Now().Add(time.Hour), "a", "b", "c")

	resp, err := srv.ListSessions(ctx, &loomv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	count := 0
	for _, sess := range resp.GetSessions() {
		if sess.GetId() == id {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("session %s listed %d times, want once", id, count)
	}

	sess := sessionByID(resp.GetSessions(), id)
	if sess.GetName() != "live name" {
		t.Errorf("name = %q, want the live name", sess.GetName())
	}
	if sess.GetState() != "active" {
		t.Errorf("state = %q, want active", sess.GetState())
	}
}

func TestServer_ListSessions_NoStore_UnchangedListing(t *testing.T) {
	ctx := context.Background()
	ag := createTestAgent()
	srv := NewServer(ag, nil)

	if sess := ag.CreateSession(ctx, "sess-only-live", "only live"); sess == nil {
		t.Fatal("CreateSession returned nil")
	}

	resp, err := srv.ListSessions(ctx, &loomv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(resp.GetSessions()) != 1 || resp.GetSessions()[0].GetId() != "sess-only-live" {
		t.Errorf("listing without a store = %v, want the single live session", resp.GetSessions())
	}
}

func TestServer_GetConversationHistory_PersistedFallback(t *testing.T) {
	ctx := context.Background()
	store := newPersistedTestStore(t)
	ag := createTestAgent()
	srv := NewServer(ag, store)

	savePersisted(t, store, "sess-history", "", time.Now(), "first", "second")

	history, err := srv.GetConversationHistory(ctx, &loomv1.GetConversationHistoryRequest{
		SessionId: "sess-history",
	})
	if err != nil {
		t.Fatalf("GetConversationHistory on a persisted session: %v", err)
	}
	if history.GetSessionId() != "sess-history" {
		t.Errorf("session_id = %q, want sess-history", history.GetSessionId())
	}
	if len(history.GetMessages()) != 2 {
		t.Fatalf("got %d messages, want 2", len(history.GetMessages()))
	}
	if history.GetMessages()[0].GetContent() != "first" {
		t.Errorf("messages[0] = %q, want %q", history.GetMessages()[0].GetContent(), "first")
	}

	// Read-only: viewing history must not adopt the session into memory.
	if _, ok := ag.GetSession("sess-history"); ok {
		t.Error("history read adopted the persisted session into agent memory")
	}
}

func TestServer_GetConversationHistory_UnknownIDStillNotFound(t *testing.T) {
	ctx := context.Background()
	store := newPersistedTestStore(t)
	srv := NewServer(createTestAgent(), store)

	_, err := srv.GetConversationHistory(ctx, &loomv1.GetConversationHistoryRequest{
		SessionId: "no-such-session",
	})
	if err == nil {
		t.Fatal("expected an error for an unknown session ID")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.NotFound {
		t.Errorf("error = %v, want codes.NotFound", err)
	}
}

func TestMultiAgentServer_ListSessions_IncludesPersisted(t *testing.T) {
	ctx := context.Background()
	store := newPersistedTestStore(t)
	ag := createTestAgent()
	srv := NewMultiAgentServer(map[string]*agent.Agent{"default": ag}, store)

	if sess := ag.CreateSession(ctx, "ma-live", "live thread"); sess == nil {
		t.Fatal("CreateSession returned nil")
	}
	savePersisted(t, store, "ma-persisted", "", time.Now().Add(time.Hour), "one")

	resp, err := srv.ListSessions(ctx, &loomv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if sessionByID(resp.GetSessions(), "ma-live") == nil {
		t.Error("live session missing from the multi-agent listing")
	}
	persisted := sessionByID(resp.GetSessions(), "ma-persisted")
	if persisted == nil {
		t.Fatal("persisted session missing from the multi-agent listing")
	}
	if persisted.GetState() != persistedSessionState {
		t.Errorf("persisted state = %q, want %q", persisted.GetState(), persistedSessionState)
	}
	if persisted.GetConversationCount() != 1 {
		t.Errorf("persisted conversation_count = %d, want 1", persisted.GetConversationCount())
	}
}

func TestMultiAgentServer_GetConversationHistory_PersistedFallback(t *testing.T) {
	ctx := context.Background()
	store := newPersistedTestStore(t)
	ag := createTestAgent()
	srv := NewMultiAgentServer(map[string]*agent.Agent{"default": ag}, store)

	savePersisted(t, store, "ma-history", "", time.Now(), "alpha", "beta")

	history, err := srv.GetConversationHistory(ctx, &loomv1.GetConversationHistoryRequest{
		SessionId: "ma-history",
	})
	if err != nil {
		t.Fatalf("GetConversationHistory on a persisted session: %v", err)
	}
	if len(history.GetMessages()) != 2 {
		t.Fatalf("got %d messages, want 2", len(history.GetMessages()))
	}
	if _, ok := ag.GetSession("ma-history"); ok {
		t.Error("history read adopted the persisted session into agent memory")
	}

	_, err = srv.GetConversationHistory(ctx, &loomv1.GetConversationHistoryRequest{
		SessionId: "ma-missing",
	})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.NotFound {
		t.Errorf("unknown session error = %v, want codes.NotFound", err)
	}
}
