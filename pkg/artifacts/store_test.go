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
package artifacts

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	_ "github.com/teradata-labs/loom/internal/sqlitedriver" // registers "sqlite3" driver
)

// artifactsSchema is the artifacts table as pkg/agent/session_store.go
// migrates it — tags and metadata_json are NULLABLE there, which is the whole
// point of the test below.
const artifactsSchema = `
CREATE TABLE artifacts (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	path TEXT NOT NULL,
	source TEXT NOT NULL,
	source_agent_id TEXT,
	purpose TEXT,
	content_type TEXT NOT NULL,
	size_bytes INTEGER NOT NULL,
	checksum TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	last_accessed_at INTEGER,
	access_count INTEGER DEFAULT 0,
	tags TEXT,
	metadata_json TEXT,
	deleted_at INTEGER,
	session_id TEXT
);`

func newStoreWithSchema(t *testing.T) *SQLiteStore {
	t.Helper()

	path := filepath.Join(t.TempDir(), "artifacts.db")
	store, err := NewSQLiteStore(path, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	_, err = store.db.Exec(artifactsSchema)
	require.NoError(t, err)

	return store
}

// A row with NULL tags is a legal row — the column is nullable and rows
// predate the code that always serialises a value. Scanning it must not fail,
// and it must not fail the OTHER rows either: the desktop's Files panel reads
// one session's whole listing, so a single unscannable row used to turn the
// panel into an opaque "converting NULL to string is unsupported".
func TestListSurvivesNullTagsAndMetadata(t *testing.T) {
	store := newStoreWithSchema(t)
	ctx := context.Background()

	insert := func(id, name string, tags, meta interface{}) {
		t.Helper()
		_, err := store.db.Exec(`
			INSERT INTO artifacts (id, name, path, source, content_type,
				size_bytes, checksum, created_at, updated_at, access_count,
				tags, metadata_json, session_id)
			VALUES (?, ?, ?, 'agent', 'text/plain', 3, 'sum', 1, 1, 0, ?, ?, 'sess-1')`,
			id, name, "/tmp/"+name, tags, meta)
		require.NoError(t, err)
	}

	insert("a-null", "null-tags.txt", nil, nil)
	insert("a-set", "has-tags.txt", `["sql"]`, `{"k":"v"}`)

	sessionID := "sess-1"
	got, err := store.List(ctx, &Filter{SessionID: &sessionID})
	require.NoError(t, err)
	require.Len(t, got, 2)

	byID := map[string]*Artifact{}
	for _, a := range got {
		byID[a.ID] = a
	}
	require.Empty(t, byID["a-null"].Tags, "NULL tags must read as no tags, not as an error")
	require.Empty(t, byID["a-null"].Metadata)
	require.Equal(t, []string{"sql"}, byID["a-set"].Tags)
	require.Equal(t, map[string]string{"k": "v"}, byID["a-set"].Metadata)

	// The single-row path scans the same columns, so it carries the same bug.
	one, err := store.Get(ctx, "a-null")
	require.NoError(t, err)
	require.Equal(t, "null-tags.txt", one.Name)

	named, err := store.GetByName(ctx, "null-tags.txt", "sess-1")
	require.NoError(t, err)
	require.Equal(t, "a-null", named.ID)
}

// Guards the assumption the fix rests on: these columns really are nullable,
// so a future migration that tightens them does not silently make the test
// vacuous.
func TestArtifactsTagColumnsAreNullable(t *testing.T) {
	store := newStoreWithSchema(t)

	rows, err := store.db.Query(`SELECT name, "notnull" FROM pragma_table_info('artifacts')`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	nullable := map[string]bool{}
	for rows.Next() {
		var name string
		var notNull int
		require.NoError(t, rows.Scan(&name, &notNull))
		nullable[name] = notNull == 0
	}
	require.NoError(t, rows.Err())
	require.True(t, nullable["tags"])
	require.True(t, nullable["metadata_json"])
}
