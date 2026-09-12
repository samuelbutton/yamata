package queue

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samuelbutton/yamata/internal/contract"
)

func analysisJob(t *testing.T, dir, id string, original contract.Result, version int) string {
	t.Helper()
	var template map[string]any
	must(t, json.Unmarshal(original.AnalysisTemplate, &template))
	template["minimum_obstacle_gap"].(map[string]any)["version"] = version
	inputs := map[string]any{"bag": original.Bag, "analysis_template": template}
	data, err := json.Marshal(inputs)
	must(t, err)
	hash, err := contract.ContentHash(data)
	must(t, err)
	job := map[string]any{"contract_version": 1, "kind": "job", "job_kind": "analysis", "job_id": id, "execution_id": original.ExecutionID, "priority": 0, "inputs_hash": hash, "inputs": inputs}
	data, err = encode(job)
	must(t, err)
	path := "jobs/" + id + ".json"
	must(t, os.WriteFile(filepath.Join(dir, path), data, 0o600))
	return path
}

func resultFor(t *testing.T, dir, jobID string) contract.Result {
	t.Helper()
	for _, data := range files(t, dir, "results") {
		var result contract.Result
		must(t, json.Unmarshal(data, &result))
		if result.JobID == jobID {
			return result
		}
	}
	t.Fatalf("missing result for %s", jobID)
	return contract.Result{}
}

func TestReanalysisPreservesMotionAndEveryPriorOutcome(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	var simulations atomic.Int32
	hook := func(_ context.Context, stage string) error {
		if stage == "simulation" {
			simulations.Add(1)
		}
		return nil
	}
	workWith(t, s, hook)
	_, original := history(t, dir)
	beforeBags, beforeResults, beforeEvents := files(t, dir, "bags"), files(t, dir, "results"), files(t, dir, "events")
	path := analysisJob(t, dir, "edge-score", original, 2)
	receipt, err := s.Import(t.Context(), path)
	must(t, err)
	if receipt.Duplicate {
		t.Fatal("new job reported duplicate")
	}
	// Restore the accepted snapshot on first outbox delivery.
	must(t, os.Remove(filepath.Join(dir, path)))
	s = open(t, dir)
	workWith(t, s, hook)
	revised := resultFor(t, dir, "edge-score")
	if simulations.Load() != 1 || revised.ExecutionID != original.ExecutionID || revised.Bag == nil || *revised.Bag != *original.Bag || revised.AnalysisID == nil || *revised.AnalysisID == *original.AnalysisID || revised.InputsHash == original.InputsHash {
		t.Fatal("analysis did not reuse original motion with independent identity", simulations.Load(), revised)
	}
	if *original.Metrics["minimum_obstacle_gap"].Value != 3400 || *revised.Metrics["minimum_obstacle_gap"].Value != 0 || revised.Metrics["minimum_obstacle_gap"].Version != 2 || revised.Status != "FAIL" {
		t.Fatal(revised)
	}
	if !reflect.DeepEqual(original.Metrics["collision_count"], revised.Metrics["collision_count"]) || !reflect.DeepEqual(original.Metrics["goal_progress"], revised.Metrics["goal_progress"]) {
		t.Fatal("other metrics changed")
	}
	for folder, before := range map[string]map[string][]byte{"bags": beforeBags, "results": beforeResults, "events": beforeEvents} {
		after := files(t, dir, folder)
		for path, data := range before {
			if !bytes.Equal(data, after[path]) {
				t.Fatal("previous output changed", folder, path)
			}
		}
	}
	if !reflect.DeepEqual(beforeBags, files(t, dir, "bags")) {
		t.Fatal("bag added or changed")
	}
	events := []contract.Event{}
	paths := []string{}
	for path, data := range files(t, dir, "events") {
		var event contract.Event
		must(t, json.Unmarshal(data, &event))
		if event.JobID == "edge-score" {
			events = append(events, event)
		}
		paths = append(paths, "events/"+path)
	}
	must(t, s.validator.Check(t.Context(), dir, paths))
	sort.Slice(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })
	if len(events) != 3 {
		t.Fatal(events)
	}
	for i, want := range []string{"PENDING", "ANALYZING", "FAIL"} {
		if events[i].State != want || events[i].Sequence != int64(i+1) {
			t.Fatal(events)
		}
	}
	accepted := files(t, dir, "results")
	eventBytes := files(t, dir, "events")
	receipt, err = s.Import(t.Context(), path)
	must(t, err)
	if !receipt.Duplicate {
		t.Fatal("duplicate receipt lost")
	}
	workWith(t, s, hook)
	if simulations.Load() != 1 || !reflect.DeepEqual(accepted, files(t, dir, "results")) || !reflect.DeepEqual(eventBytes, files(t, dir, "events")) {
		t.Fatal("duplicate repeated work")
	}
}

