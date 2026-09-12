package queue

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samuelbutton/yamata/internal/bag"
	"github.com/samuelbutton/yamata/internal/contract"
	"github.com/samuelbutton/yamata/internal/publication"
)

func setup(t *testing.T) (string, *Store) {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(filepath.Join(dir, "jobs"), os.DirFS("../../examples/jobs")); err != nil {
		t.Fatal(err)
	}
	return dir, open(t, dir)
}
func open(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func intake(t *testing.T, s *Store) Receipt {
	t.Helper()
	r, err := s.Import(t.Context(), "jobs/candidate.json")
	must(t, err)
	return r
}
func drain(t *testing.T, s *Store, sim, analysis int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	must(t, s.Work(ctx, Options{SimulationWorkers: sim, AnalysisWorkers: analysis, LeaseDuration: 200 * time.Millisecond, Drain: true}))
}
func files(t *testing.T, dir, folder string) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, folder, "*"))
	must(t, err)
	result := make(map[string][]byte)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		must(t, err)
		result[filepath.Base(path)] = data
	}
	return result
}
func verify(t *testing.T, dir string, events int) contract.Result {
	t.Helper()
	bags, results, ev := files(t, dir, "bags"), files(t, dir, "results"), files(t, dir, "events")
	if len(bags) != 1 || len(results) != 1 || len(ev) != events {
		t.Fatalf("bags=%d results=%d events=%d", len(bags), len(results), len(ev))
	}
	v, err := contract.New()
	must(t, err)
	paths := make([]string, 0, len(ev))
	sequence := map[int64]string{}
	var r contract.Result
	for _, data := range results {
		must(t, json.Unmarshal(data, &r))
	}
	for path, data := range ev {
		paths = append(paths, "events/"+path)
		var e contract.Event
		must(t, json.Unmarshal(data, &e))
		if _, ok := sequence[e.Sequence]; ok {
			t.Fatal("duplicate event sequence")
		}
		sequence[e.Sequence] = e.State
		if e.AttemptID != r.AttemptID {
			t.Fatal("changed accepted attempt")
		}
	}
	must(t, v.Check(t.Context(), dir, paths))
	for n, want := range []string{"PENDING", "RUNNING", "ANALYZING", r.Status} {
		if sequence[int64(n+1)] != want {
			t.Fatal(sequence)
		}
	}
	return r
}

func TestDurableIntakeDuplicatesAndConflicts(t *testing.T) {
	dir, s := setup(t)
	var group sync.WaitGroup
	var first atomic.Int32
	for range 8 {
		group.Go(func() {
			r, err := s.Import(t.Context(), "jobs/candidate.json")
			if err != nil {
				t.Error(err)
			}
			if !r.Duplicate {
				first.Add(1)
			}
		})
	}
	group.Wait()
	if first.Load() != 1 {
		t.Fatal("more than one initial receipt")
	}
	original, err := os.ReadFile(filepath.Join(dir, "jobs/candidate.json"))
	must(t, err)
	must(t, os.WriteFile(filepath.Join(dir, "jobs/copy.json"), original, 0o600))
	r, err := s.Import(t.Context(), "jobs/copy.json")
	must(t, err)
	if !r.Duplicate {
		t.Fatal("alternate path created work")
	}
	changed := append([]byte(" "), original...)
	must(t, os.WriteFile(filepath.Join(dir, "jobs/copy.json"), changed, 0o600))
	if _, err := s.Import(t.Context(), "jobs/copy.json"); !errors.Is(err, contract.ErrConflict) {
		t.Fatal(err)
	}
	// Intake owns a complete snapshot even before any file delivery.
	must(t, os.Remove(filepath.Join(dir, "jobs/candidate.json")))
	s2 := open(t, dir)
	drain(t, s2, 2, 2)
	result := verify(t, dir, 4)
	if result.Status != "FAIL" || *result.Metrics["collision_count"].Value != 1 {
		t.Fatal(result)
	}
	before := files(t, dir, "results")
	r = intake(t, s2)
	if !r.Duplicate {
		t.Fatal("restart forgot receipt")
	}
	drain(t, s2, 1, 1)
	for path, data := range files(t, dir, "results") {
		if !bytes.Equal(data, before[path]) {
			t.Fatal("duplicate changed result")
		}
	}
	var jobs int
	must(t, s2.db.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs))
	if jobs != 1 {
		t.Fatal(jobs)
	}
}

