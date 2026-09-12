package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/samuelbutton/yamata/internal/contract"
)

const template = `{"collision_count":{"version":1,"maximum":0},"minimum_obstacle_gap":{"version":1,"minimum_mm":0},"goal_progress":{"version":1,"minimum_ppm":1000000}}`

func fixture(positions ...int64) (contract.Bag, contract.RunInputs) {
	var in contract.RunInputs
	in.AnalysisTemplate = json.RawMessage(template)
	in.Scenario.Type = "lane"
	in.Scenario.StartSpeedMMS = 1000
	in.Scenario.VehicleLengthMM = 1000
	in.Scenario.GoalPositionMM = 2000
	in.Scenario.Obstacles = []contract.Obstacle{{PositionMM: 2000, LengthMM: 500}}
	in.RunTemplate.TickMS = 1000
	bag := contract.Bag{}
	for i, p := range positions {
		bag.Records = append(bag.Records, contract.Tick{Kind: "tick", Tick: i, TimeMS: int64(i) * 1000, PositionMM: p, SpeedMMS: 1000, Obstacles: []contract.Obstacle{{PositionMM: 2000, LengthMM: 500}}})
	}
	return bag, in
}

func assertMetric(t *testing.T, got contract.Metric, value int64, pass bool, ticks []int) {
	t.Helper()
	if got.Value == nil || *got.Value != value || got.Pass == nil || *got.Pass != pass || !reflect.DeepEqual(got.EvidenceTicks, ticks) || got.Version != 1 {
		t.Fatalf("metric = %+v; want value=%d pass=%v ticks=%v", got, value, pass, ticks)
	}
}

func TestKnownBagByHand(t *testing.T) {
	bag, in := fixture(0, 1000)
	scores, err := Evaluate(context.Background(), bag, in)
	if err != nil {
		t.Fatal(err)
	}
	if scores.Status != "FAIL" {
		t.Fatal(scores.Status)
	}
	assertMetric(t, scores.Metrics["collision_count"], 1, false, []int{1})
	// Centers at tick 1 are 1500 and 2250 mm; progress is 1000/2000.
	assertMetric(t, scores.Metrics["minimum_obstacle_gap"], 750, true, []int{1})
	assertMetric(t, scores.Metrics["goal_progress"], 500000, false, []int{1})
}

func TestContactEpisodesAndSweptContact(t *testing.T) {
	for _, tc := range []struct {
		name      string
		positions []int64
		count     int64
	}{
		{"continued overlap", []int64{0, 1000, 1100, 1200}, 1},
		{"separate episodes", []int64{0, 1000, 1100, 0, 1000}, 2},
		{"crossed between ticks", []int64{0, 10000}, 1},
		{"no contact", []int64{0, 999}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bag, in := fixture(tc.positions...)
			// Loosen the progress limit so collision alone must force FAIL.
			in.AnalysisTemplate = json.RawMessage(`{"collision_count":{"version":1,"maximum":0},"minimum_obstacle_gap":{"version":1,"minimum_mm":0},"goal_progress":{"version":1,"minimum_ppm":0}}`)
			scores, err := Evaluate(context.Background(), bag, in)
			if err != nil {
				t.Fatal(err)
			}
			evidence := []int{}
			if tc.count > 0 {
				evidence = []int{1}
			}
			assertMetric(t, scores.Metrics["collision_count"], tc.count, tc.count == 0, evidence)
			if (scores.Status == "FAIL") != (tc.count > 0) {
				t.Fatalf("collision did not determine status: %s", scores.Status)
			}
		})
	}
}

func TestRearImpactAndInitialOverlap(t *testing.T) {
	bag, in := fixture(0, 0)
	in.Scenario.Obstacles[0] = contract.Obstacle{PositionMM: -3000, SpeedMMS: 10000, LengthMM: 500}
	bag.Records[0].Obstacles[0] = in.Scenario.Obstacles[0]
	bag.Records[1].Obstacles[0] = contract.Obstacle{PositionMM: 7000, SpeedMMS: 10000, LengthMM: 500}
	scores, err := Evaluate(context.Background(), bag, in)
	if err != nil {
		t.Fatal(err)
	}
	assertMetric(t, scores.Metrics["collision_count"], 1, false, []int{1})
	bag, in = fixture(0)
	in.Scenario.Obstacles[0].PositionMM = 1000
	bag.Records[0].Obstacles[0].PositionMM = 1000
	scores, err = Evaluate(context.Background(), bag, in)
	if err != nil {
		t.Fatal(err)
	}
	assertMetric(t, scores.Metrics["collision_count"], 1, false, []int{0})
}

func TestUnavailableMetricsCannotPass(t *testing.T) {
	bag, in := fixture(0, 2000)
	in.Scenario.Obstacles = []contract.Obstacle{}
	for i := range bag.Records {
		bag.Records[i].Obstacles = []contract.Obstacle{}
	}
	scores, err := Evaluate(context.Background(), bag, in)
	if err != nil {
		t.Fatal(err)
	}
	if scores.Status != "WARN" {
		t.Fatal(scores.Status)
	}
	missing := scores.Metrics["minimum_obstacle_gap"]
	if missing.Value != nil || missing.Pass != nil || len(missing.EvidenceTicks) != 0 {
		t.Fatal(missing)
	}
	assertMetric(t, scores.Metrics["goal_progress"], 1000000, true, []int{1})
	in.Scenario.GoalPositionMM = 0
	scores, err = Evaluate(context.Background(), bag, in)
	if err != nil {
		t.Fatal(err)
	}
	if m := scores.Metrics["goal_progress"]; m.Value != nil || m.Pass != nil || scores.Status != "WARN" {
		t.Fatal(scores)
	}
	in.Scenario.GoalPositionMM = 4000
	scores, err = Evaluate(context.Background(), bag, in)
	if err != nil {
		t.Fatal(err)
	}
	if scores.Status != "FAIL" {
		t.Fatal("unavailable gap hid progress failure")
	}
}

