-- Copyright 2026 Teradata
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.

-- 000025_graph_entities_per_user.up.sql
-- Graph memory is scoped per user: several people can share one agent, and
-- each may have their own entity with the same name (e.g. "mom"). RLS already
-- hides other users' rows, but UNIQUE(agent_id, name) still made the second
-- user's insert collide with the first user's row. Uniqueness becomes
-- (agent_id, user_id, name). Existing rows are unaffected: the old
-- constraint is stricter, so every existing row satisfies the new one.

ALTER TABLE graph_entities DROP CONSTRAINT IF EXISTS graph_entities_agent_id_name_key;
ALTER TABLE graph_entities
    ADD CONSTRAINT graph_entities_agent_id_user_id_name_key UNIQUE (agent_id, user_id, name);

-- Per-user lookup indexes for the queries that filter on user_id.
CREATE INDEX IF NOT EXISTS idx_graph_edges_agent_user ON graph_edges(agent_id, user_id);
CREATE INDEX IF NOT EXISTS idx_graph_memories_agent_user_created ON graph_memories(agent_id, user_id, created_at DESC);
