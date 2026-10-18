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

//go:build integration

package postgres

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/teradata-labs/loom/pkg/memory"
)

// graphTesterPool returns a pool that logs in as an ordinary role (no
// superuser, no BYPASSRLS), so row-level security applies to it as it does
// to a deployed server. The superuser pool from testPool bypasses RLS even on
// FORCE ROW LEVEL SECURITY tables, so it only exercises the store's own
// user_id predicates.
func graphTesterPool(t *testing.T, admin *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'loom_graph_tester') THEN
				CREATE ROLE loom_graph_tester LOGIN PASSWORD 'graph-tester' NOSUPERUSER NOBYPASSRLS;
			END IF;
		END $$`,
		`GRANT USAGE ON SCHEMA public TO loom_graph_tester`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO loom_graph_tester`,
		`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO loom_graph_tester`,
		`GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO loom_graph_tester`,
	}
	for _, s := range stmts {
		_, err := admin.Exec(ctx, s)
		require.NoError(t, err, s)
	}
	u, err := url.Parse(admin.Config().ConnString())
	require.NoError(t, err)
	u.User = url.UserPassword("loom_graph_tester", "graph-tester")
	pool, err := pgxpool.New(ctx, u.String())
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// Two people share one agent. Run against both connections: the store's
// user_id predicates (superuser) and RLS on top (ordinary role).
func TestGraphMemory_PerUser(t *testing.T) {
	admin := testPool(t)
	pools := map[string]*pgxpool.Pool{"superuser": admin, "rls-role": graphTesterPool(t, admin)}
	for name, pool := range pools {
		t.Run(name, func(t *testing.T) {
			store := NewGraphMemoryStore(pool, nil, nil)
			agent := uniqueID("assistant")
			alex := ContextWithUserID(context.Background(), uniqueID("alex"))
			sam := ContextWithUserID(context.Background(), uniqueID("sam"))

			// Both can have an entity called "mom" under the same agent.
			_, err := store.CreateEntity(alex, &memory.Entity{AgentID: agent, Name: "mom", EntityType: "person"})
			require.NoError(t, err)
			_, err = store.CreateEntity(sam, &memory.Entity{AgentID: agent, Name: "mom", EntityType: "person"})
			require.NoError(t, err, "entity names are unique per user")

			fact, err := store.Remember(alex, &memory.Memory{AgentID: agent, Content: "Alex is allergic to peanuts", MemoryType: memory.MemoryTypeFact, Salience: 0.8})
			require.NoError(t, err)
			_, err = store.Remember(alex, &memory.Memory{AgentID: agent, Content: "Alex lives in Austin", MemoryType: memory.MemoryTypeFact, Salience: 0.5})
			require.NoError(t, err)

			// Sam sees none of Alex's memories or entities.
			list, total, err := store.ListMemories(sam, agent, 50, 0)
			require.NoError(t, err)
			assert.Empty(t, list)
			assert.Zero(t, total)
			recalled, err := store.Recall(sam, memory.RecallOpts{AgentID: agent, Query: "peanuts"})
			require.NoError(t, err)
			assert.Empty(t, recalled, "Alex's facts must not be recalled for Sam")
			_, err = store.GetMemory(sam, agent, fact.ID)
			assert.True(t, errors.Is(err, memory.ErrNotFound), "%v", err)
			entities, _, err := store.ListEntities(sam, agent, "", 50, 0)
			require.NoError(t, err)
			require.Len(t, entities, 1, "only Sam's own mom")

			// Sam can't forget or correct Alex's memory.
			assert.True(t, errors.Is(store.Forget(sam, fact.ID), memory.ErrNotFound))
			_, err = store.Supersede(sam, fact.ID, &memory.Memory{AgentID: agent, Content: "changed"})
			assert.True(t, errors.Is(err, memory.ErrNotFound), "%v", err)

			// Alex: newest first; a correction replaces; a forgotten one is gone.
			list, total, err = store.ListMemories(alex, agent, 50, 0)
			require.NoError(t, err)
			require.Equal(t, 2, total)
			assert.Equal(t, "Alex lives in Austin", list[0].Content)
			corrected, err := store.Supersede(alex, fact.ID, &memory.Memory{AgentID: agent, Content: "Alex is allergic to tree nuts", MemoryType: memory.MemoryTypeFact, Salience: 0.8})
			require.NoError(t, err)
			list, _, err = store.ListMemories(alex, agent, 50, 0)
			require.NoError(t, err)
			var contents []string
			for _, m := range list {
				contents = append(contents, m.Content)
			}
			assert.ElementsMatch(t, []string{"Alex lives in Austin", "Alex is allergic to tree nuts"}, contents)
			require.NoError(t, store.Forget(alex, corrected.ID))
			_, total, err = store.ListMemories(alex, agent, 50, 0)
			require.NoError(t, err)
			assert.Equal(t, 1, total)

			// Forget everything: Alex's memories and entities only.
			_, err = store.Remember(sam, &memory.Memory{AgentID: agent, Content: "Sam lives in Denver", MemoryType: memory.MemoryTypeFact, Salience: 0.5})
			require.NoError(t, err)
			n, err := store.ForgetAll(alex, agent)
			require.NoError(t, err)
			assert.Equal(t, 1, n)
			_, total, err = store.ListMemories(alex, agent, 50, 0)
			require.NoError(t, err)
			assert.Zero(t, total)
			entities, _, err = store.ListEntities(alex, agent, "", 50, 0)
			require.NoError(t, err)
			assert.Empty(t, entities)
			_, total, err = store.ListMemories(sam, agent, 50, 0)
			require.NoError(t, err)
			assert.Equal(t, 1, total, "Sam's memories stay")
			entities, _, err = store.ListEntities(sam, agent, "", 50, 0)
			require.NoError(t, err)
			assert.Len(t, entities, 1, "Sam's entities stay")
		})
	}
}
