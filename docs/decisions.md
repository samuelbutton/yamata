# Design decisions

These decisions describe the implemented teaching model and its limits.
The [code tour](code-tour.md) connects them to their implementation.

## Files define the public boundary

Complete jobs, bags, results, and events use a [versioned file contract](contract.md).
Each client owns its application records and event progress.
Workers require no client database, acknowledgment, service, or other checkout.

This boundary permits independent readers and direct inspection of every reference.
It also requires explicit schemas, path confinement, hash checks, and duplicate detection on each side.
The exchange assumes a trusted local filesystem; it supplies no remote transport or authentication protocol.

## Separate motion from scoring

The simulator produces integer records with fixed ticks and no storage or wall-clock dependency.
The recorder saves those records before analysis calculates metrics.
The same bag can support a new analysis template without simulation.

Integer arithmetic gives reproducible bytes within the supported versions.
Fixed ticks and truncation simplify the model but affect stopping distance and contact timing.
The [simulator guide](simulator.md) states the equations and physical limits.

## Preserve identities and earlier outcomes

Exact file hashes detect content changes, including whitespace changes.
The analysis identity includes the execution, bag hash, and complete analysis configuration.
Changed versions or limits produce distinct results; duplicate delivery preserves accepted bytes.

Collision count remains independent of obstacle-gap interpretation.
An unavailable metric remains null, and an operational error cannot become a pass.
The [metric guide](metrics.md) defines these outcomes.

## Keep durable ownership inside SQLite

SQLite commits complete jobs, stage leases, retry records, and exact output bytes locally.
Workers calculate outside transactions, then acceptance checks current ownership inside a short transaction.
The outbox publishes durable result files before completion events.

This design handles process restarts and publication interruptions without a separate queue service.
It retains accepted output bytes and delivered outbox records, so disk usage grows until explicit cleanup.
All queue processes share one host; network filesystems and cross-host scheduling are outside this model.

## Publish without replacement

The publisher synchronizes a temporary file, creates an atomic hard link, and synchronizes the destination directory.
An existing final name prevents replacement.
Matching bytes count as a duplicate; conflicting bytes stop publication.

This method requires local filesystem support for hard links, locks, and directory synchronization.
Standalone calls use execution locks, while queued workers use durable lease ownership.
An exchange selects one execution mode to prevent competing outcome publishers.

## Bound failures without hiding them

Each stage permits one retry after an explicit worker failure.
Lease recovery resumes unfinished ownership without consuming that failure allowance.
Controller failures, timeouts, analysis failures, and failed scores remain terminal.

Priority selects the next claim without interrupting running work.
Every tenth dispatch reserves service for waiting class-three work.
Continuous higher-priority arrivals can still delay classes one and two; the policy guarantees no completion deadline.
The [recovery guide](recovery.md) explains these limits and the upgrade procedure.

## Build the reader as a separate Go module

The independent reader has its own dependency manifest and reviewed contract copy.
It can build after its directory is copied away from the engine checkout.
This makes an accidental dependency on engine packages visible during verification.

A separate module is a dependency boundary choice, not a requirement of the file format.
It adds a second dependency installation, build, and update surface.
The root Makefile runs checks for both modules, and verification also builds a detached reader copy.

The reader commits event progress with indexed results in its own SQLite transaction.
Results remain authoritative when notifications are missing or the reader is stopped.
Rebuild reads published results and never needs producer state.

## Retain original run context for later analysis

Bag format one does not include vehicle length or goal position.
Analysis-only intake resolves those values from the original accepted run snapshot in the same queue.
An arbitrary copied bag cannot supply that context.

This preserves the existing public format and avoids guessed geometry.
It limits reanalysis to recordings whose original run exists in that queue.
The [reanalysis guide](reanalysis.md) describes the required inputs.

## Keep real reference output

The [versioned reference exchange](../examples/reference/README.md) retains exact files from a completed demo.
Its manifest pins file bytes, including real timing and attempt metadata.
Fresh verification compares deterministic inputs, bags, scores, identities, and transitions while excluding documented variable fields.

The reference serves client implementations without requiring a running producer.
It does not promise identical timings or event hashes across fresh executions.
The original contract fixtures remain separate compatibility tests.
