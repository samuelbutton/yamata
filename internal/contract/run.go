package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// RunJob contains resolved execution inputs, checked against the complete job schema.
type RunJob struct {
	ExecutionID string    `json:"execution_id"`
	InputsHash  string    `json:"inputs_hash"`
	Inputs      RunInputs `json:"inputs"`
}

// RunInputs maps the public contract without importing simulation or storage logic.
type RunInputs struct {
	Scenario struct {
		Type            string     `json:"type"`
		StartPositionMM int64      `json:"start_position_mm"`
		StartSpeedMMS   int64      `json:"start_speed_mm_s"`
		GoalPositionMM  int64      `json:"goal_position_mm"`
		VehicleLengthMM int64      `json:"vehicle_length_mm"`
		Obstacles       []Obstacle `json:"obstacles"`
	} `json:"scenario"`
	Controller  Component `json:"controller"`
	Simulator   Component `json:"simulator"`
	RunTemplate struct {
		TickMS int64 `json:"tick_ms"`
	} `json:"run_template"`
	AnalysisTemplate json.RawMessage `json:"analysis_template"`
	Seed             int64           `json:"seed"`
	Repeat           int             `json:"repeat"`
	Limits           struct {
		MaxTicks  int   `json:"max_ticks"`
		TimeoutMS int64 `json:"timeout_ms"`
	} `json:"limits"`
}

// Component pins a named algorithm and its version.
type Component struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// ReadRunJob validates a run job and its complete input hash from one bounded read.
// Schema support for a component version does not imply local execution support.
func (v *Validator) ReadRunJob(ctx context.Context, root *os.Root, path string) (RunJob, error) {
	if err := ctx.Err(); err != nil {
		return RunJob{}, err
	}
	if !strings.HasPrefix(path, "jobs/") || !strings.HasSuffix(path, ".json") {
		return RunJob{}, ErrPath
	}
	s := inspection{root: root}
	data, err := s.read(path)
	if err != nil {
		return RunJob{}, err
	}
	d, err := v.parse(data)
	if err != nil {
		return RunJob{}, err
	}
	if d.Kind != "job" || d.JobKind != "run" {
		return RunJob{}, fmt.Errorf("%w: recording requires a run job", ErrInvalid)
	}
	digest, err := contentHash(d.Inputs)
	if err != nil {
		return RunJob{}, err
	}
	if digest != d.InputsHash {
		return RunJob{}, ErrHash
	}
	var job RunJob
	if err := json.Unmarshal(data, &job); err != nil {
		return RunJob{}, err
	}
	return job, nil
}
