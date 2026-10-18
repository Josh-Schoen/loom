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

//go:build fts5

package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/teradata-labs/loom/internal/sqlitedriver"
	"github.com/teradata-labs/loom/pkg/memory"
	"github.com/teradata-labs/loom/pkg/observability"
	"github.com/teradata-labs/loom/pkg/types"
)

const isoAgent = "shared-agent"

// isoFixture is one person's graph under the shared agent.
type isoFixture struct {
	ctx    context.Context
	mom    *memory.Entity
	garden *memory.Entity
	mem    *memory.Memory
}

// seedUser gives a user an entity "mom", an entity "garden", an edge between
// them, and one memory (with an embedding) linked to "mom".
func seedUser(t *testing.T, store *GraphMemoryStore, userID, fact string) isoFixture {
	t.Helper()
	ctx := types.ContextWithUserID(context.Background(), userID)

	mom, err := store.CreateEntity(ctx, &memory.Entity{AgentID: isoAgent, Name: "mom", EntityType: "person"})
	require.NoError(t, err)
	garden, err := store.CreateEntity(ctx, &memory.Entity{AgentID: isoAgent, Name: "garden", EntityType: "place"})
	require.NoError(t, err)
	_, err = store.Relate(ctx, &memory.Edge{AgentID: isoAgent, SourceID: mom.ID, TargetID: garden.ID, Relation: "LIKES", PropertiesJSON: `{"since":2020}`})
	require.NoError(t, err)

	mem, err := store.Remember(ctx, &memory.Memory{
		AgentID:        isoAgent,
		Content:        fact,
		MemoryType:     memory.MemoryTypeFact,
		EntityIDs:      []string{mom.ID},
		Salience:       0.8,
		Embedding:      []float32{1, 0, 0},
		EmbeddingModel: "test-model",
	})
	require.NoError(t, err)
	return isoFixture{ctx: ctx, mom: mom, garden: garden, mem: mem}
}

func memoryIDs(mems []*memory.Memory) []string {
	ids := make([]string, len(mems))
	for i, m := range mems {
		ids[i] = m.ID
	}
	return ids
}

func entityIDs(ents []*memory.Entity) []string {
	ids := make([]string, len(ents))
	for i, e := range ents {
		ids[i] = e.ID
	}
	return ids
}

