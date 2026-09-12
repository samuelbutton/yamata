package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samuelbutton/yamata/internal/bag"
	"github.com/samuelbutton/yamata/internal/contract"
	"github.com/samuelbutton/yamata/internal/execution"
)

func history(t *testing.T, dir string) ([]contract.Event, contract.Result) {
	t.Helper()
	var result contract.Result
	results := files(t, dir, "results")
	if len(results) != 1 {
		t.Fatal("expected one accepted result")
	}
	for _, data := range results {
		must(t, json.Unmarshal(data, &result))
	}
	events := files(t, dir, "events")
	ordered := make([]contract.Event, len(events))
	paths := []string{}
	for path, data := range events {
		var e contract.Event
		must(t, json.Unmarshal(data, &e))
		if e.Sequence < 1 || e.Sequence > int64(len(events)) || ordered[e.Sequence-1].EventID != "" {
			t.Fatal("invalid sequence")
		}
		ordered[e.Sequence-1] = e
		paths = append(paths, "events/"+path)
	}
	v, err := contract.New()
	must(t, err)
	must(t, v.Check(t.Context(), dir, paths))
	last := ordered[len(ordered)-1]
	if last.State != result.Status || last.AttemptID != result.AttemptID || last.Result == nil {
		t.Fatal("terminal result mismatch")
	}
	return ordered, result
}
func retryCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	must(t, s.db.QueryRow("SELECT count(*) FROM retries").Scan(&n))
	return n
}
func workWith(t *testing.T, s *Store, hook func(context.Context, string) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	must(t, s.work(ctx, Options{SimulationWorkers: 1, AnalysisWorkers: 1, LeaseDuration: time.Second, Drain: true}, hook))
}
func workerError(t *testing.T, l lease) contract.Result {
	t.Helper()
	r := l.result()
	if l.stage == "analysis" {
		var err error
		r, err = execution.Analysis(r, l.job, contract.Reference{Path: "bags/" + l.job.ExecutionID + ".jsonl", SHA256: l.bagHash})
		must(t, err)
	}
	failure := "worker_failure"
	r.Status, r.FailureClass = "ERROR", &failure
	return r
}

func TestWorkerFailureRetriesOncePerStage(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	var sim, analysis atomic.Int32
	hook := func(_ context.Context, stage string) error {
		counter := &sim
		if stage == "analysis" {
			counter = &analysis
		}
		if counter.Add(1) == 1 {
			return errWorkerFailure
		}
		return nil
	}
	workWith(t, s, hook)
	if sim.Load() != 2 || analysis.Load() != 2 || retryCount(t, s) != 2 {
		t.Fatal("retry limit not independent per stage")
	}
	if len(files(t, dir, "bags")) != 1 {
		t.Fatal("retry created another bag")
	}
	events, result := history(t, dir)
	want := []string{"PENDING", "RUNNING", "RUNNING", "ANALYZING", "ANALYZING", "FAIL"}
	if len(events) != len(want) {
		t.Fatal(len(events))
	}
	for i, state := range want {
		if events[i].State != state {
			t.Fatal(events)
		}
	}
	if events[1].AttemptID == events[2].AttemptID || events[3].AttemptID == events[4].AttemptID {
		t.Fatal("retry reused attempt identity")
	}
	if result.Status != "FAIL" || *result.Metrics["collision_count"].Value != 1 {
		t.Fatal("retry concealed failed scores")
	}
	before := files(t, dir, "results")
	intake(t, s)
	workWith(t, s, hook)
	if sim.Load() != 2 || analysis.Load() != 2 {
		t.Fatal("duplicate reset retry budget")
	}
	for path, data := range before {
		if !bytes.Equal(data, files(t, dir, "results")[path]) {
			t.Fatal("duplicate changed accepted result")
		}
	}
}

