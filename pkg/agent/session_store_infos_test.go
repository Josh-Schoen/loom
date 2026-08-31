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
package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/teradata-labs/loom/pkg/observability"
)

func newInfosTestStore(t *testing.T) *SessionStore {
	t.Helper()
	store, err := NewSessionStore(t.TempDir()+"/infos.db", observability.NewNoOpTracer())
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSessionStore_ListSessionInfos_Empty(t *testing.T) {
	store := newInfosTestStore(t)

	infos, err := store.ListSessionInfos(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListSessionInfos: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("expected 0 infos on an empty store, got %d", len(infos))
	}
}

func TestSessionStore_ListSessionInfos_Populated(t *testing.T) {
	store := newInfosTestStore(t)
	ctx := context.Background()

	created := time.Unix(1_700_000_000, 0)
	updated := time.Unix(1_700_000_500, 0)
	session := &Session{
		ID:           "sess-populated",
		Name:         "named thread",
		AgentID:      "coordinator",
		CreatedAt:    created,
		UpdatedAt:    updated,
		TotalCostUSD: 1.25,
		TotalTokens:  4242,
		Context:      map[string]interface{}{},
	}
	if err := store.SaveSession(ctx, session); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	for i := 0; i < 3; i++ {
		msg := Message{Role: "user", Content: fmt.Sprintf("m%d", i), Timestamp: updated}
		if err := store.SaveMessage(ctx, session.ID, msg); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}
	}

	// A second session with no messages must still be listed, with count 0.
	bare := &Session{
		ID:        "sess-bare",
		CreatedAt: created,
		UpdatedAt: time.Unix(1_700_000_100, 0),
		Context:   map[string]interface{}{},
	}
	if err := store.SaveSession(ctx, bare); err != nil {
		t.Fatalf("SaveSession(bare): %v", err)
	}

	infos, err := store.ListSessionInfos(ctx, 0)
	if err != nil {
		t.Fatalf("ListSessionInfos: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 infos, got %d", len(infos))
	}

	byID := map[string]SessionMetadata{}
	for _, info := range infos {
		byID[info.ID] = info
	}

	got := byID["sess-populated"]
	if got.Name != "named thread" {
		t.Errorf("Name = %q, want %q", got.Name, "named thread")
	}
	if got.AgentID != "coordinator" {
		t.Errorf("AgentID = %q, want %q", got.AgentID, "coordinator")
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}
	if !got.UpdatedAt.Equal(updated) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, updated)
	}
	if got.TotalCostUSD != 1.25 {
		t.Errorf("TotalCostUSD = %v, want 1.25", got.TotalCostUSD)
	}
	if got.TotalTokens != 4242 {
		t.Errorf("TotalTokens = %d, want 4242", got.TotalTokens)
	}
	if got.MessageCount != 3 {
		t.Errorf("MessageCount = %d, want 3", got.MessageCount)
	}

	bareInfo := byID["sess-bare"]
	if bareInfo.MessageCount != 0 {
		t.Errorf("bare MessageCount = %d, want 0", bareInfo.MessageCount)
	}
	// An unnamed session must come back unnamed — no fabricated placeholder.
	if bareInfo.Name != "" {
		t.Errorf("bare Name = %q, want empty", bareInfo.Name)
	}
	if bareInfo.AgentID != "" {
		t.Errorf("bare AgentID = %q, want empty", bareInfo.AgentID)
	}
}

func TestSessionStore_ListSessionInfos_OrderingNewestFirst(t *testing.T) {
	store := newInfosTestStore(t)
	ctx := context.Background()

	// Insert oldest-first so the ordering cannot come from insertion order.
	for i, id := range []string{"oldest", "middle", "newest"} {
		session := &Session{
			ID:        id,
			CreatedAt: time.Unix(1_700_000_000, 0),
			UpdatedAt: time.Unix(int64(1_700_000_000+i*100), 0),
			Context:   map[string]interface{}{},
		}
		if err := store.SaveSession(ctx, session); err != nil {
			t.Fatalf("SaveSession(%s): %v", id, err)
		}
	}

	infos, err := store.ListSessionInfos(ctx, 0)
	if err != nil {
		t.Fatalf("ListSessionInfos: %v", err)
	}
	want := []string{"newest", "middle", "oldest"}
	if len(infos) != len(want) {
		t.Fatalf("expected %d infos, got %d", len(want), len(infos))
	}
	for i, id := range want {
		if infos[i].ID != id {
			t.Errorf("infos[%d].ID = %q, want %q", i, infos[i].ID, id)
		}
	}
}

func TestSessionStore_ListSessionInfos_Cap(t *testing.T) {
	store := newInfosTestStore(t)
	ctx := context.Background()

	const total = 7
	for i := 0; i < total; i++ {
		session := &Session{
			ID:        fmt.Sprintf("sess-%02d", i),
			CreatedAt: time.Unix(1_700_000_000, 0),
			UpdatedAt: time.Unix(int64(1_700_000_000+i), 0),
			Context:   map[string]interface{}{},
		}
		if err := store.SaveSession(ctx, session); err != nil {
			t.Fatalf("SaveSession: %v", err)
		}
	}

	infos, err := store.ListSessionInfos(ctx, 3)
	if err != nil {
		t.Fatalf("ListSessionInfos(3): %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("expected 3 infos under an explicit limit, got %d", len(infos))
	}
	// The cap must keep the newest rows, not an arbitrary three.
	if infos[0].ID != "sess-06" || infos[2].ID != "sess-04" {
		t.Errorf("capped listing = %q..%q, want sess-06..sess-04", infos[0].ID, infos[2].ID)
	}

	// limit <= 0 falls back to the documented default, which is above `total`
	// here, so every row comes back.
	all, err := store.ListSessionInfos(ctx, -1)
	if err != nil {
		t.Fatalf("ListSessionInfos(-1): %v", err)
	}
	if len(all) != total {
		t.Errorf("expected %d infos under the default limit, got %d", total, len(all))
	}
	if SessionMetadataLimitDefault != 200 {
		t.Errorf("SessionMetadataLimitDefault = %d; the doc comment says 200",
			SessionMetadataLimitDefault)
	}
}
