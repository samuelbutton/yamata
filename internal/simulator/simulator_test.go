package simulator

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
)

func example(t *testing.T, name string) Config {
	t.Helper()
	c, err := Example(name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func execute(t *testing.T, c Config) Trace {
	t.Helper()
	trace, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	return trace
}

func TestExampleOutcomes(t *testing.T) {
	cases := []struct {
		name            string
		controller      Controller
		reason          StopReason
		tick            int
		position, speed int64
	}{
		{"empty-lane", Baseline, GoalReached, 60, 60000, 10000},
		{"empty-lane", Candidate, GoalReached, 60, 60000, 10000},
		{"stopped-obstacle", Baseline, Stopped, 31, 18000, 0},
		{"stopped-obstacle", Candidate, Collision, 22, 21600, 8400},
		{"moving-obstacle", Baseline, Stopped, 32, 19000, 0},
		{"moving-obstacle", Candidate, Collision, 27, 26600, 8400},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/"+string(tc.controller), func(t *testing.T) {
			c := example(t, tc.name)
			c.Controller = tc.controller
			trace := execute(t, c)
			last := trace.Records[len(trace.Records)-1]
			if trace.Reason != tc.reason || last.Tick != tc.tick || last.PositionMM != tc.position || last.SpeedMMS != tc.speed {
				t.Fatalf("got (%s, tick=%d, position=%d, speed=%d), want (%s, %d, %d, %d)", trace.Reason, last.Tick, last.PositionMM, last.SpeedMMS, tc.reason, tc.tick, tc.position, tc.speed)
			}
			if len(trace.Records) != tc.tick+1 {
				t.Fatalf("records = %d, want initial state plus %d ticks", len(trace.Records), tc.tick)
			}
		})
	}
}

func TestCoastingMotion(t *testing.T) {
	c := example(t, "empty-lane")
	c.Scenario.StartPositionMM = -1000
	c.Scenario.StartSpeedMMS = 3000
	c.Scenario.GoalPositionMM = 200
	trace := execute(t, c)
	want := []int64{-1000, -700, -400, -100, 200}
	if len(trace.Records) != len(want) {
		t.Fatalf("records = %d, want %d", len(trace.Records), len(want))
	}
	for i, r := range trace.Records {
		if r.PositionMM != want[i] || r.SpeedMMS != 3000 || r.TimeMS != int64(i)*100 || r.AccelerationMMS2 != 0 {
			t.Fatalf("unexpected coasting record: %+v", r)
		}
	}
}

func TestStoppingDistance(t *testing.T) {
	c := example(t, "stopped-obstacle")
	c.Scenario.Obstacles[0].PositionMM = 18000 // Brake immediately with a 14,000 mm edge gap.
	trace := execute(t, c)
	last := trace.Records[len(trace.Records)-1]
	// 100 ms steps reduce speed by 400 mm/s. Summing 960, 920, ... 40, 0 mm
	// gives 12,000 mm of travel, with no negative speed or reverse motion.
	if trace.Reason != Stopped || last.Tick != 25 || last.PositionMM != 12000 || last.SpeedMMS != 0 {
		t.Fatalf("unexpected stop: reason=%s record=%+v", trace.Reason, last)
	}
	for _, r := range trace.Records[1:] {
		if r.AccelerationMMS2 != -4000 {
			t.Fatalf("brake released before stopping: %+v", r)
		}
	}
}

func TestVelocityClampAndIntegerRounding(t *testing.T) {
	for _, tc := range []struct {
		name                                         string
		speed, acceleration, dt, position, wantSpeed int64
	}{
		{"positive acceleration", 1000, 2000, 100, 120, 1200},
		{"brake clamp", 100, -4000, 100, 0, 0},
		{"submillimetre motion", 999, 0, 1, 0, 999},
		{"fractional millimetre", 1001, 0, 100, 100, 1001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := advance(Record{SpeedMMS: tc.speed}, tc.acceleration, tc.dt)
			if got.PositionMM != tc.position || got.SpeedMMS != tc.wantSpeed || got.TimeMS != tc.dt {
				t.Fatalf("got %+v, want position=%d speed=%d time=%d", got, tc.position, tc.wantSpeed, tc.dt)
			}
		})
	}
}

func TestObstacleMotion(t *testing.T) {
	c := example(t, "moving-obstacle")
	trace := execute(t, c)
	for _, r := range trace.Records {
		o := r.Obstacles[0]
		if o.PositionMM != 25000+int64(r.Tick)*200 || o.SpeedMMS != 2000 || o.LengthMM != 4000 {
			t.Fatalf("unexpected obstacle at tick %d: %+v", r.Tick, o)
		}
	}
}

