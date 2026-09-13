# Generated reference exchange

[Version one](v1/) contains exact public files from the complete demo.
It supplies two complete jobs, one recorded bag, two calculated results, and eight transition events.
Another client can copy this directory and read it without a producer process or producer database.
Use [contract version one](../../contract/v1/) to interpret the files.

| File | Purpose |
| --- | --- |
| [jobs/candidate.json](v1/jobs/candidate.json) | Complete run inputs for the deliberate collision. |
| [jobs/edge-score.json](v1/jobs/edge-score.json) | A second analysis selecting gap version two against the same bag. |
| [bags/stopped-candidate.jsonl](v1/bags/stopped-candidate.jsonl) | Original recorded motion. |
| [results/](v1/results/) | Distinct analyses with gap values of 3,400 mm and 0 mm; both report `FAIL`. |
| [events/](v1/events/) | Run transitions, one analysis retry, and analysis-only transitions. |
| [SOURCE.json](v1/SOURCE.json) | Generation command, engine source digest, and fields that vary between fresh runs. |
| [SHA256SUMS](v1/SHA256SUMS) | Exact hashes of every other file in the versioned directory. |

The reference version identifies this example collection; it does not change the public contract version.
The [contract fixtures](../../contract/v1/examples/) remain synthetic compatibility cases.
The generated files here contain actual accepted worker output.
No timestamps, attempt IDs, result bytes, or event hashes were normalized during export.

## Read the reference

Prerequisites: the tools and installed dependencies from the [README](../../README.md#run-the-first-example).
From the repository root, run these commands in one shell:

```sh
make build
state=$(mktemp -d)
./bin/reader sync --exchange-dir examples/reference/v1 --state-dir "$state"
./bin/reader list --state-dir "$state"
./bin/reader rebuild --exchange-dir examples/reference/v1 --state-dir "$state"
rm -r "$state"
make clean
```

Sync reports eight processed events and two indexed results.
Rebuild retains that progress and the same two results.
The last two commands remove temporary reader state and built binaries; the reference files remain unchanged.

Copy this versioned directory with its manifest when another client needs fixed test data.
Pin the reviewed repository revision alongside the copied contract and reference files.
Check manifest hashes before using them; a manifest does not authenticate its source.
Do not copy `.queue/`, demo reader databases, or local ownership markers into a client fixture.

## Compare a fresh run

Prerequisites: the same tools and installed dependencies.
From the repository root, run:

```sh
make verify
make clean
```

Verification requires byte-identical jobs and bags, then compares result content and transitions across fresh execution.
Result comparisons exclude `attempt_id` and `timing`.
Event comparisons exclude `attempt_id`, `event_id`, `created_at_ms`, and the referenced result's file hash.
All other result and event fields must match.
Both validators still check complete references and hashes within their own exchange.

These exclusions describe comparisons only; saved files remain unchanged.
Fresh attempts and elapsed durations naturally change result hashes and their referencing events.
The complete reference manifest still pins every saved byte.
Expect verification to pass; temporary data is removed automatically, and `make clean` removes built binaries.

## Export a new reference

Prerequisites: the installed tools and an unused demo output directory.
From the repository root, run these commands in one shell:

```sh
make demo
export_dir=$(mktemp -d)
python3 scripts/reference.py "$export_dir/reference"
cat "$export_dir/reference/SOURCE.json"
cat "$export_dir/reference/SHA256SUMS"
```

The exporter copies exact public bytes into a new directory and writes provenance and hashes.
It refuses an existing destination and does not replace the checked-in reference.
The source digest hashes sorted manifest lines for `go.mod`, `go.sum`, and Go files under `cmd/` and `internal/`.
Each line contains the file hash, two spaces, its repository-relative path, and a newline.

Review an intentional reference update before copying exported files into a versioned example directory.
Preserve old versions when another client depends on their exact files.
After inspection, remove this export and the demo:

```sh
rm -r "$export_dir"
make demo-clean
make clean
```

These commands preserve checked-in examples and unrelated project data.
