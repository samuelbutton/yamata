"""Exercise the built commands through separate local processes and public files."""

import hashlib
import json
from pathlib import Path
import selectors
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
ENGINE = ROOT / "bin/yamata"
READER = ROOT / "bin/reader"


def call(binary, *args, succeeds=True):
    result = subprocess.run(
        [str(binary), *map(str, args)], capture_output=True, text=True, timeout=30
    )
    assert (result.returncode == 0) == succeeds, (args, result.stdout, result.stderr)
    return result.stdout


def snapshot(exchange):
    return json.loads(call(ENGINE, "queue", "--exchange-dir", exchange))


def listing(state):
    return json.loads(call(READER, "list", "--state-dir", state))


def prepare(parent):
    exchange = parent / "exchange"
    exchange.mkdir()
    shutil.copytree(ROOT / "examples/jobs", exchange / "jobs")
    call(ENGINE, "enqueue", "--exchange-dir", exchange, "jobs/candidate.json")
    return exchange


def public_files(exchange):
    return {
        str(path.relative_to(exchange)): path.read_bytes()
        for folder in ("jobs", "bags", "results", "events")
        for path in (exchange / folder).glob("*")
        if path.is_file()
    }


def check_outage(parent):
    exchange = prepare(parent)
    state = parent / "reader-state"
    rebuilt = parent / "rebuilt-state"
    state.mkdir(mode=0o700)
    rebuilt.mkdir(mode=0o700)
    call(ENGINE, "fault", "--exchange-dir", exchange, "--job", "record-candidate",
         "--stage", "analysis", "--failures", "1")
    call(ENGINE, "workers", "--exchange-dir", exchange, "--analysis-workers", "0", "--drain")
    waiting = snapshot(exchange)
    assert waiting["stages"]["analysis"] == 1
    assert waiting["jobs"][0]["ready"] and not waiting["jobs"][0]["result_path"]
    bags = {path.name: path.read_bytes() for path in (exchange / "bags").glob("*")}

    process = subprocess.Popen(
        [str(READER), "watch", "--exchange-dir", str(exchange), "--state-dir", str(state)],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    try:
        with selectors.DefaultSelector() as selector:
            selector.register(process.stdout, selectors.EVENT_READ)
            assert selector.select(15), "reader did not report saved progress"
            assert process.stdout.readline().strip() == "processed_events=3 indexed_results=0"
        process.kill()  # Abrupt reader outage while the accepted execution awaits analysis.
        process.communicate(timeout=10)
        assert process.returncode != 0
    finally:
        if process.poll() is None:
            process.kill()
            process.communicate(timeout=10)

    call(ENGINE, "workers", "--exchange-dir", exchange, "--simulation-workers", "0", "--drain")
    completed = snapshot(exchange)
    assert completed["stages"]["done"] == 1 and completed["pending_files"] == 0
    assert completed["jobs"][0]["analysis_retries"] == 1
    assert listing(state)["results"] == [], "stopped reader somehow indexed completion"
    assert bags == {path.name: path.read_bytes() for path in (exchange / "bags").glob("*")}
    before = public_files(exchange)
    terminal = next(
        path for path in (exchange / "events").glob("*.json")
        if json.loads(path.read_text())["result"] is not None
    )
    shutil.copyfile(terminal, exchange / "events/duplicate.json")
    call(READER, "sync", "--exchange-dir", exchange, "--state-dir", state)
    indexed = listing(state)
    assert indexed["processed_events"] == 5 and len(indexed["results"]) == 1
    result = indexed["results"][0]["result"]
    assert result["status"] == "FAIL" and result["metrics"]["collision_count"]["value"] == 1
    call(READER, "sync", "--exchange-dir", exchange, "--state-dir", state,
         "events/duplicate.json", "events/duplicate.json")
    assert listing(state) == indexed
    for path, data in before.items():
        assert (exchange / path).read_bytes() == data, path

    # Results rebuild from public files even when every event and the engine database are absent.
    held_events = parent / "held-events"
    held_queue = parent / "held-queue"
    (exchange / "events").rename(held_events)
    (exchange / ".queue").rename(held_queue)
    try:
        call(READER, "rebuild", "--exchange-dir", exchange, "--state-dir", rebuilt)
        rebuilt_index = listing(rebuilt)
        assert rebuilt_index["processed_events"] == 0
        assert rebuilt_index["results"] == indexed["results"]
    finally:
        held_events.rename(exchange / "events")
        held_queue.rename(exchange / ".queue")
    assert snapshot(exchange)["dispatch_positions"] == completed["dispatch_positions"]

    # The reader preserves both analyses for one execution, including version-two metrics.
    template = result["analysis_template"]
    template["minimum_obstacle_gap"]["version"] = 2
    inputs = {"bag": result["bag"], "analysis_template": template}
    job = {
        "contract_version": 1, "kind": "job", "job_kind": "analysis",
        "job_id": "edge-score", "execution_id": result["execution_id"], "priority": 1,
        "inputs": inputs,
        "inputs_hash": hashlib.sha256(json.dumps(inputs, sort_keys=True, separators=(",", ":")).encode()).hexdigest(),
    }
    (exchange / "jobs/edge-score.json").write_text(json.dumps(job) + "\n")
    call(ENGINE, "enqueue", "--exchange-dir", exchange, "jobs/edge-score.json")
    call(ENGINE, "workers", "--exchange-dir", exchange, "--simulation-workers", "0", "--drain")
    call(READER, "sync", "--exchange-dir", exchange, "--state-dir", state)
    two = listing(state)
    assert len(two["results"]) == 2
    assert len({item["result"]["analysis_id"] for item in two["results"]}) == 2
    assert {item["result"]["metrics"]["minimum_obstacle_gap"]["value"] for item in two["results"]} == {0, 3400}
    print("Reader outage, restart, duplicate delivery, event-free rebuild, and second analysis: passed")


def check_failures(parent):
    for stage, count in (("simulation", 1), ("simulation", 2), ("analysis", 2)):
        experiment = parent / (stage + str(count))
        experiment.mkdir()
        exchange = prepare(experiment)
        call(ENGINE, "fault", "--exchange-dir", exchange, "--job", "record-candidate",
             "--stage", stage, "--failures", count)
        call(ENGINE, "workers", "--exchange-dir", exchange, "--drain")
        job = snapshot(exchange)["jobs"][0]
        assert job[stage + "_retries"] == 1
        assert job["state"] == ("FAIL" if count == 1 else "ERROR")
        before = public_files(exchange)
        call(ENGINE, "fault", "--exchange-dir", exchange, "--job", "record-candidate",
             "--stage", stage, "--failures", count)
        call(ENGINE, "workers", "--exchange-dir", exchange, "--drain")
        assert before == public_files(exchange)
        state = experiment / "reader-state"
        state.mkdir(mode=0o700)
        call(READER, "sync", "--exchange-dir", exchange, "--state-dir", state)
        outcome = listing(state)["results"][0]["result"]
        assert outcome["status"] == job["state"]
        if count == 2:
            assert outcome["failure_class"] == "worker_failure" and outcome["metrics"] is None
    print("Deterministic failures, bounded recovery, immutable outcomes, and error indexing: passed")


if __name__ == "__main__":
    with tempfile.TemporaryDirectory(prefix="yamata-operations-") as directory:
        parent = Path(directory)
        check_outage(parent)
        check_failures(parent)
