package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpDoesNotCreateData(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}, {"init", "--help"}, {"init", "-h"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output bytes.Buffer
			if err := run(args, &output); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "Usage:") {
				t.Fatalf("missing usage in %q", output.String())
			}
		})
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("help created files: %v", entries)
	}
}

func TestInitUsesSelectedDirectory(t *testing.T) {
	for _, path := range []string{".yamata", "custom data/nested"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			args := []string{"init"}
			if path != ".yamata" {
				args = append(args, "--data-dir", path)
			}
			var output bytes.Buffer
			if err := run(args, &output); err != nil {
				t.Fatal(err)
			}
			// Getwd resolves system aliases in temporary directory paths.
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("Data directory ready: %q\n", filepath.Join(cwd, path))
			if got := output.String(); got != want {
				t.Fatalf("output = %q, want %q", got, want)
			}
			info, err := os.Stat(filepath.Join(root, path))
			if err != nil || !info.IsDir() {
				t.Fatalf("data directory missing: %v", err)
			}
			if path != ".yamata" {
				if _, err := os.Stat(filepath.Join(root, ".yamata")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unexpected default directory: %v", err)
				}
			}
		})
	}
}

func TestInvalidArgumentsHaveNoSideEffects(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, args := range [][]string{
		{"unknown"}, {"help", "extra"}, {"init", "extra"},
		{"init", "--unknown"}, {"init", "--data-dir"},
		{"init", "--data-dir="}, {"init", "--data-dir", "new", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output bytes.Buffer
			if err := run(args, &output); err == nil {
				t.Fatal("invalid arguments succeeded")
			}
			if output.Len() != 0 {
				t.Fatalf("unexpected success output: %q", output.String())
			}
		})
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("invalid arguments created files: %v", entries)
	}
}

func TestInitReportsStorageFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"init", "--data-dir", path}, &output); err == nil {
		t.Fatal("init accepted a file as a directory")
	}
	if output.Len() != 0 {
		t.Fatalf("unexpected success output: %q", output.String())
	}
}

func TestOutputFailureIsReturned(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"init", "--data-dir", t.TempDir()}} {
		if err := run(args, failedWriter{}); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("error = %v, want closed pipe", err)
		}
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