func TestSeparatePoolsReuseAcceptedBag(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	drain(t, s, 2, 0)
	before := files(t, dir, "bags")
	if len(before) != 1 || len(files(t, dir, "results")) != 0 {
		t.Fatal("simulation did not stop at handoff")
	}
	// Analysis-only pool must consume the bag after reopening the durable queue.
	s2 := open(t, dir)
	drain(t, s2, 0, 2)
	verify(t, dir, 4)
	for path, data := range files(t, dir, "bags") {
		if !bytes.Equal(data, before[path]) {
			t.Fatal("analysis changed bag")
		}
	}
}

func TestExpiredLeaseCannotAcceptOrRenew(t *testing.T) {
	for _, stage := range []string{"simulation", "analysis"} {
		t.Run(stage, func(t *testing.T) {
			_, s := setup(t)
			intake(t, s)
			if stage == "analysis" {
				drain(t, s, 1, 0)
			} else {
				must(t, s.Flush(t.Context()))
			}
			old, err := s.claim(t.Context(), stage, time.Second)
			must(t, err)
			must(t, s.Flush(t.Context()))
			// Deterministic expired timestamp avoids timing-dependent stale-worker tests.
			_, err = s.db.Exec("UPDATE jobs SET lease_ms=0 WHERE id=?", old.id)
			must(t, err)
			newer, err := s.claim(t.Context(), stage, time.Second)
			must(t, err)
			if newer.token == old.token {
				t.Fatal("lease token reused")
			}
			if err := s.renew(t.Context(), old, time.Second); !errors.Is(err, ErrLease) {
				t.Fatal(err)
			}
			called := false
			err = s.accept(t.Context(), old, "done", "", func(*sql.Tx, int64) error { called = true; return nil })
			if !errors.Is(err, ErrLease) || called {
				t.Fatalf("stale acceptance: %v", err)
			}
			must(t, s.perform(t.Context(), newer, time.Second, nil))
			drain(t, s, 1, 1)
		})
	}
}

func TestOutboxPublicationCrashWindow(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	drain(t, s, 1, 0)
	l, err := s.claim(t.Context(), "analysis", time.Second)
	must(t, err)
	must(t, s.perform(t.Context(), l, time.Second, nil))
	var id int64
	var path, kind string
	var data []byte
	must(t, s.db.QueryRow("SELECT id,path,kind,data FROM outbox WHERE delivered=0 ORDER BY id LIMIT 1").Scan(&id, &path, &kind, &data))
	if kind != "result" {
		t.Fatal(kind)
	}
	// File publication succeeds; the process stops before delivered=1.
	must(t, publication.Document(t.Context(), s.root, s.validator, path, kind, data))
	if len(files(t, dir, "events")) != 3 {
		t.Fatal("terminal event preceded result")
	}
	reopened := open(t, dir)
	must(t, reopened.Flush(t.Context()))
	accepted, err := os.ReadFile(filepath.Join(dir, path))
	must(t, err)
	if !bytes.Equal(accepted, data) {
		t.Fatal("recovery changed result bytes")
	}
	verify(t, dir, 4)
	// Repeat the terminal-event delivery window as well.
	_, err = s.db.Exec("UPDATE outbox SET delivered=0 WHERE kind='event'")
	must(t, err)
	before := files(t, dir, "events")
	must(t, reopened.Flush(t.Context()))
	for path, data := range files(t, dir, "events") {
		if !bytes.Equal(data, before[path]) {
			t.Fatal("outbox replay changed event")
		}
	}
}

func TestOutboxFailurePreservesPendingEvent(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	drain(t, s, 1, 0)
	l, err := s.claim(t.Context(), "analysis", time.Second)
	must(t, err)
	must(t, s.perform(t.Context(), l, time.Second, nil))
	// Block the result directory. Completion must remain unpublished.
	must(t, os.WriteFile(filepath.Join(dir, "results"), []byte("keep"), 0o600))
	if err := s.Flush(t.Context()); err == nil {
		t.Fatal("publication failure hidden")
	}
	if len(files(t, dir, "events")) != 3 {
		t.Fatal("completion published before result")
	}
	must(t, os.Remove(filepath.Join(dir, "results")))
	drain(t, s, 0, 1)
	verify(t, dir, 4)
}

