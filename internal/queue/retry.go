package queue

import (
	"context"
	"crypto/rand"
	"database/sql"
	"strings"
)

// retry records the single permitted worker-failure retry for this job and stage.
// Its caller already fenced ownership in the same transaction.
func (s *Store) retry(ctx context.Context, tx *sql.Tx, l lease, analysisID *string, now int64) (bool, error) {
	var used bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM retries WHERE job=? AND stage=?)", l.id, l.stage).Scan(&used); err != nil {
		return false, err
	}
	if used {
		return false, nil
	}
	attempt := newAttempt()
	if _, err := tx.ExecContext(ctx, `INSERT INTO retries(job,stage,failed_generation,failed_attempt,next_attempt,created_ms) VALUES(?,?,?,?,?,?)`, l.id, l.stage, l.generation, l.attempt, attempt, now); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE jobs SET attempt=? WHERE id=?", attempt, l.id); err != nil {
		return false, err
	}
	state := "RUNNING"
	if l.stage == "analysis" {
		state = "ANALYZING"
	}
	if err := addEvent(ctx, tx, l.id, l.job.JobInfo, attempt, state, analysisID, nil, now); err != nil {
		return false, err
	}
	return true, nil
}

func newAttempt() string { return "queue-" + strings.ToLower(rand.Text()) }
