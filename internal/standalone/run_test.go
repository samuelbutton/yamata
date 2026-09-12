package standalone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samuelbutton/yamata/internal/contract"
)

func setup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(filepath.Join(dir, "jobs"), os.DirFS("../../examples/jobs")); err != nil {
		t.Fatal(err)
	}
	return dir
}
func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func change(t *testing.T, dir string, mutate func(map[string]any)) {
	t.Helper()
	path := filepath.Join(dir, "jobs/candidate.json")
	var job map[string]any
	if err := json.Unmarshal(read(t, path), &job); err != nil {
		t.Fatal(err)
	}
	mutate(job)
	inputs, err := json.Marshal(job["inputs"])
	if err != nil {
		t.Fatal(err)
	}
	hash, err := contract.ContentHash(inputs)
	if err != nil {
		t.Fatal(err)
	}
	job["inputs_hash"] = hash
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
func verify(t *testing.T, dir string, out Outcome) contract.Result {
	t.Helper()
	v, err := contract.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Check(context.Background(), dir, []string{out.Event.Path}); err != nil {
		t.Fatal(err)
	}
	data := read(t, filepath.Join(dir, out.Result.Path))
	if contract.Hash(data) != out.Result.SHA256 {
		t.Fatal("result hash differs")
	}
	var r contract.Result
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	var e contract.Event
	if err := json.Unmarshal(read(t, filepath.Join(dir, out.Event.Path)), &e); err != nil {
		t.Fatal(err)
	}
	if e.Result != out.Result || e.State != r.Status || e.CreatedAtMS <= 0 || r.Timing.DurationMS < 0 {
		t.Fatal("result/event relationship differs")
	}
	return r
}

func TestStandaloneOutcomes(t *testing.T) {
	for _, name := range []string{"collision", "pass", "unavailable", "metric error", "controller error", "timeout"} {
		t.Run(name, func(t *testing.T) {
			dir := setup(t)
			change(t, dir, func(job map[string]any) {
				in := job["inputs"].(map[string]any)
				template := in["analysis_template"].(map[string]any)
				switch name {
				case "collision":
					template["goal_progress"].(map[string]any)["minimum_ppm"] = 0
				case "pass":
					in["controller"].(map[string]any)["name"] = "baseline"
					template["goal_progress"].(map[string]any)["minimum_ppm"] = 250000
				case "unavailable":
					in["scenario"].(map[string]any)["obstacles"] = []any{}
				case "metric error":
					template["minimum_obstacle_gap"].(map[string]any)["version"] = 2
				case "controller error":
					in["controller"].(map[string]any)["version"] = 2
				case "timeout":
					in["limits"].(map[string]any)["max_ticks"] = 1
				}
				job["correlation_id"] = "example"
			})
			out, err := Run(context.Background(), dir, "jobs/candidate.json")
			if err != nil {
				t.Fatal(err)
			}
			r := verify(t, dir, out)
			want := map[string]string{"collision": "FAIL", "pass": "PASS", "unavailable": "WARN", "metric error": "ERROR", "controller error": "ERROR", "timeout": "ERROR"}[name]
			if out.Status != want || r.CorrelationID != "example" {
				t.Fatalf("outcome=%+v", out)
			}
			if name == "collision" && (r.Metrics["collision_count"].Pass == nil || *r.Metrics["collision_count"].Pass) {
				t.Fatal("collision did not fail")
			}
			if name == "unavailable" && (r.Metrics["minimum_obstacle_gap"].Value != nil || r.Metrics["minimum_obstacle_gap"].Pass != nil) {
				t.Fatal("unavailable metric passed")
			}
			if want == "ERROR" {
				failure := map[string]string{"metric error": "analysis_failure", "controller error": "controller_failure", "timeout": "simulation_timeout"}[name]
				if r.FailureClass == nil || *r.FailureClass != failure || r.Metrics != nil {
					t.Fatal("incorrect failure outcome")
				}
				if (r.Bag != nil) != (name == "metric error") || (r.AnalysisID != nil) != (name == "metric error") {
					t.Fatal("incorrect absent fields")
				}
			}
			again, err := Run(context.Background(), dir, "jobs/candidate.json")
			if err != nil || again != out {
				t.Fatalf("retry changed outcome: %+v %v", again, err)
			}
		})
	}
}

func TestPublishedExampleScores(t *testing.T) {
	dir := setup(t)
	for _, name := range []string{"baseline", "candidate"} {
		out, err := Run(context.Background(), dir, "jobs/"+name+".json")
		if err != nil {
			t.Fatal(err)
		}
		r := verify(t, dir, out)
		gap, progress, count := int64(7000), int64(300000), int64(0)
		if name == "candidate" {
			gap, progress, count = 3400, 360000, 1
		}
		if *r.Metrics["minimum_obstacle_gap"].Value != gap || *r.Metrics["goal_progress"].Value != progress || *r.Metrics["collision_count"].Value != count || r.Status != "FAIL" {
			t.Fatal(r.Metrics)
		}
	}
}

func TestConcurrentRetriesShareOutcome(t *testing.T) {
	dir := setup(t)
	var group sync.WaitGroup
	results := make(chan Outcome, 6)
	for range 6 {
		group.Go(func() {
			out, err := Run(context.Background(), dir, "jobs/candidate.json")
			if err != nil {
				t.Error(err)
				return
			}
			results <- out
		})
	}
	group.Wait()
	close(results)
	var first Outcome
	for out := range results {
		if first.Status == "" {
			first = out
		}
		if out != first {
			t.Fatal("concurrent calls changed accepted files")
		}
	}
	if first.Status == "" {
		t.Fatal("no completed call")
	}
	verify(t, dir, first)
	for _, folder := range []string{"bags", "results", "events"} {
		entries, err := os.ReadDir(filepath.Join(dir, folder))
		if err != nil || len(entries) != 1 {
			t.Fatalf("%s: %v %v", folder, entries, err)
		}
	}
}

func TestResultPrecedesEventAndRetryRepairs(t *testing.T) {
	dir := setup(t)
	// A blocked event directory forces a failure after durable result publication.
	if err := os.WriteFile(filepath.Join(dir, "events"), []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), dir, "jobs/candidate.json"); err == nil {
		t.Fatal("event failure hidden")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "results"))
	if err != nil || len(entries) != 1 {
		t.Fatal("result not published before event attempt")
	}
	path := filepath.Join(dir, "results", entries[0].Name())
	before := read(t, path)
	if string(read(t, filepath.Join(dir, "events"))) != "preserve" {
		t.Fatal("changed blocking file")
	}
	if err := os.Remove(filepath.Join(dir, "events")); err != nil {
		t.Fatal(err)
	}
	out, err := Run(context.Background(), dir, "jobs/candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	verify(t, dir, out)
	if !bytes.Equal(before, read(t, path)) {
		t.Fatal("repair changed accepted result")
	}
	originalEvent := read(t, filepath.Join(dir, out.Event.Path))
	if err := os.Remove(filepath.Join(dir, out.Event.Path)); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), dir, "jobs/candidate.json"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, read(t, path)) {
		t.Fatal("missing-event repair changed result")
	}
	if !bytes.Equal(originalEvent, read(t, filepath.Join(dir, out.Event.Path))) {
		t.Fatal("event repair changed immutable event bytes")
	}
}