func TestWorkerPoolsBoundedAndLeasesRenew(t *testing.T) {
	dir, s := setup(t)
	raw, err := os.ReadFile(filepath.Join(dir, "jobs/candidate.json"))
	must(t, err)
	var job map[string]any
	must(t, json.Unmarshal(raw, &job))
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		job["job_id"], job["execution_id"] = name, name
		data, err := encode(job)
		must(t, err)
		path := "jobs/" + name + ".json"
		must(t, os.WriteFile(filepath.Join(dir, path), data, 0o600))
		_, err = s.Import(t.Context(), path)
		must(t, err)
	}
	var current [2]atomic.Int32
	var peak [2]atomic.Int32
	hook := func(ctx context.Context, stage string) error {
		n := 0
		if stage == "analysis" {
			n = 1
		}
		count := current[n].Add(1)
		defer current[n].Add(-1)
		for {
			old := peak[n].Load()
			if count <= old || peak[n].CompareAndSwap(old, count) {
				break
			}
		}
		timer := time.NewTimer(350 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	must(t, s.work(ctx, Options{SimulationWorkers: 2, AnalysisWorkers: 1, LeaseDuration: 200 * time.Millisecond, Drain: true}, hook))
	if peak[0].Load() != 2 || peak[1].Load() != 1 {
		t.Fatalf("pool peaks: %d %d", peak[0].Load(), peak[1].Load())
	}
	if len(files(t, dir, "results")) != 6 {
		t.Fatal("lease renewal lost jobs")
	}
}

func TestGracefulStageCancellationAndResume(t *testing.T) {
	for _, stage := range []string{"simulation", "analysis"} {
		t.Run(stage, func(t *testing.T) {
			dir, s := setup(t)
			intake(t, s)
			if stage == "analysis" {
				drain(t, s, 1, 0)
			}
			ctx, cancel := context.WithCancel(t.Context())
			hook := func(ctx context.Context, at string) error {
				if at == stage {
					cancel()
					<-ctx.Done()
					return ctx.Err()
				}
				return nil
			}
			if err := s.work(ctx, Options{SimulationWorkers: 1, AnalysisWorkers: 1}, hook); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			var token string
			must(t, s.db.QueryRow("SELECT token FROM jobs").Scan(&token))
			if token != "" {
				t.Fatal("graceful stop retained lease")
			}
			drain(t, open(t, dir), 1, 1)
			verify(t, dir, 4)
		})
	}
}

// The helper exits during a held stage, without defers or graceful lease release.
func TestCrashHelper(t *testing.T) {
	dir := os.Getenv("YAMATA_TEST_CRASH_EXCHANGE")
	if dir == "" {
		return
	}
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	stage := os.Getenv("YAMATA_TEST_CRASH_STAGE")
	l, err := s.claim(context.Background(), stage, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if stage == "simulation" {
		// Stop after actual computation, before acceptance; no bag can escape the lease.
		if _, err := bag.Prepare(context.Background(), s.validator, l.job); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err := s.validator.ReadBag(context.Background(), s.root, "bags/"+l.job.ExecutionID+".jsonl", l.bagHash); err != nil {
			t.Fatal(err)
		}
	}
	os.Exit(23)
}

func TestProcessExitRecoveryDuringBothStages(t *testing.T) {
	for _, stage := range []string{"simulation", "analysis"} {
		t.Run(stage, func(t *testing.T) {
			dir, s := setup(t)
			intake(t, s)
			if stage == "analysis" {
				drain(t, s, 1, 0)
			} else {
				must(t, s.Flush(t.Context()))
			}
			before := files(t, dir, "bags")
			cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
			cmd.Env = append(os.Environ(), "YAMATA_TEST_CRASH_EXCHANGE="+dir, "YAMATA_TEST_CRASH_STAGE="+stage)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 23 {
				t.Fatalf("helper: %s %v", output, err)
			}
			drain(t, open(t, dir), 1, 1)
			verify(t, dir, 4)
			for path, data := range before {
				if !bytes.Equal(data, files(t, dir, "bags")[path]) {
					t.Fatal("restart replaced accepted bag")
				}
			}
		})
	}
}

func TestPrivateDatabasePathsAndVersion(t *testing.T) {
	for _, name := range []string{".queue", ".queue/queue.sqlite", ".queue/queue.sqlite-wal", ".queue/queue.sqlite-shm", ".queue/queue.sqlite-journal"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			outside := t.TempDir()
			if name != ".queue" {
				must(t, os.Mkdir(filepath.Join(dir, ".queue"), 0o700))
			}
			target := outside
			if name != ".queue" {
				target = filepath.Join(outside, "keep")
				must(t, os.WriteFile(target, []byte("unchanged"), 0o600))
			}
			must(t, os.Symlink(target, filepath.Join(dir, name)))
			if s, err := Open(t.Context(), dir); err == nil {
				s.Close()
				t.Fatal("database link accepted")
			}
			if name != ".queue" {
				data, err := os.ReadFile(target)
				must(t, err)
				if string(data) != "unchanged" {
					t.Fatal("wrote outside exchange")
				}
			}
		})
	}
	dir, s := setup(t)
	for _, pragma := range []string{"synchronous", "foreign_keys"} {
		var n int
		must(t, s.db.QueryRow("PRAGMA "+pragma).Scan(&n))
		want := 1
		if pragma == "synchronous" {
			want = 2
		}
		if n != want {
			t.Fatal(pragma, n)
		}
	}
	var mode string
	must(t, s.db.QueryRow("PRAGMA journal_mode").Scan(&mode))
	if mode != "wal" {
		t.Fatal(mode)
	}
	_, err := s.db.Exec("PRAGMA user_version=2")
	must(t, err)
	if other, err := Open(t.Context(), dir); err == nil {
		other.Close()
		t.Fatal("unknown schema version accepted")
	}
}

