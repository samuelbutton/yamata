# Contributing

Keep each change small enough to explain and verify on its own.
Use new code and synthetic data.
Follow the [writing rules](docs/writing.md) for documents, examples, and command help.

## Code rules

Keep command parsing and process configuration in `cmd/yamata`.
Pass explicit values to packages under `internal`.
Separate calculations from file access and other side effects.
Prefer the standard library and concrete types.
Add abstractions only when a real caller needs them.
Preserve error causes and report failures at the command boundary.

Test observable behavior with isolated temporary directories.
Include invalid input and failure cases when you add a new boundary.
Keep tests independent of network access, credentials, and other repositories.
Do not change existing data or permissions to make a check pass.

## Verify a change

Prerequisites: the tools listed in the [README](README.md#run-the-first-example) and write access to the checkout.
From the repository root, run:

```sh
make fmt
make check
```

The formatting command updates Go source formatting.
The check command requires formatted code, runs tests and static checks, and builds `bin/yamata`.
Expect exit code `0` from both commands.
Tests remove their temporary data automatically.

For cleanup, run:

```sh
make clean
```

This command removes the built binary.
It preserves source files and data directories.

Run the README examples when you change command behavior.
Run the [contract walkthrough](docs/contract.md#validate-the-example) when you change the exchange boundary.
After an intentional contract-file change, run `make generate` and review the updated file hashes and embedded schema.
Then run `make check`. The tests reject stale generated files and invalid compatibility examples.
In the review description, explain the behavior, its limits, and the checks you ran.
Record the tested operating system and tool versions in that description.

## Public names

Use Yamata as the project name and generic descriptions for other systems, organizations, people, and examples.
Relevant public technology names are permitted.
Preserve required repository coordinates, license notices, and dependency attribution.
Do not include former employer names, private system names, aliases, source notes, or private links.
Apply this rule to code, comments, paths, test data, output, diagrams, documents, and publication text.
Review every change for direct and indirect private references before publication.

The [writing guide](docs/writing.md#names-and-terms) gives the terminology rules.
