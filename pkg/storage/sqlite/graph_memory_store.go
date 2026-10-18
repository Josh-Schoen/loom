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

package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/teradata-labs/loom/pkg/memory"
	"github.com/teradata-labs/loom/pkg/observability"
	"github.com/teradata-labs/loom/pkg/types"
)

// graphUserID returns the user that owns graph memory rows written or read on
// behalf of ctx. A context without an identity maps to types.DefaultUserID,
// which is also the column default, so single-user installs (and rows written
// before per-user scoping) keep working unchanged.
func graphUserID(ctx context.Context) string {
	if u := types.UserIDFromContext(ctx); u != "" {
		return u
	}
	return types.DefaultUserID
}

// sqlPlaceholders returns n comma-separated "?" placeholders.
func sqlPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// GraphMemoryStore implements memory.GraphMemoryStore for SQLite.
type GraphMemoryStore struct {
	db      *sql.DB
	tc      memory.TokenCounter
	tracer  observability.Tracer
	salConf memory.SalienceConfig
}

// NewGraphMemoryStore creates a new SQLite-backed graph memory store.
func NewGraphMemoryStore(db *sql.DB, tc memory.TokenCounter, tracer observability.Tracer, opts ...GraphMemoryOption) *GraphMemoryStore {
	if tracer == nil {
		tracer = observability.NewNoOpTracer()
	}
	s := &GraphMemoryStore{db: db, tc: tc, tracer: tracer, salConf: memory.DefaultSalienceConfig()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// GraphMemoryOption configures a GraphMemoryStore.
type GraphMemoryOption func(*GraphMemoryStore)

// WithSalienceConfig overrides the default salience configuration.
func WithSalienceConfig(cfg memory.SalienceConfig) GraphMemoryOption {
	return func(s *GraphMemoryStore) {
		s.salConf = cfg
	}
}

// Compile-time interface check.
var _ memory.GraphMemoryStore = (*GraphMemoryStore)(nil)

// =============================================================================
// Entity CRUD
// =============================================================================

func (s *GraphMemoryStore) CreateEntity(ctx context.Context, entity *memory.Entity) (*memory.Entity, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.create_entity")
	defer s.tracer.EndSpan(span)

	if entity.ID == "" {
		entity.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	entity.CreatedAt = now
	entity.UpdatedAt = now

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO graph_entities (id, agent_id, name, entity_type, properties_json, owner, user_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, datetime(?), datetime(?))`,
		entity.ID, entity.AgentID, entity.Name, entity.EntityType,
		entity.PropertiesJSON, entity.Owner, graphUserID(ctx),
		now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("create entity: %w", err)
	}
	return entity, nil
}

func (s *GraphMemoryStore) GetEntity(ctx context.Context, agentID, name string) (*memory.Entity, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.get_entity")
	defer s.tracer.EndSpan(span)

	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_id, name, entity_type, properties_json, COALESCE(owner,''), created_at, updated_at
		 FROM graph_entities WHERE agent_id = ? AND user_id = ? AND name = ?`,
		agentID, graphUserID(ctx), name,
	)
	return scanEntity(row)
}

func (s *GraphMemoryStore) UpdateEntity(ctx context.Context, entity *memory.Entity) (*memory.Entity, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.update_entity")
	defer s.tracer.EndSpan(span)

	now := time.Now().UTC()
	entity.UpdatedAt = now

	result, err := s.db.ExecContext(ctx,
		`UPDATE graph_entities SET entity_type = ?, properties_json = ?, updated_at = datetime(?)
		 WHERE agent_id = ? AND user_id = ? AND name = ?`,
		entity.EntityType, entity.PropertiesJSON, now.Format(time.RFC3339),
		entity.AgentID, graphUserID(ctx), entity.Name,
	)
	if err != nil {
		return nil, fmt.Errorf("update entity: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return nil, fmt.Errorf("entity not found: agent_id=%s name=%s", entity.AgentID, entity.Name)
	}

	return s.GetEntity(ctx, entity.AgentID, entity.Name)
}

func (s *GraphMemoryStore) ListEntities(ctx context.Context, agentID, entityType string, limit, offset int) ([]*memory.Entity, int, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.list_entities")
	defer s.tracer.EndSpan(span)

	if limit <= 0 {
		limit = 50
	}

	var countQuery, query string
	var args []interface{}

	if entityType != "" {
		countQuery = `SELECT COUNT(*) FROM graph_entities WHERE agent_id = ? AND user_id = ? AND entity_type = ?`
		query = `SELECT id, agent_id, name, entity_type, properties_json, COALESCE(owner,''), created_at, updated_at
				 FROM graph_entities WHERE agent_id = ? AND user_id = ? AND entity_type = ?
				 ORDER BY name LIMIT ? OFFSET ?`
		args = []interface{}{agentID, graphUserID(ctx), entityType}
	} else {
		countQuery = `SELECT COUNT(*) FROM graph_entities WHERE agent_id = ? AND user_id = ?`
		query = `SELECT id, agent_id, name, entity_type, properties_json, COALESCE(owner,''), created_at, updated_at
				 FROM graph_entities WHERE agent_id = ? AND user_id = ?
				 ORDER BY name LIMIT ? OFFSET ?`
		args = []interface{}{agentID, graphUserID(ctx)}
	}

	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count entities: %w", err)
	}

	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list entities: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var entities []*memory.Entity
	for rows.Next() {
		e, err := scanEntityFromRows(rows)
		if err != nil {
			return nil, 0, err
		}
		entities = append(entities, e)
	}
	return entities, total, rows.Err()
}

func (s *GraphMemoryStore) SearchEntities(ctx context.Context, agentID, query string, limit int) ([]*memory.Entity, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.search_entities")
	defer s.tracer.EndSpan(span)

	if limit <= 0 {
		limit = 20
	}

	ftsQuery := toFTS5OrQuery(query)
	if ftsQuery == "" {
		// No usable search terms. An empty MATCH expression is itself a hard
		// FTS5 syntax error, and there is no non-FTS entity search to degrade
		// to (ListEntities is an unfiltered listing, not a search), so report
		// no matches without querying rather than turning a term-less search
		// into an error.
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id, e.agent_id, e.name, e.entity_type, e.properties_json, COALESCE(e.owner,''), e.created_at, e.updated_at
		 FROM graph_entities e
		 JOIN graph_entities_fts f ON f.entity_id = e.id
		 WHERE f.graph_entities_fts MATCH ? AND e.agent_id = ? AND e.user_id = ?
		 ORDER BY rank LIMIT ?`,
		ftsQuery, agentID, graphUserID(ctx), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search entities: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var entities []*memory.Entity
	for rows.Next() {
		e, err := scanEntityFromRows(rows)
		if err != nil {
			return nil, err
		}
		entities = append(entities, e)
	}
	return entities, rows.Err()
}

func (s *GraphMemoryStore) DeleteEntity(ctx context.Context, agentID, name string) error {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.delete_entity")
	defer s.tracer.EndSpan(span)

	result, err := s.db.ExecContext(ctx,
		`DELETE FROM graph_entities WHERE agent_id = ? AND user_id = ? AND name = ?`,
		agentID, graphUserID(ctx), name,
	)
	if err != nil {
		return fmt.Errorf("delete entity: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("entity not found: agent_id=%s name=%s", agentID, name)
	}
	return nil
}

// =============================================================================
// Edge CRUD
// =============================================================================

func (s *GraphMemoryStore) Relate(ctx context.Context, edge *memory.Edge) (*memory.Edge, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.relate")
	defer s.tracer.EndSpan(span)

	if edge.ID == "" {
		edge.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	edge.CreatedAt = now
	edge.UpdatedAt = now

	userID := graphUserID(ctx)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Both endpoints must be the caller's entities: an edge must never join
	// (or reveal) another user's graph.
	if err := requireOwnedEntitiesTx(ctx, tx, userID, edge.SourceID, edge.TargetID); err != nil {
		return nil, fmt.Errorf("relate: %w", err)
	}

	// Upsert: ON CONFLICT update properties and timestamp. The WHERE keeps the
	// update on the caller's own edge.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO graph_edges (id, agent_id, source_id, target_id, relation, properties_json, user_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, datetime(?), datetime(?))
		 ON CONFLICT(source_id, target_id, relation) DO UPDATE SET
		   properties_json = excluded.properties_json,
		   updated_at = excluded.updated_at
		 WHERE graph_edges.user_id = excluded.user_id`,
		edge.ID, edge.AgentID, edge.SourceID, edge.TargetID, edge.Relation,
		edge.PropertiesJSON, userID, now.Format(time.RFC3339), now.Format(time.RFC3339),
	); err != nil {
		return nil, fmt.Errorf("relate: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit relate: %w", err)
	}
	return edge, nil
}

func (s *GraphMemoryStore) Unrelate(ctx context.Context, sourceID, targetID, relation string) error {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.unrelate")
	defer s.tracer.EndSpan(span)

	_, err := s.db.ExecContext(ctx,
		`DELETE FROM graph_edges WHERE source_id = ? AND target_id = ? AND relation = ? AND user_id = ?`,
		sourceID, targetID, relation, graphUserID(ctx),
	)
	return err
}

func (s *GraphMemoryStore) Neighbors(ctx context.Context, entityID string, relation string, direction string, depth int) ([]*memory.Edge, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.neighbors")
	defer s.tracer.EndSpan(span)

	if depth <= 0 {
		depth = 1
	}

	// Numbered parameters: ?1 entity ID, ?2 relation filter ('' = any),
	// ?3 max depth, ?4 caller's user ID. Every recursive step is restricted
	// to the caller's edges so a traversal can't walk into another user's graph.
	var query string
	switch direction {
	case "inbound":
		query = `WITH RECURSIVE traverse(eid, target_id, source_id, depth, path) AS (
			SELECT e.id, e.target_id, e.source_id, 1, e.target_id || ',' || e.source_id
			FROM graph_edges e
			WHERE e.target_id = ?1 AND (?2 = '' OR e.relation = ?2) AND e.user_id = ?4
			UNION ALL
			SELECT e.id, e.target_id, e.source_id, t.depth + 1, t.path || ',' || e.source_id
			FROM graph_edges e
			JOIN traverse t ON e.target_id = t.source_id
			WHERE t.depth < ?3 AND t.path NOT LIKE '%' || e.source_id || '%'
			  AND (?2 = '' OR e.relation = ?2) AND e.user_id = ?4
		)
		SELECT e.id, e.agent_id, e.source_id, e.target_id, e.relation, e.properties_json, e.created_at, e.updated_at
		FROM traverse t
		JOIN graph_edges e ON e.id = t.eid`
	case "both":
		query = `WITH RECURSIVE traverse(eid, from_id, to_id, depth, path) AS (
			SELECT e.id, e.source_id, e.target_id, 1, e.source_id || ',' || e.target_id
			FROM graph_edges e
			WHERE (e.source_id = ?1 OR e.target_id = ?1) AND (?2 = '' OR e.relation = ?2) AND e.user_id = ?4
			UNION ALL
			SELECT e.id, e.source_id, e.target_id, t.depth + 1, t.path || ',' || e.target_id
			FROM graph_edges e
			JOIN traverse t ON (e.source_id = t.to_id OR e.target_id = t.from_id)
			WHERE t.depth < ?3 AND t.path NOT LIKE '%' || e.target_id || '%'
			  AND (?2 = '' OR e.relation = ?2) AND e.user_id = ?4
		)
		SELECT e.id, e.agent_id, e.source_id, e.target_id, e.relation, e.properties_json, e.created_at, e.updated_at
		FROM traverse t
		JOIN graph_edges e ON e.id = t.eid`
	default: // outbound
		query = `WITH RECURSIVE traverse(eid, source_id, target_id, depth, path) AS (
			SELECT e.id, e.source_id, e.target_id, 1, e.source_id || ',' || e.target_id
			FROM graph_edges e
			WHERE e.source_id = ?1 AND (?2 = '' OR e.relation = ?2) AND e.user_id = ?4
			UNION ALL
			SELECT e.id, e.source_id, e.target_id, t.depth + 1, t.path || ',' || e.target_id
			FROM graph_edges e
			JOIN traverse t ON e.source_id = t.target_id
			WHERE t.depth < ?3 AND t.path NOT LIKE '%' || e.target_id || '%'
			  AND (?2 = '' OR e.relation = ?2) AND e.user_id = ?4
		)
		SELECT e.id, e.agent_id, e.source_id, e.target_id, e.relation, e.properties_json, e.created_at, e.updated_at
		FROM traverse t
		JOIN graph_edges e ON e.id = t.eid`
	}
	args := []interface{}{entityID, relation, depth, graphUserID(ctx)}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("neighbors: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var edges []*memory.Edge
	seen := make(map[string]bool)
	for rows.Next() {
		e, err := scanEdgeFromRows(rows)
		if err != nil {
			return nil, err
		}
		if !seen[e.ID] {
			edges = append(edges, e)
			seen[e.ID] = true
		}
	}
	return edges, rows.Err()
}

func (s *GraphMemoryStore) ListEdgesFrom(ctx context.Context, entityID string) ([]*memory.Edge, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_id, source_id, target_id, relation, properties_json, created_at, updated_at
		 FROM graph_edges WHERE source_id = ? AND user_id = ? ORDER BY relation`, entityID, graphUserID(ctx))
	if err != nil {
		return nil, fmt.Errorf("list edges from: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	return scanEdgeRows(rows)
}

func (s *GraphMemoryStore) ListEdgesTo(ctx context.Context, entityID string) ([]*memory.Edge, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_id, source_id, target_id, relation, properties_json, created_at, updated_at
		 FROM graph_edges WHERE target_id = ? AND user_id = ? ORDER BY relation`, entityID, graphUserID(ctx))
	if err != nil {
		return nil, fmt.Errorf("list edges to: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	return scanEdgeRows(rows)
}

func scanEdgeRows(rows *sql.Rows) ([]*memory.Edge, error) {
	var edges []*memory.Edge
	for rows.Next() {
		e, err := scanEdgeFromRows(rows)
		if err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// =============================================================================
// Memory Operations
// =============================================================================

func (s *GraphMemoryStore) Remember(ctx context.Context, mem *memory.Memory) (*memory.Memory, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.remember")
	defer s.tracer.EndSpan(span)

	s.prepareMemory(mem)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	userID := graphUserID(ctx)
	if err := insertMemoryTx(ctx, tx, userID, mem); err != nil {
		return nil, err
	}
	if err := linkEntitiesTx(ctx, tx, userID, mem); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return mem, nil
}

func (s *GraphMemoryStore) GetMemory(ctx context.Context, agentID, memoryID string) (*memory.Memory, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.get_memory")
	defer s.tracer.EndSpan(span)

	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_id, content, summary, memory_type, COALESCE(source,''), COALESCE(source_id,''),
		        COALESCE(owner,''), COALESCE(memory_agent_id,''), tags, salience,
		        token_count, summary_token_count, access_count, properties_json,
		        created_at, accessed_at, expires_at,
		        COALESCE(event_date,''), COALESCE(event_date_confidence,'')
		 FROM graph_memories WHERE agent_id = ? AND user_id = ? AND id = ?`,
		agentID, graphUserID(ctx), memoryID,
	)
	mem, err := scanMemory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get memory %s: %w", memoryID, memory.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}

	// Load entity IDs from junction table.
	eids, err := s.loadMemoryEntityIDs(ctx, memoryID)
	if err != nil {
		return nil, err
	}
	mem.EntityIDs = eids

	// Check superseded status.
	var supersededCount int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_memory_lineage WHERE old_memory_id = ? AND relation_type = 'SUPERSEDES'`,
		memoryID,
	).Scan(&supersededCount); err != nil {
		return nil, fmt.Errorf("check superseded status: %w", err)
	}
	mem.IsSuperseded = supersededCount > 0

	return mem, nil
}

func (s *GraphMemoryStore) Recall(ctx context.Context, opts memory.RecallOpts) ([]*memory.Memory, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.recall")
	defer s.tracer.EndSpan(span)

	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.MinSalience <= 0 {
		opts.MinSalience = memory.DefaultMinSalience
	}

	var conditions []string
	var args []interface{}

	conditions = append(conditions, "m.agent_id = ?", "m.user_id = ?")
	args = append(args, opts.AgentID, graphUserID(ctx))

	// Exclude expired.
	conditions = append(conditions, "(m.expires_at IS NULL OR m.expires_at > datetime('now'))")

	// Exclude superseded.
	conditions = append(conditions,
		`m.id NOT IN (SELECT old_memory_id FROM graph_memory_lineage WHERE relation_type = 'SUPERSEDES')`)

	// Salience threshold.
	conditions = append(conditions, "m.salience >= ?")
	args = append(args, opts.MinSalience)

	// Optional type filter.
	if opts.MemoryType != "" {
		conditions = append(conditions, "m.memory_type = ?")
		args = append(args, opts.MemoryType)
	}

	// Optional entity scope.
	if len(opts.EntityIDs) > 0 {
		placeholders := make([]string, len(opts.EntityIDs))
		for i, eid := range opts.EntityIDs {
			placeholders[i] = "?"
			args = append(args, eid)
		}
		conditions = append(conditions, fmt.Sprintf(
			`m.id IN (SELECT memory_id FROM graph_memory_entities WHERE entity_id IN (%s))`,
			strings.Join(placeholders, ",")))
	}

	// Optional tag filter: match exact JSON string values within the tags array.
	// Uses instr with JSON double-quote delimiters to avoid substring false positives
	// (e.g., searching for "test" won't match "test_data").
	if len(opts.Tags) > 0 {
		for _, tag := range opts.Tags {
			conditions = append(conditions, `instr(m.tags, '"' || ? || '"') > 0`)
			args = append(args, tag)
		}
	}

	where := strings.Join(conditions, " AND ")

	// Convert the query to OR semantics for FTS5. Space-separated terms default
	// to AND in FTS5 (all must match), which is too strict for memory recall.
	// OR returns any match and lets BM25 rank multi-term matches higher.
	//
	// toFTS5OrQuery yields "" when the text holds no usable terms (punctuation
	// only, for instance). An empty MATCH expression is itself a hard FTS5
	// syntax error, so an empty result means "skip the FTS join" — fall
	// through to the salience-ordered branch instead of failing the whole
	// recall.
	var ftsQuery string
	if opts.Query != "" {
		ftsQuery = toFTS5OrQuery(opts.Query)
	}

	var query string
	if ftsQuery != "" {
		// FTS search with BM25 ranking.
		query = fmt.Sprintf(
			`SELECT m.id, m.agent_id, m.content, m.summary, m.memory_type,
			        COALESCE(m.source,''), COALESCE(m.source_id,''), COALESCE(m.owner,''),
			        COALESCE(m.memory_agent_id,''), m.tags, m.salience,
			        m.token_count, m.summary_token_count, m.access_count, m.properties_json,
			        m.created_at, m.accessed_at, m.expires_at,
			        COALESCE(m.event_date,''), COALESCE(m.event_date_confidence,'')
			 FROM graph_memories m
			 JOIN graph_memories_fts f ON f.memory_id = m.id
			 WHERE f.graph_memories_fts MATCH ? AND %s
			 ORDER BY (m.salience * (-rank)) DESC, m.created_at DESC
			 LIMIT ?`, where)
		args = append([]interface{}{ftsQuery}, args...)
	} else {
		query = fmt.Sprintf(
			`SELECT m.id, m.agent_id, m.content, m.summary, m.memory_type,
			        COALESCE(m.source,''), COALESCE(m.source_id,''), COALESCE(m.owner,''),
			        COALESCE(m.memory_agent_id,''), m.tags, m.salience,
			        m.token_count, m.summary_token_count, m.access_count, m.properties_json,
			        m.created_at, m.accessed_at, m.expires_at,
			        COALESCE(m.event_date,''), COALESCE(m.event_date_confidence,'')
			 FROM graph_memories m
			 WHERE %s
			 ORDER BY m.salience DESC, m.created_at DESC
			 LIMIT ?`, where)
	}
	args = append(args, opts.Limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("recall: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var memories []*memory.Memory
	for rows.Next() {
		mem, err := scanMemoryFromRows(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, mem)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Touch accessed memories.
	if len(memories) > 0 {
		ids := make([]string, len(memories))
		for i, m := range memories {
			ids[i] = m.ID
		}
		if err := s.TouchMemories(ctx, ids); err != nil {
			span.RecordError(fmt.Errorf("touch memories after recall: %w", err))
		}
	}

	return memories, nil
}

// VectorRecall performs brute-force cosine similarity search over all memories
// with embeddings for the given agent. Returns memories sorted by similarity.
// This is O(n) and fine for <100K memories per agent. For larger scale, use
// PostgreSQL with pgvector HNSW index.
func (s *GraphMemoryStore) VectorRecall(ctx context.Context, opts memory.VectorRecallOpts) ([]*memory.Memory, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.vector_recall")
	defer s.tracer.EndSpan(span)

	if len(opts.Embedding) == 0 {
		return nil, nil
	}
	if opts.Limit <= 0 {
		opts.Limit = 20
	}

	if opts.Model == "" {
		return nil, nil // no model specified, skip vector recall
	}

	// Fetch non-expired, non-superseded memories with embeddings from the same model.
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.agent_id, m.content, m.summary, m.memory_type,
		        COALESCE(m.source,''), COALESCE(m.source_id,''), COALESCE(m.owner,''),
		        COALESCE(m.memory_agent_id,''), m.tags, m.salience,
		        m.token_count, m.summary_token_count, m.access_count, m.properties_json,
		        m.created_at, m.accessed_at, m.expires_at,
		        COALESCE(m.event_date,''), COALESCE(m.event_date_confidence,''),
		        m.embedding
		 FROM graph_memories m
		 WHERE m.agent_id = ?
		   AND m.user_id = ?
		   AND m.embedding_model = ?
		   AND (m.expires_at IS NULL OR m.expires_at > datetime('now'))
		   AND m.embedding IS NOT NULL
		   AND m.id NOT IN (SELECT old_memory_id FROM graph_memory_lineage WHERE relation_type = 'SUPERSEDES')
		 ORDER BY m.created_at DESC`,
		opts.AgentID, graphUserID(ctx), opts.Model,
	)
	if err != nil {
		return nil, fmt.Errorf("vector recall query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	type scored struct {
		mem   *memory.Memory
		score float32
	}
	var results []scored

	for rows.Next() {
		mem, embBytes, err := scanMemoryWithEmbedding(rows)
		if err != nil {
			continue
		}
		emb := bytesToFloat32(embBytes)
		if len(emb) == 0 {
			continue
		}
		sim := memory.CosineSimilarity(opts.Embedding, emb)
		if sim >= opts.Threshold {
			results = append(results, scored{mem: mem, score: sim})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sort by similarity descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Take top-K.
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	memories := make([]*memory.Memory, len(results))
	for i, r := range results {
		memories[i] = r.mem
	}

	// Touch accessed memories.
	if len(memories) > 0 {
		ids := make([]string, len(memories))
		for i, m := range memories {
			ids[i] = m.ID
		}
		_ = s.TouchMemories(ctx, ids)
	}

	return memories, nil
}

// scanMemoryWithEmbedding scans a memory row that includes an embedding BLOB column.
func scanMemoryWithEmbedding(rows *sql.Rows) (*memory.Memory, []byte, error) {
	var m memory.Memory
	var tagsJSON, createdAtStr string
	var accessedAt, expiresAt sql.NullString
	var embBytes []byte

	err := rows.Scan(
		&m.ID, &m.AgentID, &m.Content, &m.Summary, &m.MemoryType,
		&m.Source, &m.SourceID, &m.Owner, &m.MemoryAgentID,
		&tagsJSON, &m.Salience, &m.TokenCount, &m.SummaryTokenCount,
		&m.AccessCount, &m.PropertiesJSON, &createdAtStr, &accessedAt, &expiresAt,
		&m.EventDate, &m.EventDateConfidence,
		&embBytes,
	)
	if err != nil {
		return nil, nil, err
	}

	m.CreatedAt = parseTime(createdAtStr)
	if tagsJSON != "" && tagsJSON != "null" {
		_ = json.Unmarshal([]byte(tagsJSON), &m.Tags)
	}
	if accessedAt.Valid && accessedAt.String != "" {
		t := parseTime(accessedAt.String)
		m.AccessedAt = &t
	}
	if expiresAt.Valid && expiresAt.String != "" {
		t := parseTime(expiresAt.String)
		m.ExpiresAt = &t
	}

	return &m, embBytes, nil
}

// bytesToFloat32 converts a byte slice to float32 slice (little-endian).
func bytesToFloat32(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	result := make([]float32, len(b)/4)
	for i := range result {
		bits := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
		result[i] = math.Float32frombits(bits)
	}
	return result
}

// float32ToBytes converts a float32 slice to bytes (little-endian).
// Uses encoding/binary rather than manual byte shifts so gosec's
// G115 false-positives on the intentional uint32→byte truncation
// do not need suppression comments.
func float32ToBytes(f []float32) []byte {
	b := make([]byte, len(f)*4)
	for i, v := range f {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(v))
	}
	return b
}

func (s *GraphMemoryStore) Forget(ctx context.Context, memoryID string) error {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.forget")
	defer s.tracer.EndSpan(span)

	// Already-expired memories keep their original expiry (forgetting twice
	// is not an error) but still count as matched, so only a memory the
	// caller does not own, or one that does not exist, yields ErrNotFound.
	result, err := s.db.ExecContext(ctx,
		`UPDATE graph_memories
		 SET expires_at = CASE
		       WHEN expires_at IS NOT NULL AND expires_at <= datetime('now') THEN expires_at
		       ELSE datetime('now') END
		 WHERE id = ? AND user_id = ?`,
		memoryID, graphUserID(ctx),
	)
	if err != nil {
		return fmt.Errorf("forget: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("forget: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("forget memory %s: %w", memoryID, memory.ErrNotFound)
	}
	return nil
}

func (s *GraphMemoryStore) Supersede(ctx context.Context, oldMemoryID string, newMem *memory.Memory) (*memory.Memory, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.supersede")
	defer s.tracer.EndSpan(span)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	userID := graphUserID(ctx)

	// Get old memory's salience (caller's memories only).
	var oldSalience float64
	err = tx.QueryRowContext(ctx,
		`SELECT salience FROM graph_memories WHERE id = ? AND user_id = ?`, oldMemoryID, userID,
	).Scan(&oldSalience)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("supersede memory %s: %w", oldMemoryID, memory.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get old memory: %w", err)
	}

	// Inherit salience from old memory.
	if newMem.Salience == 0 {
		newMem.Salience = oldSalience
	}

	s.prepareMemory(newMem)

	if err := insertMemoryTx(ctx, tx, userID, newMem); err != nil {
		return nil, err
	}
	if err := linkEntitiesTx(ctx, tx, userID, newMem); err != nil {
		return nil, err
	}

	// Insert lineage within the same transaction.
	_, err = tx.ExecContext(ctx,
		`INSERT INTO graph_memory_lineage (new_memory_id, old_memory_id, relation_type, created_at)
		 VALUES (?, ?, 'SUPERSEDES', datetime('now'))`,
		newMem.ID, oldMemoryID,
	)
	if err != nil {
		return nil, fmt.Errorf("insert lineage: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit supersede: %w", err)
	}

	return newMem, nil
}

func (s *GraphMemoryStore) Consolidate(ctx context.Context, memoryIDs []string, consolidated *memory.Memory) (*memory.Memory, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.consolidate")
	defer s.tracer.EndSpan(span)

	if consolidated.MemoryType == "" {
		consolidated.MemoryType = memory.MemoryTypeConsolidation
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	userID := graphUserID(ctx)

	// Every source must be one of the caller's memories; otherwise nothing
	// is written (consolidating another user's memory would leak it).
	uniqueIDs := dedupeStrings(memoryIDs)
	if len(uniqueIDs) == 0 {
		return nil, fmt.Errorf("consolidate: no source memories")
	}
	ownedArgs := make([]interface{}, 0, len(uniqueIDs)+1)
	for _, id := range uniqueIDs {
		ownedArgs = append(ownedArgs, id)
	}
	ownedArgs = append(ownedArgs, userID)
	var owned int
	var maxSalience sql.NullFloat64
	if err := tx.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*), MAX(salience) FROM graph_memories WHERE id IN (%s) AND user_id = ?`, // #nosec G201 -- placeholders are "?" only
			sqlPlaceholders(len(uniqueIDs))),
		ownedArgs...,
	).Scan(&owned, &maxSalience); err != nil {
		return nil, fmt.Errorf("check source memories: %w", err)
	}
	if owned != len(uniqueIDs) {
		return nil, fmt.Errorf("consolidate: %d of %d source memories: %w",
			len(uniqueIDs)-owned, len(uniqueIDs), memory.ErrNotFound)
	}

	// Inherit max salience from sources.
	if consolidated.Salience == 0 && maxSalience.Valid {
		consolidated.Salience = maxSalience.Float64
	}

	s.prepareMemory(consolidated)

	if err := insertMemoryTx(ctx, tx, userID, consolidated); err != nil {
		return nil, err
	}
	if err := linkEntitiesTx(ctx, tx, userID, consolidated); err != nil {
		return nil, err
	}

	// Insert lineage for each source and decay source salience.
	for _, oldID := range uniqueIDs {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO graph_memory_lineage (new_memory_id, old_memory_id, relation_type, created_at)
			 VALUES (?, ?, 'CONSOLIDATES', datetime('now'))`,
			consolidated.ID, oldID,
		)
		if err != nil {
			return nil, fmt.Errorf("insert lineage for %s: %w", oldID, err)
		}

		_, err = tx.ExecContext(ctx,
			`UPDATE graph_memories SET salience = salience * ? WHERE id = ? AND user_id = ?`,
			s.salConf.ConsolidationDecay, oldID, userID,
		)
		if err != nil {
			return nil, fmt.Errorf("decay salience for %s: %w", oldID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit consolidate: %w", err)
	}

	return consolidated, nil
}

func (s *GraphMemoryStore) GetLineage(ctx context.Context, memoryID string) ([]*memory.MemoryLineage, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.get_lineage")
	defer s.tracer.EndSpan(span)

	userID := graphUserID(ctx)

	rows, err := s.db.QueryContext(ctx,
		`SELECT new_memory_id, old_memory_id, relation_type, created_at
		 FROM graph_memory_lineage
		 WHERE (new_memory_id = ? OR old_memory_id = ?)
		   AND new_memory_id IN (SELECT id FROM graph_memories WHERE user_id = ?)
		   AND old_memory_id IN (SELECT id FROM graph_memories WHERE user_id = ?)
		 ORDER BY created_at`,
		memoryID, memoryID, userID, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get lineage: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var lineages []*memory.MemoryLineage
	for rows.Next() {
		var l memory.MemoryLineage
		var createdAtStr string
		if err := rows.Scan(&l.NewMemoryID, &l.OldMemoryID, &l.RelationType, &createdAtStr); err != nil {
			return nil, err
		}
		l.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		if l.CreatedAt.IsZero() {
			l.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAtStr)
		}
		lineages = append(lineages, &l)
	}
	return lineages, rows.Err()
}

// activeMemoryPredicate selects memories that are neither expired/forgotten
// nor superseded. SQLite never sets deleted_at on memories, but the column
// exists, so it is honoured for consistency with the Postgres backend.
const activeMemoryPredicate = `(m.expires_at IS NULL OR m.expires_at > datetime('now'))
	   AND m.deleted_at IS NULL
	   AND m.id NOT IN (SELECT old_memory_id FROM graph_memory_lineage WHERE relation_type = 'SUPERSEDES')`

// ListMemories returns the caller's active memories for agentID, newest first,
// with the total number of such memories. It does not touch access counters.
func (s *GraphMemoryStore) ListMemories(ctx context.Context, agentID string, limit, offset int) ([]*memory.Memory, int, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.list_memories")
	defer s.tracer.EndSpan(span)

	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	userID := graphUserID(ctx)

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_memories m
		 WHERE m.agent_id = ? AND m.user_id = ? AND `+activeMemoryPredicate,
		agentID, userID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count memories: %w", err)
	}

	// created_at has one-second resolution; rowid breaks ties in insertion order.
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.agent_id, m.content, m.summary, m.memory_type,
		        COALESCE(m.source,''), COALESCE(m.source_id,''), COALESCE(m.owner,''),
		        COALESCE(m.memory_agent_id,''), m.tags, m.salience,
		        m.token_count, m.summary_token_count, m.access_count, m.properties_json,
		        m.created_at, m.accessed_at, m.expires_at,
		        COALESCE(m.event_date,''), COALESCE(m.event_date_confidence,'')
		 FROM graph_memories m
		 WHERE m.agent_id = ? AND m.user_id = ? AND `+activeMemoryPredicate+`
		 ORDER BY m.created_at DESC, m.rowid DESC
		 LIMIT ? OFFSET ?`,
		agentID, userID, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var memories []*memory.Memory
	for rows.Next() {
		mem, err := scanMemoryFromRows(rows)
		if err != nil {
			return nil, 0, err
		}
		memories = append(memories, mem)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return memories, total, nil
}

// ForgetAll forgets the caller's memories for agentID and deletes the caller's
// entities and edges for agentID, in one transaction. Memories are forgotten
// the same way Forget does it (expires_at is set), including superseded ones,
// so none of them is recalled again. The returned count is the number of
// memories ListMemories would have reported before the call.
func (s *GraphMemoryStore) ForgetAll(ctx context.Context, agentID string) (int, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.forget_all")
	defer s.tracer.EndSpan(span)

	userID := graphUserID(ctx)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var active int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_memories m
		 WHERE m.agent_id = ? AND m.user_id = ? AND `+activeMemoryPredicate,
		agentID, userID,
	).Scan(&active); err != nil {
		return 0, fmt.Errorf("count memories: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE graph_memories SET expires_at = datetime('now')
		 WHERE agent_id = ? AND user_id = ?
		   AND (expires_at IS NULL OR expires_at > datetime('now'))`,
		agentID, userID,
	); err != nil {
		return 0, fmt.Errorf("forget memories: %w", err)
	}

	// Junction rows and edges cascade from graph_entities when foreign keys
	// are on; delete them explicitly so the result does not depend on the
	// connection's foreign_keys setting.
	const ownedEntities = `SELECT id FROM graph_entities WHERE agent_id = ? AND user_id = ?`
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM graph_memory_entities WHERE entity_id IN (`+ownedEntities+`)`,
		agentID, userID,
	); err != nil {
		return 0, fmt.Errorf("unlink entities: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM graph_edges
		 WHERE user_id = ?
		   AND (agent_id = ? OR source_id IN (`+ownedEntities+`) OR target_id IN (`+ownedEntities+`))`,
		userID, agentID, agentID, userID, agentID, userID,
	); err != nil {
		return 0, fmt.Errorf("delete edges: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM graph_entities WHERE agent_id = ? AND user_id = ?`,
		agentID, userID,
	); err != nil {
		return 0, fmt.Errorf("delete entities: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit forget all: %w", err)
	}
	return active, nil
}

func (s *GraphMemoryStore) TouchMemories(ctx context.Context, memoryIDs []string) error {
	if len(memoryIDs) == 0 {
		return nil
	}

	args := make([]interface{}, 0, len(memoryIDs)+1)
	for _, id := range memoryIDs {
		args = append(args, id)
	}
	args = append(args, graphUserID(ctx))

	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf( // #nosec G201 -- placeholders are "?" only
			`UPDATE graph_memories SET accessed_at = datetime('now'), access_count = access_count + 1
			 WHERE id IN (%s) AND user_id = ?`, sqlPlaceholders(len(memoryIDs))),
		args...,
	)
	return err
}

func (s *GraphMemoryStore) DecayAll(ctx context.Context, agentID string, decayFactor float64) error {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.decay_all")
	defer s.tracer.EndSpan(span)

	_, err := s.db.ExecContext(ctx,
		`UPDATE graph_memories SET salience = MAX(0.0, salience * ?)
		 WHERE agent_id = ? AND user_id = ? AND salience > 0.01`,
		decayFactor, agentID, graphUserID(ctx),
	)
	return err
}

func (s *GraphMemoryStore) ContextFor(ctx context.Context, opts memory.ContextForOpts) (*memory.EntityRecall, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.context_for")
	defer s.tracer.EndSpan(span)

	budget := memory.DefaultBudgetConfig(opts.MaxTokens)

	// Phase 1: Entity profile.
	entity, err := s.GetEntity(ctx, opts.AgentID, opts.EntityName)
	if err != nil {
		// Entity not found — return empty recall instead of crashing.
		return &memory.EntityRecall{}, nil
	}

	result := &memory.EntityRecall{Entity: entity}
	tokensUsed := budget.EntityProfileBudget // Reserve for profile.

	// Phase 2: Graph neighborhood (1-hop).
	edgesOut, err := s.ListEdgesFrom(ctx, entity.ID)
	if err != nil {
		span.RecordError(fmt.Errorf("list edges from %s: %w", entity.ID, err))
	} else {
		result.EdgesOut = edgesOut
	}
	edgesIn, err := s.ListEdgesTo(ctx, entity.ID)
	if err != nil {
		span.RecordError(fmt.Errorf("list edges to %s: %w", entity.ID, err))
	} else {
		result.EdgesIn = edgesIn
	}
	tokensUsed += budget.GraphBudget // Reserve for graph.

	// Phase 2b: Resolve entity names for edge endpoints.
	result.EntityNames = s.resolveEntityNames(ctx, result.EdgesOut, result.EdgesIn)

	// Phase 3: Memories within remaining budget.
	remainingBudget := budget.MaxTokens - tokensUsed
	if remainingBudget <= 0 {
		result.TotalTokensUsed = tokensUsed
		return result, nil
	}

	// Collect neighbor entity IDs for scoped memory search.
	entityIDs := []string{entity.ID}
	for _, e := range edgesOut {
		entityIDs = append(entityIDs, e.TargetID)
	}
	for _, e := range edgesIn {
		entityIDs = append(entityIDs, e.SourceID)
	}

	memories, err := s.Recall(ctx, memory.RecallOpts{
		AgentID:   opts.AgentID,
		Query:     opts.Topic,
		EntityIDs: entityIDs,
		Limit:     50,
	})
	if err != nil {
		span.RecordError(fmt.Errorf("recall memories for %s: %w", opts.EntityName, err))
		return result, nil // Partial result without memories.
	}

	result.TotalCandidates = len(memories)

	// Convert to scored and allocate budget.
	scored := make([]memory.ScoredMemory, len(memories))
	for i, m := range memories {
		scored[i] = memory.ScoredMemory{
			Memory:           m,
			ComputedSalience: m.Salience,
			CombinedScore:    m.Salience,
		}
	}

	packed, memTokens := memory.AllocateMemoryBudget(scored, remainingBudget)
	result.Memories = packed
	result.TotalTokensUsed = tokensUsed + memTokens

	return result, nil
}

// resolveEntityNames batch-queries entity names for all IDs referenced in edges.
// Returns a map of entity ID -> entity name for use in EntityRecall.Format().
func (s *GraphMemoryStore) resolveEntityNames(ctx context.Context, edgesOut, edgesIn []*memory.Edge) map[string]string {
	ids := make(map[string]bool)
	for _, e := range edgesOut {
		ids[e.TargetID] = true
	}
	for _, e := range edgesIn {
		ids[e.SourceID] = true
	}
	if len(ids) == 0 {
		return nil
	}

	args := make([]any, 0, len(ids)+1)
	for id := range ids {
		args = append(args, id)
	}
	args = append(args, graphUserID(ctx))

	query := fmt.Sprintf(`SELECT id, name FROM graph_entities WHERE id IN (%s) AND user_id = ?`, // #nosec G201 -- placeholders are "?" only; values passed via args
		sqlPlaceholders(len(ids)))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close() //nolint:errcheck

	names := make(map[string]string, len(ids))
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}
		names[id] = name
	}
	return names
}

func (s *GraphMemoryStore) GetStats(ctx context.Context, agentID string) (*memory.GraphStats, error) {
	ctx, span := s.tracer.StartSpan(ctx, "sqlite.graph_memory.get_stats")
	defer s.tracer.EndSpan(span)

	stats := &memory.GraphStats{MemoriesByType: make(map[string]int)}
	userID := graphUserID(ctx)

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_entities WHERE agent_id = ? AND user_id = ?`, agentID, userID,
	).Scan(&stats.EntityCount); err != nil {
		return nil, fmt.Errorf("count entities: %w", err)
	}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_edges WHERE agent_id = ? AND user_id = ?`, agentID, userID,
	).Scan(&stats.EdgeCount); err != nil {
		return nil, fmt.Errorf("count edges: %w", err)
	}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_memories WHERE agent_id = ? AND user_id = ?`, agentID, userID,
	).Scan(&stats.MemoryCount); err != nil {
		return nil, fmt.Errorf("count memories: %w", err)
	}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_memories WHERE agent_id = ? AND user_id = ? AND (expires_at IS NULL OR expires_at > datetime('now'))`,
		agentID, userID,
	).Scan(&stats.ActiveMemoryCount); err != nil {
		return nil, fmt.Errorf("count active memories: %w", err)
	}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(token_count), 0) FROM graph_memories WHERE agent_id = ? AND user_id = ?`, agentID, userID,
	).Scan(&stats.TotalMemoryTokens); err != nil {
		return nil, fmt.Errorf("sum memory tokens: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT memory_type, COUNT(*) FROM graph_memories WHERE agent_id = ? AND user_id = ? GROUP BY memory_type`, agentID, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("group memories by type: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var mtype string
		var count int
		if err := rows.Scan(&mtype, &count); err != nil {
			return nil, fmt.Errorf("scan memory type count: %w", err)
		}
		stats.MemoriesByType[mtype] = count
	}

	return stats, rows.Err()
}

func (s *GraphMemoryStore) Close() error {
	return nil // db owned externally
}

// toFTS5OrQuery converts a natural language query into an OR-joined FTS5
// bareword expression.
//
// Contract: natural language in, always-safe bareword OR-query out.
//
// The input is ALWAYS free text — an LLM completion or a raw user message —
// so this function never passes its argument through to MATCH verbatim, and
// no caller may hand it a hand-written FTS5 expression. There is deliberately
// no escape hatch: an earlier pass-through branch (any input containing
// " OR "/" AND "/" NOT "/" NEAR " or a balanced quote went straight to MATCH)
// meant ordinary prose took the raw path, and every apostrophe, comma, '?'
// and paren in it became FTS5 syntax. Verified against a migrated
// `porter unicode61` table, all four of these were hard errors:
//
//	Coach's AND player's stats             -> fts5: syntax error near "'"
//	first issue NOT resolved yet?          -> fts5: syntax error near "?"
//	car service AND GPS malfunction, March -> fts5: syntax error near ","
//	volatile table OR overflow (DECIMAL)   -> fts5: syntax error near "overflow"
//
// Both callers report such an error as zero results, which is how the az512h
// fleet run had graph-memory recall silently dead for 512 agents. Barewords
// cannot parse-error, so barewords are all this emits. Operator words in
// prose are tokenized like any other word (lowercased, so FTS5 reads them as
// barewords rather than as its uppercase-only keywords).
//
// FTS5 also defaults to AND across space-separated terms, which is too strict
// for memory recall; OR returns any match and lets BM25 rank multi-term hits
// higher.
//
// Returns "" when the input yields no usable terms. Callers MUST NOT put that
// into a MATCH — an empty MATCH expression is itself a hard FTS5 syntax
// error — and must skip the FTS join instead.
func toFTS5OrQuery(query string) string {
	terms := fts5Barewords(query)
	if len(terms) == 0 {
		return ""
	}
	return strings.Join(terms, " OR ")
}

// minFTS5TermRunes is the shortest ASCII token fts5Barewords keeps. It is the
// same threshold as minSearchTermRunes in pkg/agent: this sanitizer is the
// last filter every recall query passes through, so the two must agree or one
// of them silently discards terms the other admitted.
const minFTS5TermRunes = 2

// fts5Barewords lowercases text and splits it into FTS5 barewords: runs of
// ASCII alphanumerics, '_', and any codepoint above 127. That set is exactly
// what the FTS5 query grammar accepts unquoted, so no term it returns can be
// a MATCH parse error.
//
// Codepoints above 127 are barewords by that grammar and these indexes are
// tokenized `porter unicode61`, so restricting terms to ASCII silently
// destroyed non-Latin recall: "Привет мир Москва" sanitized to nothing (and
// so to an empty MATCH expression, a hard error), and "café" was truncated to
// "caf", which matches nothing.
//
// Minimum term length is minFTS5TermRunes, matching minSearchTermRunes in
// pkg/agent so the agent-side query builder and this sanitizer agree on what
// counts as a term — otherwise one filter admits a term the other silently
// drops. A single non-ASCII rune is always kept: a lone CJK ideograph is a
// whole word, not a fragment.
func fts5Barewords(s string) []string {
	var terms []string
	var cur strings.Builder
	runes := 0
	nonASCII := false
	flush := func() {
		if runes >= minFTS5TermRunes || nonASCII {
			terms = append(terms, cur.String())
		}
		cur.Reset()
		runes = 0
		nonASCII = false
	}
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r > 127 {
			cur.WriteRune(r)
			runes++
			if r > 127 {
				nonASCII = true
			}
		} else {
			flush()
		}
	}
	flush()
	return terms
}

