# Save and inspect simulation bags

A bag preserves a completed simulation as JSON Lines.
Its first line identifies the execution, format, complete input hash, tick duration, and record count.
Each later line records one simulation tick, including vehicle motion, control, and obstacles.
The bag contains no wall-clock timestamps, attempt identifiers, or scores.

## Save and inspect a bag

Prerequisites: the tools and dependencies in the [README](../README.md#run-the-first-example), a POSIX shell, and a writable temporary directory.
From the repository root, run these commands in the same shell:

```sh
make build
exchange=$(mktemp -d)
cp -R examples/jobs "$exchange/jobs"
bag_hash=$(./bin/yamata record --exchange-dir "$exchange" jobs/candidate.json)
./bin/yamata inspect --exchange-dir "$exchange" --sha256 "$bag_hash" \
  bags/stopped-candidate.jsonl
./bin/yamata validate --exchange-dir "$exchange" \
  jobs/candidate.json bags/stopped-candidate.jsonl
```

The recorder prints only the accepted bag's SHA-256 hash on standard output.
The shell saves that hash in `bag_hash`.
The inspector reports 23 records and a final tick of 22.
The final vehicle position is 21,600 mm, with speed 8,400 mm/s.
The validator prints `Contract valid.`. Each successful command returns exit code `0`.

The [candidate job](../examples/jobs/candidate.json) resolves the stopped-obstacle scenario from the [simulator guide](simulator.md).
It starts the vehicle at 10,000 mm/s, with length 4,000 mm.
The stopped obstacle's rear edge is at 25,000 mm.
The candidate reaches contact because it brakes late under the shared motion rules.
Inspection reports recorded motion; it does not calculate collision scores.

Keep this temporary directory for the checks below.
After completing them, remove only the temporary exchange and built binary:

```sh
rm -r "$exchange"
make clean
```

## Compare repeated recordings

Prerequisites: the preceding walkthrough's shell, built binary, `exchange`, and `bag_hash`.
From the repository root, run:

```sh
repeat_hash=$(./bin/yamata record --exchange-dir "$exchange" jobs/candidate.json)
test "$bag_hash" = "$repeat_hash"
baseline_hash=$(./bin/yamata record --exchange-dir "$exchange" jobs/baseline.json)
./bin/yamata inspect --exchange-dir "$exchange" --sha256 "$baseline_hash" \
  bags/stopped-baseline.jsonl
```

The comparison succeeds silently with exit code `0`.
The repeated command reruns the simulation and accepts the identical existing bag.
It does not modify that file.
The [baseline job](../examples/jobs/baseline.json) produces 32 records and stops at 18,000 mm on tick 31.
Use the preceding cleanup commands after completing all checks.

Identical execution IDs and resolved inputs produce identical bag bytes under the supported simulator and recorder versions.
The encoder uses fixed field order, integer values, compact JSON, and one newline after every record.
Obstacle array order remains unchanged. Empty obstacle arrays encode as `[]`.
The tests pin the example file hashes to detect accidental encoding changes.

The header's `inputs_hash` covers the entire resolved input object, including templates, seed, repeat number, and limits.
It uses the [contract's canonical input encoding](contract.md#hashes-and-identity).
The whole-file hash covers every saved byte, including the execution ID and final newline.
Changing execution identity or input metadata can change the bag hash without changing motion.

## Read the recording

The candidate bag begins with this header:

```json
{"contract_version":1,"kind":"bag","execution_id":"stopped-candidate","format_version":1,"inputs_hash":"b795b0eaff92e8f27153ce15a7266b2743101289af8b18bef491a91052a32f57","tick_ms":100,"record_count":23}
```

The format version selects the bag schema.
The input hash connects the recording to the complete job inputs.
The record count includes tick zero, so 23 records cover ticks zero through 22.

The first two tick lines are:

```json
{"kind":"tick","tick":0,"time_ms":0,"position_mm":0,"speed_mm_s":10000,"acceleration_mm_s2":0,"obstacles":[{"position_mm":25000,"speed_mm_s":0,"length_mm":4000}]}
{"kind":"tick","tick":1,"time_ms":100,"position_mm":1000,"speed_mm_s":10000,"acceleration_mm_s2":0,"obstacles":[{"position_mm":25000,"speed_mm_s":0,"length_mm":4000}]}
```

Tick zero contains the initial state and no applied control command.
Tick one shows 1,000 mm of travel during a 100 ms coast step.
The obstacle remains at its initial position.

The final line is:

```json
{"kind":"tick","tick":22,"time_ms":2200,"position_mm":21600,"speed_mm_s":8400,"acceleration_mm_s2":-4000,"obstacles":[{"position_mm":25000,"speed_mm_s":0,"length_mm":4000}]}
```

The preceding tick places the vehicle at 20,760 mm with speed 8,800 mm/s.
Braking reduces speed to 8,400 mm/s and advances position by 840 mm.
The vehicle's front edge reaches 25,600 mm, overlapping the stopped obstacle.
The simulator ends at that tick boundary; it does not interpolate the exact contact time.
The excerpts omit intermediate ticks and cannot serve as a complete bag.

## Reject incomplete or changed files

Prerequisites: the first walkthrough's shell, built binary, `exchange`, and `bag_hash`.
These commands intentionally fail validation. From the repository root, run:

```sh
sed '$d' "$exchange/bags/stopped-candidate.jsonl" > "$exchange/bags/truncated.jsonl"
./bin/yamata validate --exchange-dir "$exchange" bags/truncated.jsonl
echo "$?"
printf ' ' > "$exchange/bags/changed.jsonl"
cat "$exchange/bags/stopped-candidate.jsonl" >> "$exchange/bags/changed.jsonl"
./bin/yamata inspect --exchange-dir "$exchange" --sha256 "$bag_hash" \
  bags/changed.jsonl
echo "$?"
```

The truncated copy fails because its record count differs from the header.
The changed copy has equivalent JSON meaning but fails the pinned file hash.
Each rejected command prints an error on standard error and returns exit code `1`.
Both checks preserve the original bag.
The first walkthrough's cleanup removes these copies.

Complete-file validation checks schema versions, every record, count, final newline, tick sequence, and simulated time.
Inspection additionally checks the expected SHA-256 hash before returning a summary.
Missing records, extra records, blank lines, unknown fields, duplicate keys, and invalid units or ranges fail validation.
Each line is limited to 1 MiB, and the complete bag is limited to 16 MiB.
The shared validator owns these limits and remains compatible with the published version-one fixtures.

A hash obtained from a modified file cannot prove that the file is unchanged.
Use a hash saved at publication or obtained from a trusted contract reference.
Structural validation alone does not authenticate a source or prove physical correctness.

## Publication and failure behavior

The recorder reads a confined, regular job file and validates its schema and complete input hash.
It supports the version-one lane simulator and version-one baseline and candidate controllers.
The job's tick budget and simulation timeout remain enforced.
Invalid jobs, unsupported execution versions, canceled runs, and exhausted simulation limits produce no bag.

After a completed simulation, the recorder follows this sequence:

1. Encode the full trace within the bag size limit and validate it.
2. Create the `bags` directory when needed and synchronize its parent directory.
3. Write a private temporary file ending in `.tmp` inside `bags`.
4. Read back the staged bytes and verify their structure and expected hash.
5. Synchronize and close the staged file.
6. Atomically create the final name with a hard link that cannot replace an existing entry.
7. If the final name exists, accept matching valid bytes or return a conflict.
8. Remove the temporary name and synchronize the destination directory before reporting success.

New bags use owner-only permissions. Existing files and permissions remain unchanged.
The supported destination is a local filesystem on macOS or Linux that supports hard links and directory synchronization.
File access stays within the opened exchange directory, including during publication.

A reader sees either no final file or the complete staged file.
Temporary names are not valid bag paths, and readers ignore them.
Ordinary failures clean up temporary files.
A process exit can leave a temporary file, which remains ignored during later runs.
The recorder preserves leftover temporary files because another process may still own them.
Remove them through explicit exchange cleanup when no recording process is active.

A failure after the final name appears can leave a complete bag despite an error return.
This includes directory synchronization failure or loss of the command's output stream.
Retrying the same job validates the existing bytes and synchronizes the directory again.
Concurrent identical recordings share one accepted bag; conflicting recordings cannot overwrite it.

Publication does not create queue entries, scores, results, or events.
Durable job intake and worker recovery belong to later changes.
