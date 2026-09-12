# Exchange contract

Yamata accepts complete jobs and publishes events, bags, and results through files.
Each client owns its own database and event progress.
No reader opens another process's database.
The validator checks this boundary without a simulator, worker, or client application.

## Schemas and examples

The public contract lives in [contract/v1](../contract/v1/).
The schemas use JSON Schema Draft 2020-12.
Each entry schema selects a definition from the shared schema.
The binary carries a generated copy of that shared schema and loads no external schemas.

| File | Content |
| --- | --- |
| [job.schema.json](../contract/v1/job.schema.json) | A run job or an analysis-only job with complete inputs. |
| [event.schema.json](../contract/v1/event.schema.json) | An execution transition and an optional result reference. |
| [result.schema.json](../contract/v1/result.schema.json) | Scores or an explicit terminal failure. |
| [bag.schema.json](../contract/v1/bag.schema.json) | The first line of a bag, including its record count and input hash. |
| [tick.schema.json](../contract/v1/tick.schema.json) | Each remaining line of a bag. |
| [exchange.schema.json](../contract/v1/exchange.schema.json) | Shared definitions, required fields, units, and numeric limits. |
| [SHA256SUMS](../contract/v1/SHA256SUMS) | Hashes of the public schemas and examples. |

The examples are synthetic contract fixtures.
They do not claim to be output from an implemented simulator or scoring engine.
The tests accept the valid examples and reject each case in [invalid-cases.json](../contract/v1/examples/invalid-cases.json).
A separate test compares two files with conflicting job content.

Every document has a kind and contract version, except tick records inside a versioned bag.
All integers use decimal notation without fractions or exponents.
Duplicate object keys, unknown fields, and unsupported versions are errors.
The schemas define structural rules. The validator also checks relationships, hashes, and score consistency.

## Validate the example

