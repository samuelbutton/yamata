package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQueueCommands(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(filepath.Join(dir, "jobs"), os.DirFS("../../examples/jobs")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	args := []string{"enqueue", "--exchange-dir", dir, "jobs/candidate.json"}
	if err := run(args, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "duplicate=false") {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := run(args, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "duplicate=true") {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := run([]string{"workers", "--exchange-dir", dir, "--drain"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "queues drained") {
		t.Fatal(out.String())
	}
	results, err := filepath.Glob(filepath.Join(dir, "results", "*.json"))
	if err != nil || len(results) != 1 {
		t.Fatal(results, err)
	}
	if err := run([]string{"run", "--exchange-dir", dir, "jobs/candidate.json"}, io.Discard); err == nil {
		t.Fatal("mixed execution ownership accepted")
	}
}

func TestQueueHelpAndInvalidOptionsDoNotWrite(t *testing.T) {
	for _, command := range []string{"enqueue", "workers"} {
		dir := t.TempDir()
		if err := run([]string{command, "--exchange-dir", dir, "--help"}, io.Discard); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("help wrote files")
		}
	}
	for _, args := range [][]string{{"workers", "--simulation-workers", "0", "--analysis-workers", "0"}, {"workers", "--analysis-workers", "65"}, {"workers", "--lease-duration", "1ms"}, {"enqueue"}} {
		dir := t.TempDir()
		args = append(args, "--exchange-dir", dir)
		if err := run(args, io.Discard); err == nil {
			t.Fatal(args)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("invalid options wrote files")
		}
	}
}

type queueFailedWriter struct{}

func (queueFailedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestLostReceiptOutputCanBeRetried(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(filepath.Join(dir, "jobs"), os.DirFS("../../examples/jobs")); err != nil {
		t.Fatal(err)
	}
	args := []string{"enqueue", "--exchange-dir", dir, "jobs/candidate.json"}
	if err := run(args, queueFailedWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(args, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "duplicate=true") {
		t.Fatal(out.String())
	}
}

func TestOperationalCommands(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"queue", "--help"}, {"fault", "--help"}, {"queue", "--limit", "0"}, {"fault", "--job", "x", "--stage", "analysis", "--failures", "3"}} {
		args = append(args, "--exchange-dir", dir)
		err := run(args, io.Discard)
		if args[1] == "--help" && err != nil {
			t.Fatal(err)
		}
		if args[1] != "--help" && err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("operations initialized empty exchange", err)
	}
	if err := os.CopyFS(filepath.Join(dir, "jobs"), os.DirFS("../../examples/jobs")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"enqueue", "--exchange-dir", dir, "jobs/candidate.json"},
		{"fault", "--exchange-dir", dir, "--job", "record-candidate", "--stage", "analysis", "--failures", "1"},
		{"workers", "--exchange-dir", dir, "--drain"},
	} {
		if err := run(args, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := run([]string{"queue", "--exchange-dir", dir}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"analysis_retries": 1`) || !strings.Contains(out.String(), `"stage": "done"`) {
		t.Fatal(out.String())
	}
	if err := run([]string{"queue", "--exchange-dir", dir}, queueFailedWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}
