# Yamata

Yamata is a teaching project for local simulation execution and analysis.
The first example prepares a data directory through a command-line interface (CLI).
Simulation, recordings, scoring, and workers belong to later changes.

## Run the first example

Prerequisites: Go 1.25, GNU Make 3.81 or later, and a POSIX shell on macOS or Linux.
Use an ordinary user account with write access to the checkout.
The module uses only the Go standard library.
Builds, tests, and commands need no network connection after you install the tools.

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

## Data directory behavior

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
| [cmd/yamata/main_test.go](cmd/yamata/main_test.go) | Checks command behavior, defaults, errors, and help without writes. |
| [internal/datadir/dir_test.go](internal/datadir/dir_test.go) | Checks file preservation, cleanup, invalid paths, and permission failures. |

Command parsing passes an explicit path to the storage package.
The storage package reads no environment variables and writes no console output.
There are no background processes or external services.

Read the [contribution guide](CONTRIBUTING.md), [glossary](docs/glossary.md), and [writing rules](docs/writing.md) before changing the project.
The project uses the [Apache License 2.0](LICENSE).