Prerequisites: the tools and dependencies from the [README](../README.md#run-the-first-example).
From the repository root, run:

```sh
make build
./bin/yamata validate --exchange-dir contract/v1/examples/valid \
  events/completed-1.json jobs/analyze-1.json results/run-error.json
```

Expect `Contract valid.` and exit code `0`.
The validator follows references from the selected files and checks their content hashes.
It creates no directories, queue entries, events, or results.
For build cleanup, run:

```sh
make clean
```

The contract examples remain unchanged.

## Trace one job

1. [jobs/run-1.json](../contract/v1/examples/valid/jobs/run-1.json) identifies job `run-1` and execution `exec-1`.
2. Its inputs contain the scenario, controller, simulator, templates, seed, repeat number, and limits.
3. [bags/exec-1.jsonl](../contract/v1/examples/valid/bags/exec-1.jsonl) identifies that execution and the complete input hash.
4. Its header declares two records. Each later line records one tick.
5. [results/run-1.json](../contract/v1/examples/valid/results/run-1.json) references the job and bag by path and exact file hash.
6. The result identifies its analysis configuration and reports a collision as `FAIL`.
7. [events/completed-1.json](../contract/v1/examples/valid/events/completed-1.json) references the result and repeats its identity and status.

The result file owns the scores. The event announces that result.
The validator checks agreement between these files, including the unchanged correlation ID.
It checks score flags against limits, but does not calculate scores from motion records.

The [analysis-only job](../contract/v1/examples/valid/jobs/analyze-1.json) selects the same bag with a different obstacle-gap limit.
Its execution ID stays the same. Its complete analysis configuration produces a different analysis ID.
The [run failure](../contract/v1/examples/valid/results/run-error.json) has explicit null fields for its bag, analysis, and metrics.
Missing scores never imply a pass.

## Hashes and identity

File references contain a relative path and the lowercase SHA-256 hash of the exact file bytes.
Whitespace and the final newline affect file hashes.
The exchange directory contains `jobs/`, `events/`, `bags/`, and `results/`.
References must select files from the correct directory.
Absolute paths, parent traversal, backslashes, and links that escape the exchange directory are errors.

For `inputs_hash`, encode the complete `inputs` object with sorted keys and no insignificant whitespace.
Keep array order. Write integers in their shortest decimal form; write negative zero as zero.
The input schemas permit only ASCII field names and enumerated string values.
Hash the resulting UTF-8 bytes without a final newline.
Apply the same encoding to `analysis_template` for `analysis_hash`.

These rules define a restricted encoding for the input schemas, not a general JSON canonicalization standard.
The tests preserve fixed hashes that an independent implementation can reproduce.
File-reference hashes always use the original bytes, without this transformation.

The analysis ID is SHA-256 over this UTF-8 string, without a final newline:

```text
execution_id + "\n" + bag.sha256 + "\n" + analysis_hash
```

The event ID is SHA-256 over this UTF-8 string, without a final newline:

```text
job_id + "\n" + attempt_id + "\n" + decimal(sequence)
```

Callers supply unique job and execution IDs.
Each new run needs a new execution ID. Analysis-only jobs reuse the bag's execution ID.
A result identity is its execution and analysis pair.
A run failure without an analysis uses the execution's run-outcome identity.
Each bag belongs to one execution.

The same identity and identical file bytes represent a duplicate delivery.
Different bytes under the same identity are a conflict, including formatting-only changes.
One invocation checks all selected files and their references together.
Separate validator invocations keep no history; durable intake must later enforce uniqueness across imports.
Include all candidate files in one invocation when checking for conflicts.

## States and absent values

The normal state sequence is `PENDING`, `RUNNING`, `ANALYZING`, then `PASS`, `FAIL`, or `WARN`.
Terminal operational failures use `ERROR` and an explicit failure class.
The failure classes distinguish controller failure, simulation timeout, worker failure, and analysis failure.

Terminal events require a result reference. Other events cannot contain one.
An `ANALYZING` event requires an analysis ID.
`PENDING` and `RUNNING` events have no analysis ID.
An event sequence increases within a job; attempt IDs distinguish worker attempts.
Events also identify their producer and transition type, with a creation timestamp in Unix milliseconds.
The validator checks event identity and result agreement, without requiring a complete event history.

Each available metric has a value, version, unit, pass flag, and evidence ticks.
An unavailable metric has null value and pass fields, with an empty evidence list.
Any failed metric makes the result `FAIL`.
Otherwise, any unavailable metric makes the result `WARN`; all passing metrics produce `PASS`.
A collision count above zero always fails.
Operational errors contain no metrics.

Bag ticks start at zero and increase by one.
Each timestamp equals its tick number multiplied by the header's tick duration.
The final newline and declared record count are required.
These checks detect truncation without implementing motion equations.
The [bag recorder](bags.md) produces this format from completed simulations.

## File publication rules

These rules govern file producers. The bag recorder implements them for recordings.

1. Validate a complete file before publication.
2. Serialize competing writes to the same identity through the durable owner.
3. Compare an existing identity with its stored content hash.
4. Accept identical content as a duplicate. Reject changed content.
5. Write a temporary file in the destination directory with a `.tmp` suffix.
6. Flush its content, synchronize the file, and close it.
7. Atomically publish the final name without replacing a conflicting file.
8. Synchronize the destination directory before reporting publication success.

Readers ignore temporary files and validate complete files before use.
The validator rejects an explicitly selected temporary file.
Final file names use the identifier alphabet from the schemas.
Producers preserve immutable files until explicit project cleanup.

The bag recorder uses an atomic hard link, then removes the temporary name.
Creating the link fails if the final name already exists; concurrent publishers cannot overwrite each other.
Matching existing bytes count as duplicate output. Different or unreadable bytes produce a conflict.
Other producers will need the same publication guarantees.

Future intake records receipt only after durable queue import.
Future workers publish completion only after the result file is durable and readable.
A database outbox and startup recovery must repair interruptions between durable state and file publication.
Atomic file publication alone does not implement queue acceptance or worker recovery.

## Check a conflict

Prerequisites: the built binary, a POSIX shell, and write access to a temporary directory.
From the repository root, run:

```sh
exchange=$(mktemp -d)
cp -R contract/v1/examples/valid/. "$exchange/"
cp contract/v1/examples/invalid/conflicting-job.json "$exchange/jobs/conflicting.json"
./bin/yamata validate --exchange-dir "$exchange" \
  jobs/run-1.json jobs/conflicting.json
echo "$?"
```

Expect an identity-conflict error and exit code `1`.
Both files have valid input hashes, but their priority fields differ under the same job ID.
For cleanup, remove only the temporary copy:

```sh
rm -r "$exchange"
```

## Limits and compatibility

Validation accepts regular files only and performs bounded reads.
Each JSON document or bag line is limited to 1 MiB. A complete bag is limited to 16 MiB.
An invocation reads at most 256 distinct files and 64 MiB in total.
JSON nesting is limited to 32 levels. Reference depth is limited to eight files.
A detected cycle, exceeded limit, or unavailable reference fails validation.

The selected exchange directory is the caller's trust boundary.
The operating system resolves that directory when validation starts.
Concurrent writers must follow the immutable publication rules; validation is not a transaction across files.
The supported environment is a local filesystem on macOS or Linux.
Go 1.25.13 is the minimum supported patch version for path containment and schema URL handling.

Clients copy the public contract files and record the reviewed source revision beside them.
Verify the copied files against `SHA256SUMS` from that same revision.
The manifest detects changed bytes; it does not authenticate an untrusted source revision.
Clients do not import the internal validator package or require another repository at runtime.
Breaking format changes require a new contract version and new compatibility examples.
