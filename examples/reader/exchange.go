package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"syscall"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Only published contract files are embedded. No producer source is linked.
//
//go:embed contract/v1/exchange.schema.json
var schemaBytes []byte

type offlineLoader struct{}

func (offlineLoader) Load(string) (any, error) {
	return nil, errors.New("external schema loading is disabled")
}
func compileSchema() (*jsonschema.Schema, error) {
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(offlineLoader{})
	const uri = "https://yamata.invalid/contract/v1/exchange.schema.json"
	if err := compiler.AddResource(uri, value); err != nil {
		return nil, err
	}
	return compiler.Compile(uri)
}

type checked struct {
	path, hash string
	data       []byte
	doc        object
}
type exchange struct {
	root   *os.Root
	schema *jsonschema.Schema
}
type graph struct {
	exchange
	files      map[string]checked
	active     map[string]bool
	identities map[string]string
	bytes      int
}

func (e exchange) read(ctx context.Context, path, kind string) (checked, *graph, error) {
	g := &graph{exchange: e, files: map[string]checked{}, active: map[string]bool{}, identities: map[string]string{}}
	file, err := g.load(ctx, path, kind)
	return file, g, err
}
func (e exchange) parse(data []byte, kind string) (object, error) {
	doc, err := decode(data)
	if err != nil {
		return nil, err
	}
	if text(doc, "kind") != "tick" && integer(doc, "contract_version") != 1 {
		return nil, errors.New("unsupported contract version")
	}
	if err := e.schema.Validate(doc); err != nil {
		return nil, fmt.Errorf("invalid %s schema: %w", kind, err)
	}
	if text(doc, "kind") != kind {
		return nil, fmt.Errorf("expected %s", kind)
	}
	return doc, nil
}
func (g *graph) load(ctx context.Context, path, kind string) (checked, error) {
	if err := ctx.Err(); err != nil {
		return checked{}, err
	}
	if old, ok := g.files[path]; ok {
		if text(old.doc, "kind") != kind {
			return checked{}, errors.New("reference kind mismatch")
		}
		return old, nil
	}
	if !fs.ValidPath(path) || strings.ContainsAny(path, "\\:\x00") || !strings.HasPrefix(path, kind+"s/") || strings.HasSuffix(path, ".tmp") {
		return checked{}, errors.New("invalid exchange path")
	}
	if g.active[path] || len(g.active) >= 8 || len(g.files) >= 256 {
		return checked{}, errors.New("reference cycle or graph size limit")
	}
	g.active[path] = true
	defer delete(g.active, path)
	// Confine each reference to its published folder, not merely the exchange.
	folder := kind + "s"
	scope, err := g.directory(folder)
	if err != nil {
		return checked{}, err
	}
	defer scope.Close()
	f, err := scope.OpenFile(strings.TrimPrefix(path, folder+"/"), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return checked{}, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return checked{}, errors.Join(errors.New("expected regular file"), err, f.Close())
	}
	limit := int64(1 << 20)
	if kind == "bag" {
		limit = 16 << 20
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	err = errors.Join(err, f.Close())
	if err != nil {
		return checked{}, err
	}
	g.bytes += len(data)
	if int64(len(data)) > limit || g.bytes > 64<<20 {
		return checked{}, errors.New("exchange size limit exceeded")
	}
	var doc object
	if kind == "bag" {
		doc, err = g.bag(ctx, data)
	} else {
		doc, err = g.parse(data, kind)
	}
	if err != nil {
		return checked{}, err
	}
	file := checked{path: path, hash: digest(data), data: data, doc: doc}
	if err = g.relations(ctx, doc); err != nil {
		return checked{}, err
	}
	for _, id := range identities(doc) {
		if old, ok := g.identities[id]; ok && old != file.hash {
			return checked{}, fmt.Errorf("conflicting identity %s", id)
		}
		g.identities[id] = file.hash
	}
	g.files[path] = file
	return file, nil
}
func (g *graph) reference(ctx context.Context, ref object, kind string) (checked, error) {
	if ref == nil {
		return checked{}, errors.New("missing reference")
	}
	file, err := g.load(ctx, text(ref, "path"), kind)
	if err != nil {
		return checked{}, err
	}
	if file.hash != text(ref, "sha256") {
		return checked{}, errors.New("reference hash mismatch")
	}
	return file, nil
}
func (g *graph) bag(ctx context.Context, data []byte) (object, error) {
	if !bytes.HasSuffix(data, []byte("\n")) {
		return nil, errors.New("bag lacks final newline")
	}
	lines := bytes.SplitN(data[:len(data)-1], []byte("\n"), 100003)
	header, err := g.parse(lines[0], "bag")
	if err != nil {
		return nil, err
	}
	if int64(len(lines)-1) != integer(header, "record_count") {
		return nil, errors.New("bag record count mismatch")
	}
	for i, line := range lines[1:] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tick, err := g.parse(line, "tick")
		if err != nil {
			return nil, err
		}
		if integer(tick, "tick") != int64(i) || integer(tick, "time_ms") != int64(i)*integer(header, "tick_ms") {
			return nil, errors.New("bag tick order mismatch")
		}
	}
	return header, nil
}
func identities(d object) []string {
	switch text(d, "kind") {
	case "event":
		return []string{"event/" + text(d, "event_id"), fmt.Sprintf("sequence/%s/%d", text(d, "job_id"), integer(d, "sequence"))}
	case "job":
		ids := []string{"job/" + text(d, "job_id")}
		if text(d, "job_kind") == "run" {
			ids = append(ids, "execution/"+text(d, "execution_id"))
		}
		return ids
	case "bag":
		return []string{"bag/" + text(d, "execution_id")}
	default:
		return []string{resultKey(d)}
	}
}
func resultKey(d object) string {
	analysis := text(d, "analysis_id")
	if analysis == "" {
		analysis = "run"
	}
	return "result/" + text(d, "execution_id") + "/" + analysis
}

// candidates ignores staging files and bounds one teaching exchange to 4096 directory entries.
func (e exchange) directory(folder string) (*os.Root, error) {
	info, err := e.root.Lstat(folder)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("expected a real published directory")
	}
	return e.root.OpenRoot(folder)
}

func (e exchange) candidates(folder string) ([]string, error) {
	scope, err := e.directory(folder)
	if errors.Is(err, os.ErrNotExist) && folder == "events" {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer scope.Close()
	f, err := scope.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(4097)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > 4096 {
		return nil, errors.New("directory exceeds 4096 entries")
	}
	paths := []string{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") && !strings.HasPrefix(entry.Name(), ".") {
			paths = append(paths, folder+"/"+entry.Name())
		}
	}
	sort.Strings(paths)
	return paths, nil
}