func TestWorkerFailureExhaustionAndPanicRecovery(t *testing.T) {
	for _, stage := range []string{"simulation", "analysis"} {
		for _, mode := range []string{"permanent", "panic-once"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				dir, s := setup(t)
				intake(t, s)
				var calls atomic.Int32
				hook := func(_ context.Context, at string) error {
					if at != stage {
						return nil
					}
					n := calls.Add(1)
					if mode == "permanent" {
						return errWorkerFailure
					}
					if n == 1 {
						panic("synthetic computation failure")
					}
					return nil
				}
				workWith(t, s, hook)
				if calls.Load() != 2 || retryCount(t, s) != 1 {
					t.Fatal("wrong retry count", calls.Load())
				}
				_, result := history(t, dir)
				if mode == "permanent" {
					if result.Status != "ERROR" || result.FailureClass == nil || *result.FailureClass != "worker_failure" {
						t.Fatal(result)
					}
					if (result.Bag != nil) != (stage == "analysis") {
						t.Fatal("incorrect bag reuse")
					}
				} else if result.Status != "FAIL" {
					t.Fatal("recovered computation did not produce real scores")
				}
			})
		}
	}
}

func TestOnlyWorkerFailuresAreRetried(t *testing.T) {
	for _, kind := range []string{"controller", "timeout", "analysis", "score"} {
		t.Run(kind, func(t *testing.T) {
			dir, s := setup(t)
			addJob(t, s, dir, "example", 0, func(job map[string]any) {
				in := job["inputs"].(map[string]any)
				switch kind {
				case "controller":
					in["controller"].(map[string]any)["version"] = 2
				case "timeout":
					in["limits"].(map[string]any)["max_ticks"] = 1
				case "analysis":
					in["analysis_template"].(map[string]any)["minimum_obstacle_gap"].(map[string]any)["version"] = 2
				}
				data, err := json.Marshal(in)
				must(t, err)
				hash, err := contract.ContentHash(data)
				must(t, err)
				job["inputs_hash"] = hash
			})
			var sim, analysis atomic.Int32
			workWith(t, s, func(_ context.Context, stage string) error {
				if stage == "simulation" {
					sim.Add(1)
				} else {
					analysis.Add(1)
				}
				return nil
			})
			if retryCount(t, s) != 0 || sim.Load() != 1 {
				t.Fatal("non-worker failure retried")
			}
			wantAnalysis := int32(0)
			if kind == "analysis" || kind == "score" {
				wantAnalysis = 1
			}
			if analysis.Load() != wantAnalysis {
				t.Fatal(analysis.Load())
			}
			_, r := history(t, dir)
			want := map[string]string{"controller": "controller_failure", "timeout": "simulation_timeout", "analysis": "analysis_failure"}[kind]
			if kind == "score" {
				if r.Status != "FAIL" {
					t.Fatal(r)
				}
			} else if r.Status != "ERROR" || r.FailureClass == nil || *r.FailureClass != want {
				t.Fatal(r)
			}
		})
	}
}

func TestRetryBudgetSurvivesReopenAndLeaseRecovery(t *testing.T) {
	for _, stage := range []string{"simulation", "analysis"} {
		t.Run(stage, func(t *testing.T) {
			dir, s := setup(t)
			intake(t, s)
			prepareStage(t, s, stage)
			first := claimed(t, s, stage)
			must(t, s.perform(t.Context(), first, time.Minute, func(context.Context, string) error { return errWorkerFailure }))
			if retryCount(t, s) != 1 {
				t.Fatal("retry not durable")
			}
			s = open(t, dir)
			if !intake(t, s).Duplicate {
				t.Fatal("receipt lost")
			}
			must(t, s.Flush(t.Context()))
			interrupted := claimed(t, s, stage)
			_, err := s.db.Exec("UPDATE jobs SET lease_ms=0 WHERE id=?", interrupted.id)
			must(t, err)
			recovered := claimed(t, s, stage)
			if recovered.attempt != interrupted.attempt || recovered.generation <= interrupted.generation {
				t.Fatal("recovery identity differs")
			}
			must(t, s.perform(t.Context(), recovered, time.Minute, func(context.Context, string) error { return errWorkerFailure }))
			drain(t, s, 1, 1)
			_, r := history(t, dir)
			if retryCount(t, s) != 1 || r.Status != "ERROR" {
				t.Fatal("restart reset retry budget")
			}
		})
	}
}

