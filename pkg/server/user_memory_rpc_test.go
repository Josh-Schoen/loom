// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/agent"
	"github.com/teradata-labs/loom/pkg/memory"
	"github.com/teradata-labs/loom/pkg/observability"
	"github.com/teradata-labs/loom/pkg/storage/sqlite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type charTokenCounter struct{}

func (charTokenCounter) CountTokens(text string) int { return len(text) / 4 }

// newUserMemoryServer builds a server with one agent named "assistant" whose
// graph memory is a real SQLite store.
func newUserMemoryServer(t *testing.T, enabled bool) (*MultiAgentServer, memory.GraphMemoryStore) {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "graph.db")+"?_fk=1&_journal_mode=WAL&_busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	migrator, err := sqlite.NewMigrator(db, observability.NewNoOpTracer())
	require.NoError(t, err)
	require.NoError(t, migrator.MigrateUp(context.Background()))
	store := sqlite.NewGraphMemoryStore(db, charTokenCounter{}, observability.NewNoOpTracer())

	cfg := agent.DefaultGraphMemoryConfig()
	cfg.Enabled = enabled
	cfg.EnableExtraction = false
	ag := agent.NewAgent(&mockBackend{}, &mockLLMProvider{responses: []string{"ok"}},
		agent.WithName("assistant"), agent.WithGraphMemoryStore(store, cfg))
	srv := NewMultiAgentServer(map[string]*agent.Agent{"assistant": ag}, nil)
	return srv, store
}

func TestUserMemoriesArePerPerson(t *testing.T) {
	srv, store := newUserMemoryServer(t, true)
	alex, sam := ctxFor("alex"), ctxFor("sam")

	// Learned from Alex's conversation, as the extractor stores it.
	_, err := store.Remember(alex, &memory.Memory{AgentID: "assistant", Content: "Alex is vegetarian", MemoryType: memory.MemoryTypePreference, Source: "auto_extracted", Salience: 0.5})
	require.NoError(t, err)
	added, err := srv.AddUserMemory(alex, &loomv1.AddUserMemoryRequest{AgentId: "assistant", Content: "  My daughter Emma is 8  "})
	require.NoError(t, err)
	assert.Equal(t, "My daughter Emma is 8", added.Content)
	assert.Equal(t, "user", added.Source)

	list, err := srv.ListUserMemories(alex, &loomv1.ListUserMemoriesRequest{AgentId: "assistant"})
	require.NoError(t, err)
	assert.Equal(t, int32(2), list.TotalCount)
	require.Len(t, list.Memories, 2)
	assert.Equal(t, added.Id, list.Memories[0].Id, "newest first")

	// Sam shares the assistant but sees, changes, and forgets none of it.
	samList, err := srv.ListUserMemories(sam, &loomv1.ListUserMemoriesRequest{AgentId: "assistant"})
	require.NoError(t, err)
	assert.Empty(t, samList.Memories)
	assert.Zero(t, samList.TotalCount)
	_, err = srv.CorrectUserMemory(sam, &loomv1.CorrectUserMemoryRequest{AgentId: "assistant", MemoryId: added.Id, Content: "changed"})
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = srv.ForgetUserMemory(sam, &loomv1.ForgetUserMemoryRequest{AgentId: "assistant", MemoryId: added.Id})
	assert.Equal(t, codes.NotFound, status.Code(err))
	all, err := srv.ForgetAllUserMemories(sam, &loomv1.ForgetAllUserMemoriesRequest{AgentId: "assistant"})
	require.NoError(t, err)
	assert.Zero(t, all.ForgottenCount)

	list, err = srv.ListUserMemories(alex, &loomv1.ListUserMemoriesRequest{AgentId: "assistant"})
	require.NoError(t, err)
	assert.Len(t, list.Memories, 2, "Sam's calls must not touch Alex's memories")
}

func TestCorrectAndForgetUserMemory(t *testing.T) {
	srv, store := newUserMemoryServer(t, true)
	ctx := ctxFor("alex")

	added, err := srv.AddUserMemory(ctx, &loomv1.AddUserMemoryRequest{AgentId: "assistant", Content: "Allergic to peanuts"})
	require.NoError(t, err)
	corrected, err := srv.CorrectUserMemory(ctx, &loomv1.CorrectUserMemoryRequest{AgentId: "assistant", MemoryId: added.Id, Content: "Allergic to tree nuts, not peanuts"})
	require.NoError(t, err)
	assert.NotEqual(t, added.Id, corrected.Id)

	list, err := srv.ListUserMemories(ctx, &loomv1.ListUserMemoriesRequest{AgentId: "assistant"})
	require.NoError(t, err)
	require.Len(t, list.Memories, 1, "the corrected memory replaces the old one")
	assert.Equal(t, "Allergic to tree nuts, not peanuts", list.Memories[0].Content)

	// The old one can't be corrected or forgotten again, and is not recalled.
	_, err = srv.CorrectUserMemory(ctx, &loomv1.CorrectUserMemoryRequest{AgentId: "assistant", MemoryId: added.Id, Content: "again"})
	assert.Equal(t, codes.NotFound, status.Code(err))
	recalled, err := store.Recall(ctx, memory.RecallOpts{AgentID: "assistant", Query: "peanuts"})
	require.NoError(t, err)
	for _, m := range recalled {
		assert.NotEqual(t, added.Id, m.ID, "a corrected memory must not be recalled")
	}

	_, err = srv.ForgetUserMemory(ctx, &loomv1.ForgetUserMemoryRequest{AgentId: "assistant", MemoryId: corrected.Id})
	require.NoError(t, err)
	list, err = srv.ListUserMemories(ctx, &loomv1.ListUserMemoriesRequest{AgentId: "assistant"})
	require.NoError(t, err)
	assert.Empty(t, list.Memories)
	_, err = srv.ForgetUserMemory(ctx, &loomv1.ForgetUserMemoryRequest{AgentId: "assistant", MemoryId: corrected.Id})
	assert.Equal(t, codes.NotFound, status.Code(err), "forgetting twice is not found")
}

