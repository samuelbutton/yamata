package queue

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samuelbutton/yamata/internal/contract"
)

// legacyCopy builds the original database schema from accepted rows and exchange files.
// It copies rows through SQLite rather than copying a live database or its WAL file.
func legacyCopy(t *testing.T, source *Store, dir string) string {
	t.Helper()
	dest := t.TempDir()
	for _, folder := range []string{"jobs", "bags", "results", "events"} {
		if _, err := os.Stat(filepath.Join(dir, folder)); errors.Is(err, os.ErrNotExist) {
			continue
		}
		must(t, os.CopyFS(filepath.Join(dest, folder), os.DirFS(filepath.Join(dir, folder))))
	}
	must(t, os.Mkdir(filepath.Join(dest, ".queue"), 0o700))
	path := filepath.Join(dest, ".queue/queue.sqlite")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	must(t, err)
	must(t, f.Close())
	db, err := sql.Open("sqlite", path)
	must(t, err)
	_, err = db.Exec(schema)
	must(t, err)
	rows, err := source.db.Query("SELECT id,job_id,execution_id,path,data,attempt,stage,state,sequence,started_ms,token,lease_ms,bag_hash FROM jobs")
	must(t, err)
	for rows.Next() {
		var id, sequence, started, expires int64
		var job, execution, path, attempt, stage, state, token, hash string
		var data []byte
		must(t, rows.Scan(&id, &job, &execution, &path, &data, &attempt, &stage, &state, &sequence, &started, &token, &expires, &hash))
		_, err = db.Exec("INSERT INTO jobs VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)", id, job, execution, path, data, attempt, stage, state, sequence, started, token, expires, hash)
		must(t, err)
	}
	must(t, rows.Err())
	must(t, rows.Close())
	rows, err = source.db.Query("SELECT id,job,path,kind,data,delivered FROM outbox")
	must(t, err)
	for rows.Next() {
		var id, job, delivered int64
		var path, kind string
		var data []byte
		must(t, rows.Scan(&id, &job, &path, &kind, &data, &delivered))
		_, err = db.Exec("INSERT INTO outbox VALUES(?,?,?,?,?,?)", id, job, path, kind, data, delivered)
		must(t, err)
	}
	must(t, rows.Err())
	must(t, rows.Close())
	must(t, db.Close())
	return dest
}

func TestVersionOneMigrationPreservesWorkAndOutcomes(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	drain(t, s, 1, 1)
	addJob(t, s, dir, "low", 3, nil)
	addJob(t, s, dir, "middle", 2, nil)
	addJob(t, s, dir, "high", 0, nil)
	must(t, s.Flush(t.Context()))
	high := claimed(t, s, "simulation")
	if high.job.JobID != "high" {
		t.Fatal(high)
	}
	must(t, s.perform(t.Context(), high, time.Minute, nil)) // Accepted bag, pending publication.
	old := legacyCopy(t, s, dir)
	before := files(t, old, "results")
	upgraded := open(t, old)
	var version int
	must(t, upgraded.db.QueryRow("PRAGMA user_version").Scan(&version))
	if version != 3 {
		t.Fatal(version)
	}
	for name, want := range map[string]int{"low": 3, "middle": 2, "high": 0, "record-candidate": 1} {
		var priority int
		must(t, upgraded.db.QueryRow("SELECT priority FROM jobs WHERE job_id=?", name).Scan(&priority))
		if priority != want {
			t.Fatal(name, priority)
		}
	}
	if got := claimed(t, upgraded, "simulation").job.JobID; got != "middle" {
		t.Fatal("migrated priority lost", got)
	}
	// Leave that new lease to expire, without changing its durable job.
	_, err := upgraded.db.Exec("UPDATE jobs SET lease_ms=0 WHERE job_id='middle'")
	must(t, err)
	drain(t, upgraded, 1, 1)
	for path, data := range before {
		got, err := os.ReadFile(filepath.Join(old, "results", path))
		must(t, err)
		if string(got) != string(data) {
			t.Fatal("migration changed accepted result")
		}
	}
	if len(files(t, old, "results")) != 4 || len(files(t, old, "bags")) != 4 {
		t.Fatal("migration lost pending work")
	}
	paths := []string{}
	for path := range files(t, old, "events") {
		paths = append(paths, "events/"+path)
	}
	must(t, upgraded.validator.Check(t.Context(), old, paths))
}

func TestMigrationRollsBackInvalidSnapshot(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	old := legacyCopy(t, s, dir)
	db, err := sql.Open("sqlite", filepath.Join(old, ".queue/queue.sqlite"))
	must(t, err)
	_, err = db.Exec("UPDATE jobs SET data=?", []byte("invalid snapshot"))
	must(t, err)
	if store, err := Open(t.Context(), old); !errors.Is(err, contract.ErrInvalid) {
		if store != nil {
			store.Close()
		}
		t.Fatal(err)
	}
	var version int
	must(t, db.QueryRow("PRAGMA user_version").Scan(&version))
	if version != 1 {
		t.Fatal("partial migration committed")
	}
	var tables int
	must(t, db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('dispatches','retries')").Scan(&tables))
	if tables != 0 {
		t.Fatal("migration left partial tables")
	}
	must(t, db.Close())
}

func TestVersionTwoMigrationRetainsRetryCountersAndForeignKeys(t *testing.T) {
	dir, s := setup(t)
	intake(t, s)
	old := legacyCopy(t, s, dir)
	db, err := sql.Open("sqlite", filepath.Join(old, ".queue/queue.sqlite"))
	must(t, err)
	_, err = db.Exec(upgradeV2)
	must(t, err)
	_, err = db.Exec(`UPDATE dispatches SET position=9;
 UPDATE jobs SET generation=4;
 INSERT INTO retries(job,stage,failed_generation,failed_attempt,next_attempt,created_ms)
 SELECT id,'simulation',3,'failed',attempt,started_ms FROM jobs;`)
	must(t, err)
	must(t, db.Close())
	upgraded := open(t, old)
	if retryCount(t, upgraded) != 1 || position(t, upgraded, "simulation") != 9 || position(t, upgraded, "analysis") != 9 {
		t.Fatal("upgrade reset durable recovery or scheduling")
	}
	var enabled int
	must(t, upgraded.db.QueryRow("PRAGMA foreign_keys").Scan(&enabled))
	if enabled != 1 {
		t.Fatal("foreign keys disabled")
	}
	_, err = upgraded.db.Exec("INSERT INTO retries VALUES(999,'analysis',1,'first','second',1)")
	if err == nil {
		t.Fatal("foreign key not enforced")
	}
	must(t, upgraded.Flush(t.Context()))
	l := claimed(t, upgraded, "simulation")
	if l.generation != 5 {
		t.Fatal(l.generation)
	}
	must(t, upgraded.perform(t.Context(), l, time.Minute, func(context.Context, string) error { return errWorkerFailure }))
	drain(t, upgraded, 1, 1)
	_, result := history(t, old)
	if result.Status != "ERROR" || retryCount(t, upgraded) != 1 {
		t.Fatal("upgrade renewed exhausted retry", result)
	}
}