func TestStaleWorkerCannotConsumeRetryOrReplaceOutput(t *testing.T) {
	for _, stage := range []string{"simulation", "analysis"} {
		t.Run(stage, func(t *testing.T) {
			dir, s := setup(t)
			intake(t, s)
			prepareStage(t, s, stage)
			old := claimed(t, s, stage)
			must(t, s.Flush(t.Context()))
			_, err := s.db.Exec("UPDATE jobs SET lease_ms=0 WHERE id=?", old.id)
			must(t, err)
			newer := claimed(t, s, stage)
			forged := newer
			forged.generation = old.generation
			if err := s.renew(t.Context(), forged, time.Minute); !errors.Is(err, ErrLease) {
				t.Fatal("generation not fenced", err)
			}
			if err := s.acceptResult(t.Context(), forged, workerError(t, forged)); !errors.Is(err, ErrLease) {
				t.Fatal(err)
			}
			if err := s.acceptResult(t.Context(), old, workerError(t, old)); !errors.Is(err, ErrLease) {
				t.Fatal(err)
			}
			if retryCount(t, s) != 0 {
				t.Fatal("stale failure consumed retry")
			}
			must(t, s.perform(t.Context(), newer, time.Minute, nil))
			drain(t, s, 1, 1)
			before := files(t, dir, "results")
			if stage == "simulation" {
				data, err := bag.Prepare(t.Context(), s.validator, old.job)
				must(t, err)
				if err := s.acceptBag(t.Context(), old, data); !errors.Is(err, ErrLease) {
					t.Fatal(err)
				}
			}
			if err := s.acceptResult(t.Context(), old, workerError(t, old)); !errors.Is(err, ErrLease) {
				t.Fatal(err)
			}
			must(t, s.release(old))
			for path, data := range before {
				if !bytes.Equal(data, files(t, dir, "results")[path]) {
					t.Fatal("stale worker replaced result")
				}
			}
			if retryCount(t, s) != 0 {
				t.Fatal("late failure consumed retry")
			}
		})
	}
}

func TestRetryAndEventCommitTogether(t *testing.T) {
	_, s := setup(t)
	intake(t, s)
	must(t, s.Flush(t.Context()))
	l := claimed(t, s, "simulation")
	must(t, s.Flush(t.Context()))
	_, err := s.db.Exec(`CREATE TRIGGER reject_retry BEFORE INSERT ON outbox WHEN NEW.kind='event'
 BEGIN SELECT RAISE(ABORT,'injected event failure'); END`)
	must(t, err)
	if err := s.acceptResult(t.Context(), l, workerError(t, l)); err == nil {
		t.Fatal("failed event accepted retry")
	}
	if retryCount(t, s) != 0 {
		t.Fatal("rollback consumed retry")
	}
	var attempt, token string
	must(t, s.db.QueryRow("SELECT attempt,token FROM jobs").Scan(&attempt, &token))
	if attempt != l.attempt || token != l.token {
		t.Fatal("partial retry state committed")
	}
	_, err = s.db.Exec("DROP TRIGGER reject_retry")
	must(t, err)
	must(t, s.acceptResult(t.Context(), l, workerError(t, l)))
	if retryCount(t, s) != 1 {
		t.Fatal("retry not committed")
	}
}

func TestStorageFailureDoesNotBecomeWorkerFailure(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	drain(t, s, 1, 0)
	l := claimed(t, s, "analysis")
	must(t, os.Remove(filepath.Join(dir, "bags", l.job.ExecutionID+".jsonl")))
	if err := s.perform(t.Context(), l, time.Minute, nil); err == nil {
		t.Fatal("missing bag concealed")
	}
	if retryCount(t, s) != 0 || len(files(t, dir, "results")) != 0 {
		t.Fatal("storage error spent retry or produced outcome")
	}
}
