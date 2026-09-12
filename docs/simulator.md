# One-lane simulator

The simulator advances a vehicle and obstacles along one lane.
It returns an in-memory trace with a final observation: goal reached, vehicle stopped, or collision.
The same configuration produces the same ordered records.
The simulator uses integer arithmetic and has no clock, random source, storage, or network dependency.

## Run the examples

Prerequisites: the tools and dependencies in the [README](../README.md#run-the-first-example).
From the repository root, run:

```sh
make build
./bin/yamata simulate --scenario stopped-obstacle --controller baseline
./bin/yamata simulate --scenario stopped-obstacle --controller candidate
./bin/yamata simulate --scenario empty-lane --controller baseline
./bin/yamata simulate --scenario moving-obstacle --controller baseline
```

Each command prints the simulator version, selected inputs, final observation, tick, simulated time, position, and speed.
A completed simulation returns exit code `0`, including a run that observes a collision.
Invalid arguments or execution failures return exit code `1`.
The command writes no bags, scores, or data directories.

The stopped-obstacle example produces these final observations:

| Controller | Observation | Tick | Position (mm) | Speed (mm/s) |
| --- | --- | ---: | ---: | ---: |
| baseline | stopped | 31 | 18,000 | 0 |
| candidate | collision | 22 | 21,600 | 8,400 |

Both controllers reach the empty-lane goal on tick 60.
With the moving obstacle, the baseline stops on tick 32 and the candidate reaches contact on tick 27.
To run the second controller in either scenario, select `--controller candidate`.
For cleanup, run:

```sh
make clean
```

This command removes the built binary. There are no simulation files to remove.

## World and units

The [example definitions](../internal/simulator/examples.go) own the initial settings.
Each example starts at position zero with speed 10,000 mm/s and vehicle length 4,000 mm.
The rear-edge goal is 60,000 mm.
The tick duration is 100 ms, with a budget of 100 steps.

The empty lane has no obstacles.
The other examples place an obstacle's rear edge at 25,000 mm, with length 4,000 mm.
The stopped obstacle has zero speed. The moving obstacle travels forward at 2,000 mm/s.
Obstacles maintain their initial speed throughout a run.

| Quantity | Unit or meaning |
| --- | --- |
| Position and length | Integer millimetres; position identifies the rear edge. |
| Speed | Integer millimetres per second; speeds cannot be negative. |
| Acceleration | Integer millimetres per second squared. |
| Time | Integer milliseconds measured from the start of the simulation. |
| Tick zero | Initial position, speed, and obstacles, before any control action. |
| Later tick | State after a complete step, including the command used for that interval. |

A vehicle occupies the closed interval from its position to its position plus its length.
An obstacle occupies the same type of interval.
Touching edges count as contact.

## Step equations

Let `dt` be the tick duration in milliseconds.
Let `p` be position, `v` speed, and `a` the selected acceleration.
The simulator uses semi-implicit Euler steps:

```text
v_next = max(0, v + a * dt / 1000)
p_next = p + v_next * dt / 1000
time_next = time + dt
```

Each division uses integer truncation toward zero.
The clamp prevents braking from producing negative speed or reverse motion.
Position uses the updated speed for the whole interval.
Each obstacle advances by its constant speed multiplied by `dt / 1000`, with multiplication before division.

The controller chooses its command from the previous record.
The simulator updates the vehicle, advances all obstacles, and records the new state.
It then checks contact, the goal, and zero speed, in that order.
Checks also apply at tick zero.
A collision takes precedence when several terminal conditions occur in the same step.

For immediate braking from 10,000 mm/s, the first records are:

| Tick | Time (ms) | Acceleration (mm/s²) | Speed (mm/s) | Position (mm) |
| --- | ---: | ---: | ---: | ---: |
| 0 | 0 | 0 | 10,000 | 0 |
| 1 | 100 | −4,000 | 9,600 | 960 |
| 2 | 200 | −4,000 | 9,200 | 1,880 |
| 3 | 300 | −4,000 | 8,800 | 2,760 |
| 25 | 2,500 | −4,000 | 0 | 12,000 |

Summing the step distances gives a stopping distance of 12,000 mm.
Continuous constant braking would give 12,500 mm.
The difference comes from applying the reduced speed for each complete step.

## Controllers

Both controllers coast until their braking condition becomes true.
They then hold acceleration at −4,000 mm/s² until the vehicle stops.
They do not steer, accelerate from rest, or resume after stopping.
The same motion and contact rules apply to both controllers.

The [controller code](../internal/simulator/controller.go) uses this stopping-distance estimate:

```text
d = ceil(v * v / (2 * 4000))
gap = obstacle_position - vehicle_position - vehicle_length
baseline_threshold = d + ceil(v * dt / 1000) + 2000
candidate_threshold = floor(d / 4)
```

The baseline includes one coast step and a 2,000 mm margin.
The candidate deliberately waits until the gap reaches one quarter of the estimated stopping distance.
Either policy starts braking when any obstacle ahead reaches its threshold.
An obstacle completely behind the vehicle does not trigger braking.
Input order does not change the control decision.

These policies conservatively treat an obstacle ahead as stationary when choosing a threshold.
Actual obstacle motion still affects contact checks.
The candidate's collision follows from its later braking under the shared motion rules.
A controller can still collide when the initial state leaves insufficient braking distance.

## Contact between ticks

Contact checks use relative displacement across the whole interval.
Let `r_start` and `r_end` be obstacle position minus vehicle position at the two tick boundaries.
Contact occurs when both conditions hold:

```text
min(r_start, r_end) <= vehicle_length
max(r_start, r_end) >= -obstacle_length
```

The model treats movement between recorded positions as linear.
These conditions detect an interval crossing even if the objects no longer overlap at the final boundary.
They also detect an obstacle that catches the vehicle from behind.
Two objects moving at the same speed maintain their gap.

The trace ends at the boundary of the first tick containing contact.
It does not calculate the exact contact time or clamp positions to the contact point.
The final position can therefore lie beyond an obstacle or goal.

## Boundaries and limits

The [simulation package](../internal/simulator/simulator.go) owns typed configurations, motion records, and termination checks.
The command package selects examples and formats output.
Neither layer writes simulation data.
The [bag recorder](bags.md) maps resolved jobs into this simulator and saves complete traces.
The [metric calculator](metrics.md) reads the saved motion without running the simulator.
Run observations are separate from the contract's analysis scores and execution events.

The core accepts tick durations from 1 to 1,000 ms and budgets from 1 to 100,000 steps.
It accepts at most 32 obstacles and returns at most the initial record plus the step budget.
Positions and goals lie within ±1,000,000,000 mm.
Initial speeds range from zero to 1,000,000 mm/s. Lengths range from 1 to 1,000,000 mm.
The full coast path must remain within the position range, even when braking would end the example earlier.
This conservative check bounds arithmetic and keeps obstacle records within the version-one contract limits.

The simulator returns an error if the step budget expires before a terminal observation.
A canceled context also returns an error.
Neither error returns a completed trace.
Callers own wall-clock deadlines; those deadlines are not simulated time.
The built-in examples use no randomness, so seed and repeat metadata do not alter their motion.

Each record owns a separate obstacle slice.
Changing a returned record cannot change earlier records, another run, or the caller's input.
Callers must not mutate input slices during a run.

The model excludes steering, road friction, sensor delay, collision response, and variable obstacle behavior.
Integer truncation discards motion smaller than one millimetre per step; the model carries no fractional remainder.
Changing tick duration can change stopping distance and contact outcomes.
The simulator is a teaching model, not a production vehicle model.
