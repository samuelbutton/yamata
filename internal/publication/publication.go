// Package publication atomically publishes immutable files on local filesystems.
package publication

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"syscall"

	"github.com/samuelbutton/yamata/internal/contract"
)

// File stages, verifies, and synchronizes content before publishing without replacement.
// The caller supplies format validation; byte identity remains enforced here.
func File(ctx context.Context, dir *os.Root, name, digest string, write func(io.Writer) error, validate func([]byte) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "/\\:") || strings.HasSuffix(name, ".tmp") {
		return contract.ErrPath
	}
	temp := ".publish-" + rand.Text() + ".tmp"
	f, err := dir.OpenFile(temp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer func() {
		if temp != "" {
			err = errors.Join(err, dir.Remove(temp))
		}
	}()
	writeErr := write(f)
	if writeErr == nil {
		writeErr = verifyFile(ctx, f, digest, validate)
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := dir.Link(temp, name); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("publish file: %w", err)
		}
		info, err := dir.Lstat(name)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: existing file is not a regular file", contract.ErrConflict)
		}
		f, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			err = errors.Join(verifyFile(ctx, f, digest, validate), f.Close())
		}
		if err != nil {
			return fmt.Errorf("%w: existing file differs or cannot be validated: %w", contract.ErrConflict, err)
		}
	}
	if err := dir.Remove(temp); err != nil {
		return fmt.Errorf("remove temporary file: %w", err)
	}
	temp = ""
	return SyncDirectory(dir)
}

func verifyFile(ctx context.Context, f *os.File, digest string, validate func([]byte) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return contract.ErrPath
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, contract.MaxBagBytes+1))
	if err != nil {
		return err
	}
	if len(data) > contract.MaxBagBytes {
		return contract.ErrInvalid
	}
	if contract.Hash(data) != digest {
		return contract.ErrHash
	}
	return validate(data)
}

// SyncDirectory makes preceding directory-entry changes durable before success.
func SyncDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	if err := errors.Join(f.Sync(), f.Close()); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}
