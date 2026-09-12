// Package simulator implements the deterministic, one-lane teaching model.
// It has no filesystem, clock, random source, or external service dependencies.
package simulator

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/samuelbutton/yamata/internal/geometry"
)

// Version identifies the motion and controller rules implemented by this package.
const Version = 1

const maxPositionMM int64 = 1_000_000_000

// Controller selects a built-in policy. It cannot name an executable.
type Controller string

const (
	Baseline  Controller = "baseline"
	Candidate Controller = "candidate"
)

// StopReason explains why a simulation ended. It is not an analysis score.
type StopReason string

const (
	GoalReached StopReason = "goal_reached"
	Stopped     StopReason = "stopped"
	Collision   StopReason = "collision"
)

// ErrInvalidConfig identifies unsupported or out-of-range simulation inputs.
var ErrInvalidConfig = errors.New("invalid simulation configuration")

// ErrTickLimit means the run exhausted its step budget before a terminal observation.
var ErrTickLimit = errors.New("simulation tick limit reached")

// Obstacle occupies [PositionMM, PositionMM+LengthMM] and moves at constant speed.
type Obstacle struct {
	PositionMM int64
	SpeedMMS   int64
	LengthMM   int64
}

// Scenario defines the initial one-lane world. Positions identify rear edges.
type Scenario struct {
	StartPositionMM int64
	StartSpeedMMS   int64
	GoalPositionMM  int64
	VehicleLengthMM int64
	Obstacles       []Obstacle
}

// Config is explicit run input. TickMS and MaxTicks bound simulated time and work.
// The caller must not mutate Obstacles while Run is executing.
type Config struct {
	Scenario   Scenario
	Controller Controller
	TickMS     int64
	MaxTicks   int
}

// Record is the state at one tick boundary. Tick zero contains the initial state.
// AccelerationMMS2 is the command applied during the interval ending at this tick.
// Each record owns its obstacle slice, independent of the inputs and other records.
type Record struct {
	Tick             int
	TimeMS           int64
	PositionMM       int64
	SpeedMMS         int64
	AccelerationMMS2 int64
	Obstacles        []Obstacle
}

// Trace contains the initial record and every completed step, in order.
// Records describe motion only; scoring and persistence belong to their callers.
type Trace struct {
	Reason  StopReason
	Records []Record
}

// Run advances the model in memory. Completed runs with identical inputs are equal.
// Cancellation and exhausted limits return an error with an empty trace.
func Run(ctx context.Context, config Config) (Trace, error) {
	if err := validate(config); err != nil {
		return Trace{}, err
	}
	if err := ctx.Err(); err != nil {
		return Trace{}, err
	}
	world := config.Scenario
	current := Record{PositionMM: world.StartPositionMM, SpeedMMS: world.StartSpeedMMS, Obstacles: slices.Clone(world.Obstacles)}
	records := make([]Record, 0, min(config.MaxTicks+1, 1024))
	records = append(records, current)
	policy := controller{kind: config.Controller, tickMS: config.TickMS, vehicleLengthMM: world.VehicleLengthMM}
	if reason := terminal(current, current, world); reason != "" {
		return Trace{reason, records}, nil
	}
	for tick := 1; tick <= config.MaxTicks; tick++ {
		if err := ctx.Err(); err != nil {
			return Trace{}, err
		}
		next := advance(current, policy.acceleration(current), config.TickMS)
		records = append(records, next)
		if reason := terminal(current, next, world); reason != "" {
			return Trace{reason, records}, nil
		}
		current = next
	}
	return Trace{}, ErrTickLimit
}

func validate(c Config) error {
	if c.Controller != Baseline && c.Controller != Candidate {
		return fmt.Errorf("%w: unknown controller %q", ErrInvalidConfig, c.Controller)
	}
	if c.TickMS < 1 || c.TickMS > 1000 || c.MaxTicks < 1 || c.MaxTicks > 100000 {
		return fmt.Errorf("%w: tick duration must be 1..1000 ms and step budget 1..100000", ErrInvalidConfig)
	}
	s := c.Scenario
	if s.GoalPositionMM < -maxPositionMM || s.GoalPositionMM > maxPositionMM || len(s.Obstacles) > 32 {
		return fmt.Errorf("%w: goal or obstacle count outside model limits", ErrInvalidConfig)
	}
	if err := validateBody(s.StartPositionMM, s.StartSpeedMMS, s.VehicleLengthMM, c); err != nil {
		return err
	}
	for i, o := range s.Obstacles {
		if err := validateBody(o.PositionMM, o.SpeedMMS, o.LengthMM, c); err != nil {
			return fmt.Errorf("obstacle %d: %w", i, err)
		}
	}
	return nil
}

func validateBody(position, speed, length int64, c Config) error {
	if position < -maxPositionMM || position > maxPositionMM || speed < 0 || speed > 1_000_000 || length < 1 || length > 1_000_000 {
		return fmt.Errorf("%w: position, speed, or length outside model limits", ErrInvalidConfig)
	}
	// All motion is forward. This conservative bound also prevents arithmetic overflow.
	if position+speed*c.TickMS*int64(c.MaxTicks)/1000 > maxPositionMM {
		return fmt.Errorf("%w: full coast path exceeds position limit; reduce the step budget", ErrInvalidConfig)
	}
	return nil
}

func advance(previous Record, acceleration, tickMS int64) Record {
	speed := max(int64(0), previous.SpeedMMS+acceleration*tickMS/1000)
	next := Record{
		Tick: previous.Tick + 1, TimeMS: previous.TimeMS + tickMS,
		PositionMM: previous.PositionMM + speed*tickMS/1000,
		SpeedMMS:   speed, AccelerationMMS2: acceleration, Obstacles: slices.Clone(previous.Obstacles),
	}
	for i := range next.Obstacles {
		next.Obstacles[i].PositionMM += next.Obstacles[i].SpeedMMS * tickMS / 1000
	}
	return next
}

func terminal(previous, current Record, world Scenario) StopReason {
	for i, o := range current.Obstacles {
		// Relative displacement is linear during a tick. Test the whole interval,
		// including edge contact, so a fast vehicle cannot skip through an obstacle.
		start := previous.Obstacles[i].PositionMM - previous.PositionMM
		end := o.PositionMM - current.PositionMM
		if geometry.Contact(start, end, world.VehicleLengthMM, o.LengthMM) {
			return Collision
		}
	}
	if current.PositionMM >= world.GoalPositionMM {
		return GoalReached
	}
	if current.SpeedMMS == 0 {
		return Stopped
	}
	return ""
}