func TestCenterRoundingAndFirstEvidence(t *testing.T) {
	bag, in := fixture(0, 0)
	in.Scenario.Obstacles[0].LengthMM = 501
	for i := range bag.Records {
		bag.Records[i].Obstacles[0].LengthMM = 501
	}
	scores, err := Evaluate(context.Background(), bag, in)
	if err != nil {
		t.Fatal(err)
	}
	// Exact center distance is 1750.5 mm, rounded down once.
	assertMetric(t, scores.Metrics["minimum_obstacle_gap"], 1750, true, []int{0})
}

func TestRejectChangedWorldAndUnknownVersions(t *testing.T) {
	for _, name := range []string{"initial position", "missing obstacle", "changed length", "teleport", "version", "empty"} {
		t.Run(name, func(t *testing.T) {
			bag, in := fixture(0, 1000)
			switch name {
			case "initial position":
				bag.Records[0].PositionMM = 1
			case "missing obstacle":
				bag.Records[1].Obstacles = nil
			case "changed length":
				bag.Records[1].Obstacles[0].LengthMM = 1
			case "teleport":
				bag.Records[1].Obstacles[0].PositionMM = 99999
			case "version":
				in.AnalysisTemplate = json.RawMessage(`{"collision_count":{"version":2}}`)
			case "empty":
				bag.Records = nil
			}
			out, err := Evaluate(context.Background(), bag, in)
			if err == nil || out.Metrics != nil {
				t.Fatalf("invalid recording produced scores: %+v %v", out, err)
			}
		})
	}
	bag, in := fixture(0, 1000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Evaluate(ctx, bag, in); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func FuzzEvaluate(f *testing.F) {
	f.Add(int64(1000))
	f.Add(int64(10000))
	f.Add(int64(-1000))
	f.Fuzz(func(t *testing.T, position int64) {
		bag, in := fixture(0, position%1000000)
		out, err := Evaluate(context.Background(), bag, in)
		if err != nil {
			t.Fatal(err)
		}
		if *out.Metrics["collision_count"].Value > 0 && out.Status != "FAIL" {
			t.Fatal("collision escaped failure")
		}
		progress := *out.Metrics["goal_progress"].Value
		if progress < 0 || progress > 1000000 {
			t.Fatal("progress outside range")
		}
	})
}

func TestEdgeClearanceByHand(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		obstacle, length, want int64
	}{
		{"ahead", 2000, 501, 1000}, {"behind", -2000, 501, 1499},
		{"front touch", 1000, 501, 0}, {"rear touch", -501, 501, 0},
		{"overlap", 500, 501, 0}, {"enclosed", -100, 2000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bag, in := fixture(0, 0)
			in.AnalysisTemplate = json.RawMessage(strings.Replace(template, `"minimum_obstacle_gap":{"version":1`, `"minimum_obstacle_gap":{"version":2`, 1))
			in.Scenario.Obstacles[0] = contract.Obstacle{PositionMM: tc.obstacle, LengthMM: tc.length}
			for i := range bag.Records {
				bag.Records[i].Obstacles[0] = in.Scenario.Obstacles[0]
			}
			scores, err := Evaluate(t.Context(), bag, in)
			if err != nil {
				t.Fatal(err)
			}
			gap := scores.Metrics["minimum_obstacle_gap"]
			if gap.Version != 2 || gap.Value == nil || *gap.Value != tc.want || !reflect.DeepEqual(gap.EvidenceTicks, []int{0}) {
				t.Fatal(gap)
			}
		})
	}
}

func TestEdgeGapAvailabilityAndSweptCollision(t *testing.T) {
	bag, in := fixture(0, 10000)
	in.AnalysisTemplate = json.RawMessage(strings.Replace(template, `"minimum_obstacle_gap":{"version":1`, `"minimum_obstacle_gap":{"version":2`, 1))
	scores, err := Evaluate(t.Context(), bag, in)
	if err != nil {
		t.Fatal(err)
	}
	// Samples are separated by 1000 and 7500 mm, but the bodies cross between ticks.
	if *scores.Metrics["minimum_obstacle_gap"].Value != 1000 || *scores.Metrics["collision_count"].Value != 1 || scores.Status != "FAIL" {
		t.Fatal(scores)
	}
	in.Scenario.Obstacles = []contract.Obstacle{}
	for i := range bag.Records {
		bag.Records[i].Obstacles = []contract.Obstacle{}
	}
	scores, err = Evaluate(t.Context(), bag, in)
	if err != nil {
		t.Fatal(err)
	}
	gap := scores.Metrics["minimum_obstacle_gap"]
	if gap.Version != 2 || gap.Value != nil || gap.Pass != nil || len(gap.EvidenceTicks) != 0 || scores.Status != "WARN" {
		t.Fatal(scores)
	}
	in.AnalysisTemplate = json.RawMessage(strings.Replace(string(in.AnalysisTemplate), `"version":2`, `"version":3`, 1))
	if _, err := Evaluate(t.Context(), bag, in); !errors.Is(err, contract.ErrVersion) {
		t.Fatal(err)
	}
}
