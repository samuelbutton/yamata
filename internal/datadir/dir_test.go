package datadir_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/samuelbutton/yamata/internal/datadir"
)

func TestPreparePreservesExistingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	resolved, err := datadir.Prepare(path)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != path {
		t.Fatalf("path = %q, want %q", resolved, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got&0o077 != 0 {
		t.Fatalf("new directory permissions = %o, want no group or other access", got)
	}
	file := filepath.Join(path, "record")
	if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := datadir.Prepare(path); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "keep" {
		t.Fatalf("existing content changed: %q", content)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "record" {
		t.Fatalf("unexpected files after checks: %v", entries)
	}
}

func TestPrepareRejectsUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks require an unprivileged user")
	}
	path := t.TempDir()
	if err := os.Chmod(path, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o700); err != nil {
			t.Error(err)
		}
	})
	resolved, err := datadir.Prepare(path)
	if !errors.Is(err, os.ErrPermission) || resolved != "" {
		t.Fatalf("Prepare = (%q, %v), want empty path and permission error", resolved, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o500 {
		t.Fatalf("existing permissions changed to %o", got)
	}
}

func TestPrepareRejectsInvalidPaths(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "bad\x00path", file, filepath.Join(file, "child")} {
		t.Run(path, func(t *testing.T) {
			resolved, err := datadir.Prepare(path)
			if err == nil || resolved != "" {
				t.Fatalf("Prepare = (%q, %v), want failure", resolved, err)
			}
		})
	}
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "keep" {
		t.Fatalf("blocking file changed: %q", content)
	}
}
