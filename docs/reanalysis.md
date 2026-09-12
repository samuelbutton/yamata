# Score a saved bag again

An analysis job scores an accepted recording with a new analysis template.
It preserves the execution ID and bag hash, then publishes a separate result.
The analysis pool reads the bag without invoking simulation.
Previous results and events remain unchanged.

## Compare both gap versions

Prerequisites: the [README tools and dependencies](../README.md#run-the-first-example), Python 3, a POSIX shell, and a writable temporary directory.
From the repository root, run these commands in one shell:

```sh
make build
exchange=$(mktemp -d)
cp -R examples/jobs "$exchange/jobs"
./bin/yamata enqueue --exchange-dir "$exchange" jobs/candidate.json
./bin/yamata workers --exchange-dir "$exchange" --drain
cp -R "$exchange/bags" "$exchange/original-bags"
cp -R "$exchange/results" "$exchange/original-results"
cp -R "$exchange/events" "$exchange/original-events"
python3 - "$exchange" <<'PY'
import hashlib
import json
from pathlib import Path
import sys

exchange = Path(sys.argv[1])
original = json.loads(next((exchange / "results").glob("*.json")).read_text())
template = original["analysis_template"]
template["minimum_obstacle_gap"]["version"] = 2
inputs = {"bag": original["bag"], "analysis_template": template}
canonical = json.dumps(inputs, sort_keys=True, separators=(",", ":"))
job = {
    "contract_version": 1,
    "kind": "job",
    "job_kind": "analysis",
    "job_id": "edge-score",
    "execution_id": original["execution_id"],
    "priority": 1,
    "inputs": inputs,
    "inputs_hash": hashlib.sha256(canonical.encode()).hexdigest(),
}
(exchange / "jobs/edge-score.json").write_text(json.dumps(job, indent=2) + "\n")
PY
./bin/yamata enqueue --exchange-dir "$exchange" jobs/edge-score.json
./bin/yamata workers --exchange-dir "$exchange" --simulation-workers 0 --analysis-workers 1 --drain
diff -r "$exchange/original-bags" "$exchange/bags"
python3 - "$exchange" <<'PY'
import hashlib
import json
from pathlib import Path
import sys

exchange = Path(sys.argv[1])
for folder in ("results", "events"):
    for before in (exchange / ("original-" + folder)).glob("*.json"):
        assert before.read_bytes() == (exchange / folder / before.name).read_bytes()
results = [json.loads(path.read_text()) for path in (exchange / "results").glob("*.json")]
results.sort(key=lambda result: result["metrics"]["minimum_obstacle_gap"]["version"])
assert len(results) == 2
assert results[0]["analysis_id"] != results[1]["analysis_id"]
assert results[0]["bag"] == results[1]["bag"]
assert len(list((exchange / "bags").glob("*.jsonl"))) == 1
bag = results[0]["bag"]
assert hashlib.sha256((exchange / bag["path"]).read_bytes()).hexdigest() == bag["sha256"]
print("recording", bag["path"], bag["sha256"])
for result in results:
    gap = result["metrics"]["minimum_obstacle_gap"]
    print("result", "results/" + result["analysis_id"] + ".json")
    print("gap", gap["version"], gap["value"], gap["unit"], "status", result["status"])
PY
for event in "$exchange"/events/*.json
do
  ./bin/yamata validate --exchange-dir "$exchange" "events/${event##*/}"
done
```

Expect one unchanged recording and two distinct result paths:

| Recording | Result | Minimum obstacle gap | Aggregate status |
| --- | --- | ---: | --- |
| `bags/stopped-candidate.jsonl` | Original analysis, gap version 1 | 3,400 mm | `FAIL` |
| The same file and SHA-256 hash | New analysis, gap version 2 | 0 mm | `FAIL` |

The commands print the complete bag hash and both analysis IDs in their result paths.
All byte comparisons succeed, and each validator prints `Contract valid.`.
The second worker command disables simulation entirely.
The new job emits `PENDING`, `ANALYZING`, and `FAIL`, without a `RUNNING` event.
The worker command returns exit code `0` because queue processing completed.

Both results retain one collision at tick 22 and goal progress of 360,000 ppm.
At that tick, the vehicle occupies positions 21,600 through 25,600 mm.
The obstacle occupies positions 25,000 through 29,000 mm.
Their centers are 3,400 mm apart, but the bodies overlap, leaving zero edge clearance.
The unchanged zero gap limit passes both metrics; collision and progress still make the aggregate outcome `FAIL`.

To check duplicate delivery, continue in the same shell:

```sh
cp -R "$exchange/results" "$exchange/accepted-results"
cp -R "$exchange/events" "$exchange/accepted-events"
./bin/yamata enqueue --exchange-dir "$exchange" jobs/edge-score.json
./bin/yamata workers --exchange-dir "$exchange" --simulation-workers 0 --analysis-workers 1 --drain
diff -r "$exchange/accepted-results" "$exchange/results"
diff -r "$exchange/accepted-events" "$exchange/events"
diff -r "$exchange/original-bags" "$exchange/bags"
```

Expect `duplicate=true` and no changes to any compared file.
After both checks, remove the temporary exchange and binary:

```sh
rm -r "$exchange"
make clean
```

## Geometry, identity, and recovery

Version two measures nonnegative body separation at recorded tick boundaries.
For an obstacle ahead, clearance is its rear position minus the vehicle's front position.
For an obstacle behind, clearance is the vehicle's rear position minus the obstacle's front position.
Touching or overlapping bodies have zero clearance.
The [metric definitions](metrics.md#minimum-obstacle-gap-version-two) specify sampling, evidence, and unavailable values.

Bag format one omits vehicle length and goal position.
The queue resolves those values from the immutable original run snapshot that produced the accepted bag.
The original run and published bag must already exist in this queue.
A bag copied into a fresh queue, or produced only by `record` or standalone `run`, lacks that saved context.
Intake rejects such work before accepting an analysis job.

Use a new job ID and the original execution ID for a changed template.
Supply the complete template and recalculate the analysis job's `inputs_hash`.
The result references that analysis job, while the bag retains the original run's input hash.
Changing a metric version or limit produces a new analysis hash and analysis ID.
Changing only priority, job ID, or bag path cannot create a second owner for an existing analysis identity.

An identical delivery preserves its original receipt and outcome.
A different job claiming the same analysis identity is rejected, including the original run's analysis identity.
Previous `FAIL`, `WARN`, and `ERROR` results remain immutable.
A corrected template can score an accepted bag after an earlier `analysis_failure` without rerunning simulation.
Unknown metric versions produce a separate `analysis_failure` result with null metrics.

Analysis jobs use their own priority and the existing analysis pool's reservation cycle.
They receive the same renewable leases, fencing, ordered output delivery, and single worker-failure retry as other analysis work.
Their duration starts at the first analysis claim and includes retry and recovery delays.
Missing or changed bag files stop processing as storage errors; they never trigger simulation.
See the [recovery guide](recovery.md) before upgrading an existing queue or recovering interrupted work.
