# Priority and bounded recovery

Queued jobs use four priority classes and one worker-failure retry per stage.
Priority controls the next claim; it never interrupts running work.
Recovery preserves accepted bags, results, and events.
The [worker guide](workers.md) explains intake, pool sizes, leases, and file delivery.

## Priority classes

The job's existing `priority` field selects a class.
Both worker pools use the same class order.
A run's analysis inherits its priority; an analysis-only job supplies its own priority.

| Priority | Normal dispatch order |
| ---: | --- |
| 0 | First: highest class. |
| 1 | Second. |
| 2 | Third. |
| 3 | Fourth: lowest class, with a reserved dispatch. |

Within each class, the first imported eligible job goes first.
The original intake order also breaks ties in analysis and retries.
Each pool reserves dispatches 10, 20, 30, and subsequent multiples of ten for an eligible class-three job.
If class three has no eligible job, that dispatch uses the normal priority order.
The reservation does not transfer to another class or accumulate unused slots.

A job is eligible when its stage matches, its lease is available, and its preceding outbox files are acknowledged.
A leased job or unpublished handoff cannot occupy the reserved slot.
Only committed claims advance the dispatch counter.
Empty polls and rolled-back claims do not consume slots.
Simulation and analysis counters are independent and persist across process restarts.

All processes serving one pool share its database counter.
A worker claims the selected job and advances that counter in the same transaction.
A long-running lower-priority job keeps its lease when higher-priority work arrives.
More worker processes add capacity; they do not preempt running work.

The reservation gives class three service during a sustained higher-priority backlog.
Classes one and two have no separate reservation; continuous higher-priority arrivals can delay them.
The policy does not guarantee a wall-clock completion time.
Changing an accepted job's priority remains an identity conflict.
Submit new job and execution identities for changed simulation inputs.
For changed scoring, submit a new analysis job against the original recording; see [reanalysis](reanalysis.md).

## Failure classes and recovery

| Condition | Expected outcome | Automatic action | Recovery command |
| --- | --- | --- | --- |
| First `worker_failure` in a stage | The stage remains queued; no terminal result yet. | Retry that stage once. | Keep `yamata workers --exchange-dir "$exchange"` running. |
| Second `worker_failure` in that stage | `ERROR`, with failure class `worker_failure`. | None. | Correct the cause, then enqueue a new job with new IDs. |
| `controller_failure` | `ERROR`, without a bag or analysis reference. | None. | Correct the controller inputs, then enqueue a new job with new IDs. |
| `simulation_timeout` | `ERROR`, without a bag or analysis reference. | None. | Review the scenario and limits, then enqueue a new job with new IDs. |
| `analysis_failure` | `ERROR`, preserving the bag and analysis identity. | None. | Correct the analysis configuration, then enqueue a new job with new IDs. |
| Failed scores | `FAIL`, with calculated metrics and evidence. | None. | Review the metrics; changed inputs require a new job. |
| Unavailable metrics without failures | `WARN`, with explicit null values. | None. | Review metric availability before submitting changed work. |
| Expired stage lease | The unfinished stage becomes eligible again. | Recover that stage with a new generation. | Restart `yamata workers --exchange-dir "$exchange" --drain`. |
| Storage or publication error | The command reports an error; committed state remains. | No worker-failure retry is consumed. | Resolve the storage problem, then restart the workers. |

Simulation and analysis have separate retry allowances.
A simulation retry does not consume the analysis allowance.
A second explicit worker failure ends that stage even after process restart or duplicate job delivery.
Analysis retries read the accepted bag; they do not repeat simulation.
The retry reenters its existing priority class and retains its original intake order.

A worker computation panic is contained and classified as `worker_failure`.
Panic contents are not copied into results, events, or command errors.
Unsupported simulator versions also use the existing `worker_failure` classification and can exhaust the retry.
Unsupported controller versions and exhausted simulation limits remain non-retryable.
Unsupported metric versions produce `analysis_failure`.

The first worker failure commits a retry record, a new attempt ID, and a nonterminal event together.
That event repeats `RUNNING` or `ANALYZING` for the new attempt.
Its sequence continues the job's existing event order.
The final result references the accepted terminal attempt.
Duplicate intake cannot clear retry records or reopen a terminal outcome.

Lease recovery differs from an explicitly reported worker failure.
An expired lease can represent an interrupted computation with no accepted outcome.
Recovery preserves that attempt ID and does not spend the worker-failure retry allowance.
Each claim, including recovery, increases a durable generation number.
Tokens, generations, stage identity, and lease expiration fence acceptance inside one database transaction.
A stale worker cannot renew ownership, spend a retry, or publish a replacement outcome.

