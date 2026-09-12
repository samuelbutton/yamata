package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// JobInfo identifies an immutable job snapshot independently of its input kind.
type JobInfo struct {
	Priority      int    `json:"priority"`
	JobID         string `json:"job_id"`
	CorrelationID string `json:"correlation_id,omitempty"`
	SHA256        string `json:"-"`
	ExecutionID   string `json:"execution_id"`
	InputsHash    string `json:"inputs_hash"`
}

// Job preserves schema-validated inputs for either supported job kind.
type Job struct {
	JobInfo
	JobKind string          `json:"job_kind"`
	Inputs  json.RawMessage `json:"inputs"`
}

// RunJob contains resolved execution inputs, checked against the complete job schema.
type RunJob struct {
	JobInfo
	Inputs RunInputs `json:"inputs"`
}

// AnalysisInputs selects recorded motion and a complete scoring template.
type AnalysisInputs struct {
	Bag              Reference       `json:"bag"`
	AnalysisTemplate json.RawMessage `json:"analysis_template"`
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
	return v.ParseRunJob(ctx, data)
}

// ParseRunJob validates a saved run-job snapshot without reading a mutable path.
func (v *Validator) ParseRunJob(ctx context.Context, data []byte) (RunJob, error) {
	job, err := v.ParseJob(ctx, data)
	if err != nil {
		return RunJob{}, err
	}
	if job.JobKind != "run" {
		return RunJob{}, fmt.Errorf("%w: recording requires a run job", ErrInvalid)
	}
	out := RunJob{JobInfo: job.JobInfo}
	err = json.Unmarshal(job.Inputs, &out.Inputs)
	return out, err
}

// ParseJob validates one saved job snapshot and its complete input hash.
func (v *Validator) ParseJob(ctx context.Context, data []byte) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	d, err := v.parse(data)
	if err != nil {
		return Job{}, err
	}
	if d.Kind != "job" {
		return Job{}, fmt.Errorf("%w: expected job", ErrInvalid)
	}
	digest, err := ContentHash(d.Inputs)
	if err != nil {
		return Job{}, err
	}
	if digest != d.InputsHash {
		return Job{}, ErrHash
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return Job{}, err
	}
	job.SHA256 = hashBytes(data)
	return job, nil
}
