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

package memory

import (
	"context"
	"errors"
)

// ErrNotFound reports that a memory or entity does not exist for the caller.
// Graph memory is scoped per user (types.UserIDFromContext, with an empty
// identity meaning types.DefaultUserID), so a row owned by another user is
// reported as not found rather than as a permission error. Implementations
// wrap it; test with errors.Is.
var ErrNotFound = errors.New("graph memory: not found")

// TokenCounter counts tokens. Satisfied by *agent.TokenCounter.
type TokenCounter interface {
	CountTokens(text string) int
}

// GraphMemoryStore defines the storage interface for graph-backed episodic memory.
// Implementations exist for SQLite and PostgreSQL.
//
// Every operation is scoped to the caller: the user ID carried by ctx
// (types.UserIDFromContext; an empty ID means types.DefaultUserID). Rows are
// written with that user ID, reads only return that user's rows, and ID-based
// mutations only affect that user's rows. Several people can therefore share
// one agent without their entities, edges, or memories being visible to each
// other.
type GraphMemoryStore interface {
	// Entity CRUD (mutable)
	CreateEntity(ctx context.Context, entity *Entity) (*Entity, error)
	GetEntity(ctx context.Context, agentID, name string) (*Entity, error)
	UpdateEntity(ctx context.Context, entity *Entity) (*Entity, error)
	ListEntities(ctx context.Context, agentID, entityType string, limit, offset int) ([]*Entity, int, error)
	SearchEntities(ctx context.Context, agentID, query string, limit int) ([]*Entity, error)
	DeleteEntity(ctx context.Context, agentID, name string) error

	// Edge CRUD (mutable, UNIQUE on source+target+relation)
	Relate(ctx context.Context, edge *Edge) (*Edge, error) // upsert
	Unrelate(ctx context.Context, sourceID, targetID, relation string) error
	Neighbors(ctx context.Context, entityID string, relation string, direction string, depth int) ([]*Edge, error)
	ListEdgesFrom(ctx context.Context, entityID string) ([]*Edge, error)
	ListEdgesTo(ctx context.Context, entityID string) ([]*Edge, error)

	// Memory (append-only, immutable content)
	Remember(ctx context.Context, mem *Memory) (*Memory, error)
	// GetMemory returns ErrNotFound (wrapped) when the memory does not exist
	// for the caller under agentID.
	GetMemory(ctx context.Context, agentID, memoryID string) (*Memory, error)
	Recall(ctx context.Context, opts RecallOpts) ([]*Memory, error)
	// Forget soft-deletes (sets expires_at). Returns ErrNotFound (wrapped) when
	// the caller has no memory with this ID.
	Forget(ctx context.Context, memoryID string) error
	// Supersede creates newMem with a SUPERSEDES lineage to oldMemoryID.
	// Returns ErrNotFound (wrapped) when the caller has no memory oldMemoryID.
	Supersede(ctx context.Context, oldMemoryID string, newMem *Memory) (*Memory, error)
	Consolidate(ctx context.Context, memoryIDs []string, consolidated *Memory) (*Memory, error)
	GetLineage(ctx context.Context, memoryID string) ([]*MemoryLineage, error)

	// ListMemories returns the caller's active memories for agentID (not
	// forgotten/expired, not superseded, not deleted), newest first, and the
	// total count of such memories. EntityIDs need not be populated.
	ListMemories(ctx context.Context, agentID string, limit, offset int) ([]*Memory, int, error)
	// ForgetAll forgets every active memory the caller has for agentID and
	// deletes the caller's entities and edges for agentID, so nothing about
	// the caller is recalled afterwards. Returns the number of memories forgotten.
	ForgetAll(ctx context.Context, agentID string) (int, error)

	// Access tracking (the only allowed mutation on memories)
	TouchMemories(ctx context.Context, memoryIDs []string) error

	// Salience management
	DecayAll(ctx context.Context, agentID string, decayFactor float64) error

	// Composite query
	ContextFor(ctx context.Context, opts ContextForOpts) (*EntityRecall, error)

	// Vector similarity search (returns memories ordered by cosine similarity).
	// Implementations: SQLite uses brute-force in Go; PostgreSQL uses pgvector HNSW.
	// Returns nil, nil if embeddings are not enabled for this store.
	VectorRecall(ctx context.Context, opts VectorRecallOpts) ([]*Memory, error)

	// Stats
	GetStats(ctx context.Context, agentID string) (*GraphStats, error)

	Close() error
}

// RecallOpts configures a memory recall query.
type RecallOpts struct {
	AgentID     string
	Query       string   // FTS search query
	MemoryType  string   // filter (empty = all)
	EntityIDs   []string // scope to entities (empty = all)
	Tags        []string // filter by tags
	MinSalience float64
	MaxTokens   int // 0 = no limit
	Limit       int // max results
}

// ContextForOpts configures a composite context query.
type ContextForOpts struct {
	AgentID    string
	EntityName string
	Topic      string // FTS query for relevant memories
	MaxTokens  int    // total budget
}
