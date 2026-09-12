// Package bag connects resolved jobs, the in-memory simulator, and immutable recordings.
package bag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/samuelbutton/yamata/internal/contract"
	"github.com/samuelbutton/yamata/internal/simulator"
)

// Publication identifies the exact bytes accepted for an execution.
type Publication struct {
	Path   string
	SHA256 string
}

// Record runs a validated job and publishes bags/<execution_id>.jsonl.
// Repeated identical output succeeds; different output never replaces an existing bag.
// A failure after publication can leave a complete final file; retry the same job.
func Record(ctx context.Context, directory, jobPath string) (result Publication, err error) {
	if directory == "" {
		return result, errors.New("exchange directory is required")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return result, fmt.Errorf("open exchange: %w", err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	v, err := contract.New()
	if err != nil {
		return result, err
	}
	job, err := v.ReadRunJob(ctx, root, jobPath)
	if err != nil {
		return result, err
	}
	config, err := configuration(job.Inputs)
	if err != nil {
		return result, err
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(job.Inputs.Limits.TimeoutMS)*time.Millisecond)
	trace, err := simulator.Run(runCtx, config)
	cancel()
	if err != nil {
		return result, fmt.Errorf("simulate job: %w", err)
	}
	data, err := encode(ctx, job, trace)
	if err != nil {
		return result, err
	}
	checked, err := v.ParseBag(ctx, data)
	if err != nil {
		return result, fmt.Errorf("validate recording: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := root.Mkdir("bags", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return result, fmt.Errorf("create bags directory: %w", err)
	}
	// Persist the directory entry before publishing files inside it.
	if err := syncDirectory(root); err != nil {
		return result, err
	}
	dir, err := root.OpenRoot("bags")
	if err != nil {
		return result, fmt.Errorf("open bags directory: %w", err)
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	name := job.ExecutionID + ".jsonl"
	if err := publish(ctx, dir, name, checked.SHA256, v, func(w io.Writer) error {
		_, err := io.Copy(w, bytes.NewReader(data))
		return err
	}); err != nil {
		return result, err
	}
	return Publication{Path: "bags/" + name, SHA256: checked.SHA256}, nil
}

func configuration(in contract.RunInputs) (simulator.Config, error) {
	if in.Simulator.Version != simulator.Version || in.Controller.Version != 1 {
		return simulator.Config{}, fmt.Errorf("%w: recorder supports simulator and controller version 1", contract.ErrVersion)
	}
	s := in.Scenario
	obstacles := make([]simulator.Obstacle, len(s.Obstacles))
	for i, o := range s.Obstacles {
		obstacles[i] = simulator.Obstacle{PositionMM: o.PositionMM, SpeedMMS: o.SpeedMMS, LengthMM: o.LengthMM}
	}
	return simulator.Config{
		Scenario:   simulator.Scenario{StartPositionMM: s.StartPositionMM, StartSpeedMMS: s.StartSpeedMMS, GoalPositionMM: s.GoalPositionMM, VehicleLengthMM: s.VehicleLengthMM, Obstacles: obstacles},
		Controller: simulator.Controller(in.Controller.Name), TickMS: in.RunTemplate.TickMS, MaxTicks: in.Limits.MaxTicks,
	}, nil
}

func encode(ctx context.Context, job contract.RunJob, trace simulator.Trace) ([]byte, error) {
	buffer := boundedBuffer{}
	encoder := json.NewEncoder(&buffer)
	header := contract.BagHeader{ContractVersion: 1, Kind: "bag", ExecutionID: job.ExecutionID, FormatVersion: 1, InputsHash: job.InputsHash, TickMS: int(job.Inputs.RunTemplate.TickMS), RecordCount: len(trace.Records)}
	if err := encoder.Encode(header); err != nil {
		return nil, err
	}
	for _, r := range trace.Records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		obstacles := make([]contract.Obstacle, len(r.Obstacles))
		for i, o := range r.Obstacles {
			obstacles[i] = contract.Obstacle{PositionMM: o.PositionMM, SpeedMMS: o.SpeedMMS, LengthMM: o.LengthMM}
		}
		tick := contract.Tick{Kind: "tick", Tick: r.Tick, TimeMS: r.TimeMS, PositionMM: r.PositionMM, SpeedMMS: r.SpeedMMS, AccelerationMMS2: r.AccelerationMMS2, Obstacles: obstacles}
		if err := encoder.Encode(tick); err != nil {
			return nil, err
		}
	}
	return buffer.Bytes(), nil
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > contract.MaxBagBytes-b.Len() {
		return 0, fmt.Errorf("%w: recording exceeds 16 MiB", contract.ErrInvalid)
	}
	return b.Buffer.Write(p)
}
