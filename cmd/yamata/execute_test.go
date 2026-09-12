package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samuelbutton/yamata/internal/contract"
)

func TestRunCommandStatusAndRetry(t *testing.T) {
	dir := recordedExchange(t)
	args := []string{"run", "--exchange-dir", dir, "jobs/candidate.json"}
	var output bytes.Buffer
	if err := run(args, &output); err == nil || !strings.Contains(output.String(), "status=FAIL ") {
		t.Fatalf("collision must return failure with outcome: %v %q", err, output.String())
	}
	first := output.String()
	output.Reset()
	if err := run(args, &output); err == nil || output.String() != first {
		t.Fatalf("retry changed summary: %v %q", err, output.String())
	}
	if err := run(args, failedWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}

func TestRunCommandPass(t *testing.T) {
	dir := recordedExchange(t)
	path := filepath.Join(dir, "jobs/baseline.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var job map[string]any
	if err := json.Unmarshal(data, &job); err != nil {
		t.Fatal(err)
	}
	job["inputs"].(map[string]any)["analysis_template"].(map[string]any)["goal_progress"].(map[string]any)["minimum_ppm"] = 250000
	inputs, err := json.Marshal(job["inputs"])
	if err != nil {
		t.Fatal(err)
	}
	hash, err := contract.ContentHash(inputs)
	if err != nil {
		t.Fatal(err)
	}
	job["inputs_hash"] = hash
	data, err = json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"run", "--exchange-dir", dir, "jobs/baseline.json"}, &output); err != nil || !strings.Contains(output.String(), "status=PASS ") {
		t.Fatalf("pass = %v %q", err, output.String())
	}
}

func TestRunHelpAndInvalidArguments(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var output bytes.Buffer
	if err := run([]string{"run", "--help"}, &output); err != nil || !strings.Contains(output.String(), "Usage:") {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"run"}, {"run", "--unknown"}, {"run", "--exchange-dir"}, {"run", "--exchange-dir", dir}, {"run", "--exchange-dir", dir, "a", "b"}} {
		output.Reset()
		if err := run(args, &output); err == nil || output.Len() != 0 {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("help/arguments wrote files: %v %v", entries, err)
	}
}