func TestContactAcrossTick(t *testing.T) {
	cases := []struct {
		name                                                 string
		vehicleStart, vehicleEnd, obstacleStart, obstacleEnd int64
		contact                                              bool
	}{
		{"front edge", 0, 0, 4000, 4000, true},
		{"rear edge", 0, 0, -4000, -4000, true},
		{"overlap", 0, 0, 2000, 2000, true},
		{"one millimetre gap", 0, 0, 4001, 4001, false},
		{"vehicle passes entire obstacle", 0, 100000, 25000, 25000, true},
		{"obstacle passes entire vehicle", 0, 1000, -10000, 100000, true},
		{"obstacle remains behind", 0, 1000, -10000, -10000, false},
		{"equal speed with overlapping world paths", 0, 100000, 10000, 110000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			previous := Record{PositionMM: tc.vehicleStart, SpeedMMS: 10000, Obstacles: []Obstacle{{PositionMM: tc.obstacleStart, LengthMM: 4000}}}
			current := Record{PositionMM: tc.vehicleEnd, SpeedMMS: 10000, Obstacles: []Obstacle{{PositionMM: tc.obstacleEnd, LengthMM: 4000}}}
			got := terminal(previous, current, Scenario{VehicleLengthMM: 4000, GoalPositionMM: 1000000})
			if (got == Collision) != tc.contact {
				t.Fatalf("reason = %q, want contact=%v", got, tc.contact)
			}
		})
	}
}

func TestFastRunCannotSkipObstacle(t *testing.T) {
	c := example(t, "stopped-obstacle")
	c.TickMS = 1000
	c.Scenario.StartSpeedMMS = 1000000
	trace := execute(t, c)
	last := trace.Records[len(trace.Records)-1]
	if trace.Reason != Collision || last.Tick != 1 {
		t.Fatalf("missed between-tick contact: %s at %d", trace.Reason, last.Tick)
	}
	if last.PositionMM <= last.Obstacles[0].PositionMM+last.Obstacles[0].LengthMM {
		t.Fatal("fixture must end beyond the obstacle to prove swept contact")
	}
}

func TestInitialAndTerminalPriority(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		reason StopReason
	}{
		{"already at goal", func(c *Config) { c.Scenario.GoalPositionMM = 0 }, GoalReached},
		{"initially stopped", func(c *Config) { c.Scenario.StartSpeedMMS = 0 }, Stopped},
		{"contact before goal or stop", func(c *Config) {
			c.Scenario.GoalPositionMM = 0
			c.Scenario.StartSpeedMMS = 0
			c.Scenario.Obstacles = []Obstacle{{PositionMM: 4000, LengthMM: 4000}}
		}, Collision},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := example(t, "empty-lane")
			tc.mutate(&c)
			trace := execute(t, c)
			if trace.Reason != tc.reason || len(trace.Records) != 1 {
				t.Fatalf("got %s with %d records", trace.Reason, len(trace.Records))
			}
		})
	}
}

func TestNearestObstacleDoesNotDependOnInputOrder(t *testing.T) {
	c := example(t, "stopped-obstacle")
	near := c.Scenario.Obstacles[0]
	far := Obstacle{PositionMM: 50000, LengthMM: 4000}
	c.Scenario.Obstacles = []Obstacle{far, near}
	first := execute(t, c)
	c.Scenario.Obstacles = []Obstacle{near, far}
	second := execute(t, c)
	if first.Reason != second.Reason || len(first.Records) != len(second.Records) {
		t.Fatal("obstacle order changed termination")
	}
	for i, r := range first.Records {
		if r.PositionMM != second.Records[i].PositionMM || r.SpeedMMS != second.Records[i].SpeedMMS {
			t.Fatalf("obstacle order changed motion at tick %d", i)
		}
	}
}

func TestRepeatabilityAndOwnership(t *testing.T) {
	c := example(t, "moving-obstacle")
	original := c.Scenario.Obstacles[0]
	first := execute(t, c)
	second := execute(t, c)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("identical inputs produced different records")
	}
	if c.Scenario.Obstacles[0] != original {
		t.Fatal("simulation mutated caller input")
	}
	first.Records[0].Obstacles[0].PositionMM = -999
	if first.Records[1].Obstacles[0].PositionMM != 25200 || second.Records[0].Obstacles[0] != original || c.Scenario.Obstacles[0] != original {
		t.Fatal("record snapshots share mutable obstacles")
	}
	c.Scenario.Obstacles[0].PositionMM = -999
	fresh := example(t, "moving-obstacle")
	if fresh.Scenario.Obstacles[0] != original {
		t.Fatal("examples share mutable inputs")
	}
}

