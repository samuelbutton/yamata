package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"

	"github.com/samuelbutton/yamata/internal/queue"
)

const queueHelp = `Usage: yamata queue --exchange-dir PATH [--after ID] [--limit N]

Print a JSON snapshot of an existing queue without claiming or changing jobs.
The cursor uses intake IDs, not priority order. A null next_after ends pagination.

Options:
  --exchange-dir PATH  Required existing queued exchange.
  --after ID           Return jobs after this intake ID (default: 0).
  --limit N            Return 1 to 1000 jobs (default: 100).
  -h, --help           Show help without opening the queue.
`
const faultHelp = `Usage: yamata fault --exchange-dir PATH --job ID --stage STAGE --failures N

Configure deterministic worker failures before a job's first claim.
One failure permits recovery; two exhaust the stage's single retry.
The configuration persists across worker restarts and cannot be changed.
Identical configuration is idempotent. Use a fresh job for another experiment.

Options:
  --exchange-dir PATH  Required existing queued exchange.
  --job ID             Required accepted job ID.
  --stage STAGE        Required simulation or analysis stage.
  --failures N         Required failure count: 1 or 2.
  -h, --help           Show help without writing files.
`

func inspectQueue(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("queue", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("exchange-dir", "", "exchange directory")
	after := flags.Int64("after", 0, "intake cursor")
	limit := flags.Int("limit", 100, "page size")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeHelp(out, queueHelp)
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("queue does not accept positional arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot, err := queue.Inspect(ctx, *directory, *after, *limit)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snapshot)
}

func injectFailure(args []string, out io.Writer) (err error) {
	flags := flag.NewFlagSet("fault", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("exchange-dir", "", "exchange directory")
	job := flags.String("job", "", "job ID")
	stage := flags.String("stage", "", "stage")
	failures := flags.Int("failures", 0, "failure count")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeHelp(out, faultHelp)
		}
		return err
	}
	if flags.NArg() != 0 || *directory == "" || *job == "" || (*stage != "simulation" && *stage != "analysis") || *failures < 1 || *failures > 2 {
		return errors.New("supply an exchange, job, stage, and 1 or 2 failures; run yamata fault --help")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Require an existing current queue; a typo must not initialize an empty exchange.
	if _, err = queue.Inspect(ctx, *directory, 0, 1); err != nil {
		return err
	}
	store, err := queue.Open(ctx, *directory)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	if err = store.InjectFailure(ctx, *job, *stage, *failures); err != nil {
		return err
	}
	return writeHelp(out, "Failure configuration accepted.\n")
}
