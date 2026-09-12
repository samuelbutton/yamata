package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const examples = "../../contract/v1/examples"

func exchange(t *testing.T) (*Validator, string) {
	t.Helper()
	v, err := New()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(examples+"/valid")); err != nil {
		t.Fatal(err)
	}
	return v, root
}

func readTest(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func writeTest(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
func change(t *testing.T, path string, mutate func(map[string]any)) {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(readTest(t, path), &value); err != nil {
		t.Fatal(err)
	}
	mutate(value)
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, path, data)
}

func TestSchemaBundleMatchesPublishedSource(t *testing.T) {
	if !bytes.Equal(schemaJSON, readTest(t, "../../contract/v1/exchange.schema.json")) {
		t.Fatal("schema bundle drift; run go generate ./internal/contract")
	}
}

func TestEntrySchemas(t *testing.T) {
	const base = "https://yamata.invalid/contract/v1/"
	c := jsonschema.NewCompiler()
	c.UseLoader(noLoader{})
	files, err := filepath.Glob("../../contract/v1/*.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader(readTest(t, file)))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource(base+filepath.Base(file), value); err != nil {
			t.Fatal(err)
		}
	}
	for kind, file := range map[string]string{
		"job": "jobs/run-1.json", "event": "events/completed-1.json",
		"result": "results/run-1.json", "bag": "bags/exec-1.jsonl", "tick": "bags/exec-1.jsonl",
	} {
		t.Run(kind, func(t *testing.T) {
			schema, err := c.Compile(base + kind + ".schema.json")
			if err != nil {
				t.Fatal(err)
			}
			data := readTest(t, examples+"/valid/"+file)
			if kind == "bag" || kind == "tick" {
				lines := bytes.Split(data, []byte("\n"))
				data = lines[0]
				if kind == "tick" {
					data = lines[1]
				}
			}
			value, err := decode(data)
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(value); err != nil {
				t.Fatal(err)
			}
			value.(map[string]any)["kind"] = "unknown"
			if err := schema.Validate(value); err == nil {
				t.Fatal("entry schema accepted the wrong document kind")
			}
		})
	}
}

func TestPublishedExamples(t *testing.T) {
	v, root := exchange(t)
	var paths []string
	for _, dir := range []string{"jobs", "bags", "results", "events"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			paths = append(paths, dir+"/"+entry.Name())
		}
	}
	if err := v.Check(context.Background(), root, paths); err != nil {
		t.Fatal(err)
	}
	// The same delivery has no additional identity effect.
	if err := v.Check(context.Background(), root, append(paths, paths...)); err != nil {
		t.Fatal(err)
	}
}

