package publication

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/samuelbutton/yamata/internal/contract"
)

// Document validates and publishes one immutable exchange document or bag.
// Referenced files must already be durable and readable.
func Document(ctx context.Context, root *os.Root, v *contract.Validator, path, kind string, data []byte) (err error) {
	directory, name, ok := strings.Cut(path, "/")
	if !ok || directory != map[string]string{"job": "jobs", "bag": "bags", "result": "results", "event": "events"}[kind] {
		return contract.ErrPath
	}
	validate := func(data []byte) error {
		if kind == "bag" {
			_, err := v.ParseBag(ctx, data)
			return err
		}
		return v.ValidateDocument(ctx, root, data, kind)
	}
	if err := validate(data); err != nil {
		return err
	}
	if err := root.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := SyncDirectory(root); err != nil {
		return err
	}
	dir, err := root.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	return File(ctx, dir, name, contract.Hash(data), func(w io.Writer) error { _, err := io.Copy(w, bytes.NewReader(data)); return err }, validate)
}