Repeated abrupt exits can cause repeated unfinished computations.
The one-retry limit bounds explicitly reported worker failures, not operator restarts or lease recoveries.
Accepted output bytes still have one immutable owner.

## Observe failure outcomes

Prerequisites: the [README tools and dependencies](../README.md#run-the-first-example), Python 3, a POSIX shell, and a writable temporary directory.
From the repository root, run these commands in one shell:

```sh
make build
exchange=$(mktemp -d)
mkdir "$exchange/jobs"
python3 - "$exchange" <<'PY'
import hashlib
import json
from pathlib import Path
import sys

exchange = Path(sys.argv[1])
for name, component, field, value in [
    ("controller-failure", "controller", "version", 2),
    ("simulation-timeout", "limits", "max_ticks", 1),
    ("worker-failure", "simulator", "version", 2),
]:
    job = json.loads(Path("examples/jobs/candidate.json").read_text())
    job["job_id"] = name
    job["execution_id"] = name
    job["inputs"][component][field] = value
    canonical = json.dumps(job["inputs"], sort_keys=True, separators=(",", ":"))
    job["inputs_hash"] = hashlib.sha256(canonical.encode()).hexdigest()
    (exchange / "jobs" / (name + ".json")).write_text(json.dumps(job, indent=2) + "\n")
PY
for job in controller-failure simulation-timeout worker-failure
do
  ./bin/yamata enqueue --exchange-dir "$exchange" "jobs/$job.json"
done
./bin/yamata workers --exchange-dir "$exchange" --drain
python3 - "$exchange" <<'PY'
import json
from pathlib import Path
import sys

exchange = Path(sys.argv[1])
for path in sorted((exchange / "results").glob("*.json")):
    result = json.loads(path.read_text())
    print(result["job_id"], result["status"], result["failure_class"])
events = [json.loads(path.read_text()) for path in (exchange / "events").glob("*.json")]
for job in ("controller-failure", "simulation-timeout", "worker-failure"):
    attempts = {event["attempt_id"] for event in events if event["job_id"] == job}
    print(job, "attempts=" + str(len(attempts)))
PY
for event in "$exchange"/events/*.json
do
  ./bin/yamata validate --exchange-dir "$exchange" "events/${event##*/}"
done
```

Expect three `ERROR` results with their corresponding failure classes.
Controller failure and simulation timeout each have one attempt; worker failure has two.
The worker command returns exit code `0` because queue processing completed.
Each validator call prints `Contract valid.`.
The unchanged simulator version fails on its retry, demonstrating the bound.

For corrected work, prepare a complete job with new job and execution IDs and an updated input hash.
Enqueue that file with `yamata enqueue --exchange-dir "$exchange" jobs/corrected.json`, then run the workers again.
Do not edit accepted files or delete retry records to rerun a terminal identity.
After the example, remove its temporary exchange and binary:

```sh
rm -r "$exchange"
make clean
```

## Verify reservation and successful recovery

Prerequisites: the installed tools and dependencies from the README.
From the repository root, run:

```sh
go test ./internal/queue -run 'TestPriority|TestTenth|TestReservation|TestConcurrentClaims|TestWorkerFailure|TestOnlyWorker|TestRetry|TestStale' -count=1
```

These tests inject worker failures during simulation and analysis and verify one successful retry in each stage.
They also check retry exhaustion, unchanged bags, non-retryable failures, priority order, reservation slots, and stale-worker rejection.
Temporary databases exercise restart and concurrent claim behavior.
Expect exit code `0`.
Tests remove their temporary data automatically; no manual cleanup is needed.

The package tests also use an internal computation seam.
The [operations guide](operations.md#deterministic-failures-and-recovery) describes public deterministic failure controls.

## Upgrade an existing queue

Stop every old worker and intake process before opening the exchange with the new binary.
Keep a complete backup of the stopped exchange if you need to restore the previous binary.
Do not copy only the main SQLite file while a writer is active.

The new binary transactionally upgrades private queue schema versions one through three to version four on open.
It reads each validated saved job and preserves accepted bytes, events, identities, and unfinished stages.
The upgrade also preserves existing retry records, dispatch positions, and lease generations.
It reserves each original analysis identity before accepting new analysis jobs.

Invalid saved jobs roll back the complete upgrade.
Unknown database versions are rejected.
Public contract version one remains unchanged.

Dispatch cycles start at zero only when upgrading version one, which had no dispatch counters.
Existing leases remain in place; their later recovery receives a new generation.
Previously terminal outcomes remain terminal and gain no new retry allowance.
A new-schema database cannot be opened by the previous binary.
Use the [worker walkthrough](workers.md#import-and-process-a-job) to start the new workers after the upgrade.