func TestPublishedRejections(t *testing.T) {
	v, root := exchange(t)
	if err := os.CopyFS(filepath.Join(root, "invalid"), os.DirFS(examples+"/invalid")); err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		File  string `json:"file"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(readTest(t, examples+"/invalid-cases.json"), &cases); err != nil {
		t.Fatal(err)
	}
	categories := map[string]error{"version": ErrVersion, "hash": ErrHash, "invalid": ErrInvalid}
	for _, tc := range cases {
		t.Run(tc.File, func(t *testing.T) {
			err := v.Check(context.Background(), root, []string{"invalid/" + tc.File})
			if !errors.Is(err, categories[tc.Error]) {
				t.Fatalf("error = %v, want %v", err, categories[tc.Error])
			}
		})
	}
}

func TestIdentityConflicts(t *testing.T) {
	for _, kind := range []string{"job", "execution", "result", "event", "bag"} {
		t.Run(kind, func(t *testing.T) {
			v, root := exchange(t)
			original := "jobs/run-1.json"
			duplicate := "jobs/duplicate.json"
			switch kind {
			case "result":
				original = "results/run-1.json"
				duplicate = "results/duplicate.json"
			case "event":
				original = "events/pending-1.json"
				duplicate = "events/duplicate.json"
			case "bag":
				original = "bags/exec-1.jsonl"
				duplicate = "bags/duplicate.jsonl"
			}
			writeTest(t, filepath.Join(root, duplicate), readTest(t, filepath.Join(root, original)))
			if err := v.Check(context.Background(), root, []string{original, duplicate}); err != nil {
				t.Fatal(err)
			}
			if kind == "execution" {
				change(t, filepath.Join(root, duplicate), func(d map[string]any) { d["job_id"] = "other-job" })
			} else {
				data := readTest(t, filepath.Join(root, duplicate))
				// Whitespace changes the published bytes without changing the JSON meaning.
				writeTest(t, filepath.Join(root, duplicate), append([]byte(" "), data...))
			}
			for _, paths := range [][]string{{original, duplicate}, {duplicate, original}} {
				if err := v.Check(context.Background(), root, paths); !errors.Is(err, ErrConflict) {
					t.Fatalf("error = %v, want conflict", err)
				}
			}
		})
	}
}

func TestPathBoundaries(t *testing.T) {
	v, root := exchange(t)
	outside := t.TempDir()
	writeTest(t, filepath.Join(outside, "job.json"), readTest(t, filepath.Join(root, "jobs/run-1.json")))
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "job.json"), filepath.Join(root, "linked.json")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "pipe.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../job.json", filepath.Join(outside, "job.json"), "jobs/../jobs/run-1.json", "jobs\\run-1.json", "C:/job.json", "escape/job.json", "linked.json", "pipe.json", "jobs", "jobs/run-1.json.tmp"} {
		t.Run(path, func(t *testing.T) {
			if err := v.Check(context.Background(), root, []string{path}); !errors.Is(err, ErrPath) {
				t.Fatalf("error = %v, want path rejection", err)
			}
		})
	}
	// A reference with a valid lexical path must still reject an escaping symlink.
	if err := os.Remove(filepath.Join(root, "bags/exec-1.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "job.json"), filepath.Join(root, "bags/exec-1.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := v.Check(context.Background(), root, []string{"jobs/analyze-1.json"}); !errors.Is(err, ErrPath) {
		t.Fatalf("reference error = %v, want path rejection", err)
	}
}

func TestOutcomeSemantics(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
		valid  bool
	}{
		{"pass", func(d map[string]any) {
			d["status"] = "PASS"
			m := d["metrics"].(map[string]any)
			m["collision_count"].(map[string]any)["value"] = 0
			m["collision_count"].(map[string]any)["pass"] = true
			m["goal_progress"].(map[string]any)["value"] = 1000000
			m["goal_progress"].(map[string]any)["pass"] = true
		}, true},
		{"warn", func(d map[string]any) {
			d["status"] = "WARN"
			m := d["metrics"].(map[string]any)
			for _, name := range []string{"collision_count", "minimum_obstacle_gap", "goal_progress"} {
				x := m[name].(map[string]any)
				x["value"] = nil
				x["pass"] = nil
				x["evidence_ticks"] = []any{}
			}
		}, true},
		{"analysis error", func(d map[string]any) {
			d["status"] = "ERROR"
			d["failure_class"] = "analysis_failure"
			d["metrics"] = nil
		}, true},
		{"missing metrics", func(d map[string]any) { d["metrics"] = nil }, false},
		{"missing bag", func(d map[string]any) { d["bag"] = nil }, false},
		{"wrong execution", func(d map[string]any) { d["execution_id"] = "other" }, false},
		{"wrong correlation", func(d map[string]any) { d["correlation_id"] = "other" }, false},
		{"wrong unit", func(d map[string]any) {
			d["metrics"].(map[string]any)["collision_count"].(map[string]any)["unit"] = "mm"
		}, false},
		{"score failure as error", func(d map[string]any) { d["failure_class"] = "worker_failure" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, root := exchange(t)
			change(t, filepath.Join(root, "results/run-1.json"), tc.mutate)
			err := v.Check(context.Background(), root, []string{"results/run-1.json"})
			if (err == nil) != tc.valid {
				t.Fatalf("error = %v, want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestBagOrderingAndCompleteness(t *testing.T) {
	for _, name := range []string{"missing record", "wrong time", "wrong tick", "extra record", "blank record", "wrong version"} {
		t.Run(name, func(t *testing.T) {
			v, root := exchange(t)
			path := filepath.Join(root, "bags/exec-1.jsonl")
			data := readTest(t, path)
			switch name {
			case "missing record":
				lines := bytes.Split(data, []byte("\n"))
				data = bytes.Join(lines[:len(lines)-2], []byte("\n"))
				data = append(data, '\n')
			case "wrong time":
				data = bytes.Replace(data, []byte(`"time_ms":1000`), []byte(`"time_ms":999`), 1)
			case "wrong tick":
				data = bytes.Replace(data, []byte(`"tick":1`), []byte(`"tick":0`), 1)
			case "extra record":
				lines := bytes.Split(data, []byte("\n"))
				data = append(data, append(lines[1], '\n')...)
			case "blank record":
				data = append(data, '\n')
			case "wrong version":
				data = bytes.Replace(data, []byte(`"format_version":1`), []byte(`"format_version":2`), 1)
			}
			writeTest(t, path, data)
			if err := v.Check(context.Background(), root, []string{"bags/exec-1.jsonl"}); err == nil {
				t.Fatal("invalid bag accepted")
			}
		})
	}
}

func TestLimitsAndCancellation(t *testing.T) {
	v, root := exchange(t)
	writeTest(t, filepath.Join(root, "large.json"), bytes.Repeat([]byte(" "), maxFileBytes+1))
	if err := v.Check(context.Background(), root, []string{"large.json"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("size error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := v.Check(ctx, root, []string{"jobs/run-1.json"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestConcurrentChecksAreIndependent(t *testing.T) {
	v, root := exchange(t)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if err := v.Check(context.Background(), root, []string{"events/completed-1.json"}); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
}

func FuzzParse(f *testing.F) {
	v, err := New()
	if err != nil {
		f.Fatal(err)
	}
	for _, data := range []string{`{}`, `null`, `{"a":1,"a":2}`, `{"n":1e999}`, strings.Repeat("[", 40), `{"contract_version":2}`, `{"kind":"tick"}`} {
		f.Add([]byte(data))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 65536 {
			t.Skip("fuzz input exceeds parser test size")
		}
		_, _ = v.parse(data)
	})
}

func TestPublicFileHashes(t *testing.T) {
	const root = "../../contract/v1"
	listed := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(readTest(t, root+"/SHA256SUMS"))), "\n") {
		digest, name, ok := strings.Cut(line, "  ")
		if !ok || listed[name] || !validPath(name) {
			t.Fatalf("invalid manifest entry: %q", line)
		}
		if hashBytes(readTest(t, filepath.Join(root, name))) != digest {
			t.Fatalf("hash drift for %s; run make generate after review", name)
		}
		listed[name] = true
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() == "SHA256SUMS" {
			return nil
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !listed[filepath.ToSlash(name)] {
			t.Errorf("file missing from manifest: %s", name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEventMustMatchPublishedResult(t *testing.T) {
	for _, name := range []string{"execution", "attempt", "correlation", "state", "hash", "missing result"} {
		t.Run(name, func(t *testing.T) {
			v, root := exchange(t)
			change(t, filepath.Join(root, "events/completed-1.json"), func(d map[string]any) {
				switch name {
				case "execution":
					d["execution_id"] = "other"
				case "attempt":
					d["attempt_id"] = "other"
					d["event_id"] = hashBytes([]byte("run-1\nother\n3"))
				case "correlation":
					d["correlation_id"] = "other"
				case "state":
					d["state"] = "PASS"
				case "hash":
					d["result"].(map[string]any)["sha256"] = strings.Repeat("0", 64)
				case "missing result":
					d["result"] = nil
				}
			})
			if err := v.Check(context.Background(), root, []string{"events/completed-1.json"}); err == nil {
				t.Fatal("mismatched event accepted")
			}
		})
	}
}
