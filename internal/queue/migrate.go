package queue

import (
	"context"
	"database/sql"
	"fmt"
)

// migrate preserves version-one snapshots, outputs, active leases, and event IDs.
// Stop older worker binaries before upgrading the database.
func (s *Store) migrate(ctx context.Context) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		switch version {
		case 0:
			if _, err := tx.ExecContext(ctx, schema); err != nil {
				return err
			}
		case 1:
		case 2:
			return nil
		default:
			return fmt.Errorf("unsupported queue database version %d", version)
		}
		if _, err := tx.ExecContext(ctx, upgradeV2); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT id,data FROM jobs ORDER BY id")
		if err != nil {
			return err
		}
		defer rows.Close()
		// Validate each original snapshot instead of assigning a default priority.
		for rows.Next() {
			var id int64
			var data []byte
			if err := rows.Scan(&id, &data); err != nil {
				return err
			}
			job, err := s.validator.ParseRunJob(ctx, data)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE jobs SET priority=? WHERE id=?", job.Priority, id); err != nil {
				return err
			}
		}
		return rows.Err()
	})
}

const upgradeV2 = `
ALTER TABLE jobs ADD COLUMN priority INTEGER NOT NULL DEFAULT 0 CHECK(priority BETWEEN 0 AND 3);
ALTER TABLE jobs ADD COLUMN generation INTEGER NOT NULL DEFAULT 0 CHECK(generation >= 0);
CREATE INDEX priority_jobs ON jobs(stage, priority, id);
CREATE TABLE dispatches (
 stage TEXT PRIMARY KEY CHECK(stage IN ('simulation','analysis')),
 position INTEGER NOT NULL DEFAULT 0 CHECK(position BETWEEN 0 AND 9)
);
INSERT INTO dispatches(stage) VALUES('simulation'),('analysis');
CREATE TABLE retries (
 job INTEGER NOT NULL REFERENCES jobs(id),
 stage TEXT NOT NULL CHECK(stage IN ('simulation','analysis')),
 failed_generation INTEGER NOT NULL,
 failed_attempt TEXT NOT NULL,
 next_attempt TEXT NOT NULL,
 created_ms INTEGER NOT NULL,
 PRIMARY KEY(job,stage)
);
PRAGMA user_version=2;
`
