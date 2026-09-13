# Code tour

Start with the [complete demo](../README.md#run-the-first-example), then follow one execution through the files below.
The [decision notes](decisions.md) explain why these boundaries exist.
Each command passes explicit paths and configuration into the packages that own the operation.

## Follow the candidate job

1. Open [candidate.json](../examples/reference/v1/jobs/candidate.json).
   This complete run job identifies the scenario, controller, simulator, limits, and first analysis template.
   Its input hash covers every resolved input.
2. Follow [command dispatch](../cmd/yamata/main.go) into [queue commands](../cmd/yamata/workers.go).
   Intake validates the job, stores its exact bytes, and commits its first event before returning a receipt.
3. Read [store.go](../internal/queue/store.go) and [lease.go](../internal/queue/lease.go).
   A stage claim selects eligible work by priority and assigns temporary ownership.
   [retry.go](../internal/queue/retry.go) commits the single worker-failure retry without resetting accepted state.
4. Follow [workers.go](../internal/queue/workers.go) into [record.go](../internal/bag/record.go).
   The recorder maps resolved inputs into the [simulator](../internal/simulator/simulator.go).
   The [controller](../internal/simulator/controller.go) supplies braking decisions; shared geometry determines contact.
5. Inspect the [reference bag](../examples/reference/v1/bags/stopped-candidate.jsonl).
   Its records contain integer motion, independent of attempts or wall-clock time.
   [metrics.go](../internal/metrics/metrics.go) calculates scores from that bag and the original geometry.
6. Follow [outbox.go](../internal/queue/outbox.go) into [publication.go](../internal/publication/publication.go).
   Acceptance stores exact bytes before publication.
   The publisher validates, synchronizes, and publishes complete files without replacing existing identities.
   It publishes the result before its completion event.
7. Follow the [reader command](../examples/reader/main.go) through [exchange.go](../examples/reader/exchange.go) and [index.go](../examples/reader/index.go).
   It checks its own contract copy and commits event progress with the referenced result.
   Rebuild derives the result index from published files without queue access.
8. Open [edge-score.json](../examples/reference/v1/jobs/edge-score.json) and [analysis.go](../internal/queue/analysis.go).
   The second job retains the execution and bag identities but selects gap version two.
   It enters analysis directly and preserves the first result.

## Find a responsibility

| Path | Responsibility |
| --- | --- |
| [cmd/yamata/](../cmd/yamata/) | Arguments, process configuration, output, and exit codes. |
| [internal/datadir/](../internal/datadir/) | Explicit directory creation and access checks. |
| [contract/v1/](../contract/v1/) | Public schemas and synthetic compatibility fixtures. |
| [internal/contract/](../internal/contract/) | Confined file reads, schemas, complete bags, hashes, and relationships. |
| [internal/simulator/](../internal/simulator/) | Pure integer motion and built-in examples. |
| [internal/geometry/](../internal/geometry/) | Shared swept-contact calculation. |
| [internal/bag/](../internal/bag/) | Recording conversion and deterministic bag encoding. |
| [internal/metrics/](../internal/metrics/) | Versioned values, evidence, availability, and aggregate scores. |
| [internal/execution/](../internal/execution/) | Analysis identities and explicit failure outcomes. |
| [internal/standalone/](../internal/standalone/) | One-command execution and completion-event repair on explicit retry. |
| [internal/queue/](../internal/queue/) | Durable intake, scheduling, leases, retries, outbox, inspection, and upgrades. |
| [internal/publication/](../internal/publication/) | Complete immutable file publication. |
| [internal/ownership/](../internal/ownership/) and [internal/filelock/](../internal/filelock/) | Exchange ownership and cooperating local process locks. |
| [examples/reader/](../examples/reader/) | Independent validation, reader-owned progress, and result indexing. |
| [scripts/demo.py](../scripts/demo.py) | Demo orchestration through the public commands. |
| [scripts/reference.py](../scripts/reference.py) | Export of public files and provenance into a new reference directory. |
| [tests/walkthrough.py](../tests/walkthrough.py) | Reference comparisons, cleanup guards, and detached reader verification. |
| [tests/operations.py](../tests/operations.py) | Process interruption, duplicate delivery, reader recovery, and controlled failures. |

## Trace a saved outcome

Prerequisites: the tools and dependencies from the README and an unused demo output directory.
From the repository root, run:

```sh
make demo
./bin/yamata queue --exchange-dir .yamata/demo/exchange
./bin/reader list --state-dir .yamata/demo/reader
cat .yamata/demo/exchange/jobs/edge-score.json
```

The queue shows two completed jobs and no unpublished files.
The reader lists two distinct analyses for one execution and one bag hash.
The analysis job selects edge clearance while leaving collision and progress versions unchanged.
After tracing the files, clean up from the same directory:

```sh
make demo-clean
make clean
```

These commands remove the marked demo output and built binaries.
Other data directories remain unchanged.