// TestGraphMemoryStore_UserIsolation_ReadPaths checks that every read path
// returns only the caller's rows when two people share one agent.
func TestGraphMemoryStore_UserIsolation_ReadPaths(t *testing.T) {
	store := newTestGraphMemoryStore(t)
	alice := seedUser(t, store, "alice", "Mom birthday is in May")
	bob := seedUser(t, store, "bob", "Mom birthday is in October")

	require.NotEqual(t, alice.mom.ID, bob.mom.ID, "each user gets their own 'mom' entity")

	type view struct {
		name  string
		own   isoFixture
		other isoFixture
	}
	for _, v := range []view{{"alice", alice, bob}, {"bob", bob, alice}} {
		t.Run(v.name, func(t *testing.T) {
			ctx := v.own.ctx

			tests := []struct {
				name  string
				check func(t *testing.T)
			}{
				{"Recall FTS", func(t *testing.T) {
					got, err := store.Recall(ctx, memory.RecallOpts{AgentID: isoAgent, Query: "mom birthday"})
					require.NoError(t, err)
					assert.Equal(t, []string{v.own.mem.ID}, memoryIDs(got))
				}},
				{"Recall no query", func(t *testing.T) {
					got, err := store.Recall(ctx, memory.RecallOpts{AgentID: isoAgent})
					require.NoError(t, err)
					assert.Equal(t, []string{v.own.mem.ID}, memoryIDs(got))
				}},
				{"Recall scoped to other user's entity", func(t *testing.T) {
					got, err := store.Recall(ctx, memory.RecallOpts{AgentID: isoAgent, EntityIDs: []string{v.other.mom.ID}})
					require.NoError(t, err)
					assert.Empty(t, got)
				}},
				{"VectorRecall", func(t *testing.T) {
					got, err := store.VectorRecall(ctx, memory.VectorRecallOpts{
						AgentID: isoAgent, Embedding: []float32{1, 0, 0}, Model: "test-model", Limit: 10,
					})
					require.NoError(t, err)
					assert.Equal(t, []string{v.own.mem.ID}, memoryIDs(got))
				}},
				{"ContextFor", func(t *testing.T) {
					rc, err := store.ContextFor(ctx, memory.ContextForOpts{AgentID: isoAgent, EntityName: "mom", MaxTokens: 4000})
					require.NoError(t, err)
					require.NotNil(t, rc.Entity)
					assert.Equal(t, v.own.mom.ID, rc.Entity.ID)
					require.Len(t, rc.Memories, 1)
					assert.Equal(t, v.own.mem.ID, rc.Memories[0].Memory.ID)
					require.Len(t, rc.EdgesOut, 1)
					assert.Equal(t, v.own.garden.ID, rc.EdgesOut[0].TargetID)
					assert.Equal(t, map[string]string{v.own.garden.ID: "garden"}, rc.EntityNames)
				}},
				{"GetEntity", func(t *testing.T) {
					e, err := store.GetEntity(ctx, isoAgent, "mom")
					require.NoError(t, err)
					assert.Equal(t, v.own.mom.ID, e.ID)
				}},
				{"SearchEntities", func(t *testing.T) {
					got, err := store.SearchEntities(ctx, isoAgent, "mom garden", 10)
					require.NoError(t, err)
					assert.ElementsMatch(t, []string{v.own.mom.ID, v.own.garden.ID}, entityIDs(got))
				}},
				{"ListEntities", func(t *testing.T) {
					got, total, err := store.ListEntities(ctx, isoAgent, "", 10, 0)
					require.NoError(t, err)
					assert.Equal(t, 2, total)
					assert.ElementsMatch(t, []string{v.own.mom.ID, v.own.garden.ID}, entityIDs(got))
				}},
				{"ListEntities by type", func(t *testing.T) {
					got, total, err := store.ListEntities(ctx, isoAgent, "person", 10, 0)
					require.NoError(t, err)
					assert.Equal(t, 1, total)
					assert.Equal(t, []string{v.own.mom.ID}, entityIDs(got))
				}},
				{"ListMemories", func(t *testing.T) {
					got, total, err := store.ListMemories(ctx, isoAgent, 10, 0)
					require.NoError(t, err)
					assert.Equal(t, 1, total)
					assert.Equal(t, []string{v.own.mem.ID}, memoryIDs(got))
				}},
				{"GetMemory own", func(t *testing.T) {
					m, err := store.GetMemory(ctx, isoAgent, v.own.mem.ID)
					require.NoError(t, err)
					assert.Equal(t, []string{v.own.mom.ID}, m.EntityIDs)
				}},
				{"GetMemory other", func(t *testing.T) {
					_, err := store.GetMemory(ctx, isoAgent, v.other.mem.ID)
					assert.ErrorIs(t, err, memory.ErrNotFound)
				}},
				{"ListEdgesFrom other user's entity", func(t *testing.T) {
					got, err := store.ListEdgesFrom(ctx, v.other.mom.ID)
					require.NoError(t, err)
					assert.Empty(t, got)
				}},
				{"ListEdgesTo other user's entity", func(t *testing.T) {
					got, err := store.ListEdgesTo(ctx, v.other.garden.ID)
					require.NoError(t, err)
					assert.Empty(t, got)
				}},
				{"Neighbors other user's entity", func(t *testing.T) {
					for _, dir := range []string{"outbound", "inbound", "both"} {
						got, err := store.Neighbors(ctx, v.other.mom.ID, "", dir, 3)
						require.NoError(t, err)
						assert.Empty(t, got, dir)
					}
				}},
				{"Neighbors own entity", func(t *testing.T) {
					got, err := store.Neighbors(ctx, v.own.mom.ID, "", "both", 3)
					require.NoError(t, err)
					require.Len(t, got, 1)
					assert.Equal(t, v.own.garden.ID, got[0].TargetID)
				}},
				{"GetLineage other user's memory", func(t *testing.T) {
					got, err := store.GetLineage(ctx, v.other.mem.ID)
					require.NoError(t, err)
					assert.Empty(t, got)
				}},
				{"GetStats", func(t *testing.T) {
					stats, err := store.GetStats(ctx, isoAgent)
					require.NoError(t, err)
					assert.Equal(t, 2, stats.EntityCount)
					assert.Equal(t, 1, stats.EdgeCount)
					assert.Equal(t, 1, stats.MemoryCount)
					assert.Equal(t, 1, stats.ActiveMemoryCount)
					assert.Equal(t, map[string]int{memory.MemoryTypeFact: 1}, stats.MemoriesByType)
				}},
			}
			for _, tt := range tests {
				t.Run(tt.name, tt.check)
			}
		})
	}
}

