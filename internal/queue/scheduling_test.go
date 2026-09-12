package queue

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func addJob(t *testing.T, s *Store, dir, id string, priority int, change func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile("../../examples/jobs/candidate.json")
	must(t, err)
	var job map[string]any
	must(t, json.Unmarshal(data, &job))
	job["job_id"], job["execution_id"], job["priority"] = id, id, priority
	if change != nil {
		change(job)
	}
	data, err = encode(job)
	must(t, err)
	path := "jobs/" + id + ".json"
	must(t, os.WriteFile(filepath.Join(dir, path), data, 0o600))
	_, err = s.Import(t.Context(), path)
	must(t, err)
}
func claimed(t *testing.T, s *Store, stage string) lease {
	t.Helper()
	l, err := s.claim(t.Context(), stage, time.Minute)
	must(t, err)
	return l
}
func prepareStage(t *testing.T, s *Store, stage string) {
	t.Helper()
	if stage == "analysis" {
		drain(t, s, 1, 0)
	} else {
		must(t, s.Flush(t.Context()))
	}
}
func position(t *testing.T, s *Store, stage string) int {
	t.Helper()
	var n int
	must(t, s.db.QueryRow("SELECT position FROM dispatches WHERE stage=?", stage).Scan(&n))
	return n
}

func TestPriorityOrderIsStableInBothPools(t *testing.T) {
	for _, stage := range []string{"simulation", "analysis"} {
		t.Run(stage, func(t *testing.T) {
			dir, s := setup(t)
			for _, entry := range []struct {
				id       string
				priority int
			}{{"low", 3}, {"middle-first", 2}, {"normal", 1}, {"high-first", 0}, {"middle-second", 2}, {"high-second", 0}} {
				addJob(t, s, dir, entry.id, entry.priority, nil)
			}
			prepareStage(t, s, stage)
			for _, want := range []string{"high-first", "high-second", "normal", "middle-first", "middle-second", "low"} {
				if got := claimed(t, s, stage).job.JobID; got != want {
					t.Fatalf("got %s, want %s", got, want)
				}
			}
			if _, err := s.claim(t.Context(), stage, time.Minute); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal(err)
			}
			if position(t, s, stage) != 6 {
				t.Fatal("empty claim advanced dispatch position")
			}
		})
	}
}

func TestTenthDispatchReservationSurvivesReopen(t *testing.T) {
	for _, stage := range []string{"simulation", "analysis"} {
		t.Run(stage, func(t *testing.T) {
			dir, s := setup(t)
			addJob(t, s, dir, "low-first", 3, nil)
			addJob(t, s, dir, "low-second", 3, nil)
			for n := 1; n <= 18; n++ {
				addJob(t, s, dir, fmt.Sprintf("high-%02d", n), 0, nil)
			}
			prepareStage(t, s, stage)
			high := 0
			for n := 1; n <= 20; n++ {
				if n == 10 {
					s = open(t, dir)
				}
				want := ""
				switch n {
				case 10:
					want = "low-first"
				case 20:
					want = "low-second"
				default:
					high++
					want = fmt.Sprintf("high-%02d", high)
				}
				if got := claimed(t, s, stage).job.JobID; got != want {
					t.Fatalf("dispatch %d: got %s, want %s", n, got, want)
				}
			}
			if position(t, s, stage) != 0 {
				t.Fatal("reservation cycle differs")
			}
		})
	}
}

func TestReservationFallsBackWhenLowestIsNotEligible(t *testing.T) {
	for _, mode := range []string{"absent", "leased", "unpublished"} {
		t.Run(mode, func(t *testing.T) {
			dir, s := setup(t)
			addJob(t, s, dir, "middle", 2, nil)
			for n := 1; n <= 10; n++ {
				addJob(t, s, dir, fmt.Sprintf("high-%02d", n), 0, nil)
			}
			must(t, s.Flush(t.Context()))
			for range 9 {
				claimed(t, s, "simulation")
			}
			if mode != "absent" {
				addJob(t, s, dir, "low", 3, nil)
				if mode == "leased" {
					must(t, s.Flush(t.Context()))
					_, err := s.db.Exec("UPDATE jobs SET lease_ms=? WHERE job_id='low'", time.Now().Add(time.Minute).UnixMilli())
					must(t, err)
				}
			}
			if got := claimed(t, s, "simulation").job.JobID; got != "high-10" {
				t.Fatal("ineligible reservation displaced highest class:", got)
			}
		})
	}
}

