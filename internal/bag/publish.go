package bag

import (
	"context"
	"github.com/samuelbutton/yamata/internal/contract"
	"github.com/samuelbutton/yamata/internal/publication"
	"io"
	"os"
)

func publish(ctx context.Context, dir *os.Root, name, digest string, v *contract.Validator, write func(io.Writer) error) error {
	return publication.File(ctx, dir, name, digest, write, func(data []byte) error {
		_, err := v.ParseBag(ctx, data)
		return err
	})
}

func syncDirectory(root *os.Root) error { return publication.SyncDirectory(root) }