// =============================================================================
// Transaction Helpers
// =============================================================================

// prepareMemory initializes a memory's ID, timestamps, and token counts.
func (s *GraphMemoryStore) prepareMemory(mem *memory.Memory) {
	if mem.ID == "" {
		mem.ID = uuid.New().String()
	}
	mem.CreatedAt = time.Now().UTC()
	if s.tc != nil {
		mem.TokenCount = s.tc.CountTokens(mem.Content)
		if mem.Summary != "" {
			mem.SummaryTokenCount = s.tc.CountTokens(mem.Summary)
		}
	}
	if mem.Salience == 0 {
		mem.Salience = memory.DefaultSalience
	}
}

// insertMemoryTx inserts a memory row owned by userID within an existing transaction.
func insertMemoryTx(ctx context.Context, tx *sql.Tx, userID string, mem *memory.Memory) error {
	tagsJSON, err := json.Marshal(mem.Tags)
	if err != nil {
		tagsJSON = []byte("[]")
	}

	// expires_at is stored through datetime() (UTC "YYYY-MM-DD HH:MM:SS"),
	// the same text format as datetime('now'), which every expiry predicate
	// compares it against as a string. A raw RFC3339 value ("...T...Z")
	// sorts after datetime('now') on the same day, so a TTL that had
	// already passed earlier that day still read as active.
	var expiresAt interface{}
	if mem.ExpiresAt != nil {
		expiresAt = mem.ExpiresAt.Format(time.RFC3339)
	}

	var embBytes interface{}
	var embModel interface{}
	if len(mem.Embedding) > 0 {
		embBytes = float32ToBytes(mem.Embedding)
		embModel = mem.EmbeddingModel
	}

	var eventDate interface{}
	if mem.EventDate != "" {
		eventDate = mem.EventDate
	}
	var eventDateConfidence interface{}
	if mem.EventDateConfidence != "" {
		eventDateConfidence = mem.EventDateConfidence
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO graph_memories
		 (id, agent_id, content, summary, memory_type, source, source_id, owner, memory_agent_id,
		  tags, salience, token_count, summary_token_count, access_count, properties_json,
		  created_at, accessed_at, expires_at, embedding, embedding_model,
		  event_date, event_date_confidence, user_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, datetime(?), NULL, datetime(?), ?, ?, ?, ?, ?)`,
		mem.ID, mem.AgentID, mem.Content, mem.Summary, mem.MemoryType,
		mem.Source, mem.SourceID, mem.Owner, mem.MemoryAgentID,
		string(tagsJSON), mem.Salience, mem.TokenCount, mem.SummaryTokenCount,
		mem.PropertiesJSON, mem.CreatedAt.Format(time.RFC3339), expiresAt, embBytes, embModel,
		eventDate, eventDateConfidence, userID,
	)
	if err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}
	return nil
}

// linkEntitiesTx links entity IDs to a memory via the junction table within a transaction.
// If mem.EntityRoles is populated, it takes precedence (includes per-entity roles).
// Otherwise falls back to mem.EntityIDs with default RoleAbout.
// Every linked entity must belong to userID; a memory is never attached to
// another user's entity.
func linkEntitiesTx(ctx context.Context, tx *sql.Tx, userID string, mem *memory.Memory) error {
	type link struct{ id, role string }
	var links []link
	if len(mem.EntityRoles) > 0 {
		for _, er := range mem.EntityRoles {
			role := er.Role
			if role == "" {
				role = memory.RoleAbout
			}
			links = append(links, link{er.ID, role})
		}
	} else {
		// Fallback: EntityIDs without explicit roles -> default to RoleAbout.
		for _, eid := range mem.EntityIDs {
			links = append(links, link{eid, memory.RoleAbout})
		}
	}
	if len(links) == 0 {
		return nil
	}

	ids := make([]string, len(links))
	for i, l := range links {
		ids[i] = l.id
	}
	if err := requireOwnedEntitiesTx(ctx, tx, userID, ids...); err != nil {
		return fmt.Errorf("link entities: %w", err)
	}

	for _, l := range links {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO graph_memory_entities (memory_id, entity_id, role) VALUES (?, ?, ?)`,
			mem.ID, l.id, l.role,
		); err != nil {
			return fmt.Errorf("link entity %s (role %s): %w", l.id, l.role, err)
		}
	}
	return nil
}