func TestPriorityDoesNotPreemptCurrentLease(t *testing.T) {
	dir, s := setup(t)
	addJob(t, s, dir, "low", 3, nil)
	must(t, s.Flush(t.Context()))
	low := claimed(t, s, "simulation")
	addJob(t, s, dir, "high", 0, nil)
	must(t, s.Flush(t.Context()))
	if got := claimed(t, s, "simulation").job.JobID; got != "high" {
		t.Fatal(got)
	}
	must(t, s.renew(t.Context(), low, time.Minute))
	must(t, s.perform(t.Context(), low, time.Minute, nil))
	must(t, s.Flush(t.Context()))
	if len(files(t, dir, "bags")) != 1 {
		t.Fatal("higher priority displaced running work")
	}
}

func TestDispatchPositionAndClaimRollbackTogether(t *testing.T) {
	dir, s := setup(t)
	addJob(t, s, dir, "high", 0, nil)
	must(t, s.Flush(t.Context()))
	_, err := s.db.Exec(`CREATE TRIGGER reject_claim BEFORE INSERT ON outbox WHEN NEW.kind='event'
 BEGIN SELECT RAISE(ABORT,'injected event failure'); END`)
	must(t, err)
	if _, err := s.claim(t.Context(), "simulation", time.Minute); err == nil {
		t.Fatal("failed claim accepted")
	}
	if position(t, s, "simulation") != 0 {
		t.Fatal("failed claim spent reservation position")
	}
	var token string
	var generation int64
	must(t, s.db.QueryRow("SELECT token,generation FROM jobs").Scan(&token, &generation))
	if token != "" || generation != 0 {
		t.Fatal("partial claim committed")
	}
	_, err = s.db.Exec("DROP TRIGGER reject_claim")
	must(t, err)
	if l := claimed(t, s, "simulation"); l.generation != 1 {
		t.Fatal(l.generation)
	}
	if position(t, s, "simulation") != 1 {
		t.Fatal("successful claim not counted")
	}
}

func TestConcurrentClaimsShareReservationCounter(t *testing.T) {
	dir, s := setup(t)
	for n := 1; n <= 20; n++ {
		priority := 0
		if n <= 2 {
			priority = 3
		}
		addJob(t, s, dir, fmt.Sprintf("job-%02d", n), priority, nil)
	}
	must(t, s.Flush(t.Context()))
	// Record commit order independently of goroutine completion order.
	_, err := s.db.Exec(`CREATE TABLE observed_dispatches(n INTEGER PRIMARY KEY,job TEXT,priority INTEGER);
 CREATE TRIGGER observe_claim AFTER UPDATE OF generation ON jobs WHEN NEW.generation>OLD.generation
 BEGIN INSERT INTO observed_dispatches(job,priority) VALUES(NEW.job_id,NEW.priority); END;`)
	must(t, err)
	second := open(t, dir)
	errorsOut := make(chan error, 2)
	for _, store := range []*Store{s, second} {
		go func() {
			for {
				_, err := store.claim(t.Context(), "simulation", time.Minute)
				if errors.Is(err, sql.ErrNoRows) {
					errorsOut <- nil
					return
				}
				if err != nil {
					errorsOut <- err
					return
				}
			}
		}()
	}
	must(t, <-errorsOut)
	must(t, <-errorsOut)
	rows, err := s.db.Query("SELECT n,priority FROM observed_dispatches ORDER BY n")
	must(t, err)
	defer rows.Close()
	total := 0
	for rows.Next() {
		var n, priority int
		must(t, rows.Scan(&n, &priority))
		want := 0
		if n%10 == 0 {
			want = 3
		}
		if priority != want {
			t.Fatalf("dispatch %d priority=%d want=%d", n, priority, want)
		}
		total++
	}
	must(t, rows.Err())
	if total != 20 {
		t.Fatal("claims lost or duplicated", total)
	}
}