func TestRejectedJobsCreateNoReceipt(t *testing.T) {
	dir, s := setup(t)
	data, err := os.ReadFile(filepath.Join(dir, "jobs/candidate.json"))
	must(t, err)
	data = bytes.Replace(data, []byte(`"start_speed_mm_s": 10000`), []byte(`"start_speed_mm_s": 10001`), 1)
	must(t, os.WriteFile(filepath.Join(dir, "jobs/candidate.json"), data, 0o600))
	if _, err := s.Import(t.Context(), "jobs/candidate.json"); !errors.Is(err, contract.ErrHash) {
		t.Fatal(err)
	}
	for _, path := range []string{"../jobs/candidate.json", "jobs/nested/candidate.json", "jobs/candidate.json.tmp"} {
		if _, err := s.Import(t.Context(), path); !errors.Is(err, contract.ErrPath) {
			t.Fatal(path, err)
		}
	}
	var n int
	must(t, s.db.QueryRow("SELECT count(*) FROM jobs").Scan(&n))
	if n != 0 {
		t.Fatal("invalid job queued")
	}
}

func TestChangedExecutionIdentityConflicts(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	data, err := os.ReadFile(filepath.Join(dir, "jobs/candidate.json"))
	must(t, err)
	var job map[string]any
	must(t, json.Unmarshal(data, &job))
	job["job_id"] = "different-job"
	data, err = encode(job)
	must(t, err)
	must(t, os.WriteFile(filepath.Join(dir, "jobs/other.json"), data, 0o600))
	if _, err := s.Import(t.Context(), "jobs/other.json"); !errors.Is(err, contract.ErrConflict) {
		t.Fatal(err)
	}
	if !intake(t, s).Duplicate {
		t.Fatal("lost execution identity")
	}
}

