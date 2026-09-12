package simulator

import "fmt"

// Example returns fresh inputs for a built-in scenario using the baseline controller.
func Example(name string) (Config, error) {
	config := Config{
		Scenario:   Scenario{StartPositionMM: 0, StartSpeedMMS: 10000, GoalPositionMM: 60000, VehicleLengthMM: 4000},
		Controller: Baseline, TickMS: 100, MaxTicks: 100,
	}
	switch name {
	case "empty-lane":
	case "stopped-obstacle":
		config.Scenario.Obstacles = []Obstacle{{PositionMM: 25000, LengthMM: 4000}}
	case "moving-obstacle":
		config.Scenario.Obstacles = []Obstacle{{PositionMM: 25000, SpeedMMS: 2000, LengthMM: 4000}}
	default:
		return Config{}, fmt.Errorf("%w: unknown scenario %q", ErrInvalidConfig, name)
	}
	return config, nil
}
