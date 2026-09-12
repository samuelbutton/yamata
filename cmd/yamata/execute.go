package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/samuelbutton/yamata/internal/standalone"
)

const executeHelp = `Usage: yamata run --exchange-dir PATH JOB

Record a standalone run job, score its saved bag, and publish a result and completion event.
JOB is a relative jobs/*.json path. Put options before JOB.
The summary identifies the status and published result and event paths.
PASS returns exit code 0. FAIL, WARN, ERROR, and command failures return exit code 1.
Repeated calls preserve accepted results and repair missing completion events.

Options:
  --exchange-dir PATH  Required existing exchange directory containing the job.
  -h, --help           Show this help without writing files.
`

func execute(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("exchange-dir", "", "exchange directory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeHelp(output, executeHelp)
		}
		return fmt.Errorf("run options: %w", err)
	}
	if *directory == "" || flags.NArg() != 1 {
		return errors.New("supply --exchange-dir and one job; run yamata run --help")
	}
	out, err := standalone.Run(context.Background(), *directory, flags.Arg(0))
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "status=%s result=%s result_sha256=%s event=%s event_sha256=%s\n", out.Status, out.Result.Path, out.Result.SHA256, out.Event.Path, out.Event.SHA256); err != nil {
		return fmt.Errorf("write outcome summary (files are already saved): %w", err)
	}
	if out.Status != "PASS" {
		return fmt.Errorf("execution finished with %s; see %s", out.Status, out.Result.Path)
	}
	return nil
}
