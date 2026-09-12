package standalone

import (
	"context"
	"errors"
	"github.com/samuelbutton/yamata/internal/filelock"
	"github.com/samuelbutton/yamata/internal/ownership"
	"os"
)

func lock(ctx context.Context, root *os.Root, execution string) (_ *os.File, err error) {
	if err := ownership.Select(ctx, root, "standalone"); err != nil {
		return nil, err
	}
	dir, err := root.OpenRoot(".standalone")
	if err != nil {
		return nil, err
	}
	f, err := filelock.Acquire(ctx, dir, execution+".lock")
	closeErr := dir.Close()
	if closeErr != nil && f != nil {
		closeErr = errors.Join(closeErr, f.Close())
		f = nil
	}
	return f, errors.Join(err, closeErr)
}
