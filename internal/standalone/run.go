// Package standalone executes one run job through durable bag, result, and event files.
package standalone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/samuelbutton/yamata/internal/bag"
	"github.com/samuelbutton/yamata/internal/contract"
	"github.com/samuelbutton/yamata/internal/metrics"
	"github.com/samuelbutton/yamata/internal/publication"
	"github.com/samuelbutton/yamata/internal/simulator"
)

// Outcome points to an authoritative result and its completion event.
type Outcome struct {
	Status string
	Result contract.Reference
	Event  contract.Reference
}

// Run records a standalone job, scores its saved bag, and publishes its outcome.
// Score failures and execution failures have durable outcomes; storage errors may not.
// Repeating the same job preserves accepted files and repairs a missing completion event.
func Run(ctx context.Context, directory, jobPath string) (out Outcome, err error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	v, err := contract.New()
	if err != nil {
		return out, err
	}
	job, err := v.ReadRunJob(ctx, root, jobPath)
	if err != nil {
		return out, err
	}
	guard, err := lock(ctx, root, job.ExecutionID)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, guard.Close()) }()
	jobRef := contract.Reference{Path: jobPath, SHA256: job.SHA256}
	failurePath := "results/" + job.ExecutionID + "-run.json"
	old, err := v.ReadDocument(ctx, root, failurePath, "result")
	if err == nil {
		var result contract.Result
		if err := json.Unmarshal(old, &result); err != nil {
			return out, err
		}
		if result.Job != jobRef || result.AnalysisID != nil || result.Status != "ERROR" {
			return out, contract.ErrConflict
		}
		return finish(ctx, root, v, failurePath, result, old)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return out, err
	}
	started := time.Now()
	result := contract.Result{ContractVersion: 1, Kind: "result", ExecutionID: job.ExecutionID, JobID: job.JobID, AttemptID: "standalone-" + strconv.FormatInt(started.UnixMilli(), 10), CorrelationID: job.CorrelationID, Job: jobRef, InputsHash: job.InputsHash}
	path := failurePath
	pub, runErr := bag.Record(ctx, directory, jobPath)
	if runErr != nil {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		failure := ""
		switch {
		case errors.Is(runErr, simulator.ErrTickLimit), errors.Is(runErr, context.DeadlineExceeded):
			failure = "simulation_timeout"
		case errors.Is(runErr, contract.ErrVersion) && job.Inputs.Controller.Version != 1:
			failure = "controller_failure"
		case errors.Is(runErr, contract.ErrVersion), errors.Is(runErr, simulator.ErrInvalidConfig):
			failure = "worker_failure"
		default:
			return out, runErr
		}
		result.Status = "ERROR"
		result.FailureClass = &failure
	} else {
		recording, err := v.ReadBag(ctx, root, pub.Path, pub.SHA256)
		if err != nil {
			return out, err
		}
		if recording.Header.ExecutionID != job.ExecutionID || recording.Header.InputsHash != job.InputsHash {
			return out, contract.ErrConflict
		}
		hash, err := contract.ContentHash(job.Inputs.AnalysisTemplate)
		if err != nil {
			return out, err
		}
		id := contract.AnalysisID(job.ExecutionID, pub.SHA256, hash)
		result.Bag = &contract.Reference{Path: pub.Path, SHA256: pub.SHA256}
		result.AnalysisID = &id
		result.AnalysisHash = &hash
		result.AnalysisTemplate = job.Inputs.AnalysisTemplate
		path = "results/" + id + ".json"
		scores, err := metrics.Evaluate(ctx, recording, job.Inputs)
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			failure := "analysis_failure"
			result.Status = "ERROR"
			result.FailureClass = &failure
		} else {
			result.Status = scores.Status
			result.Metrics = scores.Metrics
		}
	}
	result.Timing.DurationMS = time.Since(started).Milliseconds()
	// Existing timing belongs to the accepted outcome, not this repeated invocation.
	old, err = v.ReadDocument(ctx, root, path, "result")
	if err == nil {
		var existing contract.Result
		if err := json.Unmarshal(old, &existing); err != nil {
			return out, err
		}
		result.Timing = existing.Timing
		result.AttemptID = existing.AttemptID
	} else if !errors.Is(err, os.ErrNotExist) {
		return out, err
	}
	data, err := encode(result)
	if err != nil {
		return out, err
	}
	if old != nil && !bytes.Equal(data, old) {
		return out, contract.ErrConflict
	}
	return finish(ctx, root, v, path, result, data)
}

func finish(ctx context.Context, root *os.Root, v *contract.Validator, path string, result contract.Result, data []byte) (Outcome, error) {
	started, err := strconv.ParseInt(strings.TrimPrefix(result.AttemptID, "standalone-"), 10, 64)
	if err != nil || !strings.HasPrefix(result.AttemptID, "standalone-") || started < 0 || started > 9007199254740991-result.Timing.DurationMS {
		return Outcome{}, contract.ErrConflict
	}
	if err := publishJSON(ctx, root, v, path, "result", data); err != nil {
		return Outcome{}, err
	}
	ref := contract.Reference{Path: path, SHA256: contract.Hash(data)}
	event := contract.Event{ContractVersion: 1, Kind: "event", ExecutionID: result.ExecutionID, JobID: result.JobID, AttemptID: result.AttemptID, CorrelationID: result.CorrelationID, EventID: contract.EventID(result.JobID, result.AttemptID, 1), Sequence: 1, State: result.Status, AnalysisID: result.AnalysisID, Result: ref, EventType: "execution_transition", Producer: "yamata", CreatedAtMS: started + result.Timing.DurationMS}
	eventPath := "events/" + event.EventID + ".json"
	old, err := v.ReadDocument(ctx, root, eventPath, "event")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Outcome{}, err
	}
	data, err = encode(event)
	if err != nil {
		return Outcome{}, err
	}
	if old != nil && !bytes.Equal(data, old) {
		return Outcome{}, contract.ErrConflict
	}
	if err := publishJSON(ctx, root, v, eventPath, "event", data); err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: result.Status, Result: ref, Event: contract.Reference{Path: eventPath, SHA256: contract.Hash(data)}}, nil
}

func encode(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func publishJSON(ctx context.Context, root *os.Root, v *contract.Validator, path, kind string, data []byte) (err error) {
	validate := func(data []byte) error { return v.ValidateDocument(ctx, root, data, kind) }
	if err := validate(data); err != nil {
		return err
	}
	directory, name, _ := strings.Cut(path, "/")
	if err := root.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := publication.SyncDirectory(root); err != nil {
		return err
	}
	dir, err := root.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	return publication.File(ctx, dir, name, contract.Hash(data), func(w io.Writer) error { _, err := io.Copy(w, bytes.NewReader(data)); return err }, validate)
}