func TestLimitsAndCancellation(t *testing.T) {
	c := example(t, "empty-lane")
	c.MaxTicks = 1
	trace, err := Run(context.Background(), c)
	if !errors.Is(err, ErrTickLimit) || len(trace.Records) != 0 || trace.Reason != "" {
		t.Fatalf("got (%+v, %v), want empty trace and tick limit", trace, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	trace, err = Run(ctx, c)
	if !errors.Is(err, context.Canceled) || len(trace.Records) != 0 {
		t.Fatalf("cancellation = (%+v, %v)", trace, err)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"unknown controller", func(c *Config) { c.Controller = "external" }},
		{"zero tick duration", func(c *Config) { c.TickMS = 0 }},
		{"long tick duration", func(c *Config) { c.TickMS = 1001 }},
		{"zero budget", func(c *Config) { c.MaxTicks = 0 }},
		{"large budget", func(c *Config) { c.MaxTicks = 100001 }},
		{"negative speed", func(c *Config) { c.Scenario.StartSpeedMMS = -1 }},
		{"large speed", func(c *Config) { c.Scenario.StartSpeedMMS = math.MaxInt64 }},
		{"large position", func(c *Config) { c.Scenario.StartPositionMM = math.MaxInt64 }},
		{"large goal", func(c *Config) { c.Scenario.GoalPositionMM = math.MaxInt64 }},
		{"empty vehicle", func(c *Config) { c.Scenario.VehicleLengthMM = 0 }},
		{"negative obstacle speed", func(c *Config) { c.Scenario.Obstacles[0].SpeedMMS = -1 }},
		{"empty obstacle", func(c *Config) { c.Scenario.Obstacles[0].LengthMM = 0 }},
		{"many obstacles", func(c *Config) { c.Scenario.Obstacles = make([]Obstacle, 33) }},
		{"vehicle leaves coordinate range", func(c *Config) { c.Scenario.StartPositionMM = 1000000000 }},
		{"obstacle leaves coordinate range", func(c *Config) { c.Scenario.Obstacles[0].PositionMM = 1000000000; c.Scenario.Obstacles[0].SpeedMMS = 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := example(t, "stopped-obstacle")
			tc.mutate(&c)
			trace, err := Run(context.Background(), c)
			if !errors.Is(err, ErrInvalidConfig) || len(trace.Records) != 0 {
				t.Fatalf("got (%+v, %v), want invalid configuration", trace, err)
			}
		})
	}
	if _, err := Example("unknown"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("unknown example error = %v", err)
	}
}

func FuzzRun(f *testing.F) {
	f.Add(int64(0), int64(10000), int64(100), uint8(100), int64(25000), int64(0))
	f.Add(int64(-1000), int64(1), int64(1), uint8(5), int64(9000), int64(2000))
	f.Fuzz(func(t *testing.T, position, speed, dt int64, budget uint8, obstacle, obstacleSpeed int64) {
		c := Config{Scenario: Scenario{StartPositionMM: position, StartSpeedMMS: speed, GoalPositionMM: 60000, VehicleLengthMM: 4000, Obstacles: []Obstacle{{PositionMM: obstacle, SpeedMMS: obstacleSpeed, LengthMM: 4000}}}, Controller: Candidate, TickMS: dt, MaxTicks: int(budget)}
		trace, err := Run(context.Background(), c)
		if err != nil {
			if !errors.Is(err, ErrInvalidConfig) && !errors.Is(err, ErrTickLimit) {
				t.Fatal(err)
			}
			if len(trace.Records) != 0 {
				t.Fatal("error returned a completed trace")
			}
			return
		}
		if len(trace.Records) == 0 || len(trace.Records) > c.MaxTicks+1 {
			t.Fatal("invalid record count")
		}
		for i, r := range trace.Records {
			if r.Tick != i || r.TimeMS != int64(i)*dt || r.SpeedMMS < 0 {
				t.Fatalf("invalid record: %+v", r)
			}
			if i > 0 && (r.PositionMM < trace.Records[i-1].PositionMM || r.SpeedMMS > trace.Records[i-1].SpeedMMS) {
				t.Fatal("coast/brake policy reversed or accelerated")
			}
		}
	})
}
