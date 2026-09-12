// Package execution constructs outcomes shared by synchronous and queued execution.
package execution

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/samuelbutton/yamata/internal/contract"
	"github.com/samuelbutton/yamata/internal/metrics"
	"github.com/samuelbutton/yamata/internal/simulator"
)

// RunFailure classifies execution errors without disguising storage failures.
func RunFailure(job contract.RunJob, err error) string {
	switch {
	case errors.Is(err, simulator.ErrTickLimit), errors.Is(err, context.DeadlineExceeded):
		return "simulation_timeout"
	case errors.Is(err, contract.ErrVersion) && job.Inputs.Controller.Version != 1:
		return "controller_failure"
	case errors.Is(err, contract.ErrVersion), errors.Is(err, simulator.ErrInvalidConfig):
		return "worker_failure"
	default:
		return ""
	}
}

// Analysis attaches the identity of a saved bag and the complete analysis template.
func Analysis(result contract.Result, template json.RawMessage, ref contract.Reference) (contract.Result, error) {
	hash, err := contract.ContentHash(template)
	if err != nil {
		return result, err
	}
	id := contract.AnalysisID(result.ExecutionID, ref.SHA256, hash)
	result.Bag, result.AnalysisID, result.AnalysisHash = &ref, &id, &hash
	result.AnalysisTemplate = template
	return result, nil
}

// Score evaluates recorded motion and retains explicit analysis failures.
func Score(ctx context.Context, result contract.Result, recording contract.Bag, inputs contract.RunInputs) (contract.Result, error) {
	scores, err := metrics.Evaluate(ctx, recording, inputs)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		failure := "analysis_failure"
		result.Status, result.FailureClass = "ERROR", &failure
	} else {
		result.Status, result.Metrics = scores.Status, scores.Metrics
	}
	return result, nil
}
