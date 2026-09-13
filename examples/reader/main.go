// The example reader owns only its index and event progress.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const help = `Usage: reader COMMAND --state-dir PATH [--exchange-dir PATH] [EVENT ...]

Commands:
  sync     Validate events and commit progress with referenced results.
  watch    Repeat sync each second until interrupted.
  rebuild  Replace the result index from result files without reading events.
  list     Print saved event count and indexed results as JSON.

Use an existing owner-only state directory outside the exchange.
The reader opens only published exchange files and its own reader.sqlite file.
Sync accepts optional relative events/*.json paths, including duplicates.
Without paths, sync scans event files. Staging files are ignored.
Each event and its index changes commit together; rebuild commits as one unit.
List needs only --state-dir. Other commands also require --exchange-dir.
Options must precede event paths. Use --help to display this help.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "reader: %v\n", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, out io.Writer) (err error) {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "help" || args[0] == "-h")) {
		_, err = io.WriteString(out, help)
		return err
	}
	command := args[0]
	if command != "sync" && command != "watch" && command != "rebuild" && command != "list" {
		return errors.New("unknown reader command")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", "", "reader state")
	directory := flags.String("exchange-dir", "", "exchange")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err = io.WriteString(out, help)
			return err
		}
		return err
	}
	if *state == "" || (command != "list" && *directory == "") || (command == "list" && *directory != "") || (command != "sync" && flags.NArg() != 0) {
		return errors.New("supply required directories and event paths only for sync; run reader --help")
	}
	var e exchange
	source := ""
	if command != "list" {
		source, err = filepath.Abs(*directory)
		if err != nil {
			return err
		}
		source, err = filepath.EvalSymlinks(source)
		if err != nil {
			return err
		}
		e.root, err = os.OpenRoot(source)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, e.root.Close()) }()
		e.schema, err = compileSchema()
		if err != nil {
			return err
		}
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	idx, err := openIndex(startup, *state, source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, idx.close()) }()
	if command == "list" {
		snapshot, err := idx.snapshot(startup)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(snapshot)
	}
	previous := -1
	for {
		pass, stop := context.WithTimeout(ctx, 30*time.Second)
		if command == "rebuild" {
			err = idx.rebuild(pass, e)
		} else {
			err = idx.sync(pass, e, flags.Args())
		}
		if err == nil {
			var events, results int
			err = idx.db.QueryRowContext(pass, "SELECT (SELECT count(*) FROM seen),(SELECT count(*) FROM results)").Scan(&events, &results)
			if err == nil && events != previous {
				_, err = fmt.Fprintf(out, "processed_events=%d indexed_results=%d\n", events, results)
				previous = events
			}
		}
		stop()
		if ctx.Err() != nil && command == "watch" {
			return nil
		}
		if err != nil || command != "watch" {
			return err
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
