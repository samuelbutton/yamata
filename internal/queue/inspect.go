package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// QueueJob exposes operational identifiers and state without job payloads or lease tokens.
type QueueJob struct {
	ID                 int64   `json:"queue_id"`
	JobID              string  `json:"job_id"`
	ExecutionID        string  `json:"execution_id"`
	Kind               string  `json:"job_kind"`
	Priority           int     `json:"priority"`
	Stage              string  `json:"stage"`
	State              string  `json:"state"`
	AttemptID          string  `json:"attempt_id"`
	Generation         int64   `json:"generation"`
	Sequence           int64   `json:"sequence"`
	LeaseUntilMS       int64   `json:"lease_until_ms"`
	Ready              bool    `json:"ready"`
	PendingFiles       int     `json:"pending_files"`
	SimulationRetries  int     `json:"simulation_retries"`
	AnalysisRetries    int     `json:"analysis_retries"`
	SimulationFailures int     `json:"simulation_failures"`
	AnalysisFailures   int     `json:"analysis_failures"`
	BagPath            string  `json:"bag_path"`
	BagHash            string  `json:"bag_hash"`
	AnalysisID         *string `json:"analysis_id"`
	ResultPath         string  `json:"result_path"`
}

// Snapshot is a consistent queue view; NextAfter continues pagination by intake ID.
type Snapshot struct {
	ObservedAtMS      int64          `json:"observed_at_ms"`
	TotalJobs         int            `json:"total_jobs"`
	PendingFiles      int            `json:"pending_files"`
	Stages            map[string]int `json:"stages"`
	DispatchPositions map[string]int `json:"dispatch_positions"`
	Jobs              []QueueJob     `json:"jobs"`
	NextAfter         *int64         `json:"next_after"`
}

// Inspect reads an existing current-schema queue without initializing, migrating, or claiming it.
func Inspect(ctx context.Context, directory string, after int64, limit int) (out Snapshot, err error) {
	if directory == "" || after < 0 || limit < 1 || limit > 1000 {
		return out, errors.New("supply an exchange, nonnegative cursor, and limit from 1 to 1000")
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return out, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return out, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return out, err
	}
	defer root.Close()
	info, err := root.Lstat(".queue")
	if err != nil {
		return out, err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return out, errors.New("expected a private queue directory")
	}
	for _, name := range []string{"queue.sqlite", "queue.sqlite-wal", "queue.sqlite-shm", "queue.sqlite-journal"} {
		info, err := root.Lstat(".queue/" + name)
		if errors.Is(err, os.ErrNotExist) && name != "queue.sqlite" {
			continue
		}
		if err != nil {
			return out, err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return out, errors.New("expected private regular queue files")
		}
	}
	uri := url.URL{Scheme: "file", Path: filepath.Join(directory, ".queue/queue.sqlite")}
	uri.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(5000)"}}.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return out, err
	}
	if version != 4 {
		return out, fmt.Errorf("queue schema %d requires upgrade before inspection", version)
	}
	out = Snapshot{ObservedAtMS: time.Now().UnixMilli(), Stages: map[string]int{}, DispatchPositions: map[string]int{}, Jobs: []QueueJob{}}
	if err = tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM jobs),(SELECT count(*) FROM outbox WHERE delivered=0)").Scan(&out.TotalJobs, &out.PendingFiles); err != nil {
		return out, err
	}
	for _, stage := range []string{"simulation", "analysis", "done"} {
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE stage=?", stage).Scan(&n); err != nil {
			return out, err
		}
		out.Stages[stage] = n
		if stage != "done" {
			if err = tx.QueryRowContext(ctx, "SELECT position FROM dispatches WHERE stage=?", stage).Scan(&n); err != nil {
				return out, err
			}
			out.DispatchPositions[stage] = n
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,job_id,execution_id,job_kind,priority,stage,state,attempt,generation,sequence,lease_ms,
 (SELECT count(*) FROM outbox WHERE job=jobs.id AND delivered=0),
 (SELECT count(*) FROM retries WHERE job=jobs.id AND stage='simulation'),
 (SELECT count(*) FROM retries WHERE job=jobs.id AND stage='analysis'),
 coalesce((SELECT failures FROM faults WHERE job=jobs.id AND stage='simulation'),0),
 coalesce((SELECT failures FROM faults WHERE job=jobs.id AND stage='analysis'),0),
 bag_path,bag_hash,analysis_id,coalesce((SELECT path FROM outbox WHERE job=jobs.id AND kind='result'),'')
 FROM jobs WHERE id>? ORDER BY id LIMIT ?`, after, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var j QueueJob
		if err = rows.Scan(&j.ID, &j.JobID, &j.ExecutionID, &j.Kind, &j.Priority, &j.Stage, &j.State, &j.AttemptID, &j.Generation, &j.Sequence, &j.LeaseUntilMS, &j.PendingFiles, &j.SimulationRetries, &j.AnalysisRetries, &j.SimulationFailures, &j.AnalysisFailures, &j.BagPath, &j.BagHash, &j.AnalysisID, &j.ResultPath); err != nil {
			return out, err
		}
		if len(out.Jobs) == limit {
			value := out.Jobs[limit-1].ID
			out.NextAfter = &value
			break
		}
		j.Ready = j.Stage != "done" && j.LeaseUntilMS <= out.ObservedAtMS && j.PendingFiles == 0
		out.Jobs = append(out.Jobs, j)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
