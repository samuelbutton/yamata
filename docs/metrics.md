# Versioned metrics and standalone outcomes

The metric calculator reads recorded motion from a validated bag.
It uses the original job's scenario geometry and analysis limits.
It does not run the simulator or assign scores from controller names.
The standalone command records a job, reads its saved bag, and publishes the calculated outcome.

## Run and score a job

Prerequisites: the tools and dependencies from the [README](../README.md#run-the-first-example), a POSIX shell, and a writable temporary directory.
From the repository root, run these commands in the same shell:

```sh
make build
exchange=$(mktemp -d)
cp -R examples/jobs "$exchange/jobs"
./bin/yamata run --exchange-dir "$exchange" jobs/candidate.json
echo "$?"
for event in "$exchange"/events/*.json
do
  ./bin/yamata validate --exchange-dir "$exchange" "events/${event##*/}"
done
cat "$exchange"/results/*.json
```

The run prints `status=FAIL`, result and event paths, and their exact file hashes.
It returns exit code `1` because the candidate collides and misses the configured progress limit.
The validator prints `Contract valid.` and returns exit code `0`.
Validation success confirms file consistency; it does not mean the simulation passed.

The result contains these scores:

| Metric | Value | Unit | Pass | Evidence tick |
| --- | ---: | --- | --- | ---: |
| collision_count | 1 | count | false | 22 |
| minimum_obstacle_gap | 3,400 | mm | true | 22 |
| goal_progress | 360,000 | ppm | false | 22 |

The result owns these scores. The completion event references the result and repeats its status.
Keep the temporary exchange for the retry check below.
After all checks, remove only this temporary exchange and the built binary:

```sh
rm -r "$exchange"
make clean
```

## Collision count, version one

The collision count is the number of contact episodes across all obstacles.
Initial overlap counts as an episode at tick zero.
Continued overlap with the same obstacle does not add another episode.
Separation followed by renewed contact starts another episode.

Contact uses the same swept interval rule as the [simulator](simulator.md#contact-between-ticks).
Let `r_start` and `r_end` be obstacle rear position minus vehicle rear position at adjacent boundaries.
Contact occurs when both conditions hold:

```text
min(r_start, r_end) <= vehicle_length
max(r_start, r_end) >= -obstacle_length
```

This rule includes touching edges, between-tick crossings, and obstacles approaching from behind.
The value uses unit `count`. The maximum permitted count is always zero.
Any nonzero count fails the metric and makes the aggregate status `FAIL`.
The evidence list contains the first contact tick, or no ticks when the count is zero.

In the candidate example, the final vehicle occupies positions 21,600 through 25,600 mm.
The obstacle occupies positions 25,000 through 29,000 mm.
Their overlap establishes one episode at tick 22.
Collision failure remains blocking even if every other metric passes or is unavailable.

## Minimum obstacle gap, version one

Version one measures center distance at recorded tick boundaries.
It examines every obstacle at every tick and keeps the smallest distance.
It includes obstacles behind the vehicle.

```text
doubled_distance = abs(2 * (obstacle_position - vehicle_position)
                       + obstacle_length - vehicle_length)
distance_mm = floor(doubled_distance / 2)
minimum_obstacle_gap = minimum(distance_mm over all recorded obstacles)
```

Doubling preserves half-millimetre centers until the final division.
The result rounds down to integer millimetres.
The metric passes when its value meets the analysis template's `minimum_mm` limit.
Its evidence identifies the earliest tick containing the minimum value.

At candidate tick 22, both objects have length 4,000 mm.
Their centers are 23,600 and 27,000 mm, giving 3,400 mm of center distance.
The default limit is zero, so this metric passes even though the bodies overlap.
Collision count independently blocks the aggregate outcome.

This sampled center distance is not edge clearance or the minimum distance between ticks.
A later metric version will change the gap definition while preserving version one's meaning.
The current calculator rejects unsupported versions instead of substituting another formula.

With no obstacles, the minimum is unavailable.
Its `value` and `pass` fields are `null`, and its evidence list is empty.
A missing value never becomes zero or a passing flag.

## Goal progress, version one

Goal progress compares the final rear position with the scenario's starting rear position and goal.
It uses integer parts per million, where 1,000,000 means complete progress.

```text
goal_distance = goal_position - start_position
travel = final_position - start_position
progress_ppm = clamp(travel * 1000000 / goal_distance, 0, 1000000)
```

Integer division truncates toward zero before clamping.
Travel behind the starting point becomes zero; travel beyond the goal becomes 1,000,000.
The metric passes when progress meets the template's `minimum_ppm` limit.
Its evidence contains the final tick.
When the goal distance is zero or negative, progress is unavailable with null value and pass fields.

The candidate travels 21,600 mm toward a goal 60,000 mm from its starting point.
Its progress is `21600 * 1000000 / 60000 = 360000` ppm.
The configured minimum is 1,000,000 ppm, so the metric fails.

The baseline stops at 18,000 mm and has no collisions.
Its progress is 300,000 ppm, so the unchanged default job also produces `FAIL`.
Its minimum center distance is 7,000 mm, first reached on tick 30.
Stopping before contact does not automatically satisfy a full-progress requirement.

## Availability, versions, and failure results

The aggregate status follows this order:

1. Any failed metric produces `FAIL`.
2. Otherwise, any unavailable metric produces `WARN`.
3. All available, passing metrics produce `PASS`.

An empty-lane run reaches the goal but reports `WARN` because obstacle gap is unavailable.
The command returns exit code `0` only for `PASS`.
`FAIL`, `WARN`, `ERROR`, and command errors return exit code `1`.

Each metric records its own version, unit, value, pass flag, and evidence ticks.
Changing any formula requires a new metric version.
Changing a limit changes the complete analysis hash, even when the metric version stays the same.
The [analysis identity](contract.md#hashes-and-identity) includes execution identity, bag hash, and the complete analysis hash.
Results use `results/<analysis_id>.json`, so distinct analyses have distinct file names.

Schema-valid but unsupported metric versions produce an `analysis_failure` result.
It preserves the bag and analysis references, sets status `ERROR`, and has null metrics.
Changed initial state or inconsistent obstacle records also prevent scoring.
The calculator checks obstacle count, length, speed, and constant-speed movement against the original scenario.

An unsupported controller version produces `controller_failure`.
Unsupported simulator versions or invalid execution geometry produce `worker_failure`.
An exhausted tick budget or simulation deadline produces `simulation_timeout`.
These run failures have null bag, analysis, and metric fields and use `results/<execution_id>-run.json`.
Invalid contract jobs are rejected before execution and produce no outcome.

Storage errors or caller cancellation can prevent outcome publication.
The command reports the error instead of manufacturing a passing result.
No automatic execution retry occurs in this command.

## Retry without changing accepted files

Prerequisites: the first walkthrough's shell, built binary, and `exchange` directory.
From the repository root, run:

```sh
cp -R "$exchange/results" "$exchange/expected-results"
cp -R "$exchange/events" "$exchange/expected-events"
./bin/yamata run --exchange-dir "$exchange" jobs/candidate.json
echo "$?"
diff -r "$exchange/expected-results" "$exchange/results"
diff -r "$exchange/expected-events" "$exchange/events"
```

The run still returns exit code `1` for the same failed scores.
Both comparisons succeed silently with exit code `0`.
Accepted result bytes, timing, event identity, and event bytes remain unchanged.
Use the first walkthrough's cleanup commands after completing the comparisons.

The command holds an operating-system file lock for the execution while it publishes the outcome.
Separate execution IDs can run independently.
Lock files live under `.standalone/`; closing the process releases ownership without removing the file.
Persistent lock files prevent simultaneous callers from locking different file objects under one name.
Do not remove them while any standalone command is active.

The accepted attempt ID contains its starting Unix millisecond value.
The result also saves the elapsed duration before result publication.
Their sum supplies the completion timestamp when constructing the event.
These persisted values let an explicit retry recreate a missing event with identical bytes.
They remain separate from reproducible bag content and metric values.

Results and events use the shared staged-file publisher.
It validates references, synchronizes complete bytes, publishes without replacement, and synchronizes the destination directory.
The result becomes durable and readable before publication of its completion event begins.
A failure between those operations can leave a result without an event.
Repeating the same job validates the accepted result and repairs the missing event.

Standalone completion emits one terminal event with sequence one for its accepted attempt.
It does not emit queue transitions or run background workers.
The [worker guide](workers.md) explains durable queues and outbox recovery.
Use a separate exchange for queued execution. Explicit worker-failure retry policies belong to a later change.
