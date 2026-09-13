"""Verify the demo, checked-in reference, detached reader, and cleanup boundaries."""

import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts"))
from demo import ROOT, ENGINE, REFERENCE, MARKER, MARKER_BYTES, call, clean, run, public_files
from reference import export, manifest


def stable_results(exchange):
    outcomes = {}
    for path in (exchange / "results").glob("*.json"):
        result = json.loads(path.read_text())
        del result["attempt_id"]
        del result["timing"]
        outcomes[path.name] = result
    return outcomes


def stable_events(exchange):
    transitions = {}
    for path in (exchange / "events").glob("*.json"):
        event = json.loads(path.read_text())
        for key in ("attempt_id", "event_id", "created_at_ms"):
            del event[key]
        if event["result"] is not None:
            del event["result"]["sha256"]
        identity = (event["job_id"], event["sequence"])
        assert identity not in transitions
        transitions[identity] = event
    return transitions


def check_reference(exchange):
    files = {str(path.relative_to(REFERENCE)): path.read_bytes()
             for path in REFERENCE.rglob("*") if path.is_file() and path.name != "SHA256SUMS"}
    assert manifest(files) == (REFERENCE / "SHA256SUMS").read_text(), "reference manifest drift"
    assert (REFERENCE / "jobs/candidate.json").read_bytes() == (ROOT / "examples/jobs/candidate.json").read_bytes()
    call(ENGINE, "validate", "--exchange-dir", REFERENCE, *public_files(REFERENCE))
    for folder in ("jobs", "bags"):
        assert {path.name: path.read_bytes() for path in (exchange / folder).iterdir()} == {
            path.name: path.read_bytes() for path in (REFERENCE / folder).iterdir()}
    assert stable_results(exchange) == stable_results(REFERENCE)
    assert stable_events(exchange) == stable_events(REFERENCE)


def rejected(action):
    try:
        action()
    except (ValueError, FileExistsError):
        return
    raise AssertionError("an unsafe replacement or cleanup was accepted")


def check_cleanup(parent):
    unrelated = parent / "other-data"
    unrelated.mkdir()
    (unrelated / "keep").write_text("preserve")
    rejected(lambda: clean(unrelated))
    (unrelated / MARKER).write_text("wrong marker")
    rejected(lambda: clean(unrelated))
    (unrelated / MARKER).unlink()
    marker = parent / "marker"
    marker.write_bytes(MARKER_BYTES)
    (unrelated / MARKER).symlink_to(marker)
    rejected(lambda: clean(unrelated))
    link = parent / "linked-demo"
    link.symlink_to(unrelated, target_is_directory=True)
    rejected(lambda: clean(link))
    rejected(lambda: run(link))
    rejected(lambda: clean(link / "nested"))
    owned = parent / "owned"
    owned.mkdir()
    (owned / MARKER).write_bytes(MARKER_BYTES)
    (owned / "outside").symlink_to(unrelated, target_is_directory=True)
    clean(owned)
    clean(owned)  # Missing output is an idempotent cleanup.
    assert (unrelated / "keep").read_text() == "preserve" and marker.exists()


def check_detached(parent):
    detached = parent / "detached-reader"
    shutil.copytree(ROOT / "examples/reader", detached)
    exchange = parent / "public-exchange"
    shutil.copytree(REFERENCE, exchange)
    state = parent / "detached-state"
    state.mkdir(mode=0o700)
    env = dict(os.environ, GOWORK="off")
    go = shlex.split(os.environ.get("GO", "go"))
    for args in (("build", "-trimpath", "-o", str(parent / "reader"), "."), ("test", "./...")):
        subprocess.run([*go, *args], cwd=detached, env=env, check=True, timeout=180)
    dependencies = subprocess.check_output([*go, "list", "-deps", "./..."], cwd=detached, env=env, text=True)
    assert not any(path.startswith("github.com/samuelbutton/yamata/") and
                   not path.startswith("github.com/samuelbutton/yamata/examples/reader")
                   for path in dependencies.splitlines())
    reader = parent / "reader"
    call(reader, "sync", "--exchange-dir", exchange, "--state-dir", state)
    indexed = json.loads(call(reader, "list", "--state-dir", state))
    assert indexed["processed_events"] == 8 and len(indexed["results"]) == 2
    shutil.rmtree(exchange / "events")
    call(reader, "rebuild", "--exchange-dir", exchange, "--state-dir", state)
    assert json.loads(call(reader, "list", "--state-dir", state)) == indexed
    print("Detached reader build, tests, public reference consumption, and rebuild: passed", flush=True)


if __name__ == "__main__":
    with tempfile.TemporaryDirectory(prefix="yamata-walkthrough-") as directory:
        parent = Path(directory).resolve()
        destination = parent / "demo"
        run(destination)
        before = public_files(destination / "exchange")
        rejected(lambda: run(destination))
        assert public_files(destination / "exchange") == before
        check_reference(destination / "exchange")
        exported = parent / "exported"
        export(destination, exported)
        assert public_files(exported) == before
        exported_files = {str(path.relative_to(exported)): path.read_bytes()
                          for path in exported.rglob("*") if path.is_file() and path.name != "SHA256SUMS"}
        assert manifest(exported_files) == (exported / "SHA256SUMS").read_text()
        rejected(lambda: export(destination, exported))
        assert public_files(exported) == before
        clean(destination)
        assert not destination.exists()
        check_cleanup(parent)
        check_detached(parent)
    print("Demo, stable reference values, manifest, and scoped cleanup: passed", flush=True)
