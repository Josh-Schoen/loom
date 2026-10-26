-- Copyright 2026 Teradata
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--     http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

-- 000010_graph_entities_per_user.down.sql
-- Restores UNIQUE(agent_id, name) on graph_entities.
--
-- This fails (and the migrator rolls the transaction back, leaving the
-- database unchanged) if two users under the same agent have an entity with
-- the same name: the old constraint cannot hold both rows. Remove or rename
-- one of them before rolling back.
--
-- Same rebuild procedure as the up migration; see the notes there.

DROP INDEX IF EXISTS idx_graph_edges_agent_user;
DROP INDEX IF EXISTS idx_graph_memories_agent_user_created;

CREATE TEMP TABLE _m9_graph_edges AS
    SELECT id, agent_id, source_id, target_id, relation, properties_json,
           user_id, created_at, updated_at, deleted_at
    FROM graph_edges;
CREATE TEMP TABLE _m9_graph_memory_entities AS
    SELECT memory_id, entity_id, role FROM graph_memory_entities;
DELETE FROM graph_memory_entities;
DELETE FROM graph_edges;

CREATE TABLE graph_entities_m9 (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    name TEXT NOT NULL,
    entity_type TEXT NOT NULL DEFAULT 'concept',
    properties_json TEXT NOT NULL DEFAULT '{}',
    owner TEXT,
    user_id TEXT NOT NULL DEFAULT 'default-user',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    deleted_at TEXT,
    UNIQUE(agent_id, name)
);

INSERT INTO graph_entities_m9
    (id, agent_id, name, entity_type, properties_json, owner, user_id,
     created_at, updated_at, deleted_at)
SELECT id, agent_id, name, entity_type, properties_json, owner, user_id,
       created_at, updated_at, deleted_at
FROM graph_entities;

DROP TABLE graph_entities;
ALTER TABLE graph_entities_m9 RENAME TO graph_entities;

CREATE INDEX IF NOT EXISTS idx_graph_entities_agent ON graph_entities(agent_id);
CREATE INDEX IF NOT EXISTS idx_graph_entities_type ON graph_entities(agent_id, entity_type);
CREATE INDEX IF NOT EXISTS idx_graph_entities_owner ON graph_entities(owner);
CREATE INDEX IF NOT EXISTS idx_graph_entities_user ON graph_entities(user_id);

CREATE TRIGGER IF NOT EXISTS graph_entities_fts_insert AFTER INSERT ON graph_entities
BEGIN
    INSERT INTO graph_entities_fts(entity_id, name, entity_type, properties_json)
    VALUES (NEW.id, NEW.name, NEW.entity_type, NEW.properties_json);
END;

CREATE TRIGGER IF NOT EXISTS graph_entities_fts_update AFTER UPDATE ON graph_entities
BEGIN
    DELETE FROM graph_entities_fts WHERE entity_id = OLD.id;
    INSERT INTO graph_entities_fts(entity_id, name, entity_type, properties_json)
    VALUES (NEW.id, NEW.name, NEW.entity_type, NEW.properties_json);
END;

CREATE TRIGGER IF NOT EXISTS graph_entities_fts_delete AFTER DELETE ON graph_entities
BEGIN
    DELETE FROM graph_entities_fts WHERE entity_id = OLD.id;
END;

INSERT INTO graph_edges
    (id, agent_id, source_id, target_id, relation, properties_json,
     user_id, created_at, updated_at, deleted_at)
SELECT id, agent_id, source_id, target_id, relation, properties_json,
       user_id, created_at, updated_at, deleted_at
FROM _m9_graph_edges;
INSERT INTO graph_memory_entities (memory_id, entity_id, role)
SELECT memory_id, entity_id, role FROM _m9_graph_memory_entities;

DROP TABLE _m9_graph_edges;
DROP TABLE _m9_graph_memory_entities;