func TestChangedResultAndJobAreNotOverwritten(t *testing.T) {
	for _, mode := range []string{"result", "job", "event"} {
		t.Run(mode, func(t *testing.T) {
			dir := setup(t)
			out, err := Run(context.Background(), dir, "jobs/candidate.json")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, out.Result.Path)
			if mode == "job" {
				path = filepath.Join(dir, "jobs/candidate.json")
			}
			if mode == "event" {
				path = filepath.Join(dir, out.Event.Path)
			}
			changed := append([]byte(" "), read(t, path)...)
			if err := os.WriteFile(path, changed, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Run(context.Background(), dir, "jobs/candidate.json"); err == nil {
				t.Fatal("changed content accepted")
			}
			if !bytes.Equal(changed, read(t, path)) {
				t.Fatal("conflicting content overwritten")
			}
		})
	}
}

func TestInvalidJobAndCanceledLock(t *testing.T) {
	dir := setup(t)
	path := filepath.Join(dir, "jobs/candidate.json")
	data := bytes.Replace(read(t, path), []byte(`"start_speed_mm_s": 10000`), []byte(`"start_speed_mm_s": 10001`), 1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), dir, "jobs/candidate.json"); !errors.Is(err, contract.ErrHash) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("invalid job wrote files: %v %v", entries, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	first, err := lock(context.Background(), root, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := lock(ctx, root, "test"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".standalone/test.lock")); err != nil {
		t.Fatal(err)
	}
}

func TestOutcomePublicationStaysInsideExchange(t *testing.T) {
	for _, folder := range []string{"results", "events", ".standalone"} {
		t.Run(folder, func(t *testing.T) {
			dir := setup(t)
			outside := t.TempDir()
			if err := os.Symlink(outside, filepath.Join(dir, folder)); err != nil {
				t.Fatal(err)
			}
			if _, err := Run(context.Background(), dir, "jobs/candidate.json"); err == nil {
				t.Fatal("escaping output path accepted")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("wrote outside exchange: %v %v", entries, err)
			}
		})
	}
}

func TestAnalysisIdentityUsesCompleteTemplate(t *testing.T) {
	bagHash := strings.Repeat("a", 64)
	one, err := contract.ContentHash(json.RawMessage(`{"x":1,"y":2}`))
	if err != nil {
		t.Fatal(err)
	}
	two, err := contract.ContentHash(json.RawMessage(`{"y":2,"x":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if contract.AnalysisID("execution", bagHash, one) == contract.AnalysisID("execution", bagHash, two) {
		t.Fatal("analysis identity ignored changed settings")
	}
}
