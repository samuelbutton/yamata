package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/samuelbutton/yamata/internal/contract"
)

// originalRun resolves geometry from the immutable snapshot that produced this bag.
// Bag format one omits vehicle length and goal position; neither can be inferred.
func (s *Store) originalRun(ctx context.Context, tx *sql.Tx, executionID, bagHash string) (contract.RunJob, error) {
	var data []byte
	err := tx.QueryRowContext(ctx, "SELECT data FROM jobs WHERE job_kind='run' AND execution_id=? AND bag_hash=?", executionID, bagHash).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.RunJob{}, fmt.Errorf("%w: analysis requires the original run and accepted bag in this queue", contract.ErrInvalid)
	}
	if err != nil {
		return contract.RunJob{}, err
	}
	return s.validator.ParseRunJob(ctx, data)
}
