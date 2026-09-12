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

func recordedExchange(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(filepath.Join(dir, "jobs"), os.DirFS("../../examples/jobs")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRecordAndInspectCommands(t *testing.T) {
	dir := recordedExchange(t)
	for _, controller := range []string{"baseline", "candidate"} {
		var output bytes.Buffer
		args := []string{"record", "--exchange-dir", dir, "jobs/" + controller + ".json"}
		if err := run(args, &output); err != nil {
			t.Fatal(err)
		}
		hash := strings.TrimSpace(output.String())
		if len(hash) != 64 {
			t.Fatalf("hash = %q", hash)
		}
		output.Reset()
		if err := run(args, &output); err != nil || strings.TrimSpace(output.String()) != hash {
			t.Fatalf("duplicate = %q, %v", output.String(), err)
		}
		output.Reset()
		args = []string{"inspect", "--exchange-dir", dir, "--sha256", hash, "bags/stopped-" + controller + ".jsonl"}
		if err := run(args, &output); err != nil {
			t.Fatal(err)
		}
		want := "final_tick=31 final_time_ms=3100 final_position_mm=18000 final_speed_mm_s=0"
		if controller == "candidate" {
			want = "final_tick=22 final_time_ms=2200 final_position_mm=21600 final_speed_mm_s=8400"
		}
		if !strings.Contains(output.String(), want) || !strings.Contains(output.String(), "sha256="+hash) {
			t.Fatalf("summary = %q", output.String())
		}
		if err := run(args, failedWriter{}); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("inspect output failure = %v", err)
		}
		args[4] = strings.Repeat("0", 64)
		output.Reset()
		if err := run(args, &output); err == nil || output.Len() != 0 {
			t.Fatalf("incorrect hash produced success: %v, %q", err, output.String())
		}
	}
}

func TestRecordingCLIInvalidArgumentsAndHelp(t *testing.T) {
	for _, command := range []string{"record", "inspect"} {
		var output bytes.Buffer
		if err := run([]string{command, "--help"}, &output); err != nil || !strings.Contains(output.String(), "Usage:") {
			t.Fatalf("help: %v %q", err, output.String())
		}
		for _, args := range [][]string{{command}, {command, "--unknown"}, {command, "--exchange-dir"}, {command, "--exchange-dir", t.TempDir()}, {command, "--exchange-dir", t.TempDir(), "a", "b"}} {
			output.Reset()
			if err := run(args, &output); err == nil || output.Len() != 0 {
				t.Fatalf("invalid arguments: %v %v", args, err)
			}
		}
	}
}

func TestLostPublicationOutputCanBeRetried(t *testing.T) {
	dir := recordedExchange(t)
	args := []string{"record", "--exchange-dir", dir, "jobs/baseline.json"}
	if err := run(args, failedWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "bags/stopped-baseline.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run(args, &output); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "bags/stopped-baseline.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || output.Len() != 65 {
		t.Fatal("retry changed saved output")
	}
}