func TestMultipleQueueProcesses(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	// Independent database handles exercise the same transaction boundary as separate processes.
	second := open(t, dir)
	var group sync.WaitGroup
	for _, store := range []*Store{s, second} {
		group.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := store.Work(ctx, Options{SimulationWorkers: 2, AnalysisWorkers: 2, Drain: true}); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	verify(t, dir, 4)
}

func TestQueuedFailureResults(t *testing.T) {
	for _, kind := range []string{"controller", "simulator", "timeout", "analysis"} {
		t.Run(kind, func(t *testing.T) {
			dir, s := setup(t)
			path := filepath.Join(dir, "jobs/candidate.json")
			data, err := os.ReadFile(path)
			must(t, err)
			var job map[string]any
			must(t, json.Unmarshal(data, &job))
			in := job["inputs"].(map[string]any)
			switch kind {
			case "controller", "simulator":
				in[kind].(map[string]any)["version"] = 2
			case "timeout":
				in["limits"].(map[string]any)["max_ticks"] = 1
			case "analysis":
				in["analysis_template"].(map[string]any)["minimum_obstacle_gap"].(map[string]any)["version"] = 2
			}
			raw, err := json.Marshal(in)
			must(t, err)
			hash, err := contract.ContentHash(raw)
			must(t, err)
			job["inputs_hash"] = hash
			data, err = encode(job)
			must(t, err)
			must(t, os.WriteFile(path, data, 0o600))
			intake(t, s)
			drain(t, s, 1, 1)
			results := files(t, dir, "results")
			if len(results) != 1 {
				t.Fatal("missing failure result")
			}
			want := map[string]string{"controller": "controller_failure", "simulator": "worker_failure", "timeout": "simulation_timeout", "analysis": "analysis_failure"}[kind]
			for _, data := range results {
				var r contract.Result
				must(t, json.Unmarshal(data, &r))
				if r.Status != "ERROR" || r.FailureClass == nil || *r.FailureClass != want {
					t.Fatal(r)
				}
			}
			paths := []string{}
			for path := range files(t, dir, "events") {
				paths = append(paths, "events/"+path)
			}
			must(t, s.validator.Check(t.Context(), dir, paths))
			before := files(t, dir, "events")
			intake(t, s)
			drain(t, s, 1, 1)
			if len(files(t, dir, "events")) != len(before) {
				t.Fatal("business failure retried")
			}
		})
	}
}

func TestQueueRejectsStandaloneOwnership(t *testing.T) {
	dir := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dir, ".standalone"), 0o700))
	if s, err := Open(t.Context(), dir); !errors.Is(err, contract.ErrConflict) {
		if s != nil {
			s.Close()
		}
		t.Fatal(err)
	}
}

func TestHandoffTransactionRollbackAndRecovery(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	must(t, s.Flush(t.Context()))
	l, err := s.claim(t.Context(), "simulation", time.Second)
	must(t, err)
	must(t, s.Flush(t.Context()))
	data, err := bag.Prepare(t.Context(), s.validator, l.job)
	must(t, err)
	_, err = s.db.Exec(`CREATE TRIGGER reject_handoff BEFORE INSERT ON outbox WHEN NEW.kind='event'
  BEGIN SELECT RAISE(ABORT,'injected event failure'); END`)
	must(t, err)
	if err := s.acceptBag(t.Context(), l, data); err == nil {
		t.Fatal("failed transition committed")
	}
	var stage, token string
	must(t, s.db.QueryRow("SELECT stage,token FROM jobs").Scan(&stage, &token))
	if stage != "simulation" || token != l.token {
		t.Fatal("failed transaction lost stage ownership")
	}
	var n int
	must(t, s.db.QueryRow("SELECT count(*) FROM outbox WHERE kind='bag'").Scan(&n))
	if n != 0 {
		t.Fatal("partial handoff committed")
	}
	_, err = s.db.Exec("DROP TRIGGER reject_handoff")
	must(t, err)
	must(t, s.acceptBag(t.Context(), l, data))
	if len(files(t, dir, "bags")) != 0 {
		t.Fatal("acceptance bypassed outbox")
	}
	// Recovery after commit, before the first bag publication, must finish analysis.
	drain(t, open(t, dir), 0, 1)
	verify(t, dir, 4)
}

func TestIntakeProcessHelper(t *testing.T) {
	dir := os.Getenv("YAMATA_TEST_INTAKE_EXCHANGE")
	if dir == "" {
		return
	}
	s, err := Open(t.Context(), dir)
	must(t, err)
	_, err = s.Import(t.Context(), "jobs/candidate.json")
	must(t, err)
	must(t, s.Close())
}

func TestConcurrentFirstOpenAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	must(t, os.CopyFS(filepath.Join(dir, "jobs"), os.DirFS("../../examples/jobs")))
	var group sync.WaitGroup
	for range 6 {
		group.Go(func() {
			cmd := exec.Command(os.Args[0], "-test.run=^TestIntakeProcessHelper$")
			cmd.Env = append(os.Environ(), "YAMATA_TEST_INTAKE_EXCHANGE="+dir)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("concurrent intake: %s %v", output, err)
			}
		})
	}
	group.Wait()
	s := open(t, dir)
	var n int
	must(t, s.db.QueryRow("SELECT count(*) FROM jobs").Scan(&n))
	if n != 1 {
		t.Fatal("duplicate intake after concurrent open")
	}
	drain(t, s, 1, 1)
	verify(t, dir, 4)
}
