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
	"errors"
	"fmt"
)

// renumbered lists migrations that databases may have recorded under an
// earlier version. This fork's graph_entities_per_user migration has moved
// as upstream added migrations under the numbers it held: it shipped as
// 000009 (upstream then added 000009_task_created_via), moved to 000010
// (upstream then added 000010_workflow_checkpoints), and is now 000015, after
// every upstream migration. A database that ran it under an old number has
// that number recorded for a different migration than the one it now names.
var renumbered = []struct {
	from, to    int
	description string
}{
	{from: 9, to: 15, description: "graph_entities_per_user"},
	{from: 10, to: 15, description: "graph_entities_per_user"},
}

// repairRenumbered moves each renumbered migration's record to its new
// version and applies the migrations numbered from its old version up to
// its new one that the database has not recorded (they never ran there),
// in one transaction. It matches on the recorded description, so a
// database that ran the current migration at that version is left alone.
func (m *Migrator) repairRenumbered(ctx context.Context) error {
	for _, r := range renumbered {
		var desc sql.NullString
		err := m.db.QueryRowContext(ctx, "SELECT description FROM schema_migrations WHERE version = ?", r.from).Scan(&desc)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("check renumbered migration %d: %w", r.from, err)
		}
		if desc.String != r.description {
			continue
		}
		if err := m.moveAndApply(ctx, r.from, r.to, r.description); err != nil {
			return err
		}
	}
	return nil
}

func (m *Migrator) moveAndApply(ctx context.Context, from, to int, description string) error {
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		"UPDATE schema_migrations SET version = ? WHERE version = ? AND description = ?", to, from, description); err != nil {
		return fmt.Errorf("move migration %d (%s) to %d: %w", from, description, to, err)
	}
	for _, mig := range m.migrations {
		if mig.Version < from || mig.Version >= to {
			continue
		}
		var n int
		if err := tx.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM schema_migrations WHERE version = ?", mig.Version).Scan(&n); err != nil {
			return fmt.Errorf("check migration %d: %w", mig.Version, err)
		}
		if n > 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, mig.UpSQL); err != nil {
			return fmt.Errorf("migration %d failed: %w", mig.Version, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, description) VALUES (?, ?) ON CONFLICT (version) DO NOTHING",
			mig.Version, mig.Description); err != nil {
			return fmt.Errorf("failed to record migration version: %w", err)
		}
	}
	return tx.Commit()
}
