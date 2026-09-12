package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/samuelbutton/yamata/internal/simulator"
)

const simulateHelp = `Usage: yamata simulate [--scenario NAME] [--controller NAME]

Run a built-in scenario in memory and print its final observation.
A collision is a simulation outcome, not an execution error or a passing score.
This command writes no bags, results, or data directories.

Options:
  --scenario NAME    empty-lane, stopped-obstacle, or moving-obstacle
                     (default: stopped-obstacle).
  --controller NAME  baseline or candidate (default: baseline).
  -h, --help         Show this help.
`

func simulate(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("simulate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	scenario := flags.String("scenario", "stopped-obstacle", "built-in scenario")
	controller := flags.String("controller", "baseline", "built-in controller")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeHelp(output, simulateHelp)
		}
		return fmt.Errorf("simulate options: %w", err)
	}
	if flags.NArg() != 0 {
		return errors.New("simulate does not accept positional arguments; use --scenario NAME")
	}
	config, err := simulator.Example(*scenario)
	if err != nil {
		return err
	}
	config.Controller = simulator.Controller(*controller)
	trace, err := simulator.Run(context.Background(), config)
	if err != nil {
		return err
	}
	final := trace.Records[len(trace.Records)-1]
	_, err = fmt.Fprintf(output, "simulator_version=%d scenario=%s controller=%s outcome=%s tick=%d time_ms=%d position_mm=%d speed_mm_s=%d\n",
		simulator.Version, *scenario, *controller, trace.Reason, final.Tick, final.TimeMS, final.PositionMM, final.SpeedMMS)
	if err != nil {
		return fmt.Errorf("write simulation summary: %w", err)
	}
	return nil
}
