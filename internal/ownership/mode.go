// Package ownership prevents standalone and queued publishers from sharing an exchange.
package ownership

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/samuelbutton/yamata/internal/contract"
	"github.com/samuelbutton/yamata/internal/filelock"
	"github.com/samuelbutton/yamata/internal/publication"
)

// Select durably reserves one execution mode, serialized across local processes.
// Separate exchanges keep standalone attempts independent from queue attempts.
func Select(ctx context.Context, root *os.Root, mode string) (err error) {
	other := "standalone"
	if mode == "standalone" {
		other = "queue"
	} else if mode != "queue" {
		return contract.ErrInvalid
	}
	guard, err := filelock.Acquire(ctx, root, ".execution.lock")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, guard.Close()) }()
	if _, err := root.Lstat("." + other); err == nil {
		return fmt.Errorf("%w: exchange already uses %s execution", contract.ErrConflict, other)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := root.Mkdir("."+mode, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return publication.SyncDirectory(root)
}
