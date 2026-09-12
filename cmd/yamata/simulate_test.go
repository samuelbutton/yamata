package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestSimulateCommand(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"simulate"}, "outcome=stopped tick=31 time_ms=3100 position_mm=18000 speed_mm_s=0"},
		{[]string{"simulate", "--controller", "candidate"}, "outcome=collision tick=22 time_ms=2200 position_mm=21600 speed_mm_s=8400"},
		{[]string{"simulate", "--scenario", "empty-lane"}, "outcome=goal_reached"},
		{[]string{"simulate", "--scenario", "moving-obstacle"}, "outcome=stopped tick=32"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var output bytes.Buffer
			if err := run(tc.args, &output); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), tc.want) {
				t.Fatalf("output = %q, want %q", output.String(), tc.want)
			}
		})
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("simulation wrote files: %v", entries)
	}
}

func TestSimulateInvalidInputAndHelp(t *testing.T) {
	for _, args := range [][]string{
		{"simulate", "--scenario", "unknown"}, {"simulate", "--controller", "unknown"},
		{"simulate", "--unknown"}, {"simulate", "--scenario"}, {"simulate", "file.json"},
	} {
		var output bytes.Buffer
		if err := run(args, &output); err == nil || output.Len() != 0 {
			t.Fatalf("args=%v: error=%v output=%q", args, err, output.String())
		}
	}
	var help bytes.Buffer
	if err := run([]string{"simulate", "--help"}, &help); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help.String(), "Usage:") {
		t.Fatalf("help = %q", help.String())
	}
	if err := run([]string{"simulate"}, failedWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("output failure = %v", err)
	}
}
