package queue

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"time"

	"github.com/samuelbutton/yamata/internal/contract"
	"github.com/samuelbutton/yamata/internal/execution"
)

type lease struct {
	id                                   int64
	job                                  contract.RunJob
	path, attempt, token, stage, bagHash string
	started                              int64
}

func (s *Store) claim(ctx context.Context, stage string, duration time.Duration) (l lease, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var data []byte
		var state string
		now := time.Now().UnixMilli()
		err := tx.QueryRowContext(ctx, `SELECT id,data,path,attempt,started_ms,bag_hash,state FROM jobs
   WHERE stage=? AND lease_ms<=? AND NOT EXISTS (SELECT 1 FROM outbox WHERE job=jobs.id AND delivered=0)
   ORDER BY id LIMIT 1`, stage, now).Scan(&l.id, &data, &l.path, &l.attempt, &l.started, &l.bagHash, &state)
		if err != nil {
			return err
		}
		l.job, err = s.validator.ParseRunJob(ctx, data)
		if err != nil {
			return err
		}
		l.token, l.stage = rand.Text(), stage
		if state == "PENDING" {
			l.started = now
		}
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET token=?,lease_ms=?,started_ms=? WHERE id=?", l.token, now+duration.Milliseconds(), l.started, l.id); err != nil {
			return err
		}
		if state == "PENDING" {
			return addEvent(ctx, tx, l.id, l.job, l.attempt, "RUNNING", nil, nil, now)
		}
		return nil
	})
	return l, err
}

func (s *Store) renew(ctx context.Context, l lease, duration time.Duration) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		result, err := tx.ExecContext(ctx, `UPDATE jobs SET lease_ms=? WHERE id=? AND token=? AND stage=? AND lease_ms>?`, now+duration.Milliseconds(), l.id, l.token, l.stage, now)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrLease
		}
		return nil
	})
}

func (s *Store) accept(ctx context.Context, l lease, stage, hash string, write func(*sql.Tx, int64) error) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		result, err := tx.ExecContext(ctx, `UPDATE jobs SET stage=?,bag_hash=?,token='',lease_ms=0 WHERE id=? AND token=? AND stage=? AND lease_ms>?`, stage, hash, l.id, l.token, l.stage, now)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrLease
		}
		return write(tx, now)
	})
}

func (l lease) result() contract.Result {
	return contract.Result{ContractVersion: 1, Kind: "result", ExecutionID: l.job.ExecutionID, JobID: l.job.JobID, AttemptID: l.attempt, CorrelationID: l.job.CorrelationID, Job: contract.Reference{Path: l.path, SHA256: l.job.SHA256}, InputsHash: l.job.InputsHash}
}

func (s *Store) acceptBag(ctx context.Context, l lease, data []byte) error {
	ref := contract.Reference{Path: "bags/" + l.job.ExecutionID + ".jsonl", SHA256: contract.Hash(data)}
	result, err := execution.Analysis(l.result(), l.job, ref)
	if err != nil {
		return err
	}
	return s.accept(ctx, l, "analysis", ref.SHA256, func(tx *sql.Tx, now int64) error {
		if err := addFile(ctx, tx, l.id, ref.Path, "bag", data); err != nil {
			return err
		}
		return addEvent(ctx, tx, l.id, l.job, l.attempt, "ANALYZING", result.AnalysisID, nil, now)
	})
}

func (s *Store) acceptResult(ctx context.Context, l lease, result contract.Result) error {
	path := "results/" + l.job.ExecutionID + "-run.json"
	if result.AnalysisID != nil {
		path = "results/" + *result.AnalysisID + ".json"
	}
	result.Timing.DurationMS = max(0, time.Now().UnixMilli()-l.started)
	data, err := encode(result)
	if err != nil {
		return err
	}
	if err := s.validator.ValidateDocument(ctx, s.root, data, "result"); err != nil {
		return err
	}
	ref := contract.Reference{Path: path, SHA256: contract.Hash(data)}
	return s.accept(ctx, l, "done", l.bagHash, func(tx *sql.Tx, now int64) error {
		if err := addFile(ctx, tx, l.id, path, "result", data); err != nil {
			return err
		}
		return addEvent(ctx, tx, l.id, l.job, l.attempt, result.Status, result.AnalysisID, &ref, now)
	})
}

func (s *Store) release(l lease) error {
	// Cleanup has a bounded context independent of the canceled worker.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, "UPDATE jobs SET token='',lease_ms=0 WHERE id=? AND token=? AND stage=?", l.id, l.token, l.stage)
	return err
}

func idle(err error) bool { return errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrLease) }
