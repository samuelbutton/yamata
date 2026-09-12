package queue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/samuelbutton/yamata/internal/bag"
	"github.com/samuelbutton/yamata/internal/contract"
	"github.com/samuelbutton/yamata/internal/execution"
)

// Options bounds independently sized stage pools. Zero disables one stage.
type Options struct {
	SimulationWorkers int
	AnalysisWorkers   int
	LeaseDuration     time.Duration
	// Drain exits after the enabled stages and all pending file deliveries finish.
	Drain bool
}

// Validate checks pool and lease bounds before a caller opens durable state.
func (o Options) Validate() error {
	if o.SimulationWorkers < 0 || o.AnalysisWorkers < 0 || o.SimulationWorkers > 64 || o.AnalysisWorkers > 64 || o.SimulationWorkers+o.AnalysisWorkers < 1 || o.SimulationWorkers+o.AnalysisWorkers > 64 {
		return errors.New("supply 1 to 64 total workers; stage counts cannot be negative")
	}
	if o.LeaseDuration < 100*time.Millisecond || o.LeaseDuration > time.Hour {
		return errors.New("lease duration must be between 100ms and 1h")
	}
	return nil
}

// Work serves the queue until canceled, drained, or a storage error occurs.
// Graceful cancellation releases leases; process death leaves them to expire.
func (s *Store) Work(ctx context.Context, options Options) error {
	return s.work(ctx, options, nil)
}

// beforeStage is a package-test seam for stopping a real worker at a stage boundary.
func (s *Store) work(ctx context.Context, options Options, beforeStage func(context.Context, string) error) error {
	if options.LeaseDuration == 0 {
		options.LeaseDuration = 30 * time.Second
	}
	if err := options.Validate(); err != nil {
		return err
	}
	workCtx, cancel := context.WithCancelCause(ctx)
	var group sync.WaitGroup
	launch := func(fn func() error) {
		group.Go(func() {
			if err := fn(); err != nil {
				cancel(err)
			}
		})
	}
	defer group.Wait()
	// Cancel before waiting for the pools on every exit path.
	defer cancel(nil)
	launch(func() error { return repeat(workCtx, func() error { return s.Flush(workCtx) }) })
	for stage, count := range map[string]int{"simulation": options.SimulationWorkers, "analysis": options.AnalysisWorkers} {
		for range count {
			launch(func() error {
				return repeat(workCtx, func() error {
					l, err := s.claim(workCtx, stage, options.LeaseDuration)
					if idle(err) {
						return nil
					}
					if err != nil {
						return err
					}
					err = s.perform(workCtx, l, options.LeaseDuration, beforeStage)
					if idle(err) {
						return nil
					}
					return err
				})
			})
		}
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-workCtx.Done():
			return context.Cause(workCtx)
		case <-ticker.C:
			if !options.Drain {
				continue
			}
			var pending int
			err := s.db.QueryRowContext(workCtx, `SELECT
    (SELECT count(*) FROM jobs WHERE (stage='simulation' AND ?>0) OR (stage='analysis' AND ?>0)) +
    (SELECT count(*) FROM outbox WHERE delivered=0)`, options.SimulationWorkers, options.AnalysisWorkers).Scan(&pending)
			if err != nil {
				if workCtx.Err() != nil {
					return context.Cause(workCtx)
				}
				return err
			}
			if pending == 0 {
				return nil
			}
		}
	}
}

func repeat(ctx context.Context, action func() error) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := action(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Store) perform(ctx context.Context, l lease, duration time.Duration, beforeStage func(context.Context, string) error) (err error) {
	stageCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(duration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-stageCtx.Done():
				done <- nil
				return
			case <-ticker.C:
				if err := s.renew(stageCtx, l, duration); err != nil {
					cancel()
					done <- err
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		renewalErr := <-done
		if err != nil {
			err = errors.Join(err, renewalErr, s.release(l))
		}
	}()
	out, err := s.calculate(stageCtx, l, beforeStage)
	if stageCtx.Err() != nil {
		return stageCtx.Err()
	}
	if errors.Is(err, errWorkerFailure) {
		failure := "worker_failure"
		out.result.Status, out.result.FailureClass = "ERROR", &failure
	} else if err != nil {
		return err
	}
	if out.bag != nil {
		return s.acceptBag(stageCtx, l, out.bag)
	}
	return s.acceptResult(stageCtx, l, out.result)
}

var errWorkerFailure = errors.New("worker computation failed")

type stageOutput struct {
	bag    []byte
	result contract.Result
}

// calculate contains worker computations; a panic loses this calculation only.
// Panic values are deliberately excluded from results and errors.
func (s *Store) calculate(ctx context.Context, l lease, beforeStage func(context.Context, string) error) (out stageOutput, err error) {
	out.result = l.result()
	if l.stage == "analysis" {
		ref := contract.Reference{Path: l.bagPath, SHA256: l.bagHash}
		out.result, err = execution.Analysis(out.result, l.run.AnalysisTemplate, ref)
		if err != nil {
			return out, err
		}
	}
	defer func() {
		if recover() != nil {
			out.bag = nil
			err = errWorkerFailure
		}
	}()
	if beforeStage != nil {
		if err := beforeStage(ctx, l.stage); err != nil {
			return out, err
		}
	}
	if l.stage == "simulation" {
		out.bag, err = bag.Prepare(ctx, s.validator, l.runJob())
		if err == nil {
			return out, nil
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		failure := execution.RunFailure(l.runJob(), err)
		if failure == "" {
			return out, fmt.Errorf("prepare recording: %w", err)
		}
		out.result.Status, out.result.FailureClass = "ERROR", &failure
		return out, nil
	}
	ref := out.result.Bag
	recording, err := s.validator.ReadBag(ctx, s.root, ref.Path, ref.SHA256)
	if err != nil {
		return out, err
	}
	out.result, err = execution.Score(ctx, out.result, recording, l.run)
	return out, err
}
