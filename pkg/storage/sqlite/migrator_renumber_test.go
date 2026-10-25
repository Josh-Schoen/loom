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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/teradata-labs/loom/pkg/observability"
)

// forkEraDB builds a database the way the fork's builds before the
// renumber left it: migrations 1–8, then graph_entities_per_user recorded
// as version 9, with some graph data in place.
func forkEraDB(t *testing.T) (*sql.DB, *Migrator) {
	t.Helper()
	ctx := context.Background()
	db := openMigrationTestDB(t, true)
	migrator, err := NewMigrator(db, observability.NewNoOpTracer())
	require.NoError(t, err)
	migrateTo(t, migrator, 8)

	for _, s := range []string{
		`INSERT INTO graph_entities (id, agent_id, name, entity_type) VALUES ('e-mom', 'a1', 'mom', 'person')`,
		`INSERT INTO graph_entities (id, agent_id, name, entity_type) VALUES ('e-garden', 'a1', 'garden', 'place')`,
		`INSERT INTO graph_edges (id, agent_id, source_id, target_id, relation) VALUES ('g1', 'a1', 'e-mom', 'e-garden', 'LIKES')`,
	} {
		_, err := db.Exec(s)
		require.NoError(t, err, s)
	}

	var perUser *Migration
	for i := range migrator.migrations {
		if migrator.migrations[i].Description == "graph_entities_per_user" {
			perUser = &migrator.migrations[i]
		}
	}
	require.NotNil(t, perUser)
	require.NoError(t, migrator.applyMigration(ctx, Migration{
		Version: 9, Description: perUser.Description, UpSQL: perUser.UpSQL,
	}))
	return db, migrator
}

func recordedMigrations(t *testing.T, db *sql.DB) map[int]string {
	t.Helper()
	rows, err := db.Query(`SELECT version, description FROM schema_migrations`)
	require.NoError(t, err)
	defer rows.Close() //nolint:errcheck
	got := map[int]string{}
	for rows.Next() {
		var v int
		var d sql.NullString
		require.NoError(t, rows.Scan(&v, &d))
		got[v] = d.String
	}
	require.NoError(t, rows.Err())
	return got
}

func hasColumn(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n))
	return n == 1
}

func perUserConstraint(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var ddl string
	require.NoError(t, db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'graph_entities'`).Scan(&ddl))
	return strings.Contains(ddl, "UNIQUE(agent_id, user_id, name)")
}

// A fork-era database moves graph_entities_per_user to 10 without running it
// again, gets upstream's task_created_via as 9, and keeps its graph data.
func TestRepairRenumbered_ForkEraVersion9(t *testing.T) {
	ctx := context.Background()
	db, migrator := forkEraDB(t)
	require.False(t, hasColumn(t, db, "tasks", "created_via"))

	require.NoError(t, migrator.MigrateUp(ctx))

	got := recordedMigrations(t, db)
	assert.Equal(t, "task_created_via", got[9])
	assert.Equal(t, "graph_entities_per_user", got[10])
	assert.True(t, hasColumn(t, db, "tasks", "created_via"))
	assert.True(t, perUserConstraint(t, db))

	var entities, edges int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM graph_entities`).Scan(&entities))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM graph_edges`).Scan(&edges))
	assert.Equal(t, 2, entities)
	assert.Equal(t, 1, edges)

	// Running again changes nothing.
	require.NoError(t, migrator.MigrateUp(ctx))
	assert.Equal(t, got, recordedMigrations(t, db))
	version, err := migrator.CurrentVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, latestMigrationVersion(t), version)
}

// If upstream's 9 cannot be applied, the move is rolled back too: the
// database still records graph_entities_per_user at 9 and MigrateUp fails.
func TestRepairRenumbered_RollsBackOnFailure(t *testing.T) {
	ctx := context.Background()
	db, migrator := forkEraDB(t)
	_, err := db.Exec(`ALTER TABLE tasks ADD COLUMN created_via TEXT NOT NULL DEFAULT ''`)
	require.NoError(t, err)
	before := recordedMigrations(t, db)

	require.Error(t, migrator.MigrateUp(ctx))
	assert.Equal(t, before, recordedMigrations(t, db))
	assert.Equal(t, "graph_entities_per_user", before[9])
}

// A database that ran upstream's 9 is not touched by the repair and gets
// the per-user rebuild as 10.
func TestRepairRenumbered_UpstreamVersion9(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t, true)
	migrator, err := NewMigrator(db, observability.NewNoOpTracer())
	require.NoError(t, err)
	migrateTo(t, migrator, 9)
	require.False(t, perUserConstraint(t, db))

	require.NoError(t, migrator.MigrateUp(ctx))

	got := recordedMigrations(t, db)
	assert.Equal(t, "task_created_via", got[9])
	assert.Equal(t, "graph_entities_per_user", got[10])
	assert.True(t, perUserConstraint(t, db))
}
