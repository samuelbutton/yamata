// Package queue owns durable job intake, stage leases, and ordered file delivery.
package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samuelbutton/yamata/internal/contract"
	"github.com/samuelbutton/yamata/internal/filelock"
	"github.com/samuelbutton/yamata/internal/ownership"
	"github.com/samuelbutton/yamata/internal/publication"
	_ "modernc.org/sqlite" // Registers the pinned SQLite database/sql driver.
)

// ErrLease means this worker no longer owns the stage it tried to finish.
var ErrLease = errors.New("stage lease expired or replaced")

// Store contains one local queue connection. Separate processes can open the same queue.
// Callers must stop their workers before closing it.
type Store struct {
	db        *sql.DB
	root      *os.Root
	validator *contract.Validator
}

// Open creates or opens the private queue beneath an existing exchange directory.
// The exchange and its private directory must not be replaced while open.
func Open(ctx context.Context, directory string) (_ *Store, err error) {
	directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	s := &Store{root: root}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	if err = ownership.Select(ctx, root, "queue"); err != nil {
		return nil, err
	}
	info, err := root.Lstat(".queue")
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%w: .queue must be a private directory", contract.ErrPath)
	}
	dir, err := root.OpenRoot(".queue")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	// Journal-mode changes need exclusive initialization, even with a busy timeout.
	guard, err := filelock.Acquire(ctx, dir, "open.lock")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, guard.Close()) }()
	// SQLite opens its own sidecars. Reject preexisting links and special files.
	for _, name := range []string{"queue.sqlite", "queue.sqlite-wal", "queue.sqlite-shm", "queue.sqlite-journal"} {
		info, statErr := dir.Lstat(name)
		if statErr == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0) {
			return nil, contract.ErrPath
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return nil, statErr
		}
	}
	f, err := dir.OpenFile("queue.sqlite", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err == nil {
		err = errors.Join(f.Sync(), f.Close())
	}
	if err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err = publication.SyncDirectory(dir); err != nil {
		return nil, err
	}
	if err = publication.SyncDirectory(root); err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: filepath.Join(directory, ".queue", "queue.sqlite")}
	q := url.Values{"_txlock": {"immediate"}, "_pragma": {"busy_timeout(5000)", "foreign_keys(1)", "synchronous(FULL)"}}
	uri.RawQuery = q.Encode()
	s.db, err = sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	s.db.SetMaxOpenConns(1)
	s.validator, err = contract.New()
	if err != nil {
		return nil, err
	}
	var journal string
	if err = s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
		return nil, err
	}
	if journal != "wal" {
		if err = s.db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journal); err != nil {
			return nil, err
		}
		if journal != "wal" {
			return nil, errors.New("queue requires SQLite WAL mode")
		}
	}
	err = s.migrate(ctx)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Close releases database and directory handles without deleting durable state.
func (s *Store) Close() error {
	var err error
	if s.db != nil {
		err = s.db.Close()
	}
	if s.root != nil {
		err = errors.Join(err, s.root.Close())
	}
	return err
}

const schema = `
CREATE TABLE jobs (
 id INTEGER PRIMARY KEY,
 job_id TEXT NOT NULL UNIQUE,
 execution_id TEXT NOT NULL UNIQUE,
 path TEXT NOT NULL UNIQUE,
 data BLOB NOT NULL,
 attempt TEXT NOT NULL,
 stage TEXT NOT NULL CHECK(stage IN ('simulation','analysis','done')),
 state TEXT NOT NULL,
 sequence INTEGER NOT NULL DEFAULT 0,
 started_ms INTEGER NOT NULL,
 token TEXT NOT NULL DEFAULT '',
 lease_ms INTEGER NOT NULL DEFAULT 0,
 bag_hash TEXT NOT NULL DEFAULT ''
);
CREATE INDEX ready_jobs ON jobs(stage, lease_ms, id);
CREATE TABLE outbox (
 id INTEGER PRIMARY KEY,
 job INTEGER NOT NULL REFERENCES jobs(id),
 path TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL CHECK(kind IN ('job','bag','result','event')),
 data BLOB NOT NULL,
 delivered INTEGER NOT NULL DEFAULT 0 CHECK(delivered IN (0,1))
);
CREATE INDEX pending_files ON outbox(delivered, id);
PRAGMA user_version=1;
`

func (s *Store) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Receipt identifies a committed job. Duplicate delivery preserves its first receipt.
type Receipt struct {
	JobID, ExecutionID string
	Duplicate          bool
}

// Import validates one snapshot and commits it with its receipt event before returning.
// The first path owns the reference; identical bytes at another path are a duplicate.
func (s *Store) Import(ctx context.Context, path string) (receipt Receipt, err error) {
	if !strings.HasPrefix(path, "jobs/") || strings.Count(path, "/") != 1 || !strings.HasSuffix(path, ".json") {
		return receipt, contract.ErrPath
	}
	data, err := s.validator.ReadDocument(ctx, s.root, path, "job")
	if err != nil {
		return receipt, err
	}
	job, err := s.validator.ParseRunJob(ctx, data)
	if err != nil {
		return receipt, err
	}
	receipt.JobID, receipt.ExecutionID = job.JobID, job.ExecutionID
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var matches, conflicts int
		if err := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(data != ?),0)
            FROM jobs WHERE job_id=? OR execution_id=? OR path=?`, data, job.JobID, job.ExecutionID, path).Scan(&matches, &conflicts); err != nil {
			return err
		}
		if conflicts != 0 {
			return contract.ErrConflict
		}
		if matches != 0 {
			receipt.Duplicate = true
			return nil
		}
		attempt := newAttempt()
		now := time.Now().UnixMilli()
		inserted, err := tx.ExecContext(ctx, `INSERT INTO jobs(job_id,execution_id,path,data,attempt,priority,stage,state,started_ms) VALUES(?,?,?,?,?,?,'simulation','PENDING',?)`, job.JobID, job.ExecutionID, path, data, attempt, job.Priority, now)
		if err != nil {
			return err
		}
		id, err := inserted.LastInsertId()
		if err != nil {
			return err
		}
		if err := addFile(ctx, tx, id, path, "job", data); err != nil {
			return err
		}
		return addEvent(ctx, tx, id, job, attempt, "PENDING", nil, nil, now)
	})
	return receipt, err
}

func encode(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
func addFile(ctx context.Context, tx *sql.Tx, id int64, path, kind string, data []byte) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO outbox(job,path,kind,data) VALUES(?,?,?,?)", id, path, kind, data)
	return err
}
func addEvent(ctx context.Context, tx *sql.Tx, id int64, job contract.RunJob, attempt, state string, analysis *string, result *contract.Reference, now int64) error {
	var sequence int64
	if err := tx.QueryRowContext(ctx, "UPDATE jobs SET sequence=sequence+1,state=? WHERE id=? RETURNING sequence", state, id).Scan(&sequence); err != nil {
		return err
	}
	event := contract.Event{ContractVersion: 1, Kind: "event", ExecutionID: job.ExecutionID, JobID: job.JobID, AttemptID: attempt, CorrelationID: job.CorrelationID, EventID: contract.EventID(job.JobID, attempt, sequence), Sequence: sequence, State: state, AnalysisID: analysis, Result: result, EventType: "execution_transition", Producer: "yamata", CreatedAtMS: now}
	data, err := encode(event)
	if err != nil {
		return err
	}
	return addFile(ctx, tx, id, "events/"+event.EventID+".json", "event", data)
}