// TestGraphMemoryStore_UserIsolation_CrossUserMutations checks that ID-based
// mutations never touch another user's rows, and that Forget/Supersede/
// GetMemory/Consolidate report another user's memory as not found.
func TestGraphMemoryStore_UserIsolation_CrossUserMutations(t *testing.T) {
	tests := []struct {
		name string
		// act runs as bob against alice's fixture.
		act func(t *testing.T, store *GraphMemoryStore, alice, bob isoFixture)
	}{
		{"Forget", func(t *testing.T, store *GraphMemoryStore, alice, bob isoFixture) {
			assert.ErrorIs(t, store.Forget(bob.ctx, alice.mem.ID), memory.ErrNotFound)
		}},
		{"Supersede", func(t *testing.T, store *GraphMemoryStore, alice, bob isoFixture) {
			_, err := store.Supersede(bob.ctx, alice.mem.ID, &memory.Memory{AgentID: isoAgent, Content: "replaced"})
			assert.ErrorIs(t, err, memory.ErrNotFound)
		}},
		{"GetMemory", func(t *testing.T, store *GraphMemoryStore, alice, bob isoFixture) {
			_, err := store.GetMemory(bob.ctx, isoAgent, alice.mem.ID)
			assert.ErrorIs(t, err, memory.ErrNotFound)
		}},
		{"Consolidate", func(t *testing.T, store *GraphMemoryStore, alice, bob isoFixture) {
			_, err := store.Consolidate(bob.ctx, []string{bob.mem.ID, alice.mem.ID},
				&memory.Memory{AgentID: isoAgent, Content: "merged"})
			assert.ErrorIs(t, err, memory.ErrNotFound)
		}},
		{"TouchMemories", func(t *testing.T, store *GraphMemoryStore, alice, bob isoFixture) {
			require.NoError(t, store.TouchMemories(bob.ctx, []string{alice.mem.ID}))
		}},
		{"DecayAll", func(t *testing.T, store *GraphMemoryStore, alice, bob isoFixture) {
			require.NoError(t, store.DecayAll(bob.ctx, isoAgent, 0.1))
		}},
		{"Unrelate", func(t *testing.T, store *GraphMemoryStore, alice, bob isoFixture) {
			require.NoError(t, store.Unrelate(bob.ctx, alice.mom.ID, alice.garden.ID, "LIKES"))
		}},
		{"Relate onto other user's entities", func(t *testing.T, store *GraphMemoryStore, alice, bob isoFixture) {
			_, err := store.Relate(bob.ctx, &memory.Edge{
				AgentID: isoAgent, SourceID: alice.mom.ID, TargetID: alice.garden.ID, Relation: "LIKES",
				PropertiesJSON: `{"hijacked":true}`,
			})
			assert.ErrorIs(t, err, memory.ErrNotFound)
			_, err = store.Relate(bob.ctx, &memory.Edge{
				AgentID: isoAgent, SourceID: bob.mom.ID, TargetID: alice.garden.ID, Relation: "VISITS",
			})
			assert.ErrorIs(t, err, memory.ErrNotFound)
		}},
		{"Remember linked to other user's entity", func(t *testing.T, store *GraphMemoryStore, alice, bob isoFixture) {
			_, err := store.Remember(bob.ctx, &memory.Memory{
				AgentID: isoAgent, Content: "attached to alice's mom", EntityIDs: []string{alice.mom.ID},
			})
			assert.ErrorIs(t, err, memory.ErrNotFound)
		}},
		{"UpdateEntity and DeleteEntity by name", func(t *testing.T, store *GraphMemoryStore, alice, bob isoFixture) {
			_, err := store.UpdateEntity(bob.ctx, &memory.Entity{AgentID: isoAgent, Name: "mom", EntityType: "relative"})
			require.NoError(t, err) // updates bob's own "mom"
			require.NoError(t, store.DeleteEntity(bob.ctx, isoAgent, "mom"))
		}},
		{"ForgetAll", func(t *testing.T, store *GraphMemoryStore, alice, bob isoFixture) {
			n, err := store.ForgetAll(bob.ctx, isoAgent)
			require.NoError(t, err)
			assert.Equal(t, 1, n)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestGraphMemoryStore(t)
			alice := seedUser(t, store, "alice", "Mom birthday is in May")
			bob := seedUser(t, store, "bob", "Mom birthday is in October")

			before, err := store.GetMemory(alice.ctx, isoAgent, alice.mem.ID)
			require.NoError(t, err)

			tt.act(t, store, alice, bob)

			// Alice's graph is exactly as it was.
			after, err := store.GetMemory(alice.ctx, isoAgent, alice.mem.ID)
			require.NoError(t, err)
			assert.Nil(t, after.ExpiresAt, "alice's memory must not be forgotten")
			assert.False(t, after.IsSuperseded, "alice's memory must not be superseded")
			assert.InDelta(t, before.Salience, after.Salience, 1e-9, "alice's salience must not change")
			assert.Equal(t, before.AccessCount, after.AccessCount, "alice's access count must not change")

			lineage, err := store.GetLineage(alice.ctx, alice.mem.ID)
			require.NoError(t, err)
			assert.Empty(t, lineage)

			mom, err := store.GetEntity(alice.ctx, isoAgent, "mom")
			require.NoError(t, err)
			assert.Equal(t, alice.mom.ID, mom.ID)
			assert.Equal(t, "person", mom.EntityType)

			edges, err := store.ListEdgesFrom(alice.ctx, alice.mom.ID)
			require.NoError(t, err)
			require.Len(t, edges, 1)
			assert.Equal(t, `{"since":2020}`, edges[0].PropertiesJSON, "alice's edge properties must not change")

			recalled, err := store.Recall(alice.ctx, memory.RecallOpts{AgentID: isoAgent, Query: "birthday"})
			require.NoError(t, err)
			assert.Equal(t, []string{alice.mem.ID}, memoryIDs(recalled))
		})
	}
}

