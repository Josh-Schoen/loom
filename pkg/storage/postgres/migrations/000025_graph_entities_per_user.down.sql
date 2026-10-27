-- Copyright 2026 Teradata
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.

-- 000025_graph_entities_per_user.down.sql
-- Restores UNIQUE(agent_id, name) on graph_entities.
--
-- Adding the constraint fails if two users under the same agent have an
-- entity with the same name; remove or rename one of them before rolling
-- back.

DROP INDEX IF EXISTS idx_graph_memories_agent_user_created;
DROP INDEX IF EXISTS idx_graph_edges_agent_user;

ALTER TABLE graph_entities DROP CONSTRAINT IF EXISTS graph_entities_agent_id_user_id_name_key;
ALTER TABLE graph_entities
    ADD CONSTRAINT graph_entities_agent_id_name_key UNIQUE (agent_id, name);