func TestForgetAllUserMemories(t *testing.T) {
	srv, store := newUserMemoryServer(t, true)
	alex, sam := ctxFor("alex"), ctxFor("sam")
	for _, c := range []string{"Lives in Austin", "Works nights", "Has a dog named Biscuit"} {
		_, err := srv.AddUserMemory(alex, &loomv1.AddUserMemoryRequest{AgentId: "assistant", Content: c})
		require.NoError(t, err)
	}
	_, err := store.CreateEntity(alex, &memory.Entity{AgentID: "assistant", Name: "biscuit", EntityType: "concept"})
	require.NoError(t, err)
	_, err = srv.AddUserMemory(sam, &loomv1.AddUserMemoryRequest{AgentId: "assistant", Content: "Lives in Denver"})
	require.NoError(t, err)

	resp, err := srv.ForgetAllUserMemories(alex, &loomv1.ForgetAllUserMemoriesRequest{AgentId: "assistant"})
	require.NoError(t, err)
	assert.Equal(t, int32(3), resp.ForgottenCount)

	list, err := srv.ListUserMemories(alex, &loomv1.ListUserMemoriesRequest{AgentId: "assistant"})
	require.NoError(t, err)
	assert.Empty(t, list.Memories)
	entities, _, err := store.ListEntities(alex, "assistant", "", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, entities, "the people, places, and things mentioned are forgotten too")

	samList, err := srv.ListUserMemories(sam, &loomv1.ListUserMemoriesRequest{AgentId: "assistant"})
	require.NoError(t, err)
	assert.Len(t, samList.Memories, 1, "another person's memories stay")
}

func TestUserMemoryRequests(t *testing.T) {
	srv, _ := newUserMemoryServer(t, true)
	ctx := ctxFor("alex")

	tests := []struct {
		name string
		call func() error
		code codes.Code
	}{
		{"missing agent", func() error {
			_, err := srv.ListUserMemories(ctx, &loomv1.ListUserMemoriesRequest{})
			return err
		}, codes.InvalidArgument},
		{"unknown agent", func() error {
			_, err := srv.ListUserMemories(ctx, &loomv1.ListUserMemoriesRequest{AgentId: "nobody"})
			return err
		}, codes.NotFound},
		{"empty content", func() error {
			_, err := srv.AddUserMemory(ctx, &loomv1.AddUserMemoryRequest{AgentId: "assistant", Content: "   "})
			return err
		}, codes.InvalidArgument},
		{"content too long", func() error {
			_, err := srv.AddUserMemory(ctx, &loomv1.AddUserMemoryRequest{AgentId: "assistant", Content: strings.Repeat("é", maxUserMemoryChars+1)})
			return err
		}, codes.InvalidArgument},
		{"missing memory id", func() error {
			_, err := srv.ForgetUserMemory(ctx, &loomv1.ForgetUserMemoryRequest{AgentId: "assistant"})
			return err
		}, codes.InvalidArgument},
		{"unknown memory", func() error {
			_, err := srv.ForgetUserMemory(ctx, &loomv1.ForgetUserMemoryRequest{AgentId: "assistant", MemoryId: "mem_nope"})
			return err
		}, codes.NotFound},
		{"negative offset", func() error {
			_, err := srv.ListUserMemories(ctx, &loomv1.ListUserMemoriesRequest{AgentId: "assistant", Offset: -1})
			return err
		}, codes.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.code, status.Code(tt.call()))
		})
	}

	// Exactly the limit is accepted.
	_, err := srv.AddUserMemory(ctx, &loomv1.AddUserMemoryRequest{AgentId: "assistant", Content: strings.Repeat("é", maxUserMemoryChars)})
	assert.NoError(t, err)
}

func TestUserMemoriesNeedGraphMemory(t *testing.T) {
	srv, _ := newUserMemoryServer(t, false)
	_, err := srv.ListUserMemories(ctxFor("alex"), &loomv1.ListUserMemoriesRequest{AgentId: "assistant"})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))

	noStore := agent.NewAgent(&mockBackend{}, &mockLLMProvider{responses: []string{"ok"}}, agent.WithName("assistant"))
	plain := NewMultiAgentServer(map[string]*agent.Agent{"assistant": noStore}, nil)
	_, err = plain.AddUserMemory(ctxFor("alex"), &loomv1.AddUserMemoryRequest{AgentId: "assistant", Content: "x"})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}
