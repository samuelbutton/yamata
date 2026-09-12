package bag

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/samuelbutton/yamata/internal/contract"
	"github.com/samuelbutton/yamata/internal/simulator"
)

func setup(t *testing.T) (string, *contract.Validator, *os.Root) {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(filepath.Join(dir, "jobs"), os.DirFS("../../examples/jobs")); err != nil {
		t.Fatal(err)
	}
	v, err := contract.New()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	return dir, v, root
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func digest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func modifyJob(t *testing.T, path string, change func(map[string]any)) {
	t.Helper()
	var job map[string]any
	if err := json.Unmarshal(read(t, path), &job); err != nil {
		t.Fatal(err)
	}
	change(job)
	inputs, err := json.Marshal(job["inputs"])
	if err != nil {
		t.Fatal(err)
	}
	// Round-trip typed test values into maps so nested keys are also sorted.
	var canonical any
	if err := json.Unmarshal(inputs, &canonical); err != nil {
		t.Fatal(err)
	}
	inputs, err = json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	job["inputs_hash"] = digest(inputs)
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRecordAllExamples(t *testing.T) {
	for _, scenario := range []string{"empty-lane", "stopped-obstacle", "moving-obstacle"} {
		for _, controller := range []simulator.Controller{simulator.Baseline, simulator.Candidate} {
			t.Run(scenario+"/"+string(controller), func(t *testing.T) {
				dir, v, root := setup(t)
				c, err := simulator.Example(scenario)
				if err != nil {
					t.Fatal(err)
				}
				c.Controller = controller
				modifyJob(t, filepath.Join(dir, "jobs/baseline.json"), func(job map[string]any) {
					in := job["inputs"].(map[string]any)
					in["controller"].(map[string]any)["name"] = string(controller)
					s := in["scenario"].(map[string]any)
					obstacles := []contract.Obstacle{}
					for _, o := range c.Scenario.Obstacles {
						obstacles = append(obstacles, contract.Obstacle{PositionMM: o.PositionMM, SpeedMMS: o.SpeedMMS, LengthMM: o.LengthMM})
					}
					s["obstacles"] = obstacles
				})
				jobBytes := read(t, filepath.Join(dir, "jobs/baseline.json"))
				first, err := Record(context.Background(), dir, "jobs/baseline.json")
				if err != nil {
					t.Fatal(err)
				}
				second, err := Record(context.Background(), dir, "jobs/baseline.json")
				if err != nil || first != second {
					t.Fatalf("repeat = %+v, %v; first = %+v", second, err, first)
				}
				bag, err := v.ReadBag(context.Background(), root, first.Path, first.SHA256)
				if err != nil {
					t.Fatal(err)
				}
				trace, err := simulator.Run(context.Background(), c)
				if err != nil {
					t.Fatal(err)
				}
				if len(bag.Records) != len(trace.Records) {
					t.Fatal("record count differs")
				}
				for i, r := range trace.Records {
					got := bag.Records[i]
					if got.Tick != r.Tick || got.TimeMS != r.TimeMS || got.PositionMM != r.PositionMM || got.SpeedMMS != r.SpeedMMS || got.AccelerationMMS2 != r.AccelerationMMS2 {
						t.Fatalf("motion differs at tick %d", i)
					}
					if len(got.Obstacles) != len(r.Obstacles) {
						t.Fatal("obstacle count differs")
					}
					for j, o := range r.Obstacles {
						if got.Obstacles[j] != (contract.Obstacle{PositionMM: o.PositionMM, SpeedMMS: o.SpeedMMS, LengthMM: o.LengthMM}) {
							t.Fatalf("obstacle differs at tick %d", i)
						}
					}
				}
				job, err := v.ReadRunJob(context.Background(), root, "jobs/baseline.json")
				if err != nil {
					t.Fatal(err)
				}
				if bag.Header.InputsHash != job.InputsHash || bag.Header.ExecutionID != job.ExecutionID {
					t.Fatal("bag lost input identity")
				}
				if !bytes.Equal(jobBytes, read(t, filepath.Join(dir, "jobs/baseline.json"))) {
					t.Fatal("recorder modified job")
				}
				if err := v.Check(context.Background(), dir, []string{first.Path}); err != nil {
					t.Fatal(err)
				}
				entries, err := os.ReadDir(filepath.Join(dir, "bags"))
				if err != nil || len(entries) != 1 {
					t.Fatalf("unexpected publication entries: %v, %v", entries, err)
				}
				info, err := os.Stat(filepath.Join(dir, first.Path))
				if err != nil || info.Mode().Perm()&0o077 != 0 {
					t.Fatalf("bag permissions: %v, %v", info, err)
				}
			})
		}
	}
}

func TestRecordRejectsBeforePublication(t *testing.T) {
	for _, name := range []string{"simulator version", "controller version", "step budget", "analysis job", "bad hash", "unsafe id"} {
		t.Run(name, func(t *testing.T) {
			dir, _, _ := setup(t)
			modifyJob(t, filepath.Join(dir, "jobs/baseline.json"), func(job map[string]any) {
				in := job["inputs"].(map[string]any)
				switch name {
				case "simulator version":
					in["simulator"].(map[string]any)["version"] = 2
				case "controller version":
					in["controller"].(map[string]any)["version"] = 2
				case "step budget":
					in["limits"].(map[string]any)["max_ticks"] = 1
				case "analysis job":
					job["job_kind"] = "analysis"
				case "unsafe id":
					job["execution_id"] = "../outside"
				}
			})
			if name == "bad hash" {
				path := filepath.Join(dir, "jobs/baseline.json")
				data := bytes.Replace(read(t, path), []byte(`"start_speed_mm_s":10000`), []byte(`"start_speed_mm_s":10001`), 1)
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Record(context.Background(), dir, "jobs/baseline.json"); err == nil {
				t.Fatal("invalid job recorded")
			}
			if _, err := os.Stat(filepath.Join(dir, "bags")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("publication began: %v", err)
			}
		})
	}
	dir, _, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Record(ctx, dir, "jobs/baseline.json"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRecordConflictPreservesBytes(t *testing.T) {
	dir, _, _ := setup(t)
	first, err := Record(context.Background(), dir, "jobs/baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	before := read(t, filepath.Join(dir, first.Path))
	modifyJob(t, filepath.Join(dir, "jobs/baseline.json"), func(job map[string]any) {
		job["inputs"].(map[string]any)["controller"].(map[string]any)["name"] = "candidate"
	})
	if _, err := Record(context.Background(), dir, "jobs/baseline.json"); !errors.Is(err, contract.ErrConflict) {
		t.Fatalf("conflict = %v", err)
	}
	if !bytes.Equal(before, read(t, filepath.Join(dir, first.Path))) {
		t.Fatal("conflicting recording replaced original")
	}
}

func TestConcurrentPublication(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "duplicates", true: "conflict"}[conflict], func(t *testing.T) {
			dir, v, root := setup(t)
			if conflict {
				modifyJob(t, filepath.Join(dir, "jobs/candidate.json"), func(job map[string]any) { job["execution_id"] = "stopped-baseline" })
			}
			type outcome struct {
				p   Publication
				err error
			}
			results := make(chan outcome, 8)
			var group sync.WaitGroup
			for i := 0; i < 8; i++ {
				group.Go(func() {
					path := "jobs/baseline.json"
					if conflict && i%2 == 1 {
						path = "jobs/candidate.json"
					}
					p, err := Record(context.Background(), dir, path)
					results <- outcome{p, err}
				})
			}
			group.Wait()
			close(results)
			var accepted []Publication
			conflicts := 0
			for r := range results {
				if r.err == nil {
					accepted = append(accepted, r.p)
				} else if errors.Is(r.err, contract.ErrConflict) {
					conflicts++
				} else {
					t.Fatal(r.err)
				}
			}
			want := 8
			if conflict {
				want = 4
			}
			if len(accepted) != want || conflicts != 8-want {
				t.Fatalf("accepted=%d conflicts=%d", len(accepted), conflicts)
			}
			for _, p := range accepted {
				if !reflect.DeepEqual(p, accepted[0]) {
					t.Fatal("multiple accepted byte sequences")
				}
			}
			if _, err := v.ReadBag(context.Background(), root, accepted[0].Path, accepted[0].SHA256); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInputHashCoversMetadata(t *testing.T) {
	dir, v, root := setup(t)
	first, err := Record(context.Background(), dir, "jobs/baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	modifyJob(t, filepath.Join(dir, "jobs/baseline.json"), func(job map[string]any) { job["execution_id"] = "repeat"; job["inputs"].(map[string]any)["seed"] = 1 })
	second, err := Record(context.Background(), dir, "jobs/baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	a, err := v.ReadBag(context.Background(), root, first.Path, first.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	b, err := v.ReadBag(context.Background(), root, second.Path, second.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if a.Header.InputsHash == b.Header.InputsHash || a.SHA256 == b.SHA256 || !reflect.DeepEqual(a.Records, b.Records) {
		t.Fatal("metadata hash and motion boundaries are incorrect")
	}
}

func TestReadChangedBag(t *testing.T) {
	dir, v, root := setup(t)
	p, err := Record(context.Background(), dir, "jobs/baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, p.Path)
	data := read(t, path)
	if err := os.WriteFile(path, append([]byte(" "), data...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := v.ReadBag(context.Background(), root, p.Path, p.SHA256); !errors.Is(err, contract.ErrHash) {
		t.Fatalf("changed bytes = %v", err)
	}
	if _, err := v.ReadBag(context.Background(), root, p.Path, strings.Repeat("g", 64)); !errors.Is(err, contract.ErrInvalid) {
		t.Fatalf("bad hash = %v", err)
	}
}

func TestExampleEncodingHashes(t *testing.T) {
	// Fixed hashes detect accidental changes to version-one recorder encoding.
	for name, want := range map[string]string{
		"baseline":  "8aff9e0851be74dab84da2d49bd0964e80ac693bf66385b21496c4b7dc29b835",
		"candidate": "2f29d5ce47e405601017a3fd75f149150b3d460e26cd0898e543c37b6073e50b",
	} {
		dir, _, _ := setup(t)
		p, err := Record(context.Background(), dir, "jobs/"+name+".json")
		if err != nil {
			t.Fatal(err)
		}
		if p.SHA256 != want {
			t.Fatalf("%s encoding hash = %s, want %s", name, p.SHA256, want)
		}
	}
}
