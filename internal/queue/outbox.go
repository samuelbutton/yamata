package queue

import (
	"context"
	"database/sql"
	"errors"

	"github.com/samuelbutton/yamata/internal/publication"
)

// Flush publishes committed files in insertion order, then acknowledges each file.
// A crash after publication repeats the same immutable bytes on the next call.
// Multiple publishers can race safely on the same oldest record.
func (s *Store) Flush(ctx context.Context) error {
	for {
		var id int64
		var path, kind string
		var data []byte
		err := s.db.QueryRowContext(ctx, "SELECT id,path,kind,data FROM outbox WHERE delivered=0 ORDER BY id LIMIT 1").Scan(&id, &path, &kind, &data)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := publication.Document(ctx, s.root, s.validator, path, kind, data); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, "UPDATE outbox SET delivered=1 WHERE id=?", id); err != nil {
			return err
		}
	}
}
