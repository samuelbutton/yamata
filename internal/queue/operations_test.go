package queue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samuelbutton/yamata/internal/contract"
)

func TestDeterministicFailuresSurviveRestartAndCannotRearm(t *testing.T) {
	for _, stage := range []string{"simulation", "analysis"} {
		for _, count := range []int{1, 2} {
			t.Run(stage+string(rune('0'+count)), func(t *testing.T) {
				dir, s := setup(t)
				receipt := intake(t, s)
				must(t, s.InjectFailure(t.Context(), receipt.JobID, stage, count))
				must(t, s.InjectFailure(t.Context(), receipt.JobID, stage, count))
				if err := s.InjectFailure(t.Context(), receipt.JobID, stage, 3-count); !errors.Is(err, contract.ErrConflict) {
					t.Fatal(err)
				}
				prepareStage(t, s, stage)
				l := claimed(t, s, stage)
				must(t, s.perform(t.Context(), l, time.Minute, nil))
				if retryCount(t, s) != 1 {
					t.Fatal("failure did not commit retry")
				}
				s = open(t, dir)
				drain(t, s, 1, 1)
				_, result := history(t, dir)
				want := "FAIL"
				if count == 2 {
					want = "ERROR"
				}
				if result.Status != want || retryCount(t, s) != 1 {
					t.Fatal(result)
				}
				before := files(t, dir, "results")
				must(t, s.InjectFailure(t.Context(), receipt.JobID, stage, count))
				drain(t, s, 1, 1)
				for name, data := range before {
					if string(data) != string(files(t, dir, "results")[name]) {
						t.Fatal("rearmed outcome")
					}
				}
			})
		}
	}
}
func TestFailureControlsRejectLateAndInvalidConfiguration(t *testing.T) {
	_, s := setup(t)
	receipt := intake(t, s)
	for _, tc := range []struct {
		job, stage string
		count      int
	}{{receipt.JobID, "unknown", 1}, {receipt.JobID, "analysis", 0}, {"missing", "simulation", 1}} {
		if err := s.InjectFailure(t.Context(), tc.job, tc.stage, tc.count); err == nil {
			t.Fatal(tc)
		}
	}
	must(t, s.Flush(t.Context()))
	l := claimed(t, s, "simulation")
	if err := s.InjectFailure(t.Context(), receipt.JobID, "simulation", 1); !errors.Is(err, contract.ErrConflict) {
		t.Fatal(err)
	}
	must(t, s.release(l))
}
func TestInspectionShowsPublicationLeasesRetriesAndPagination(t *testing.T) {
	dir, s := setup(t)
	receipt := intake(t, s)
	addJob(t, s, dir, "second", 3, nil)
	must(t, s.InjectFailure(t.Context(), receipt.JobID, "analysis", 1))
	initial, err := Inspect(t.Context(), dir, 0, 1)
	must(t, err)
	if initial.TotalJobs != 2 || initial.PendingFiles != 4 || len(initial.Jobs) != 1 || initial.NextAfter == nil || initial.Jobs[0].Ready || initial.Jobs[0].AnalysisFailures != 1 {
		t.Fatal(initial)
	}
	page, err := Inspect(t.Context(), dir, *initial.NextAfter, 1)
	must(t, err)
	if len(page.Jobs) != 1 || page.Jobs[0].JobID != "second" || page.NextAfter != nil {
		t.Fatal(page)
	}
	must(t, s.Flush(t.Context()))
	l := claimed(t, s, "simulation")
	view, err := Inspect(t.Context(), dir, 0, 10)
	must(t, err)
	if view.Jobs[0].Ready || view.Jobs[0].LeaseUntilMS == 0 || view.Jobs[0].Generation != 1 || view.DispatchPositions["simulation"] != 1 {
		t.Fatal(view)
	}
	must(t, s.perform(t.Context(), l, time.Minute, nil))
	drain(t, s, 1, 1)
	view, err = Inspect(t.Context(), dir, 0, 10)
	must(t, err)
	if view.Stages["done"] != 2 || view.PendingFiles != 0 || view.Jobs[0].AnalysisRetries != 1 || view.Jobs[0].ResultPath == "" || view.Jobs[0].AnalysisID == nil {
		t.Fatal(view)
	}
	if position(t, s, "simulation") != 2 {
		t.Fatal("inspection claimed work")
	}
}
func TestInspectionDoesNotInitializeOrUpgrade(t *testing.T) {
	dir := t.TempDir()
	if _, err := Inspect(t.Context(), dir, 0, 100); err == nil {
		t.Fatal("missing queue accepted")
	}
	entries, err := os.ReadDir(dir)
	must(t, err)
	if len(entries) != 0 {
		t.Fatal("inspection wrote state")
	}
	source, s := setup(t)
	intake(t, s)
	old := legacyCopy(t, s, source)
	if _, err := Inspect(t.Context(), old, 0, 100); err == nil {
		t.Fatal("old queue silently upgraded")
	}
	must(t, os.Symlink(filepath.Join(old, ".queue"), filepath.Join(dir, ".queue")))
	if _, err := Inspect(t.Context(), dir, 0, 100); err == nil {
		t.Fatal("queue link accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Inspect(ctx, source, 0, 100); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
