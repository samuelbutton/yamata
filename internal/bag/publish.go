package bag

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/samuelbutton/yamata/internal/contract"
)

// publish stages private bytes before atomically creating the final directory entry.
// Link provides portable no-replace publication on the supported local filesystems.
// Competing publishers cannot overwrite the winner, even between the existence check
// and publication. The write callback permits bounded encoding and fault testing.
func publish(ctx context.Context, dir *os.Root, name, digest string, v *contract.Validator, write func(io.Writer) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	temp := ".bag-" + rand.Text() + ".tmp"
	f, err := dir.OpenFile(temp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary bag: %w", err)
	}
	defer func() {
		if temp != "" {
			err = errors.Join(err, dir.Remove(temp))
		}
	}()
	writeErr := write(f)
	if writeErr == nil {
		writeErr = verifyStaged(ctx, f, digest, v)
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return fmt.Errorf("write temporary bag: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := dir.Link(temp, name); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("publish bag: %w", err)
		}
		info, err := dir.Lstat(name)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: existing bag is not a regular file", contract.ErrConflict)
		}
		if _, err := v.ReadBag(ctx, dir, name, digest); err != nil {
			return fmt.Errorf("%w: existing bag differs or cannot be validated: %w", contract.ErrConflict, err)
		}
	}
	if err := dir.Remove(temp); err != nil {
		return fmt.Errorf("remove temporary bag: %w", err)
	}
	temp = ""
	return syncDirectory(dir)
}

func verifyStaged(ctx context.Context, f *os.File, digest string, v *contract.Validator) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, contract.MaxBagBytes+1))
	if err != nil {
		return err
	}
	checked, err := v.ParseBag(ctx, data)
	if err != nil {
		return err
	}
	if checked.SHA256 != digest {
		return contract.ErrHash
	}
	return nil
}

func syncDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	if err := errors.Join(f.Sync(), f.Close()); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}
