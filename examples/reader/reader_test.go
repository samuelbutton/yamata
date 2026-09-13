package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) (exchange, *index, string, string) {
	t.Helper()
	dir := t.TempDir()
	state := t.TempDir()
	must(t, os.Chmod(state, 0o700))
	must(t, os.CopyFS(dir, os.DirFS("contract/v1/examples/valid")))
	source, err := filepath.EvalSymlinks(dir)
	must(t, err)
	root, err := os.OpenRoot(source)
	must(t, err)
	t.Cleanup(func() { root.Close() })
	schema, err := compileSchema()
	must(t, err)
	idx, err := openIndex(t.Context(), state, source)
	must(t, err)
	t.Cleanup(func() { idx.close() })
	return exchange{root: root, schema: schema}, idx, dir, state
}
func TestReaderRestartDuplicatesAndOutOfOrderDelivery(t *testing.T) {
	e, idx, dir, state := fixture(t)
	must(t, idx.consume(t.Context(), e, "events/completed-1.json"))
	snapshot, err := idx.snapshot(t.Context())
	must(t, err)
	if snapshot.Events != 1 || len(snapshot.Results) != 1 {
		t.Fatal(snapshot)
	}
	source, err := filepath.EvalSymlinks(dir)
	must(t, err)
	next, err := openIndex(t.Context(), state, source)
	must(t, err)
	defer next.close()
	data, err := os.ReadFile(filepath.Join(dir, "events/completed-1.json"))
	must(t, err)
	must(t, os.WriteFile(filepath.Join(dir, "events/duplicate.json"), data, 0o600))
	must(t, next.sync(t.Context(), e, []string{"events/completed-1.json", "events/duplicate.json", "events/pending-1.json"}))
	got, err := next.snapshot(t.Context())
	must(t, err)
	if got.Events != 2 || !reflect.DeepEqual(got.Results, snapshot.Results) {
		t.Fatal(got)
	}
}
func TestReaderRebuildWithoutEventsAndRollbackOnBadResult(t *testing.T) {
	e, idx, dir, _ := fixture(t)
	must(t, os.Rename(filepath.Join(dir, "events"), filepath.Join(dir, "unavailable-events")))
	must(t, idx.rebuild(t.Context(), e))
	before, err := idx.snapshot(t.Context())
	must(t, err)
	if before.Events != 0 || len(before.Results) != 2 {
		t.Fatal(before)
	}
	must(t, os.WriteFile(filepath.Join(dir, "results/invalid.json"), []byte("{"), 0o600))
	if err := idx.rebuild(t.Context(), e); err == nil {
		t.Fatal("bad result accepted")
	}
	after, err := idx.snapshot(t.Context())
	must(t, err)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("partial rebuild committed")
	}
}
func TestProgressAndResultRollbackTogether(t *testing.T) {
	e, idx, _, _ := fixture(t)
	_, err := idx.db.Exec(`CREATE TRIGGER reject_progress BEFORE INSERT ON seen BEGIN SELECT RAISE(ABORT,'injected write failure'); END;`)
	must(t, err)
	if err := idx.consume(t.Context(), e, "events/completed-1.json"); err == nil {
		t.Fatal("failed progress accepted")
	}
	snapshot, err := idx.snapshot(t.Context())
	must(t, err)
	if snapshot.Events != 0 || len(snapshot.Results) != 0 {
		t.Fatal("result or receipt survived rollback")
	}
	_, err = idx.db.Exec("DROP TRIGGER reject_progress")
	must(t, err)
	must(t, idx.consume(t.Context(), e, "events/completed-1.json"))
}
func TestChangedEventAndPoisonMessageDoNotHideOtherEvents(t *testing.T) {
	e, idx, dir, _ := fixture(t)
	must(t, idx.consume(t.Context(), e, "events/completed-1.json"))
	path := filepath.Join(dir, "events/completed-1.json")
	data, err := os.ReadFile(path)
	must(t, err)
	must(t, os.WriteFile(path, append(data, ' '), 0o600))
	if err := idx.sync(t.Context(), e, nil); err == nil {
		t.Fatal("changed ID accepted")
	}
	snapshot, err := idx.snapshot(t.Context())
	must(t, err)
	if snapshot.Events != 2 || len(snapshot.Results) != 1 {
		t.Fatal("poison blocked independent progress")
	}
}
func TestConcurrentReadersCommitOneIndexEntry(t *testing.T) {
	e, idx, dir, state := fixture(t)
	source, err := filepath.EvalSymlinks(dir)
	must(t, err)
	second, err := openIndex(t.Context(), state, source)
	must(t, err)
	defer second.close()
	var group sync.WaitGroup
	failures := make(chan error, 2)
	for _, i := range []*index{idx, second} {
		group.Go(func() { failures <- i.sync(t.Context(), e, nil) })
	}
	group.Wait()
	close(failures)
	for err := range failures {
		must(t, err)
	}
	snapshot, err := idx.snapshot(t.Context())
	must(t, err)
	if snapshot.Events != 2 || len(snapshot.Results) != 1 {
		t.Fatal(snapshot)
	}
}
func TestPinnedSchemaAndCompatibilityFiles(t *testing.T) {
	manifest, err := os.ReadFile("contract/v1/SHA256SUMS")
	must(t, err)
	for _, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			t.Fatal(line)
		}
		data, err := os.ReadFile(filepath.Join("contract/v1", parts[1]))
		must(t, err)
		if digest(data) != parts[0] {
			t.Fatal("pinned contract drift", parts[1])
		}
	}
	e, _, _, _ := fixture(t)
	cases, err := os.ReadFile("contract/v1/examples/invalid-cases.json")
	must(t, err)
	var invalid []struct {
		File string `json:"file"`
	}
	must(t, json.Unmarshal(cases, &invalid))
	for _, tc := range invalid {
		t.Run(tc.File, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("contract/v1/examples/invalid", tc.File))
			must(t, err)
			g := &graph{exchange: e, files: map[string]checked{}, active: map[string]bool{}, identities: map[string]string{}}
			if strings.HasSuffix(tc.File, ".jsonl") {
				_, err = g.bag(t.Context(), data)
			} else {
				var d object
				d, err = decode(data)
				if err == nil {
					d, err = g.parse(data, text(d, "kind"))
				}
				if err == nil {
					err = g.relations(t.Context(), d)
				}
			}
			if err == nil {
				t.Fatal("invalid compatibility case accepted")
			}
		})
	}
}
func TestReaderRejectsUnsafePathsAndStatePlacement(t *testing.T) {
	e, _, dir, state := fixture(t)
	for _, path := range []string{"../outside.json", "/tmp/outside.json", "events/partial.json.tmp"} {
		if _, _, err := e.read(t.Context(), path, "event"); err == nil {
			t.Fatal(path)
		}
	}
	must(t, os.WriteFile(filepath.Join(state, "outside.json"), []byte("private"), 0o600))
	must(t, os.Symlink(filepath.Join(state, "outside.json"), filepath.Join(dir, "events/link.json")))
	if _, _, err := e.read(t.Context(), "events/link.json", "event"); err == nil {
		t.Fatal("outside symlink followed")
	}
	must(t, syscall.Mkfifo(filepath.Join(dir, "events/pipe.json"), 0o600))
	if _, _, err := e.read(t.Context(), "events/pipe.json", "event"); err == nil {
		t.Fatal("pipe accepted")
	}
	source, err := filepath.EvalSymlinks(dir)
	must(t, err)
	if idx, err := openIndex(t.Context(), dir, source); err == nil {
		idx.close()
		t.Fatal("state inside exchange accepted")
	}
	other := t.TempDir()
	must(t, os.Chmod(other, 0o700))
	must(t, os.Symlink(filepath.Join(state, "reader.sqlite"), filepath.Join(other, "reader.sqlite")))
	if idx, err := openIndex(t.Context(), other, source); err == nil {
		idx.close()
		t.Fatal("database link accepted")
	}
}
func TestReaderCLIAndEmptyWatchInput(t *testing.T) {
	dir := t.TempDir()
	state := t.TempDir()
	must(t, os.Chmod(state, 0o700))
	var out bytes.Buffer
	must(t, run(t.Context(), []string{"sync", "--exchange-dir", dir, "--state-dir", state}, &out))
	if !strings.Contains(out.String(), "processed_events=0 indexed_results=0") {
		t.Fatal(out.String())
	}
	for _, args := range [][]string{{"--help"}, {"watch", "--help"}, {"list", "--state-dir", state}} {
		must(t, run(t.Context(), args, io.Discard))
	}
	for _, args := range [][]string{{"unknown"}, {"sync"}, {"rebuild", "--exchange-dir", dir, "--state-dir", state, "events/x.json"}} {
		if err := run(t.Context(), args, io.Discard); err == nil {
			t.Fatal(args)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := run(ctx, []string{"sync", "--exchange-dir", dir, "--state-dir", state}, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func FuzzJSONBoundary(f *testing.F) {
	f.Add([]byte(`{"contract_version":1,"kind":"event"}`))
	f.Add([]byte(`{"kind":"a","kind":"b"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		_, _ = decode(data)
	})
}

func TestReaderDoesNotFollowReferencesIntoPrivateDirectories(t *testing.T) {
	e, _, dir, _ := fixture(t)
	must(t, os.Mkdir(filepath.Join(dir, ".queue"), 0o700))
	data, err := os.ReadFile(filepath.Join(dir, "events/pending-1.json"))
	must(t, err)
	must(t, os.WriteFile(filepath.Join(dir, ".queue/private.json"), data, 0o600))
	must(t, os.Symlink("../.queue/private.json", filepath.Join(dir, "events/private.json")))
	if _, _, err := e.read(t.Context(), "events/private.json", "event"); err == nil {
		t.Fatal("private file followed through published alias")
	}
}
