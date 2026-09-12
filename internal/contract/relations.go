package contract

import (
	"context"
	"encoding/json"
	"fmt"
)

type inputs struct {
	Bag              *Reference      `json:"bag"`
	AnalysisTemplate json.RawMessage `json:"analysis_template"`
	RunTemplate      struct {
		TickMS int `json:"tick_ms"`
	} `json:"run_template"`
	Limits struct {
		MaxTicks int `json:"max_ticks"`
	} `json:"limits"`
}

func (s *inspection) checkDocument(ctx context.Context, d document) error {
	switch d.Kind {
	case "job":
		digest, err := ContentHash(d.Inputs)
		if err != nil {
			return err
		}
		if digest != d.InputsHash {
			return ErrHash
		}
		var in inputs
		if err := json.Unmarshal(d.Inputs, &in); err != nil {
			return err
		}
		if d.JobKind == "analysis" {
			bag, err := s.reference(ctx, in.Bag, "bag")
			if err != nil {
				return err
			}
			if bag.ExecutionID != d.ExecutionID {
				return fmt.Errorf("%w: analysis execution differs from bag", ErrInvalid)
			}
		}
	case "result":
		return s.checkResult(ctx, d)
	case "event":
		if d.EventID != eventID(d) {
			return fmt.Errorf("%w: event identity", ErrHash)
		}
		terminal := d.State == "PASS" || d.State == "FAIL" || d.State == "WARN" || d.State == "ERROR"
		if terminal != (d.Result != nil) {
			return fmt.Errorf("%w: terminal event requires a result; other events cannot include one", ErrInvalid)
		}
		if (d.State == "PENDING" || d.State == "RUNNING") && d.AnalysisID != nil {
			return fmt.Errorf("%w: analysis identity is not yet known", ErrInvalid)
		}
		if d.State == "ANALYZING" && d.AnalysisID == nil {
			return fmt.Errorf("%w: missing analysis identity", ErrInvalid)
		}
		if terminal {
			result, err := s.reference(ctx, d.Result, "result")
			if err != nil {
				return err
			}
			if !sameJob(d, result) || d.AttemptID != result.AttemptID ||
				d.State != result.Status || !equalStrings(d.AnalysisID, result.AnalysisID) {
				return fmt.Errorf("%w: event differs from result", ErrInvalid)
			}
		}
	}
	return nil
}

func equalStrings(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func sameJob(a, b document) bool {
	return a.ExecutionID == b.ExecutionID && a.JobID == b.JobID && a.CorrelationID == b.CorrelationID
}

func (s *inspection) checkResult(ctx context.Context, d document) error {
	job, err := s.reference(ctx, d.Job, "job")
	if err != nil {
		return err
	}
	if !sameJob(d, job) || d.InputsHash != job.InputsHash {
		return fmt.Errorf("%w: result differs from job", ErrInvalid)
	}
	if (d.Status == "ERROR") != (d.FailureClass != nil) {
		return fmt.Errorf("%w: failure class and status disagree", ErrInvalid)
	}
	var in inputs
	if err := json.Unmarshal(job.Inputs, &in); err != nil {
		return err
	}
	if d.AnalysisID == nil {
		if d.Status != "ERROR" || job.JobKind != "run" || *d.FailureClass == "analysis_failure" {
			return fmt.Errorf("%w: invalid run failure", ErrInvalid)
		}
		if d.Bag != nil || d.AnalysisHash != nil || string(d.AnalysisTemplate) != "null" || d.Metrics != nil {
			return fmt.Errorf("%w: run failure must have null bag, analysis, and metrics", ErrInvalid)
		}
		return nil
	}
	bag, err := s.reference(ctx, d.Bag, "bag")
	if err != nil {
		return err
	}
	if d.AnalysisHash == nil || bag.ExecutionID != d.ExecutionID {
		return fmt.Errorf("%w: missing analysis hash or wrong bag execution", ErrInvalid)
	}
	if job.JobKind == "run" && (bag.InputsHash != job.InputsHash || bag.TickMS != in.RunTemplate.TickMS || bag.RecordCount > in.Limits.MaxTicks+1) {
		return fmt.Errorf("%w: bag differs from run inputs", ErrInvalid)
	}
	if job.JobKind == "analysis" && (in.Bag == nil || *in.Bag != *d.Bag) {
		return fmt.Errorf("%w: result uses a different analysis bag", ErrInvalid)
	}
	digest, err := ContentHash(d.AnalysisTemplate)
	if err != nil {
		return err
	}
	want, err := ContentHash(in.AnalysisTemplate)
	if err != nil {
		return err
	}
	if digest != want || digest != *d.AnalysisHash || *d.AnalysisID != AnalysisID(d.ExecutionID, d.Bag.SHA256, digest) {
		return ErrHash
	}
	if d.Status == "ERROR" {
		if d.Metrics != nil || (*d.FailureClass != "worker_failure" && *d.FailureClass != "analysis_failure") {
			return fmt.Errorf("%w: invalid analysis failure", ErrInvalid)
		}
		return nil
	}
	return checkMetrics(d, bag.RecordCount)
}

func checkMetrics(d document, records int) error {
	if len(d.Metrics) != 3 {
		return fmt.Errorf("%w: completed analysis requires metrics", ErrInvalid)
	}
	var template map[string]struct {
		Version    int   `json:"version"`
		MinimumMM  int64 `json:"minimum_mm"`
		MinimumPPM int64 `json:"minimum_ppm"`
	}
	if err := json.Unmarshal(d.AnalysisTemplate, &template); err != nil {
		return err
	}
	status := "PASS"
	for name, m := range d.Metrics {
		unit := map[string]string{"collision_count": "count", "minimum_obstacle_gap": "mm", "goal_progress": "ppm"}[name]
		if m.Unit != unit || m.Version != template[name].Version {
			return fmt.Errorf("%w: metric unit or version", ErrInvalid)
		}
		for _, tick := range m.EvidenceTicks {
			if tick >= records {
				return fmt.Errorf("%w: evidence tick outside bag", ErrInvalid)
			}
		}
		if m.Value == nil {
			if m.Pass != nil || len(m.EvidenceTicks) != 0 {
				return fmt.Errorf("%w: unavailable metric cannot have a pass flag or evidence", ErrInvalid)
			}
			if status != "FAIL" {
				status = "WARN"
			}
			continue
		}
		if m.Pass == nil {
			return fmt.Errorf("%w: available metric needs a pass flag", ErrInvalid)
		}
		pass := false
		switch name {
		case "collision_count":
			if *m.Value < 0 {
				return fmt.Errorf("%w: negative collision count", ErrInvalid)
			}
			pass = *m.Value == 0
		case "minimum_obstacle_gap":
			pass = *m.Value >= template[name].MinimumMM
		case "goal_progress":
			if *m.Value < 0 || *m.Value > 1000000 {
				return fmt.Errorf("%w: progress outside range", ErrInvalid)
			}
			pass = *m.Value >= template[name].MinimumPPM
		}
		if pass != *m.Pass {
			return fmt.Errorf("%w: metric pass flag contradicts score limit", ErrInvalid)
		}
		if !pass {
			status = "FAIL"
		}
	}
	if status != d.Status {
		return fmt.Errorf("%w: result status contradicts metrics", ErrInvalid)
	}
	return nil
}
