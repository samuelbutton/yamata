package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// Result is an immutable scoring outcome or an explicit execution failure.
type Result struct {
	ContractVersion  int             `json:"contract_version"`
	Kind             string          `json:"kind"`
	ExecutionID      string          `json:"execution_id"`
	JobID            string          `json:"job_id"`
	AttemptID        string          `json:"attempt_id"`
	CorrelationID    string          `json:"correlation_id,omitempty"`
	Job              Reference       `json:"job"`
	InputsHash       string          `json:"inputs_hash"`
	Bag              *Reference      `json:"bag"`
	AnalysisID       *string         `json:"analysis_id"`
	AnalysisTemplate json.RawMessage `json:"analysis_template"`
	AnalysisHash     *string         `json:"analysis_hash"`
	Status           string          `json:"status"`
	FailureClass     *string         `json:"failure_class"`
	Timing           struct {
		DurationMS int64 `json:"duration_ms"`
	} `json:"timing"`
	Metrics map[string]Metric `json:"metrics"`
}

// Event records an execution transition. Terminal events reference a published result.
type Event struct {
	ContractVersion int        `json:"contract_version"`
	Kind            string     `json:"kind"`
	ExecutionID     string     `json:"execution_id"`
	JobID           string     `json:"job_id"`
	AttemptID       string     `json:"attempt_id"`
	CorrelationID   string     `json:"correlation_id,omitempty"`
	EventID         string     `json:"event_id"`
	Sequence        int64      `json:"sequence"`
	State           string     `json:"state"`
	AnalysisID      *string    `json:"analysis_id"`
	Result          *Reference `json:"result"`
	EventType       string     `json:"event_type"`
	Producer        string     `json:"producer"`
	CreatedAtMS     int64      `json:"created_at_ms"`
}

// Hash returns the lowercase SHA-256 hash of exact bytes.
func Hash(data []byte) string { return hashBytes(data) }

// EventID implements the published job/attempt/sequence identity rule.
func EventID(job, attempt string, sequence int64) string {
	return hashBytes([]byte(job + "\n" + attempt + "\n" + strconv.FormatInt(sequence, 10)))
}

// ValidateDocument validates staged JSON and its already-published references.
// All referenced files are opened under root; staged bytes need no temporary path.
func (v *Validator) ValidateDocument(ctx context.Context, root *os.Root, data []byte, kind string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d, err := v.parse(data)
	if err != nil {
		return err
	}
	if d.Kind != kind {
		return fmt.Errorf("%w: expected %s document", ErrInvalid, kind)
	}
	s := inspection{validator: v, root: root, checked: make(map[string]checkedFile), active: make(map[string]bool), identities: make(map[string]string)}
	return s.checkDocument(ctx, d)
}

// ReadDocument returns one bounded snapshot after validating its complete reference graph.
func (v *Validator) ReadDocument(ctx context.Context, root *os.Root, path, kind string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := inspection{root: root}
	data, err := s.read(path)
	if err != nil {
		return nil, err
	}
	if err := v.ValidateDocument(ctx, root, data, kind); err != nil {
		return nil, err
	}
	return data, nil
}
