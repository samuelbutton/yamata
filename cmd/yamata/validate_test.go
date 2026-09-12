package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/samuelbutton/yamata/internal/contract"
)

func TestValidateCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("../../contract/v1/examples/valid")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	var output bytes.Buffer
	args := []string{"validate", "--exchange-dir", root, "events/completed-1.json", "jobs/analyze-1.json", "results/run-error.json"}
	if err := run(args, &output); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "Contract valid.\n" {
		t.Fatalf("output = %q", got)
	}
	if err := run(args, failedWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("output failure = %v", err)
	}
	if err := run([]string{"validate", "--exchange-dir", root, "../outside.json"}, io.Discard); !errors.Is(err, contract.ErrPath) {
		t.Fatalf("path error = %v", err)
	}
}

func TestValidateOptionsDoNotCreateFiles(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, args := range [][]string{{"validate"}, {"validate", "--exchange-dir", "missing"}, {"validate", "--unknown"}, {"validate", "file.json"}} {
		if err := run(args, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if err := run([]string{"validate", "--help"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unexpected files: %v", entries)
	}
}

func TestValidateReportsConflictWithoutSuccess(t *testing.T) {
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("../../contract/v1/examples/valid")); err != nil {
		t.Fatal(err)
	}
	changed, err := os.ReadFile("../../contract/v1/examples/invalid/conflicting-job.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "jobs/conflicting.json"), changed, 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = run([]string{"validate", "--exchange-dir", root, "jobs/run-1.json", "jobs/conflicting.json"}, &output)
	if !errors.Is(err, contract.ErrConflict) || output.Len() != 0 {
		t.Fatalf("error = %v, output = %q", err, output.String())
	}
}