func TestGraphMemoryStore_ForgetSupersede_NotFound(t *testing.T) {
	store := newTestGraphMemoryStore(t)
	ctx := types.ContextWithUserID(context.Background(), "alice")

	assert.ErrorIs(t, store.Forget(ctx, "no-such-memory"), memory.ErrNotFound)
	_, err := store.Supersede(ctx, "no-such-memory", &memory.Memory{AgentID: isoAgent, Content: "x"})
	assert.ErrorIs(t, err, memory.ErrNotFound)
	_, err = store.GetMemory(ctx, isoAgent, "no-such-memory")
	assert.ErrorIs(t, err, memory.ErrNotFound)

	// A failed Supersede writes nothing.
	_, total, err := store.ListMemories(ctx, isoAgent, 10, 0)
	require.NoError(t, err)
	assert.Zero(t, total)

	// GetMemory is scoped to the agent too.
	mem, err := store.Remember(ctx, &memory.Memory{AgentID: isoAgent, Content: "fact"})
	require.NoError(t, err)
	_, err = store.GetMemory(ctx, "other-agent", mem.ID)
	assert.ErrorIs(t, err, memory.ErrNotFound)

	// Forgetting twice is not an error.
	require.NoError(t, store.Forget(ctx, mem.ID))
	require.NoError(t, store.Forget(ctx, mem.ID))
}

func TestGraphMemoryStore_SameEntityNamePerUser(t *testing.T) {
	store := newTestGraphMemoryStore(t)
	alice := types.ContextWithUserID(context.Background(), "alice")
	bob := types.ContextWithUserID(context.Background(), "bob")

	a, err := store.CreateEntity(alice, &memory.Entity{AgentID: isoAgent, Name: "mom", EntityType: "person"})
	require.NoError(t, err)
	b, err := store.CreateEntity(bob, &memory.Entity{AgentID: isoAgent, Name: "mom", EntityType: "person"})
	require.NoError(t, err, "two users may each have an entity named mom under one agent")
	assert.NotEqual(t, a.ID, b.ID)

	// Uniqueness still holds within one user.
	_, err = store.CreateEntity(alice, &memory.Entity{AgentID: isoAgent, Name: "mom", EntityType: "person"})
	assert.Error(t, err)

	// Deleting one user's entity leaves the other's.
	require.NoError(t, store.DeleteEntity(alice, isoAgent, "mom"))
	got, err := store.GetEntity(bob, isoAgent, "mom")
	require.NoError(t, err)
	assert.Equal(t, b.ID, got.ID)
	assert.Error(t, store.DeleteEntity(alice, isoAgent, "mom"), "alice's mom is gone; bob's is not hers to delete")
}