// requireOwnedEntitiesTx returns memory.ErrNotFound (wrapped) unless every
// entity ID exists and belongs to userID.
func requireOwnedEntitiesTx(ctx context.Context, tx *sql.Tx, userID string, entityIDs ...string) error {
	unique := dedupeStrings(entityIDs)
	if len(unique) == 0 {
		return nil
	}
	args := make([]interface{}, 0, len(unique)+1)
	for _, id := range unique {
		args = append(args, id)
	}
	args = append(args, userID)
	var n int
	if err := tx.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM graph_entities WHERE id IN (%s) AND user_id = ?`, // #nosec G201 -- placeholders are "?" only
			sqlPlaceholders(len(unique))),
		args...,
	).Scan(&n); err != nil {
		return fmt.Errorf("check entity ownership: %w", err)
	}
	if n != len(unique) {
		return fmt.Errorf("%d of %d entities: %w", len(unique)-n, len(unique), memory.ErrNotFound)
	}
	return nil
}

// dedupeStrings returns the distinct values of in, preserving first-seen order.
func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// =============================================================================
// Scan / Parse Helpers
// =============================================================================

func (s *GraphMemoryStore) loadMemoryEntityIDs(ctx context.Context, memoryID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT entity_id FROM graph_memory_entities WHERE memory_id = ?`, memoryID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func scanEntity(row *sql.Row) (*memory.Entity, error) {
	var e memory.Entity
	var createdAt, updatedAt string
	err := row.Scan(&e.ID, &e.AgentID, &e.Name, &e.EntityType, &e.PropertiesJSON, &e.Owner, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	e.CreatedAt = parseTime(createdAt)
	e.UpdatedAt = parseTime(updatedAt)
	return &e, nil
}

func scanEntityFromRows(rows *sql.Rows) (*memory.Entity, error) {
	var e memory.Entity
	var createdAt, updatedAt string
	err := rows.Scan(&e.ID, &e.AgentID, &e.Name, &e.EntityType, &e.PropertiesJSON, &e.Owner, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	e.CreatedAt = parseTime(createdAt)
	e.UpdatedAt = parseTime(updatedAt)
	return &e, nil
}

func scanEdgeFromRows(rows *sql.Rows) (*memory.Edge, error) {
	var e memory.Edge
	var createdAt, updatedAt string
	err := rows.Scan(&e.ID, &e.AgentID, &e.SourceID, &e.TargetID, &e.Relation, &e.PropertiesJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	e.CreatedAt = parseTime(createdAt)
	e.UpdatedAt = parseTime(updatedAt)
	return &e, nil
}

func scanMemory(row *sql.Row) (*memory.Memory, error) {
	var m memory.Memory
	var tagsJSON, createdAtStr string
	var accessedAt, expiresAt sql.NullString

	err := row.Scan(&m.ID, &m.AgentID, &m.Content, &m.Summary, &m.MemoryType,
		&m.Source, &m.SourceID, &m.Owner, &m.MemoryAgentID,
		&tagsJSON, &m.Salience, &m.TokenCount, &m.SummaryTokenCount,
		&m.AccessCount, &m.PropertiesJSON, &createdAtStr, &accessedAt, &expiresAt,
		&m.EventDate, &m.EventDateConfidence,
	)
	if err != nil {
		return nil, err
	}

	m.CreatedAt = parseTime(createdAtStr)
	if tagsJSON != "" && tagsJSON != "null" {
		if err := json.Unmarshal([]byte(tagsJSON), &m.Tags); err != nil {
			return nil, fmt.Errorf("unmarshal tags for memory %s: %w", m.ID, err)
		}
	}

	if accessedAt.Valid && accessedAt.String != "" {
		t := parseTime(accessedAt.String)
		m.AccessedAt = &t
	}
	if expiresAt.Valid && expiresAt.String != "" {
		t := parseTime(expiresAt.String)
		m.ExpiresAt = &t
	}

	return &m, nil
}

func scanMemoryFromRows(rows *sql.Rows) (*memory.Memory, error) {
	var m memory.Memory
	var tagsJSON, createdAtStr string
	var accessedAt, expiresAt sql.NullString

	err := rows.Scan(&m.ID, &m.AgentID, &m.Content, &m.Summary, &m.MemoryType,
		&m.Source, &m.SourceID, &m.Owner, &m.MemoryAgentID,
		&tagsJSON, &m.Salience, &m.TokenCount, &m.SummaryTokenCount,
		&m.AccessCount, &m.PropertiesJSON, &createdAtStr, &accessedAt, &expiresAt,
		&m.EventDate, &m.EventDateConfidence,
	)
	if err != nil {
		return nil, err
	}

	m.CreatedAt = parseTime(createdAtStr)
	if tagsJSON != "" && tagsJSON != "null" {
		if err := json.Unmarshal([]byte(tagsJSON), &m.Tags); err != nil {
			return nil, fmt.Errorf("unmarshal tags for memory %s: %w", m.ID, err)
		}
	}

	if accessedAt.Valid && accessedAt.String != "" {
		t := parseTime(accessedAt.String)
		m.AccessedAt = &t
	}
	if expiresAt.Valid && expiresAt.String != "" {
		t := parseTime(expiresAt.String)
		m.ExpiresAt = &t
	}

	return &m, nil
}

func parseTime(s string) time.Time {
	// Try RFC3339 first, then SQLite datetime format.
	t, err := time.Parse(time.RFC3339, s)
	if err == nil {
		return t
	}
	t, err = time.Parse("2006-01-02 15:04:05", s)
	if err == nil {
		return t
	}
	t, err = time.Parse("2006-01-02T15:04:05", s)
	if err == nil {
		return t
	}
	return time.Time{}
}