func TestReanalysisIdentityConflictsAndMissingContext(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	drain(t, s, 1, 1)
	_, original := history(t, dir)
	same := analysisJob(t, dir, "same-score", original, 1)
	if _, err := s.Import(t.Context(), same); !errors.Is(err, contract.ErrConflict) {
		t.Fatal("original identity not reserved", err)
	}
	path := analysisJob(t, dir, "edge-score", original, 2)
	_, err := s.Import(t.Context(), path)
	must(t, err)
	alias := analysisJob(t, dir, "alias-score", original, 2)
	if _, err := s.Import(t.Context(), alias); !errors.Is(err, contract.ErrConflict) {
		t.Fatal("analysis identity not reserved", err)
	}
	// Another threshold has an independent identity, even at the same metric version.
	var template map[string]any
	must(t, json.Unmarshal(original.AnalysisTemplate, &template))
	template["minimum_obstacle_gap"].(map[string]any)["minimum_mm"] = 1
	original.AnalysisTemplate, err = json.Marshal(template)
	must(t, err)
	changed := analysisJob(t, dir, "new-limit", original, 2)
	_, err = s.Import(t.Context(), changed)
	must(t, err)
	drain(t, s, 0, 2)
	if len(files(t, dir, "results")) != 3 {
		t.Fatal("independent threshold result missing")
	}
	// Public files alone do not supply the original vehicle geometry and goal.
	fresh := t.TempDir()
	must(t, os.Mkdir(filepath.Join(fresh, "jobs"), 0o700))
	must(t, os.CopyFS(filepath.Join(fresh, "bags"), os.DirFS(filepath.Join(dir, "bags"))))
	path = analysisJob(t, fresh, "missing-run", original, 2)
	other := open(t, fresh)
	if _, err := other.Import(t.Context(), path); !errors.Is(err, contract.ErrInvalid) {
		t.Fatal(err)
	}
	var count int
	must(t, other.db.QueryRow("SELECT count(*) FROM jobs").Scan(&count))
	if count != 0 {
		t.Fatal("partial intake")
	}
}

func TestReanalysisRetryAndExpiredLeaseReuseSavedBag(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	drain(t, s, 1, 1)
	_, original := history(t, dir)
	before := files(t, dir, "bags")
	path := analysisJob(t, dir, "edge-score", original, 2)
	_, err := s.Import(t.Context(), path)
	must(t, err)
	must(t, s.Flush(t.Context()))
	first := claimed(t, s, "analysis")
	must(t, s.perform(t.Context(), first, time.Minute, func(context.Context, string) error { return errWorkerFailure }))
	s = open(t, dir)
	must(t, s.Flush(t.Context()))
	abandoned := claimed(t, s, "analysis")
	_, err = s.db.Exec("UPDATE jobs SET lease_ms=0 WHERE id=?", abandoned.id)
	must(t, err)
	recovered := claimed(t, s, "analysis")
	if recovered.attempt != abandoned.attempt || recovered.generation <= abandoned.generation {
		t.Fatal("lease recovery lost identity")
	}
	if err := s.renew(t.Context(), abandoned, time.Minute); !errors.Is(err, ErrLease) {
		t.Fatal(err)
	}
	must(t, s.perform(t.Context(), recovered, time.Minute, func(context.Context, string) error { return errWorkerFailure }))
	drain(t, s, 0, 1)
	result := resultFor(t, dir, "edge-score")
	if result.Status != "ERROR" || result.FailureClass == nil || *result.FailureClass != "worker_failure" || retryCount(t, s) != 1 || !reflect.DeepEqual(before, files(t, dir, "bags")) {
		t.Fatal(result)
	}
	if _, err := s.claim(t.Context(), "simulation", time.Minute); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("analysis entered simulation", err)
	}
	for path := range files(t, dir, "events") {
		must(t, s.validator.Check(t.Context(), dir, []string{"events/" + path}))
	}
}

func TestReanalysisUsesPinnedAlternateBagPath(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	drain(t, s, 1, 1)
	_, original := history(t, dir)
	data, err := os.ReadFile(filepath.Join(dir, original.Bag.Path))
	must(t, err)
	original.Bag.Path = "bags/saved-copy.jsonl"
	must(t, os.WriteFile(filepath.Join(dir, original.Bag.Path), data, 0o600))
	path := analysisJob(t, dir, "edge-score", original, 2)
	// A changed bag is rejected before committing intake.
	must(t, os.WriteFile(filepath.Join(dir, original.Bag.Path), append([]byte(" "), data...), 0o600))
	if _, err := s.Import(t.Context(), path); !errors.Is(err, contract.ErrHash) {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(dir, original.Bag.Path), data, 0o600))
	_, err = s.Import(t.Context(), path)
	must(t, err)
	drain(t, s, 0, 1)
	revised := resultFor(t, dir, "edge-score")
	if revised.Bag == nil || *revised.Bag != *original.Bag || revised.Status != "FAIL" {
		t.Fatal(revised)
	}
}

func TestReanalysisCorrectsFailedScoringWithoutSimulation(t *testing.T) {
	dir, s := setup(t)
	addJob(t, s, dir, "unsupported-analysis", 1, func(job map[string]any) {
		inputs := job["inputs"].(map[string]any)
		inputs["analysis_template"].(map[string]any)["minimum_obstacle_gap"].(map[string]any)["version"] = 3
		data, err := json.Marshal(inputs)
		must(t, err)
		job["inputs_hash"], err = contract.ContentHash(data)
		must(t, err)
	})
	drain(t, s, 1, 1)
	_, original := history(t, dir)
	if original.Status != "ERROR" || original.FailureClass == nil || *original.FailureClass != "analysis_failure" {
		t.Fatal(original)
	}
	before := files(t, dir, "results")
	path := analysisJob(t, dir, "supported-analysis", original, 2)
	_, err := s.Import(t.Context(), path)
	must(t, err)
	workWith(t, s, func(_ context.Context, stage string) error {
		if stage == "simulation" {
			return errors.New("unexpected simulation")
		}
		return nil
	})
	if resultFor(t, dir, "supported-analysis").Status != "FAIL" {
		t.Fatal("corrected analysis missing")
	}
	for path, data := range before {
		if !bytes.Equal(data, files(t, dir, "results")[path]) {
			t.Fatal("original error replaced")
		}
	}
}
