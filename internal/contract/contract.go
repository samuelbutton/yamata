// Package contract validates the versioned exchange files without worker state.
package contract

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:generate go run generate.go

//go:embed schema.json
var schemaJSON []byte

// Contract error categories remain available through errors.Is.
var (
	ErrInvalid  = errors.New("invalid contract")
	ErrVersion  = errors.New("unsupported version")
	ErrHash     = errors.New("hash mismatch")
	ErrConflict = errors.New("identity conflict")
	ErrPath     = errors.New("invalid exchange path")
)

// Validator holds immutable compiled schemas. Each Check owns its validation state.
type Validator struct{ schema *jsonschema.Schema }

// New compiles the bundled schema without loading external resources.
func New() (*Validator, error) {
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.UseLoader(noLoader{})
	const url = "https://yamata.invalid/contract/v1/exchange.schema.json"
	if err := c.AddResource(url, value); err != nil {
		return nil, err
	}
	schema, err := c.Compile(url)
	if err != nil {
		return nil, err
	}
	return &Validator{schema: schema}, nil
}

type noLoader struct{}

func (noLoader) Load(string) (any, error) {
	return nil, errors.New("external schema loading is disabled")
}

type reference struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type metric struct {
	Value         *int64 `json:"value"`
	Version       int    `json:"version"`
	Unit          string `json:"unit"`
	Pass          *bool  `json:"pass"`
	EvidenceTicks []int  `json:"evidence_ticks"`
}
type document struct {
	Kind             string            `json:"kind"`
	Version          int               `json:"contract_version"`
	ExecutionID      string            `json:"execution_id"`
	JobID            string            `json:"job_id"`
	JobKind          string            `json:"job_kind"`
	AttemptID        string            `json:"attempt_id"`
	CorrelationID    string            `json:"correlation_id"`
	EventID          string            `json:"event_id"`
	Sequence         int64             `json:"sequence"`
	Inputs           json.RawMessage   `json:"inputs"`
	InputsHash       string            `json:"inputs_hash"`
	Job              *reference        `json:"job"`
	Bag              *reference        `json:"bag"`
	Result           *reference        `json:"result"`
	AnalysisID       *string           `json:"analysis_id"`
	AnalysisTemplate json.RawMessage   `json:"analysis_template"`
	AnalysisHash     *string           `json:"analysis_hash"`
	Status           string            `json:"status"`
	State            string            `json:"state"`
	FailureClass     *string           `json:"failure_class"`
	Metrics          map[string]metric `json:"metrics"`
	FormatVersion    int               `json:"format_version"`
	TickMS           int               `json:"tick_ms"`
	RecordCount      int               `json:"record_count"`
}

func (v *Validator) parse(data []byte) (document, error) {
	if len(data) > 1<<20 {
		return document{}, fmt.Errorf("%w: JSON record exceeds 1 MiB", ErrInvalid)
	}
	value, err := decode(data)
	if err != nil {
		return document{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if object, ok := value.(map[string]any); ok && object["kind"] != "tick" {
		if object["contract_version"] != json.Number("1") {
			return document{}, ErrVersion
		}
		if object["kind"] == "bag" && object["format_version"] != json.Number("1") {
			return document{}, ErrVersion
		}
	}
	if err := v.schema.Validate(value); err != nil {
		return document{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var d document
	if err := json.Unmarshal(data, &d); err != nil {
		return d, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return d, nil
}

func (d document) identity() string {
	switch d.Kind {
	case "job":
		return "job/" + d.JobID
	case "event":
		return "event/" + d.EventID
	case "result":
		if d.AnalysisID != nil {
			return "result/" + d.ExecutionID + "/" + *d.AnalysisID
		}
		return "result/" + d.ExecutionID + "/run"
	default:
		return "bag/" + d.ExecutionID
	}
}

func analysisID(execution, bag, analysis string) string {
	return hashBytes([]byte(execution + "\n" + bag + "\n" + analysis))
}
func eventID(d document) string {
	return hashBytes([]byte(d.JobID + "\n" + d.AttemptID + "\n" + strconv.FormatInt(d.Sequence, 10)))
}
