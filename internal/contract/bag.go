package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// MaxBagBytes bounds both recording and inspection memory use.
const MaxBagBytes = maxFileBytes

// Obstacle is the version-one wire representation, independent of simulator types.
type Obstacle struct {
	PositionMM int64 `json:"position_mm"`
	SpeedMMS   int64 `json:"speed_mm_s"`
	LengthMM   int64 `json:"length_mm"`
}

// BagHeader precedes exactly RecordCount tick records.
type BagHeader struct {
	ContractVersion int    `json:"contract_version"`
	Kind            string `json:"kind"`
	ExecutionID     string `json:"execution_id"`
	FormatVersion   int    `json:"format_version"`
	InputsHash      string `json:"inputs_hash"`
	TickMS          int    `json:"tick_ms"`
	RecordCount     int    `json:"record_count"`
}

// Tick is one recorded boundary of simulation time.
type Tick struct {
	Kind             string     `json:"kind"`
	Tick             int        `json:"tick"`
	TimeMS           int64      `json:"time_ms"`
	PositionMM       int64      `json:"position_mm"`
	SpeedMMS         int64      `json:"speed_mm_s"`
	AccelerationMMS2 int64      `json:"acceleration_mm_s2"`
	Obstacles        []Obstacle `json:"obstacles"`
}

// Bag owns a complete, schema-checked recording and the hash of its exact bytes.
// Structural validity does not prove motion correctness or authenticate its source.
type Bag struct {
	Header  BagHeader
	Records []Tick
	SHA256  string
}

// ParseBag checks every line, the declared count, tick ordering, and simulated time.
// No partially parsed bag is returned on failure.
func (v *Validator) ParseBag(ctx context.Context, data []byte) (Bag, error) {
	if err := ctx.Err(); err != nil {
		return Bag{}, err
	}
	if len(data) > MaxBagBytes || !bytes.HasSuffix(data, []byte("\n")) {
		return Bag{}, fmt.Errorf("%w: bag exceeds 16 MiB or lacks final newline", ErrInvalid)
	}
	lines := bytes.SplitN(data[:len(data)-1], []byte("\n"), 100003)
	d, err := v.parse(lines[0])
	if err != nil {
		return Bag{}, err
	}
	if d.Kind != "bag" || len(lines)-1 != d.RecordCount {
		return Bag{}, fmt.Errorf("%w: bag record count", ErrInvalid)
	}
	var bag Bag
	if err := json.Unmarshal(lines[0], &bag.Header); err != nil {
		return Bag{}, err
	}
	bag.Records = make([]Tick, 0, d.RecordCount)
	for i, line := range lines[1:] {
		if err := ctx.Err(); err != nil {
			return Bag{}, err
		}
		record, err := v.parse(line)
		if err != nil {
			return Bag{}, fmt.Errorf("tick %d: %w", i, err)
		}
		var tick Tick
		if err := json.Unmarshal(line, &tick); err != nil {
			return Bag{}, err
		}
		if record.Kind != "tick" || tick.Tick != i || tick.TimeMS != int64(i*d.TickMS) {
			return Bag{}, fmt.Errorf("%w: unordered bag ticks", ErrInvalid)
		}
		bag.Records = append(bag.Records, tick)
	}
	bag.SHA256 = hashBytes(data)
	return bag, nil
}

// ReadBag reads a confined regular file and verifies a caller-pinned content hash.
// The hash must come from a trusted publication or reference, not the file being checked.
func (v *Validator) ReadBag(ctx context.Context, root *os.Root, path, expectedHash string) (Bag, error) {
	if len(expectedHash) != 64 || strings.Trim(expectedHash, "0123456789abcdef") != "" {
		return Bag{}, fmt.Errorf("%w: supply a lowercase SHA-256 hash", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return Bag{}, err
	}
	if !strings.HasSuffix(path, ".jsonl") {
		return Bag{}, ErrPath
	}
	s := inspection{root: root}
	data, err := s.read(path)
	if err != nil {
		return Bag{}, err
	}
	if hashBytes(data) != expectedHash {
		return Bag{}, ErrHash
	}
	return v.ParseBag(ctx, data)
}
