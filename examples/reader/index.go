package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type index struct {
	db   *sql.DB
	root *os.Root
}

func (i *index) close() error { return errors.Join(i.db.Close(), i.root.Close()) }

// openIndex requires an existing owner-only directory separate from the exchange.
func openIndex(ctx context.Context, directory, source string) (out *index, err error) {
	directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	if source != "" {
		rel, err := filepath.Rel(source, directory)
		if err != nil {
			return nil, err
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return nil, errors.New("reader state must be outside the exchange")
		}
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			root.Close()
		}
	}()
	info, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("reader state directory must be owner-only")
	}
	for _, name := range []string{"reader.sqlite", "reader.sqlite-journal", "reader.sqlite-wal", "reader.sqlite-shm"} {
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("reader database files must be private regular files")
		}
	}
	if source == "" {
		if _, err := root.Lstat("reader.sqlite"); err != nil {
			return nil, err
		}
	}
	f, err := root.OpenFile("reader.sqlite", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err == nil {
		err = errors.Join(f.Sync(), f.Close())
		if err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: filepath.Join(directory, "reader.sqlite")}
	uri.RawQuery = url.Values{"_txlock": {"immediate"}, "_pragma": {"busy_timeout(5000)", "synchronous(FULL)"}}.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	defer func() {
		if err != nil {
			db.Close()
		}
	}()
	i := &index{db: db, root: root}
	err = i.transaction(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if version == 0 {
			if source == "" {
				return errors.New("initialize reader state with sync or rebuild")
			}
			if _, err := tx.ExecContext(ctx, indexSchema); err != nil {
				return err
			}
		}
		if version != 0 && version != 1 {
			return errors.New("unsupported reader database version")
		}
		if source != "" {
			var stored string
			err := tx.QueryRowContext(ctx, "SELECT path FROM source").Scan(&stored)
			if errors.Is(err, sql.ErrNoRows) {
				_, err = tx.ExecContext(ctx, "INSERT INTO source(path) VALUES(?)", source)
				return err
			}
			if err != nil {
				return err
			}
			if stored != source {
				return errors.New("reader state belongs to a different exchange")
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	if err = errors.Join(dir.Sync(), dir.Close()); err != nil {
		return nil, err
	}
	return i, nil
}

const indexSchema = `
CREATE TABLE source(path TEXT NOT NULL);
CREATE TABLE identities(id TEXT PRIMARY KEY,hash TEXT NOT NULL);
CREATE TABLE seen(event_id TEXT PRIMARY KEY,job_id TEXT NOT NULL,sequence INTEGER NOT NULL,hash TEXT NOT NULL,UNIQUE(job_id,sequence));
CREATE TABLE results(id TEXT PRIMARY KEY,path TEXT NOT NULL,hash TEXT NOT NULL,data BLOB NOT NULL);
PRAGMA user_version=1;
`

func (i *index) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func remember(ctx context.Context, tx *sql.Tx, g *graph) error {
	for id, hash := range g.identities {
		var old string
		err := tx.QueryRowContext(ctx, "SELECT hash FROM identities WHERE id=?", id).Scan(&old)
		if err == nil {
			if old != hash {
				return fmt.Errorf("changed published identity %s", id)
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO identities(id,hash) VALUES(?,?)", id, hash); err != nil {
			return err
		}
	}
	return nil
}
func saveResult(ctx context.Context, tx *sql.Tx, file checked) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO results(id,path,hash,data) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING", resultKey(file.doc), file.path, file.hash, file.data)
	return err
}
func (i *index) consume(ctx context.Context, e exchange, path string) error {
	event, g, err := e.read(ctx, path, "event")
	if err != nil {
		return err
	}
	return i.transaction(ctx, func(tx *sql.Tx) error {
		if err := remember(ctx, tx, g); err != nil {
			return err
		}
		if ref := nested(event.doc, "result"); ref != nil {
			if err := saveResult(ctx, tx, g.files[text(ref, "path")]); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO seen(event_id,job_id,sequence,hash) VALUES(?,?,?,?) ON CONFLICT(event_id) DO NOTHING", text(event.doc, "event_id"), text(event.doc, "job_id"), integer(event.doc, "sequence"), event.hash)
		return err
	})
}
func (i *index) sync(ctx context.Context, e exchange, paths []string) error {
	var err error
	if len(paths) == 0 {
		paths, err = e.candidates("events")
		if err != nil {
			return err
		}
	}
	if len(paths) > 4096 {
		return errors.New("at most 4096 events per scan")
	}
	var failures []error
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := i.consume(ctx, e, path); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", path, err))
		}
	}
	return errors.Join(failures...)
}
func (i *index) rebuild(ctx context.Context, e exchange) error {
	paths, err := e.candidates("results")
	if err != nil {
		return err
	}
	return i.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM results"); err != nil {
			return err
		}
		for _, path := range paths {
			file, g, err := e.read(ctx, path, "result")
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if err := remember(ctx, tx, g); err != nil {
				return err
			}
			if err := saveResult(ctx, tx, file); err != nil {
				return err
			}
		}
		return nil
	})
}

type indexedResult struct {
	Path   string          `json:"path"`
	Hash   string          `json:"sha256"`
	Result json.RawMessage `json:"result"`
}
type indexSnapshot struct {
	Events  int             `json:"processed_events"`
	Results []indexedResult `json:"results"`
}

func (i *index) snapshot(ctx context.Context) (out indexSnapshot, err error) {
	out.Results = []indexedResult{}
	err = i.transaction(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM seen").Scan(&out.Events); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT path,hash,data FROM results ORDER BY id LIMIT 4097")
		if err != nil {
			return err
		}
		defer rows.Close()
		total := 0
		for rows.Next() {
			var r indexedResult
			if err := rows.Scan(&r.Path, &r.Hash, &r.Result); err != nil {
				return err
			}
			total += len(r.Result)
			if total > 64<<20 {
				return errors.New("result listing exceeds 64 MiB")
			}
			out.Results = append(out.Results, r)
		}
		if len(out.Results) > 4096 {
			return errors.New("result listing exceeds 4096 entries")
		}
		return rows.Err()
	})
	return out, err
}