func TestGraphMemoryStore_ListMemories(t *testing.T) {
	store := newTestGraphMemoryStore(t)
	ctx := types.ContextWithUserID(context.Background(), "alice")

	remember := func(content string) *memory.Memory {
		m, err := store.Remember(ctx, &memory.Memory{AgentID: isoAgent, Content: content})
		require.NoError(t, err)
		return m
	}
	m1 := remember("first")
	m2 := remember("second")
	m3 := remember("third")
	m4 := remember("fourth")
	m5 := remember("fifth")
	// Other agent, same user: not listed.
	_, err := store.Remember(ctx, &memory.Memory{AgentID: "other-agent", Content: "elsewhere"})
	require.NoError(t, err)
	// Expired by TTL: not listed.
	past := time.Now().Add(-time.Hour)
	_, err = store.Remember(ctx, &memory.Memory{AgentID: isoAgent, Content: "expired", ExpiresAt: &past})
	require.NoError(t, err)

	require.NoError(t, store.Forget(ctx, m2.ID))
	m6, err := store.Supersede(ctx, m4.ID, &memory.Memory{AgentID: isoAgent, Content: "fourth, corrected"})
	require.NoError(t, err)

	// Active: m1, m3, m5, m6 — newest first.
	want := []string{m6.ID, m5.ID, m3.ID, m1.ID}

	tests := []struct {
		name          string
		limit, offset int
		want          []string
	}{
		{"all", 10, 0, want},
		{"default limit", 0, 0, want},
		{"first page", 2, 0, want[:2]},
		{"second page", 2, 2, want[2:]},
		{"past the end", 2, 10, nil},
		{"negative offset", 10, -1, want},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, total, err := store.ListMemories(ctx, isoAgent, tt.limit, tt.offset)
			require.NoError(t, err)
			assert.Equal(t, 4, total)
			if tt.want == nil {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, tt.want, memoryIDs(got))
		})
	}

	// ListMemories is a listing, not a recall: it must not touch access counters.
	m, err := store.GetMemory(ctx, isoAgent, m1.ID)
	require.NoError(t, err)
	assert.Zero(t, m.AccessCount)
}

func TestGraphMemoryStore_ForgetAll(t *testing.T) {
	store := newTestGraphMemoryStore(t)
	alice := seedUser(t, store, "alice", "Mom birthday is in May")
	bob := seedUser(t, store, "bob", "Mom birthday is in October")

	// Alice also has data under another agent, and a superseded memory.
	_, err := store.Remember(alice.ctx, &memory.Memory{AgentID: "other-agent", Content: "kept elsewhere"})
	require.NoError(t, err)
	_, err = store.CreateEntity(alice.ctx, &memory.Entity{AgentID: "other-agent", Name: "mom"})
	require.NoError(t, err)
	_, err = store.Supersede(alice.ctx, alice.mem.ID, &memory.Memory{
		AgentID: isoAgent, Content: "Mom birthday is May 4", EntityIDs: []string{alice.mom.ID},
	})
	require.NoError(t, err)

	n, err := store.ForgetAll(alice.ctx, isoAgent)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "count matches what ListMemories reported (superseded memory excluded)")

	// Nothing about alice is recalled under the agent.
	got, total, err := store.ListMemories(alice.ctx, isoAgent, 10, 0)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, got)
	recalled, err := store.Recall(alice.ctx, memory.RecallOpts{AgentID: isoAgent, Query: "mom birthday"})
	require.NoError(t, err)
	assert.Empty(t, recalled)
	vec, err := store.VectorRecall(alice.ctx, memory.VectorRecallOpts{
		AgentID: isoAgent, Embedding: []float32{1, 0, 0}, Model: "test-model",
	})
	require.NoError(t, err)
	assert.Empty(t, vec)
	ents, entTotal, err := store.ListEntities(alice.ctx, isoAgent, "", 10, 0)
	require.NoError(t, err)
	assert.Zero(t, entTotal)
	assert.Empty(t, ents)
	rc, err := store.ContextFor(alice.ctx, memory.ContextForOpts{AgentID: isoAgent, EntityName: "mom", MaxTokens: 4000})
	require.NoError(t, err)
	assert.Nil(t, rc.Entity)
	stats, err := store.GetStats(alice.ctx, isoAgent)
	require.NoError(t, err)
	assert.Zero(t, stats.EntityCount)
	assert.Zero(t, stats.EdgeCount)
	assert.Zero(t, stats.ActiveMemoryCount)
	var junction int
	require.NoError(t, store.db.QueryRow(
		`SELECT COUNT(*) FROM graph_memory_entities WHERE entity_id IN (?, ?)`, alice.mom.ID, alice.garden.ID,
	).Scan(&junction))
	assert.Zero(t, junction)

	// A second call has nothing left to forget.
	n, err = store.ForgetAll(alice.ctx, isoAgent)
	require.NoError(t, err)
	assert.Zero(t, n)

	// Alice can start over with the same names.
	_, err = store.CreateEntity(alice.ctx, &memory.Entity{AgentID: isoAgent, Name: "mom"})
	require.NoError(t, err)

	// Alice's other agent is untouched.
	_, otherTotal, err := store.ListMemories(alice.ctx, "other-agent", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, otherTotal)
	_, err = store.GetEntity(alice.ctx, "other-agent", "mom")
	require.NoError(t, err)

	// Bob is untouched.
	bobMems, bobTotal, err := store.ListMemories(bob.ctx, isoAgent, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, bobTotal)
	assert.Equal(t, []string{bob.mem.ID}, memoryIDs(bobMems))
	bobStats, err := store.GetStats(bob.ctx, isoAgent)
	require.NoError(t, err)
	assert.Equal(t, 2, bobStats.EntityCount)
	assert.Equal(t, 1, bobStats.EdgeCount)
	bobMem, err := store.GetMemory(bob.ctx, isoAgent, bob.mem.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{bob.mom.ID}, bobMem.EntityIDs)
}

