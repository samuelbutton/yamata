package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/samuelbutton/yamata/internal/bag"
)

const recordHelp = `Usage: yamata record --exchange-dir PATH JOB

Run a resolved job and save bags/<execution_id>.jsonl in the exchange directory.
Print the bag's SHA-256 hash on standard output after durable publication.
Identical output is accepted again. Different bytes cannot replace an existing bag.
JOB must be a relative jobs/*.json path. Put options before JOB.
This command creates no scores, events, or queue entries.

Options:
  --exchange-dir PATH  Required existing exchange directory containing the job.
  -h, --help           Show this help without writing files.
`

func record(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("record", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("exchange-dir", "", "exchange directory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeHelp(output, recordHelp)
		}
		return fmt.Errorf("record options: %w", err)
	}
	if *directory == "" || flags.NArg() != 1 {
		return errors.New("supply --exchange-dir and one job; run yamata record --help")
	}
	publication, err := bag.Record(context.Background(), *directory, flags.Arg(0))
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, publication.SHA256); err != nil {
		return fmt.Errorf("write publication hash (bag is already saved): %w", err)
	}
	return nil
}
