# Troubleshooting

Run commands from the repository root unless a procedure states another directory.
Use the [README](../README.md#run-the-first-example) to install tools and dependencies first.
Keep published files unchanged while diagnosing a failed command.

## Find the next action

| Observation | Meaning and action |
| --- | --- |
| `go`, `make`, or `python3` is missing | Install the README prerequisites, then retry. |
| Go cannot find a dependency offline | Install dependencies in both Go modules while connected, then repeat offline verification. |
| `make demo` reports an existing directory | The demo preserves previous output. Inspect it, then use the cleanup procedure below. |
| Demo cleanup rejects a marker or link | The path is not a recognized demo directory. Inspect its ownership and contents before removing anything manually. |
| Demo scores are `FAIL` | The candidate deliberately collides. Check metric values; successful processing does not require passing scores. |
| Standalone `run` returns exit code `1` | Read its reported outcome or error. `FAIL`, `WARN`, and `ERROR` all return `1`. |
| Queue workers exit successfully with unfinished jobs | A disabled stage can remain queued after `--drain`. Enable workers for that stage. |
| A queue reports unpublished files | Resolve the reported storage error, then restart workers to resume the outbox. |
| Duplicate intake reports a conflict | The identity or path already owns different bytes. Submit changed work under valid new identities. |
| A copied bag cannot enter analysis | The queue needs the original accepted run snapshot for geometry. Follow the [reanalysis guide](reanalysis.md). |
| Reader state belongs to another exchange | Create fresh reader state for the new canonical exchange path. |
| Reader reports a bad event or reference | Resolve the producer's publication problem, then rerun sync. Valid events can commit despite another rejected event. |
| Queue inspection requires an upgrade | Stop older processes and follow the [upgrade procedure](recovery.md#upgrade-an-existing-queue). |
| A job has terminal `worker_failure` | The retry allowance is exhausted. Correct the cause and submit a fresh job. |
| Reference verification reports drift | Review the changed inputs, formulas, or files. Do not regenerate expected results solely to silence a failure. |

## Inspect and repeat a demo

Prerequisites: an existing completed demo and the built binaries.
From the repository root, run:

```sh
./bin/yamata queue --exchange-dir .yamata/demo/exchange
./bin/reader list --state-dir .yamata/demo/reader
```

Expect two completed jobs, no pending files, eight processed events, and two indexed results.
An interrupted demo can contain fewer completed stages; inspect the reported error before cleanup.
After saving any results you need, remove only demo output and repeat:

```sh
make demo-clean
make demo
make demo-clean
make clean
```

The new demo repeats the stable scores and preserves unrelated directories.
The final commands remove that demo and its binaries.
Cleanup does not terminate processes; stop any workers or readers you started manually first.

## Recover a lost reader index

Prerequisites: a completed demo and built binaries.
From the repository root, run these commands in one shell:

```sh
state=$(mktemp -d)
./bin/reader rebuild --exchange-dir .yamata/demo/exchange --state-dir "$state"
./bin/reader list --state-dir "$state"
rm -r "$state"
```

Expect zero processed events and two results with `FAIL` outcomes.
Rebuild reads result files and their references; it does not replay events.
The final command removes only the newly created reader state.
The original exchange and original reader remain unchanged.
Use `make demo-clean` and `make clean` when the demo is no longer needed.

## Recover interrupted workers

Prerequisites: the built binary and an existing queue selected by your shell's `exchange` variable.
From the repository root, run:

```sh
./bin/yamata queue --exchange-dir "$exchange"
./bin/yamata workers --exchange-dir "$exchange" --drain
./bin/yamata queue --exchange-dir "$exchange"
```

Workers resume eligible stages and pending publication.
An abruptly stopped worker's lease must expire before another process can claim its stage.
Expect completed jobs and no pending files when no storage or input problem remains.
This recovery procedure preserves the exchange; it has no data cleanup step.
Run `make clean` after stopping all commands to remove only the binaries.
