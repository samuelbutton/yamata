package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/samuelbutton/yamata/internal/contract"
)

const inspectHelp = `Usage: yamata inspect --exchange-dir PATH --sha256 HASH BAG

Validate a complete bag and its expected hash, then print a motion summary.
Use the hash printed by record or supplied in a trusted file reference.
This command reads the saved bag without running the simulator or calculating scores.
BAG is relative to the exchange directory. Put options before BAG.

Options:
  --exchange-dir PATH  Required exchange directory.
  --sha256 HASH        Required lowercase SHA-256 hash of the expected bytes.
  -h, --help           Show this help without writing files.
`

func inspect(args []string, output io.Writer) (err error) {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("exchange-dir", "", "exchange directory")
	digest := flags.String("sha256", "", "expected bag hash")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeHelp(output, inspectHelp)
		}
		return fmt.Errorf("inspect options: %w", err)
	}
	if *directory == "" || *digest == "" || flags.NArg() != 1 {
		return errors.New("supply --exchange-dir, --sha256, and one bag; run yamata inspect --help")
	}
	v, err := contract.New()
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(*directory)
	if err != nil {
		return fmt.Errorf("open exchange: %w", err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	bag, err := v.ReadBag(context.Background(), root, flags.Arg(0), *digest)
	if err != nil {
		return err
	}
	first, last := bag.Records[0], bag.Records[len(bag.Records)-1]
	_, err = fmt.Fprintf(output, "execution_id=%s format_version=%d inputs_hash=%s sha256=%s records=%d tick_ms=%d start_position_mm=%d final_tick=%d final_time_ms=%d final_position_mm=%d final_speed_mm_s=%d\n",
		bag.Header.ExecutionID, bag.Header.FormatVersion, bag.Header.InputsHash, bag.SHA256, len(bag.Records), bag.Header.TickMS, first.PositionMM, last.Tick, last.TimeMS, last.PositionMM, last.SpeedMMS)
	if err != nil {
		return fmt.Errorf("write bag summary: %w", err)
	}
	return nil
}
