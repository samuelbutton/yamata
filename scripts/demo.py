"""Run the public walkthrough using only built commands and published files."""

import argparse
import json
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]
ENGINE = ROOT / "bin/yamata"
READER = ROOT / "bin/reader"
REFERENCE = ROOT / "examples/reference/v1"
MARKER = ".yamata-demo"
MARKER_BYTES = b"Yamata demo directory version 1\n"


def call(binary, *args, succeeds=True):
    result = subprocess.run(
        [str(binary), *map(str, args)], capture_output=True, text=True, timeout=30
    )
    if (result.returncode == 0) != succeeds:
        raise RuntimeError(f"Command failed: {args}\n{result.stdout}{result.stderr}")
    return result.stdout


def snapshot(exchange):
    return json.loads(call(ENGINE, "queue", "--exchange-dir", exchange))


def listing(state):
    return json.loads(call(READER, "list", "--state-dir", state))


def public_files(exchange):
    return {
        str(path.relative_to(exchange)): path.read_bytes()
        for folder in ("jobs", "bags", "results", "events")
        for path in sorted((exchange / folder).glob("*"))
        if path.is_file()
    }


def clean(directory):
    """Remove only a marked demo directory; never follow its ancestor links."""
    if any(path.is_symlink() for path in (directory, *directory.parents)):
        raise ValueError("Demo cleanup refuses symbolic links.")
    if not directory.exists():
        return
    marker = directory / MARKER
    if marker.is_symlink() or not marker.is_file() or marker.read_bytes() != MARKER_BYTES:
        raise ValueError("Demo cleanup requires its original ownership marker.")
    shutil.rmtree(directory)


def run(directory):
    if any(path.is_symlink() for path in (directory, *directory.parents)):
        raise ValueError("The demo refuses symbolic links in its output path.")
    directory.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    directory.mkdir(mode=0o700)  # Exclusive creation preserves every existing experiment.
    (directory / MARKER).write_bytes(MARKER_BYTES)
    exchange = directory / "exchange"
    state = directory / "reader"
    rebuilt = directory / "rebuilt"
    for path in (exchange, state, rebuilt):
        path.mkdir(mode=0o700)
    shutil.copytree(REFERENCE / "jobs", exchange / "jobs")

    call(ENGINE, "enqueue", "--exchange-dir", exchange, "jobs/candidate.json")
    call(ENGINE, "fault", "--exchange-dir", exchange, "--job", "record-candidate",
         "--stage", "analysis", "--failures", "1")
    call(ENGINE, "workers", "--exchange-dir", exchange, "--analysis-workers", "0", "--drain")
    call(READER, "sync", "--exchange-dir", exchange, "--state-dir", state)
    assert listing(state) == {"processed_events": 3, "results": []}
    print("Recording saved; the reader has stopped before analysis.", flush=True)

    call(ENGINE, "workers", "--exchange-dir", exchange, "--simulation-workers", "0", "--drain")
    first = snapshot(exchange)
    assert first["stages"]["done"] == 1 and first["pending_files"] == 0
    assert first["jobs"][0]["analysis_retries"] == 1
    assert listing(state)["results"] == []
    before = public_files(exchange)
    call(READER, "sync", "--exchange-dir", exchange, "--state-dir", state)
    accepted = listing(state)
    assert accepted["processed_events"] == 5 and len(accepted["results"]) == 1
    terminal = next(path for path, data in before.items()
                    if path.startswith("events/") and json.loads(data)["result"] is not None)
    call(READER, "sync", "--exchange-dir", exchange, "--state-dir", state, terminal, terminal)
    assert listing(state) == accepted
    print("Analysis recovered once while the reader was down; duplicate reading changed nothing.", flush=True)

    call(ENGINE, "enqueue", "--exchange-dir", exchange, "jobs/edge-score.json")
    call(ENGINE, "workers", "--exchange-dir", exchange, "--simulation-workers", "0", "--drain")
    assert snapshot(exchange)["dispatch_positions"]["simulation"] == first["dispatch_positions"]["simulation"]
    for path, data in before.items():
        assert (exchange / path).read_bytes() == data, path
    call(READER, "sync", "--exchange-dir", exchange, "--state-dir", state)
    indexed = listing(state)
    assert indexed["processed_events"] == 8 and len(indexed["results"]) == 2
    results = sorted((item["result"] for item in indexed["results"]),
                     key=lambda result: result["metrics"]["minimum_obstacle_gap"]["version"])
    assert len({result["analysis_id"] for result in results}) == 2
    assert results[0]["bag"] == results[1]["bag"]
    assert len(list((exchange / "bags").iterdir())) == 1
    for result, value in zip(results, (3400, 0)):
        gap = result["metrics"]["minimum_obstacle_gap"]
        assert gap["value"] == value and result["status"] == "FAIL"
        assert result["metrics"]["collision_count"]["value"] == 1
        print(f"Gap version {gap['version']}: {value} mm; collision count: 1; outcome: FAIL.", flush=True)

    # Rebuild proves that public results remain sufficient without notifications or queue state.
    (exchange / "events").rename(directory / "held-events")
    (exchange / ".queue").rename(directory / "held-queue")
    try:
        call(READER, "rebuild", "--exchange-dir", exchange, "--state-dir", rebuilt)
        assert listing(rebuilt) == {"processed_events": 0, "results": indexed["results"]}
    finally:
        (directory / "held-events").rename(exchange / "events")
        (directory / "held-queue").rename(exchange / ".queue")
    final = public_files(exchange)
    for job in ("candidate", "edge-score"):
        call(ENGINE, "enqueue", "--exchange-dir", exchange, f"jobs/{job}.json")
    call(ENGINE, "workers", "--exchange-dir", exchange, "--drain")
    assert public_files(exchange) == final
    call(ENGINE, "validate", "--exchange-dir", exchange, *final)
    (directory / "index.json").write_text(json.dumps(indexed, indent=2) + "\n")
    print("One bag, two results, eight events; duplicate jobs and event-free rebuild: passed.", flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--clean", action="store_true", help="Remove only the marked .yamata/demo directory.")
    args = parser.parse_args()
    try:
        destination = ROOT / ".yamata/demo"
        if args.clean:
            clean(destination)
            print("Demo cleanup complete; other data is preserved.")
        else:
            run(destination)
            print("Read .yamata/demo/index.json and .yamata/demo/exchange/. Clean up with make demo-clean.")
    except (OSError, ValueError, RuntimeError, AssertionError, subprocess.TimeoutExpired) as error:
        parser.exit(1, f"Demo failed: {error}\nExisting output is preserved. Use make demo-clean before another demo.\n")
