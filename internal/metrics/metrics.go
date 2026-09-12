// Package metrics calculates versioned scores from recorded motion without simulation.
package metrics

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/samuelbutton/yamata/internal/contract"
	"github.com/samuelbutton/yamata/internal/geometry"
)

// Scores separates aggregate status from each metric's value, limit, and evidence.
type Scores struct {
	Status  string
	Metrics map[string]contract.Metric
}

type limit struct {
	Version    int   `json:"version"`
	Maximum    int64 `json:"maximum"`
	MinimumMM  int64 `json:"minimum_mm"`
	MinimumPPM int64 `json:"minimum_ppm"`
}

// Evaluate requires a schema-validated bag and run inputs from the same execution.
// It checks the recorded initial state and obstacle continuity before scoring.
// Empty obstacle sets and undefined goal distances produce explicit null metrics.
func Evaluate(ctx context.Context, bag contract.Bag, in contract.RunInputs) (Scores, error) {
	var limits map[string]limit
	if err := json.Unmarshal(in.AnalysisTemplate, &limits); err != nil {
		return Scores{}, err
	}
	for _, name := range []string{"collision_count", "minimum_obstacle_gap", "goal_progress"} {
		if limits[name].Version != 1 && !(name == "minimum_obstacle_gap" && limits[name].Version == 2) {
			return Scores{}, fmt.Errorf("%w: unsupported metric version for %s", contract.ErrVersion, name)
		}
	}
	if len(limits) != 3 || limits["collision_count"].Maximum != 0 {
		return Scores{}, fmt.Errorf("%w: invalid metric limits", contract.ErrInvalid)
	}
	if len(bag.Records) == 0 {
		return Scores{}, fmt.Errorf("%w: empty recording", contract.ErrInvalid)
	}
	s := in.Scenario
	first := bag.Records[0]
	if first.PositionMM != s.StartPositionMM || first.SpeedMMS != s.StartSpeedMMS || first.AccelerationMMS2 != 0 || len(first.Obstacles) != len(s.Obstacles) {
		return Scores{}, fmt.Errorf("%w: initial record differs from scenario", contract.ErrInvalid)
	}
	for i, o := range s.Obstacles {
		if first.Obstacles[i] != o {
			return Scores{}, fmt.Errorf("%w: initial obstacle differs from scenario", contract.ErrInvalid)
		}
	}
	var collisions int64
	contactEvidence := []int{}
	var minimum *int64
	minimumTick := 0
	active := make([]bool, len(s.Obstacles))
	for i, r := range bag.Records {
		if err := ctx.Err(); err != nil {
			return Scores{}, err
		}
		if len(r.Obstacles) != len(s.Obstacles) {
			return Scores{}, fmt.Errorf("%w: obstacle count changed", contract.ErrInvalid)
		}
		for j, o := range r.Obstacles {
			if o.LengthMM != s.Obstacles[j].LengthMM || o.SpeedMMS != s.Obstacles[j].SpeedMMS {
				return Scores{}, fmt.Errorf("%w: obstacle shape or speed changed", contract.ErrInvalid)
			}
			end := o.PositionMM - r.PositionMM
			start := end
			if i > 0 {
				previous := bag.Records[i-1]
				if o.PositionMM != previous.Obstacles[j].PositionMM+o.SpeedMMS*in.RunTemplate.TickMS/1000 {
					return Scores{}, fmt.Errorf("%w: obstacle motion changed", contract.ErrInvalid)
				}
				start = previous.Obstacles[j].PositionMM - previous.PositionMM
			}
			if geometry.Contact(start, end, s.VehicleLengthMM, o.LengthMM) && !active[j] {
				collisions++
				if len(contactEvidence) == 0 {
					contactEvidence = append(contactEvidence, r.Tick)
				}
			}
			active[j] = geometry.Contact(end, end, s.VehicleLengthMM, o.LengthMM)
			// Double centers retain odd lengths until the final integer truncation.
			distance := 2*end + o.LengthMM - s.VehicleLengthMM
			if distance < 0 {
				distance = -distance
			}
			distance /= 2
			if limits["minimum_obstacle_gap"].Version == 2 {
				distance = max(int64(0), end-s.VehicleLengthMM, -end-o.LengthMM)
			}
			if minimum == nil || distance < *minimum {
				minimum = &distance
				minimumTick = r.Tick
			}
		}
	}
	values := map[string]contract.Metric{
		"collision_count":      measured(collisions, "count", collisions == 0, contactEvidence),
		"minimum_obstacle_gap": unavailable("mm"),
		"goal_progress":        unavailable("ppm"),
	}
	if minimum != nil {
		values["minimum_obstacle_gap"] = measured(*minimum, "mm", *minimum >= limits["minimum_obstacle_gap"].MinimumMM, []int{minimumTick})
	}
	if distance := s.GoalPositionMM - s.StartPositionMM; distance > 0 {
		last := bag.Records[len(bag.Records)-1]
		progress := min(int64(1000000), max(int64(0), (last.PositionMM-s.StartPositionMM)*1000000/distance))
		values["goal_progress"] = measured(progress, "ppm", progress >= limits["goal_progress"].MinimumPPM, []int{last.Tick})
	}
	gap := values["minimum_obstacle_gap"]
	gap.Version = limits["minimum_obstacle_gap"].Version
	values["minimum_obstacle_gap"] = gap
	status := "PASS"
	for _, m := range values {
		if m.Pass != nil && !*m.Pass {
			status = "FAIL"
			break
		}
		if m.Pass == nil {
			status = "WARN"
		}
	}
	return Scores{Status: status, Metrics: values}, nil
}

func measured(value int64, unit string, pass bool, ticks []int) contract.Metric {
	return contract.Metric{Value: &value, Version: 1, Unit: unit, Pass: &pass, EvidenceTicks: ticks}
}
func unavailable(unit string) contract.Metric {
	return contract.Metric{Version: 1, Unit: unit, EvidenceTicks: []int{}}
}
