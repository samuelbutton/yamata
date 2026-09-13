package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/samuelbutton/yamata/internal/datadir"
)

const help = `Yamata: local simulation tools.

Usage:
  yamata init [--data-dir PATH]
  yamata validate --exchange-dir PATH FILE [FILE ...]
  yamata simulate [--scenario NAME] [--controller NAME]
  yamata record --exchange-dir PATH JOB
  yamata run --exchange-dir PATH JOB
  yamata enqueue --exchange-dir PATH JOB
  yamata workers --exchange-dir PATH [OPTIONS]
  yamata queue --exchange-dir PATH [OPTIONS]
  yamata fault --exchange-dir PATH --job ID --stage STAGE --failures N
  yamata inspect --exchange-dir PATH --sha256 HASH BAG
  yamata help

Commands:
  init  Create and check the data directory.
  validate  Check exchange files and their references.
  simulate  Run a built-in simulation in memory.
  record  Run a resolved job and save an immutable bag.
  run  Record, score, and publish a standalone job outcome.
  enqueue  Commit a job to the durable queue.
  workers  Run simulation and analysis worker pools.
  queue  Inspect queue state and publication progress.
  fault  Configure deterministic worker failures.
  inspect  Check a saved bag and print its motion summary.
  help  Show this help.

Run yamata COMMAND --help for options.
`

const initHelp = `Usage: yamata init [--data-dir PATH]

Create the data directory and check that a file can be written and removed.
Existing files remain unchanged.

Options:
  --data-dir PATH  Data directory (default: .yamata in the current directory).
  -h, --help       Show this help without creating a directory.
`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		// Reporting to stderr is best effort when the output stream is unavailable.
		_, _ = fmt.Fprintf(os.Stderr, "yamata: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 0 {
		return writeHelp(output, help)
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			return errors.New("help does not accept arguments")
		}
		return writeHelp(output, help)
	case "init":
		return initialize(args[1:], output)
	case "validate":
		return validate(args[1:], output)
	case "simulate":
		return simulate(args[1:], output)
	case "record":
		return record(args[1:], output)
	case "run":
		return execute(args[1:], output)
	case "enqueue":
		return enqueue(args[1:], output)
	case "workers":
		return workers(args[1:], output)
	case "queue":
		return inspectQueue(args[1:], output)
	case "fault":
		return injectFailure(args[1:], output)
	case "inspect":
		return inspect(args[1:], output)
	default:
		return fmt.Errorf("unknown command %q; run yamata help", args[0])
	}
}

func initialize(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("data-dir", ".yamata", "data directory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeHelp(output, initHelp)
		}
		return fmt.Errorf("init options: %w; run yamata init --help", err)
	}
	if flags.NArg() != 0 {
		return errors.New("init does not accept positional arguments; use --data-dir PATH")
	}
	resolved, err := datadir.Prepare(*path)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "Data directory ready: %q\n", resolved); err != nil {
		return fmt.Errorf("write confirmation: %w", err)
	}
	return nil
}

func writeHelp(output io.Writer, text string) error {
	if _, err := io.WriteString(output, text); err != nil {
		return fmt.Errorf("write help: %w", err)
	}
	return nil
}
