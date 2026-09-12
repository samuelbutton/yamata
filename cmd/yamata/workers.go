package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/samuelbutton/yamata/internal/queue"
)

const enqueueHelp = `Usage: yamata enqueue --exchange-dir PATH JOB

Import a complete run job into SQLite before printing its receipt.
JOB is a relative jobs/*.json path. Identical deliveries share one receipt.
Options must precede JOB.

Options:
  --exchange-dir PATH  Required existing exchange directory.
  -h, --help           Show help without writing files.
`
const workersHelp = `Usage: yamata workers --exchange-dir PATH [OPTIONS]

Run independently bounded simulation and analysis pools until interrupted.
Zero workers disables that stage. Supply 1 to 64 workers in total.
The drain option finishes enabled stages and pending file deliveries, then exits.
Worker success describes queue processing; results contain simulation scores.

Options:
  --exchange-dir PATH   Required existing exchange directory.
  --simulation-workers N  Simulation pool size (default: 1).
  --analysis-workers N    Analysis pool size (default: 1).
  --lease-duration D      Lease duration, 100ms to 1h (default: 30s).
  --drain                 Exit when enabled stages drain.
  -h, --help              Show help without writing files.
`

func enqueue(args []string, output io.Writer) (err error) {
	flags := flag.NewFlagSet("enqueue", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("exchange-dir", "", "exchange directory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeHelp(output, enqueueHelp)
		}
		return err
	}
	if *directory == "" || flags.NArg() != 1 {
		return errors.New("supply --exchange-dir and one job; run yamata enqueue --help")
	}
	ctx := context.Background()
	s, err := queue.Open(ctx, *directory)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	receipt, err := s.Import(ctx, flags.Arg(0))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "accepted job=%s execution=%s duplicate=%t\n", receipt.JobID, receipt.ExecutionID, receipt.Duplicate)
	return err
}

func workers(args []string, output io.Writer) (err error) {
	flags := flag.NewFlagSet("workers", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("exchange-dir", "", "exchange directory")
	sim := flags.Int("simulation-workers", 1, "simulation pool size")
	analysis := flags.Int("analysis-workers", 1, "analysis pool size")
	lease := flags.Duration("lease-duration", 30*time.Second, "lease duration")
	drain := flags.Bool("drain", false, "drain enabled stages")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeHelp(output, workersHelp)
		}
		return err
	}
	if *directory == "" || flags.NArg() != 0 {
		return errors.New("supply --exchange-dir without positional arguments; run yamata workers --help")
	}
	options := queue.Options{SimulationWorkers: *sim, AnalysisWorkers: *analysis, LeaseDuration: *lease, Drain: *drain}
	if err := options.Validate(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, err := queue.Open(ctx, *directory)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	if err := s.Work(ctx, options); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return writeHelp(output, "Workers stopped; unfinished stages remain queued.\n")
		}
		return err
	}
	return writeHelp(output, "Enabled queues drained; outcome files are published.\n")
}