// TestGraphMemoryStore_IdentityLessContextIsDefaultUser checks that a context
// without a user ID reads and writes as types.DefaultUserID, so single-user
// installs and rows written before per-user scoping keep working.
func TestGraphMemoryStore_IdentityLessContextIsDefaultUser(t *testing.T) {
	store := newTestGraphMemoryStore(t)
	anon := context.Background()
	explicit := types.ContextWithUserID(context.Background(), types.DefaultUserID)
	alice := types.ContextWithUserID(context.Background(), "alice")

	mem, err := store.Remember(anon, &memory.Memory{AgentID: isoAgent, Content: "written without identity"})
	require.NoError(t, err)
	_, err = store.CreateEntity(anon, &memory.Entity{AgentID: isoAgent, Name: "mom"})
	require.NoError(t, err)

	var owner string
	require.NoError(t, store.db.QueryRow(`SELECT user_id FROM graph_memories WHERE id = ?`, mem.ID).Scan(&owner))
	assert.Equal(t, types.DefaultUserID, owner)

	for name, ctx := range map[string]context.Context{"no identity": anon, "default-user": explicit} {
		t.Run(name, func(t *testing.T) {
			_, err := store.GetMemory(ctx, isoAgent, mem.ID)
			require.NoError(t, err)
			_, err = store.GetEntity(ctx, isoAgent, "mom")
			require.NoError(t, err)
			_, total, err := store.ListMemories(ctx, isoAgent, 10, 0)
			require.NoError(t, err)
			assert.Equal(t, 1, total)
		})
	}

	_, err = store.GetMemory(alice, isoAgent, mem.ID)
	assert.ErrorIs(t, err, memory.ErrNotFound)
	_, total, err := store.ListMemories(alice, isoAgent, 10, 0)
	require.NoError(t, err)
	assert.Zero(t, total)
}

// openMigrationTestDB opens a file database with foreign keys set as asked,
// via the DSN so every pooled connection gets the same setting.
func openMigrationTestDB(t *testing.T, foreignKeys bool) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "graph.db")
	db, err := sql.Open("sqlite3", sqlitedriver.DSN(path, sqlitedriver.Options{
		BusyTimeoutMS: 5000, WAL: true, ForeignKeys: foreignKeys,
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var fk int
	require.NoError(t, db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk))
	require.Equal(t, foreignKeys, fk == 1, "PRAGMA foreign_keys")
	return db
}

// migrateTo applies every embedded migration up to and including version.
func migrateTo(t *testing.T, m *Migrator, version int) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, m.ensureMigrationsTable(ctx))
	for _, mig := range m.migrations {
		if mig.Version <= version {
			require.NoError(t, m.applyMigration(ctx, mig), "migration %d", mig.Version)
		}
	}
}

