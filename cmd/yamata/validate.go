package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/samuelbutton/yamata/internal/contract"
)

const validateHelp = `Usage: yamata validate --exchange-dir PATH FILE [FILE ...]

Validate files and their references without changing the exchange directory.
FILE paths are relative to the exchange directory. Put options before files.
Include related files together to check for conflicting identities.

Options:
  --exchange-dir PATH  Required exchange directory.
  -h, --help           Show this help.
`

func validate(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("exchange-dir", "", "exchange directory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeHelp(output, validateHelp)
		}
		return fmt.Errorf("validate options: %w", err)
	}
	if *directory == "" || flags.NArg() == 0 {
		return errors.New("supply --exchange-dir and at least one file; run yamata validate --help")
	}
	validator, err := contract.New()
	if err != nil {
		return fmt.Errorf("compile contract: %w", err)
	}
	if err := validator.Check(context.Background(), *directory, flags.Args()); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, "Contract valid."); err != nil {
		return fmt.Errorf("write confirmation: %w", err)
	}
	return nil
}
