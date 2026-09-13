# Independent exchange reader

This example is a separate Go module and command.
It reads published jobs, bags, results, and events through a pinned contract copy.
It imports no Yamata packages and opens no producer database.
Its own SQLite file contains event progress, accepted identity hashes, and a result index.

## Build and run separately

Prerequisites: Go 1.25.13, a POSIX shell, and a writable temporary directory on macOS or Linux.
From this directory, install dependencies with `go mod download`.
After installation, these commands need no network access:

```sh
tools=$(mktemp -d)
state=$(mktemp -d)
go build -o "$tools/reader" .
go test ./...
"$tools/reader" sync --state-dir "$state" --exchange-dir contract/v1/examples/valid
"$tools/reader" list --state-dir "$state"
"$tools/reader" rebuild --state-dir "$state" --exchange-dir contract/v1/examples/valid
"$tools/reader" list --state-dir "$state"
```

The first scan records two events and indexes one `FAIL` result.
Rebuild finds two results, including an `ERROR` result that has no example completion event.
Neither result is interpreted as passing.
The examples remain unchanged.
For cleanup, run:

```sh
rm -r "$tools" "$state"
```

You can copy this entire directory elsewhere and run the same procedure.
The build and tests need no parent checkout, worker binary, or Go workspace.
The [source record](contract/SOURCE.json) identifies the pinned producer revision.
Tests verify the copied files against the [contract manifest](contract/v1/SHA256SUMS).
Review any contract update and its source revision together.
The [Apache License 2.0](LICENSE) applies to this example.

## Command behavior

`sync` consumes explicit event paths or scans the event directory once.
`watch` repeats the scan every second until `SIGINT` or `SIGTERM`.
Both commands validate file shapes, references, hashes, identities, metric flags, and aggregate status.
They do not calculate metrics from motion or interpret controller names.

Each accepted event commits its identity and any referenced result in one transaction.
Progress records all accepted event IDs and job sequence numbers, rather than a filename cursor.
Duplicate delivery has no additional effect.
Events can arrive out of order without hiding an earlier event.
Changed bytes under an accepted identity produce an error.

`rebuild` replaces the result index from all published result files in one transaction.
It reads referenced jobs and bags but never reads events.
A missing reference, invalid result, or interrupted transaction preserves the previous index.
Rebuild keeps existing event progress; a fresh state directory starts with no event progress.
`list` prints the saved progress count and indexed result records as JSON.

State directories must already exist, have owner-only access, and remain outside the exchange.
A state directory belongs to one canonical exchange path.
Use a fresh state directory after moving or replacing an exchange.
Keep every published folder, state directory, and database path stable while commands run.

Each scan accepts at most 4,096 directory entries or explicit event paths.
A reference graph permits 256 files, eight reference levels, and 64 MiB total content.
JSON files and bag lines permit 1 MiB; complete bags permit 16 MiB.
Each scan or rebuild has a 30-second deadline.
Listing permits 4,096 results and 64 MiB of stored result content.
These bounds suit the local teaching example, not a production event service.

A bad event does not prevent other valid events in the scan from committing.
The command reports failed paths and exits unsuccessfully; watch stops after that scan.
Correct the publication problem and restart the reader to retry those events.
The reader never edits, deletes, quarantines, or acknowledges producer files.
Progress and identity history remain until you explicitly remove the reader state directory.
