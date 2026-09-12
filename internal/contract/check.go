package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"syscall"
)

const maxFileBytes = 16 << 20
const maxTotalBytes = 64 << 20
const maxFiles = 256

type checkedFile struct {
	document document
	hash     string
}
type inspection struct {
	validator  *Validator
	root       *os.Root
	checked    map[string]checkedFile
	active     map[string]bool
	identities map[string]string
	bytes      int
	files      int
}

// Check validates selected relative files and all their references in one exchange.
// It performs no writes. Conflicting identities are checked within this invocation.
func (v *Validator) Check(ctx context.Context, directory string, paths []string) (err error) {
	if directory == "" || len(paths) == 0 || len(paths) > maxFiles {
		return fmt.Errorf("%w: supply a directory and 1 to 256 files", ErrInvalid)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("open exchange directory: %w", err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	s := inspection{validator: v, root: root, checked: make(map[string]checkedFile), active: make(map[string]bool), identities: make(map[string]string)}
	for _, path := range paths {
		if _, err := s.load(ctx, path, ""); err != nil {
			return fmt.Errorf("%q: %w", path, err)
		}
	}
	return nil
}

func validPath(path string) bool {
	return path != "." && fs.ValidPath(path) && !strings.ContainsAny(path, "\\:\x00") && !strings.HasSuffix(path, ".tmp")
}

func (s *inspection) read(path string) ([]byte, error) {
	if !validPath(path) {
		return nil, ErrPath
	}
	// Nonblocking open permits rejection of pipes without waiting for a writer.
	f, err := s.root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPath, err)
	}
	info, statErr := f.Stat()
	if statErr != nil || !info.Mode().IsRegular() {
		return nil, errors.Join(fmt.Errorf("%w: expected a regular file", ErrPath), statErr, f.Close())
	}
	data, readErr := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err := errors.Join(readErr, f.Close()); err != nil {
		return nil, err
	}
	s.bytes += len(data)
	if len(data) > maxFileBytes || s.bytes > maxTotalBytes {
		return nil, fmt.Errorf("%w: exchange size limit exceeded", ErrInvalid)
	}
	return data, nil
}

func (s *inspection) load(ctx context.Context, path, kind string) (checkedFile, error) {
	if err := ctx.Err(); err != nil {
		return checkedFile{}, err
	}
	if old, ok := s.checked[path]; ok {
		if kind != "" && old.document.Kind != kind {
			return checkedFile{}, fmt.Errorf("%w: wrong reference kind", ErrInvalid)
		}
		return old, nil
	}
	if s.active[path] || len(s.active) >= 8 || s.files >= maxFiles {
		return checkedFile{}, fmt.Errorf("%w: reference cycle or file limit", ErrInvalid)
	}
	s.active[path] = true
	s.files++
	defer delete(s.active, path)
	data, err := s.read(path)
	if err != nil {
		return checkedFile{}, err
	}
	var d document
	if strings.HasSuffix(path, ".jsonl") {
		d, err = s.bag(ctx, data)
	} else {
		d, err = s.validator.parse(data)
		if err == nil && (d.Kind == "bag" || d.Kind == "tick") {
			err = fmt.Errorf("%w: bags require JSON Lines", ErrInvalid)
		}
	}
	if err != nil {
		return checkedFile{}, err
	}
	if kind != "" && kind != d.Kind {
		return checkedFile{}, fmt.Errorf("%w: wrong reference kind", ErrInvalid)
	}
	if err = s.checkDocument(ctx, d); err != nil {
		return checkedFile{}, err
	}
	digest := hashBytes(data)
	keys := []string{d.identity()}
	if d.Kind == "job" && d.JobKind == "run" {
		keys = append(keys, "execution/"+d.ExecutionID)
	}
	for _, key := range keys {
		if previous, ok := s.identities[key]; ok && previous != digest {
			return checkedFile{}, fmt.Errorf("%w: %s", ErrConflict, key)
		}
	}
	for _, key := range keys {
		s.identities[key] = digest
	}
	result := checkedFile{d, digest}
	s.checked[path] = result
	return result, nil
}

func (s *inspection) reference(ctx context.Context, r *reference, kind string) (document, error) {
	if r == nil || !strings.HasPrefix(r.Path, kind+"s/") {
		return document{}, fmt.Errorf("%w: wrong reference directory", ErrPath)
	}
	file, err := s.load(ctx, r.Path, kind)
	if err != nil {
		return document{}, err
	}
	if file.hash != r.SHA256 {
		return document{}, ErrHash
	}
	return file.document, nil
}

func (s *inspection) bag(ctx context.Context, data []byte) (document, error) {
	if !bytes.HasSuffix(data, []byte("\n")) {
		return document{}, fmt.Errorf("%w: bag must end with a newline", ErrInvalid)
	}
	lines := bytes.SplitN(data[:len(data)-1], []byte("\n"), 100003)
	d, err := s.validator.parse(lines[0])
	if err != nil {
		return d, err
	}
	if d.Kind != "bag" || len(lines)-1 != d.RecordCount {
		return d, fmt.Errorf("%w: bag record count", ErrInvalid)
	}
	for i, line := range lines[1:] {
		if err := ctx.Err(); err != nil {
			return d, err
		}
		record, err := s.validator.parse(line)
		if err != nil {
			return d, err
		}
		var tick struct {
			Tick   int `json:"tick"`
			TimeMS int `json:"time_ms"`
		}
		if err := json.Unmarshal(line, &tick); err != nil {
			return d, err
		}
		if record.Kind != "tick" || tick.Tick != i || tick.TimeMS != i*d.TickMS {
			return d, fmt.Errorf("%w: unordered bag ticks", ErrInvalid)
		}
	}
	return d, nil
}