// TestMigration000009_PreservesGraphData upgrades a version-8 database that
// already holds graph data and checks that entities, edges, memory links, the
// entity FTS index and its triggers all survive the graph_entities rebuild,
// with and without foreign keys enabled.
func TestMigration000009_PreservesGraphData(t *testing.T) {
	for _, fk := range []bool{true, false} {
		name := "foreign_keys_off"
		if fk {
			name = "foreign_keys_on"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db := openMigrationTestDB(t, fk)
			migrator, err := NewMigrator(db, observability.NewNoOpTracer())
			require.NoError(t, err)
			migrateTo(t, migrator, 8)

			// Version-8 data: rows written before per-user scoping are
			// default-user; one row belongs to another user.
			stmts := []string{
				`INSERT INTO graph_entities (id, agent_id, name, entity_type) VALUES ('e-mom', 'a1', 'mom', 'person')`,
				`INSERT INTO graph_entities (id, agent_id, name, entity_type) VALUES ('e-garden', 'a1', 'garden', 'place')`,
				`INSERT INTO graph_entities (id, agent_id, name, entity_type, user_id) VALUES ('e-carol-dog', 'a1', 'dog', 'animal', 'carol')`,
				`INSERT INTO graph_edges (id, agent_id, source_id, target_id, relation) VALUES ('g1', 'a1', 'e-mom', 'e-garden', 'LIKES')`,
				`INSERT INTO graph_memories (id, agent_id, content, summary, token_count) VALUES ('m1', 'a1', 'Mom tends the garden', '', 5)`,
				`INSERT INTO graph_memory_entities (memory_id, entity_id, role) VALUES ('m1', 'e-mom', 'about')`,
			}
			for _, s := range stmts {
				_, err := db.Exec(s)
				require.NoError(t, err, s)
			}
			// The old constraint rejects a second "mom" under the agent.
			_, err = db.Exec(`INSERT INTO graph_entities (id, agent_id, name, user_id) VALUES ('x', 'a1', 'mom', 'carol')`)
			require.Error(t, err)

			require.NoError(t, migrator.MigrateUp(ctx))
			version, err := migrator.CurrentVersion(ctx)
			require.NoError(t, err)
			require.GreaterOrEqual(t, version, 9)

			store := NewGraphMemoryStore(db, &mockTokenCounter{}, observability.NewNoOpTracer())
			def := context.Background()

			// Existing default-user data is intact and reachable.
			ents, total, err := store.ListEntities(def, "a1", "", 10, 0)
			require.NoError(t, err)
			assert.Equal(t, 2, total)
			assert.ElementsMatch(t, []string{"e-mom", "e-garden"}, entityIDs(ents))
			edges, err := store.ListEdgesFrom(def, "e-mom")
			require.NoError(t, err)
			require.Len(t, edges, 1)
			assert.Equal(t, "e-garden", edges[0].TargetID)
			mem, err := store.GetMemory(def, "a1", "m1")
			require.NoError(t, err)
			assert.Equal(t, []string{"e-mom"}, mem.EntityIDs)
			rc, err := store.ContextFor(def, memory.ContextForOpts{AgentID: "a1", EntityName: "mom", MaxTokens: 4000})
			require.NoError(t, err)
			require.NotNil(t, rc.Entity)
			require.Len(t, rc.Memories, 1)
			assert.Equal(t, "m1", rc.Memories[0].Memory.ID)

			// Entity FTS index survived without duplicates.
			found, err := store.SearchEntities(def, "a1", "garden", 10)
			require.NoError(t, err)
			assert.Equal(t, []string{"e-garden"}, entityIDs(found))
			var ftsRows int
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM graph_entities_fts`).Scan(&ftsRows))
			assert.Equal(t, 3, ftsRows)

			// Carol's pre-existing row stays hers.
			carol := types.ContextWithUserID(context.Background(), "carol")
			dog, err := store.GetEntity(carol, "a1", "dog")
			require.NoError(t, err)
			assert.Equal(t, "e-carol-dog", dog.ID)
			_, err = store.GetEntity(def, "a1", "dog")
			assert.Error(t, err)

			// New constraint: carol can now have her own "mom".
			carolMom, err := store.CreateEntity(carol, &memory.Entity{AgentID: "a1", Name: "mom", EntityType: "person"})
			require.NoError(t, err)

			// FTS triggers were recreated: insert, update and delete stay in sync.
			found, err = store.SearchEntities(carol, "a1", "mom", 10)
			require.NoError(t, err)
			assert.Equal(t, []string{carolMom.ID}, entityIDs(found))
			_, err = store.UpdateEntity(carol, &memory.Entity{AgentID: "a1", Name: "mom", EntityType: "grandparent"})
			require.NoError(t, err)
			found, err = store.SearchEntities(carol, "a1", "grandparent", 10)
			require.NoError(t, err)
			assert.Equal(t, []string{carolMom.ID}, entityIDs(found))
			require.NoError(t, store.DeleteEntity(carol, "a1", "mom"))
			found, err = store.SearchEntities(carol, "a1", "mom grandparent", 10)
			require.NoError(t, err)
			assert.Empty(t, found)

			// Foreign keys from edges and memory links still point at graph_entities.
			if fk {
				_, err = db.Exec(`INSERT INTO graph_edges (id, agent_id, source_id, target_id, relation) VALUES ('bad', 'a1', 'nope', 'e-mom', 'X')`)
				assert.Error(t, err, "edge FK must reference the rebuilt graph_entities")
				require.NoError(t, store.DeleteEntity(def, "a1", "mom"))
				var n int
				require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM graph_edges WHERE source_id = 'e-mom'`).Scan(&n))
				assert.Zero(t, n, "ON DELETE CASCADE from graph_entities to graph_edges")
				require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM graph_memory_entities WHERE entity_id = 'e-mom'`).Scan(&n))
				assert.Zero(t, n, "ON DELETE CASCADE from graph_entities to graph_memory_entities")
			}

			// The FK check passes and no scratch tables were left behind.
			rows, err := db.Query(`PRAGMA foreign_key_check`)
			require.NoError(t, err)
			assert.False(t, rows.Next(), "foreign_key_check reports violations")
			require.NoError(t, rows.Close())
			var leftovers int
			require.NoError(t, db.QueryRow(
				`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE '%m9%'`).Scan(&leftovers))
			assert.Zero(t, leftovers)

			// Indexes were recreated.
			for _, idx := range []string{
				"idx_graph_entities_agent", "idx_graph_entities_type", "idx_graph_entities_owner",
				"idx_graph_entities_user", "idx_graph_entities_agent_user_type",
				"idx_graph_edges_agent_user", "idx_graph_memories_agent_user_created",
			} {
				var n int
				require.NoError(t, db.QueryRow(
					`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&n))
				assert.Equal(t, 1, n, idx)
			}
		})
	}
}

