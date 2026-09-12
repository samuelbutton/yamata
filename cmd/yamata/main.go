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
  yamata help

Commands:
  init  Create and check the data directory.
  help  Show this help.

Run yamata init --help for options.
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
