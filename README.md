# Yamata

Yamata is a teaching project for local simulation execution and analysis.
The first example prepares a data directory through a command-line interface (CLI).
The exchange validator checks versioned files and their references without changing them.
The simulator runs three built-in scenarios with two braking controllers.
The recorder saves immutable bags, and the inspector checks their content hashes and recorded motion.
Scoring and workers belong to later changes.

## Run the first example

Prerequisites: Go 1.25.13, GNU Make 3.81 or later, and a POSIX shell on macOS or Linux.
Use an ordinary user account with write access to the checkout.
Install the pinned Go dependencies with `go mod download` from the repository root.
Builds, tests, and commands need no network connection after you install the tools and dependencies.

From the repository root, run:

```sh
make build
./bin/yamata --help
./bin/yamata init
```

The help command lists the available commands.
The `init` command creates `.yamata` in the current directory.
It prints `Data directory ready:` followed by the absolute path in quotes.
The directory is empty after the check.

To check the same directory again, run:

```sh
./bin/yamata init --data-dir .yamata
```

The command reports the same path and preserves existing files.
For cleanup after this example, run:

```sh
rmdir .yamata
make clean
```

The `rmdir` command removes only an empty directory.
The `make clean` command removes the built binary and preserves data directories.

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

Run `make clean` to remove the built binary.
Read the [simulator guide](docs/simulator.md) for all scenarios, equations, and model limits.

## Data directory behavior

To save a simulation, follow the [bag walkthrough](docs/bags.md#save-and-inspect-a-bag).
It uses complete job files from [examples/jobs](examples/jobs/) and keeps generated bags in a temporary exchange directory.

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

All successful commands return exit code `0`.
Invalid arguments, storage failures, and output failures return exit code `1`.
Errors appear on standard error. Help and confirmation appear on standard output.
Help does not create a data directory.

## Verify a rejected directory

Prerequisites: the built binary, a POSIX shell, and an ordinary user account.
From the repository root, run:

```sh
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

| Path | Responsibility |
| --- | --- |
| [cmd/yamata/main.go](cmd/yamata/main.go) | Parses commands, selects the data directory, and reports errors. |
| [internal/datadir/dir.go](internal/datadir/dir.go) | Creates the directory and checks file access. |
| [contract/v1/](contract/v1/) | Publishes schemas, compatibility examples, and file hashes. |
| [internal/contract/](internal/contract/) | Validates file shapes, identities, references, and hashes. |
| [cmd/yamata/validate.go](cmd/yamata/validate.go) | Exposes the read-only exchange validator. |
| [internal/simulator/simulator.go](internal/simulator/simulator.go) | Advances integer motion and checks contact, goals, and limits. |
| [internal/simulator/controller.go](internal/simulator/controller.go) | Selects when each built-in controller starts braking. |
| [internal/simulator/examples.go](internal/simulator/examples.go) | Defines the three example scenarios. |
| [cmd/yamata/simulate.go](cmd/yamata/simulate.go) | Runs an example and prints its final observation. |
| [internal/bag/record.go](internal/bag/record.go) | Maps resolved jobs to simulations and encodes complete bags. |
| [internal/bag/publish.go](internal/bag/publish.go) | Publishes complete files without replacing conflicting output. |
| [internal/contract/bag.go](internal/contract/bag.go) | Reads complete bags and verifies pinned file hashes. |
| [cmd/yamata/record.go](cmd/yamata/record.go) | Records a job and prints its bag hash. |
| [cmd/yamata/inspect.go](cmd/yamata/inspect.go) | Inspects saved motion without running a simulation. |
| [cmd/yamata/main_test.go](cmd/yamata/main_test.go) | Checks command behavior, defaults, errors, and help without writes. |
| [internal/datadir/dir_test.go](internal/datadir/dir_test.go) | Checks file preservation, cleanup, invalid paths, and permission failures. |

Command parsing passes an explicit path to the storage package.
The storage package reads no environment variables and writes no console output.
There are no background processes or external services.

Read the [contribution guide](CONTRIBUTING.md), [glossary](docs/glossary.md), and [writing rules](docs/writing.md) before changing the project.
Follow the [exchange contract walkthrough](docs/contract.md) to trace a job through its event and result.
The project uses the [Apache License 2.0](LICENSE).