// TestMigration000009_Down checks the rollback: it restores the old
// constraint while keeping data, and refuses (leaving the database
// unchanged) when two users share an entity name under one agent.
func TestMigration000009_Down(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t, true)
	migrator, err := NewMigrator(db, observability.NewNoOpTracer())
	require.NoError(t, err)
	migrateTo(t, migrator, 9)

	store := NewGraphMemoryStore(db, &mockTokenCounter{}, observability.NewNoOpTracer())
	alice := seedUser(t, store, "alice", "Mom birthday is in May")
	bob := seedUser(t, store, "bob", "Mom birthday is in October")

	// Duplicate names across users: rollback must fail and change nothing.
	require.Error(t, migrator.MigrateDown(ctx, 1))
	version, err := migrator.CurrentVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, 9, version)
	_, err = store.GetEntity(bob.ctx, isoAgent, "mom")
	require.NoError(t, err)
	edges, err := store.ListEdgesFrom(bob.ctx, bob.mom.ID)
	require.NoError(t, err)
	assert.Len(t, edges, 1)

	// Without the clash, rollback succeeds and keeps alice's graph.
	_, err = store.ForgetAll(bob.ctx, isoAgent)
	require.NoError(t, err)
	require.NoError(t, migrator.MigrateDown(ctx, 1))
	version, err = migrator.CurrentVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, 8, version)

	edges, err = store.ListEdgesFrom(alice.ctx, alice.mom.ID)
	require.NoError(t, err)
	assert.Len(t, edges, 1)
	mem, err := store.GetMemory(alice.ctx, isoAgent, alice.mem.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{alice.mom.ID}, mem.EntityIDs)
	found, err := store.SearchEntities(alice.ctx, isoAgent, "garden", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{alice.garden.ID}, entityIDs(found))

	// The old constraint is back.
	_, err = store.CreateEntity(bob.ctx, &memory.Entity{AgentID: isoAgent, Name: "mom"})
	assert.Error(t, err)
}
