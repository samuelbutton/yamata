package bag

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samuelbutton/yamata/internal/contract"
)

func TestStagingFailuresLeaveNoFinalFile(t *testing.T) {
	for _, mode := range []string{"write error", "silent truncation", "changed bytes", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			_, v, root := setup(t)
			data := read(t, "../../contract/v1/examples/valid/bags/exec-1.jsonl")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := publish(ctx, root, "test.jsonl", digest(data), v, func(w io.Writer) error {
				if mode == "changed bytes" {
					_, err := w.Write(append([]byte(" "), data...))
					return err
				}
				if _, err := w.Write(data[:len(data)/2]); err != nil {
					return err
				}
				if mode == "write error" {
					return io.ErrUnexpectedEOF
				}
				if mode == "canceled" {
					cancel()
				}
				return nil
			})
			if err == nil {
				t.Fatal("failed staging was accepted")
			}
			if mode == "write error" && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(err)
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(root.Name())
			if err != nil || len(entries) != 1 || entries[0].Name() != "jobs" {
				t.Fatalf("staging left files: %v, %v", entries, err)
			}
		})
	}
}

func TestPartialBagIsInvisible(t *testing.T) {
	_, v, root := setup(t)
	data := read(t, "../../contract/v1/examples/valid/bags/exec-1.jsonl")
	ready, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- publish(context.Background(), root, "test.jsonl", digest(data), v, func(w io.Writer) error {
			_, err := w.Write(data[:len(data)/2])
			close(ready)
			<-release
			if err != nil {
				return err
			}
			_, err = w.Write(data[len(data)/2:])
			return err
		})
	}()
	<-ready
	_, statErr := root.Stat("test.jsonl")
	entries, readErr := os.ReadDir(root.Name())
	var temporary string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			temporary = entry.Name()
		}
	}
	_, inspectionErr := v.ReadBag(context.Background(), root, temporary, digest(data))
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(statErr, os.ErrNotExist) || readErr != nil || temporary == "" || !errors.Is(inspectionErr, contract.ErrPath) {
		t.Fatalf("partial file became readable: %v %v %v", statErr, readErr, inspectionErr)
	}
	if _, err := v.ReadBag(context.Background(), root, "test.jsonl", digest(data)); err != nil {
		t.Fatal(err)
	}
}

func TestStagingProcessExit(t *testing.T) {
	if dir := os.Getenv("YAMATA_TEST_STAGING_DIR"); dir != "" {
		root, err := os.OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		v, err := contract.New()
		if err != nil {
			t.Fatal(err)
		}
		_ = publish(context.Background(), root, "stopped-baseline.jsonl", strings.Repeat("0", 64), v, func(w io.Writer) error {
			if _, err := io.WriteString(w, `{"kind":"bag"`); err != nil {
				t.Fatal(err)
			}
			os.Exit(23) // Simulate process loss before deferred cleanup or publication.
			return nil
		})
		t.Fatal("staging child did not exit")
	}
	dir, _, _ := setup(t)
	bags := filepath.Join(dir, "bags")
	if err := os.Mkdir(bags, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestStagingProcessExit$")
	cmd.Env = append(os.Environ(), "YAMATA_TEST_STAGING_DIR="+bags)
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("child exit: %v", err)
	}
	entries, err := os.ReadDir(bags)
	if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".tmp") {
		t.Fatalf("crashed publication: %v %v", entries, err)
	}
	if _, err := Record(context.Background(), dir, "jobs/baseline.json"); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(bags)
	if err != nil || len(entries) != 2 {
		t.Fatalf("retry did not preserve unrelated temporary file: %v %v", entries, err)
	}
}

func TestPublicationPathAndPermissionBoundaries(t *testing.T) {
	for _, mode := range []string{"escaping bags directory", "escaping job", "existing symlink", "existing directory", "unwritable"} {
		t.Run(mode, func(t *testing.T) {
			dir, _, _ := setup(t)
			outside := t.TempDir()
			marker := filepath.Join(outside, "marker")
			if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
				t.Fatal(err)
			}
			bags := filepath.Join(dir, "bags")
			if mode == "escaping bags directory" {
				if err := os.Symlink(outside, bags); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(bags, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "escaping job":
				if err := os.Remove(filepath.Join(dir, "jobs/baseline.json")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(marker, filepath.Join(dir, "jobs/baseline.json")); err != nil {
					t.Fatal(err)
				}
			case "existing symlink":
				if err := os.Symlink(marker, filepath.Join(bags, "stopped-baseline.jsonl")); err != nil {
					t.Fatal(err)
				}
			case "existing directory":
				if err := os.Mkdir(filepath.Join(bags, "stopped-baseline.jsonl"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "unwritable":
				if os.Geteuid() == 0 {
					t.Skip("requires an ordinary user")
				}
				if err := os.Chmod(bags, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(bags, 0o700); err != nil {
						t.Error(err)
					}
				})
			}
			if _, err := Record(context.Background(), dir, "jobs/baseline.json"); err == nil {
				t.Fatal("unsafe publication accepted")
			}
			if string(read(t, marker)) != "preserve" {
				t.Fatal("changed outside file")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 1 {
				t.Fatalf("wrote outside exchange: %v %v", entries, err)
			}
		})
	}
}

func TestRecordingSizeBound(t *testing.T) {
	var b boundedBuffer
	if _, err := b.Write(bytes.Repeat([]byte("x"), contract.MaxBagBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("x")); !errors.Is(err, contract.ErrInvalid) {
		t.Fatalf("size limit: %v", err)
	}
	if b.Len() != contract.MaxBagBytes {
		t.Fatal("writer exceeded bound")
	}
}
