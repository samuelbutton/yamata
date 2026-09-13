# Yamata

## In plain language

Yamata shows how a computer can test a driving decision in a simplified virtual world.
A vehicle approaches an obstacle, and different braking rules show what happens when it brakes early or too late.
The project records what happened and checks results, such as whether the vehicle hit the obstacle or reached its goal.

You can check the same recording with different scoring rules without repeating the drive.
The project also shows how interrupted work can continue and how another program can read the results independently.
It is a small learning example that runs on your own computer.

## Technical summary

Yamata is a local teaching project for simulation, saved recordings, and versioned scoring.
A deliberately late braking controller reaches an obstacle.
Workers save the motion and calculate scores from that recording.
An independent reader indexes the published outcomes in its own database.

## Run the first example

Prerequisites: Go 1.25.13, GNU Make 3.81 or later, Python 3.11 or later, and a POSIX shell.
Use an ordinary account with write access to this checkout on macOS or Linux.
The local filesystem must support locks, hard links, and directory synchronization.
No other checkout, service, credential, or C compiler is required.

Install the pinned dependencies from the repository root:

```sh
go mod download
(cd examples/reader && go mod download)
```

These commands download the declared dependencies into Go's module cache.
Keep that cache for builds and tests.
All remaining commands work without network access after installation.

From the repository root, run:

```sh
make demo
cat .yamata/demo/index.json
```

The demo builds both commands and uses the [versioned jobs](examples/reference/v1/jobs/).
It saves one bag, recovers one analysis failure, and calculates two results against the same recording.
The second analysis runs with simulation disabled.
Both results contain one collision and report `FAIL`.

| Gap calculation | Minimum gap | Outcome |
| --- | ---: | --- |
| Version 1: center distance | 3,400 mm | `FAIL` |
| Version 2: body-edge clearance | 0 mm | `FAIL` |

Workers finish while the reader is stopped.
The restarted reader handles duplicate events, then a separate reader rebuilds from results without events or producer state.
Duplicate jobs preserve every published byte.
The final output reports one bag, two results, eight events, and `passed`.
The command returns exit code `0` when these checks succeed; it does not require passing simulation scores.

The demo preserves its exchange, reader databases, and readable index under `.yamata/demo/`.
It refuses an existing output directory so repeated commands cannot replace an experiment.
After inspecting the files, clean up from the repository root:

```sh
make demo-clean
make clean
```

The first command removes only the demo directory with its original ownership marker.
It rejects symbolic links in that directory's path and preserves other data under `.yamata/`.
The second command removes only `bin/yamata` and `bin/reader`.
Run `make demo` again after cleanup to create another experiment.

## Verify the project

Prerequisites: the tools and installed dependencies above.
From the repository root, run:

```sh
make verify
make clean
```

Verification checks formatting, builds, tests, and static checks in both Go modules.
It runs the demo in a temporary directory and compares its stable content with the [reference exchange](examples/reference/README.md).
It also checks cleanup boundaries, worker recovery, reader interruption, duplicate delivery, and failure outcomes.
A copied reader builds separately and consumes only published files.
Expect lines ending in `passed` and exit code `0`.

Verification removes its temporary exchanges and reader databases automatically.
It preserves existing demo data and source files.
The cleanup command removes the built binaries.
See [troubleshooting](docs/troubleshooting.md) if a command fails.

## Run a simulation

Prerequisites: the tools and dependencies listed above.
From the repository root, run:

```sh
make build
./bin/yamata simulate --controller baseline
./bin/yamata simulate --controller candidate
```

The default scenario contains a stopped obstacle.
The baseline stops before contact. The candidate brakes late and reaches contact.
Each command prints its final observation and returns exit code `0` after a completed simulation.
A collision is an observed event, not a passing score.
The command writes no data files.

Run `make clean` to remove the built binaries.
Read the [simulator guide](docs/simulator.md) for all scenarios, equations, and model limits.

## Data directory behavior

Prerequisites: the tools and dependencies above.
From the repository root, run:

```sh
make build
./bin/yamata --help
./bin/yamata init
./bin/yamata init --data-dir .yamata
```

Help lists the commands without creating data.
Initialization prints `Data directory ready:` and the absolute path in quotes.
Repeating initialization preserves existing data.
For cleanup, run `make clean` to remove the binaries.
Remove `.yamata` with `rmdir .yamata` only when it is empty.

To save a simulation, follow the [bag walkthrough](docs/bags.md#save-and-inspect-a-bag).
It uses complete job files from [examples/jobs](examples/jobs/) and keeps generated bags in a temporary exchange directory.
To record and score a job, follow the [metrics walkthrough](docs/metrics.md#run-and-score-a-job).
The `run` command returns exit code `0` only for a `PASS` outcome.
To import jobs and run durable worker pools, follow the [worker walkthrough](docs/workers.md#import-and-process-a-job).

Use separate exchange directories for standalone and queued execution.

Read the [recovery guide](docs/recovery.md) for priority order, retry limits, and existing queue upgrades.
Follow the [reanalysis walkthrough](docs/reanalysis.md) to compare two gap versions against one unchanged recording.
Use the [operations walkthrough](docs/operations.md) to inspect queues, control failures, and recover an independent reader.

Use `yamata init --data-dir PATH` to select a different directory.
Relative paths start at the current directory, regardless of the binary location.
The setting applies to that command only. Yamata does not save the selected path.
Choose a directory reserved for Yamata data.
The operating system resolves symbolic links in the selected path.

The command creates missing directories with owner access only, subject to the process permission mask.
It checks file creation, writing, closing, and removal with a temporary file.
It preserves existing files and directory permissions.
Success confirms access at the time of the check; later writes can still fail.
If a check fails, newly created directories can remain.

Successful utility commands return exit code `0`; `run` also requires a passing score.
Invalid arguments, storage failures, and output failures return exit code `1`.
Errors appear on standard error. Help and confirmation appear on standard output.
Help does not create a data directory.

## Verify a rejected directory

Prerequisites: the built binaries, a POSIX shell, and an ordinary user account.
From the repository root, run:

```sh
make build
check_dir=$(mktemp -d)
chmod 500 "$check_dir"
./bin/yamata init --data-dir "$check_dir"
echo "$?"
chmod 700 "$check_dir"
rmdir "$check_dir"
```

Expect an error and exit code `1`.
The last two commands restore access and remove the empty test directory.
An administrator account can bypass permission checks. Do not use one for this example.

## Code tour

Follow the [code tour](docs/code-tour.md) from complete job inputs through recording, scoring, publication, and independent reading.
The [decision notes](docs/decisions.md) explain the boundaries and their costs.
The [reference guide](examples/reference/README.md) supplies generated files for another reader implementation.

Read the [contribution guide](CONTRIBUTING.md), [glossary](docs/glossary.md), and [writing rules](docs/writing.md) before changing the project.
Follow the [exchange contract walkthrough](docs/contract.md) to trace a job through its event and result.
The project uses the [Apache License 2.0](LICENSE).
