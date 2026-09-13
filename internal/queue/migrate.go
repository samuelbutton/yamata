package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/samuelbutton/yamata/internal/contract"
)

// migrate preserves snapshots, accepted outputs, leases, and retry history.
// Stop older worker binaries before upgrading the database.
func (s *Store) migrate(ctx context.Context) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version == 4 {
		return nil
	}
	if version < 0 || version > 4 {
		return fmt.Errorf("unsupported queue database version %d", version)
	}
	// SQLite table rebuilding requires foreign keys disabled outside the transaction.
	// Pin the connection so this setting cannot affect a different connection.
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, restore := conn.ExecContext(cleanup, "PRAGMA foreign_keys=ON")
		err = errors.Join(err, restore)
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if version < 3 {
		if version == 0 {
			if _, err := tx.ExecContext(ctx, schema); err != nil {
				return err
			}
		}
		if version < 2 {
			if _, err := tx.ExecContext(ctx, upgradeV2); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, upgradeV3); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT id,data,bag_hash FROM jobs ORDER BY id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var data []byte
			var bagHash string
			if err := rows.Scan(&id, &data, &bagHash); err != nil {
				return err
			}
			job, err := s.validator.ParseRunJob(ctx, data)
			if err != nil {
				return err
			}
			var analysisID *string
			if bagHash != "" {
				hash, err := contract.ContentHash(job.Inputs.AnalysisTemplate)
				if err != nil {
					return err
				}
				value := contract.AnalysisID(job.ExecutionID, bagHash, hash)
				analysisID = &value
			}
			if _, err := tx.ExecContext(ctx, "UPDATE jobs SET priority=?,bag_path=?,analysis_id=? WHERE id=?", job.Priority, "bags/"+job.ExecutionID+".jsonl", analysisID, id); err != nil {
				return err
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, upgradeV4); err != nil {
		return err
	}
	checks, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer checks.Close()
	if checks.Next() {
		return errors.New("queue migration violates foreign keys")
	}
	if err := checks.Err(); err != nil {
		return err
	}
	if err := checks.Close(); err != nil {
		return err
	}
	return tx.Commit()
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

const upgradeV3 = `
CREATE TABLE new_jobs (
 id INTEGER PRIMARY KEY,
 job_id TEXT NOT NULL UNIQUE,
 execution_id TEXT NOT NULL,
 path TEXT NOT NULL UNIQUE,
 data BLOB NOT NULL,
 attempt TEXT NOT NULL,
 stage TEXT NOT NULL CHECK(stage IN ('simulation','analysis','done')),
 state TEXT NOT NULL,
 sequence INTEGER NOT NULL DEFAULT 0,
 started_ms INTEGER NOT NULL,
 token TEXT NOT NULL DEFAULT '',
 lease_ms INTEGER NOT NULL DEFAULT 0,
 bag_hash TEXT NOT NULL DEFAULT '',
 priority INTEGER NOT NULL DEFAULT 0 CHECK(priority BETWEEN 0 AND 3),
 generation INTEGER NOT NULL DEFAULT 0 CHECK(generation >= 0),
 job_kind TEXT NOT NULL DEFAULT 'run' CHECK(job_kind IN ('run','analysis')),
 bag_path TEXT NOT NULL DEFAULT '',
 analysis_id TEXT UNIQUE,
 CHECK(job_kind='run' OR stage IN ('analysis','done'))
);
INSERT INTO new_jobs(id,job_id,execution_id,path,data,attempt,stage,state,sequence,started_ms,token,lease_ms,bag_hash,priority,generation)
 SELECT id,job_id,execution_id,path,data,attempt,stage,state,sequence,started_ms,token,lease_ms,bag_hash,priority,generation FROM jobs;
DROP TABLE jobs;
ALTER TABLE new_jobs RENAME TO jobs;
CREATE UNIQUE INDEX run_execution ON jobs(execution_id) WHERE job_kind='run';
CREATE INDEX ready_jobs ON jobs(stage,lease_ms,id);
CREATE INDEX priority_jobs ON jobs(stage,priority,id);
PRAGMA user_version=3;
`

const upgradeV4 = `
CREATE TABLE faults (
 job INTEGER NOT NULL REFERENCES jobs(id),
 stage TEXT NOT NULL CHECK(stage IN ('simulation','analysis')),
 failures INTEGER NOT NULL CHECK(failures IN (1,2)),
 PRIMARY KEY(job,stage)
);
PRAGMA user_version=4;
`
