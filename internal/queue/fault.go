package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/samuelbutton/yamata/internal/contract"
)

// InjectFailure arms a fixed worker-failure count before a job's first claim.
// The durable retry record determines whether a later attempt still fails.
func (s *Store) InjectFailure(ctx context.Context, jobID, stage string, failures int) error {
	if jobID == "" || (stage != "simulation" && stage != "analysis") || failures < 1 || failures > 2 {
		return errors.New("supply a job, simulation or analysis stage, and 1 or 2 failures")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var id, generation int64
		var kind string
		if err := tx.QueryRowContext(ctx, "SELECT id,generation,job_kind FROM jobs WHERE job_id=?", jobID).Scan(&id, &generation, &kind); err != nil {
			return err
		}
		if kind == "analysis" && stage == "simulation" {
			return errors.New("analysis jobs have no simulation stage")
		}
		var existing int
		err := tx.QueryRowContext(ctx, "SELECT failures FROM faults WHERE job=? AND stage=?", id, stage).Scan(&existing)
		if err == nil {
			if existing == failures {
				return nil
			}
			return contract.ErrConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if generation != 0 {
			return fmt.Errorf("%w: configure failures before the first claim", contract.ErrConflict)
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO faults(job,stage,failures) VALUES(?,?,?)", id, stage, failures)
		return err
	})
}

func (s *Store) injectedFailure(ctx context.Context, l lease) (bool, error) {
	var fail bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM faults WHERE job=? AND stage=?
 AND failures > (SELECT count(*) FROM retries WHERE job=? AND stage=?))`, l.id, l.stage, l.id, l.stage).Scan(&fail)
	return fail, err
}
