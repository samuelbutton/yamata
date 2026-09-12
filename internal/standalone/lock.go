package standalone

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// lock serializes standalone calls for one execution, including result/event repair.
// Kernel locks release on process exit. Lock files stay in place to avoid inode races.
func lock(ctx context.Context, root *os.Root, execution string) (*os.File, error) {
	if err := root.Mkdir(".standalone", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	dir, err := root.OpenRoot(".standalone")
	if err != nil {
		return nil, err
	}
	name := execution + ".lock"
	f, err := dir.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		f, err = dir.OpenFile(name, os.O_RDWR|syscall.O_NONBLOCK, 0)
	}
	closeErr := dir.Close()
	if err != nil {
		return nil, errors.Join(err, closeErr)
	}
	if closeErr != nil {
		return nil, errors.Join(closeErr, f.Close())
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.Join(fmt.Errorf("invalid standalone lock file"), err, f.Close())
	}
	timer := time.NewTicker(10 * time.Millisecond)
	defer timer.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, f.Close())
		}
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(err, f.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), f.Close())
		case <-timer.C:
		}
	}
}
