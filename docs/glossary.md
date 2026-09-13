# Glossary

Use each term with the meaning below.
Add necessary technical terms when a change introduces them.

| Term | Meaning |
| --- | --- |
| Absolute path | A path that starts at the filesystem root. |
| Analysis | One scoring operation on a saved bag with a complete metric configuration. |
| Analysis ID | A hash identifying an execution, bag, and complete analysis configuration. |
| Attempt | One identified attempt to complete a job; recovery can replace stage leases within the same attempt. |
| Bag | A JSON Lines file with a header and ordered simulation records. |
| Binary | The executable program produced by the Go build command. |
| Checkout | A local copy of the repository files. |
| Coast | Move with zero commanded acceleration. |
| CLI | Command-line interface: commands and options entered in a shell. |
| Current directory | The directory from which the shell starts a command. |
| Data directory | A directory reserved for files created by Yamata. |
| Canonical encoding | One defined byte representation of an input object for configuration hashing. |
| Content hash | A SHA-256 digest calculated from specified bytes. |
| Contact episode | One continuous period of contact between the vehicle and one obstacle. |
| Controller | A built-in policy that selects the vehicle's acceleration or braking command. |
| Correlation ID | An optional caller identifier that passes through unchanged. |
| Event | An immutable record of an execution transition. |
| Exchange directory | The filesystem boundary containing job, event, bag, and result files. |
| Execution | One identified simulation run. |
| Exit code | A number that reports command success or failure to the shell. |
| Failure control | A fixed configuration that makes a selected worker stage report one or two failures. |
| Fencing | Acceptance checks that reject a stale worker after its stage ownership changes. |
| Generation | An increasing claim number that distinguishes current ownership from stale worker claims. |
| Go | The programming language and toolchain used to build Yamata. |
| Hard link | A directory entry that names the same file as another entry. |
| File lock | Operating-system coordination that prevents cooperating processes from owning the same operation simultaneously. |
| GNU Make | The build tool that runs targets defined in `Makefile`. |
| Job | Complete instructions for a run or an analysis of a saved bag. |
| JSON | JavaScript Object Notation, the structured format used by exchange documents. |
| JSON Lines | A format with one complete JSON value per line. |
| JSON Schema | A standard for defining and validating the structure of JSON values. |
| Lease | Temporary stage ownership identified by a token and an expiration time. |
| Manifest | A list of relative file paths and their exact content hashes. |
| Metric | A named, versioned calculation on a bag. |
| Module | A group of Go packages with a module path and a declared Go version. |
| Outbox | Durable records of exact files awaiting publication and acknowledgment. |
| Package | Go source files compiled together under one package name. |
| Permission mask | A process setting that can remove permissions from newly created files or directories. |
| Pinned hash | An expected content hash saved separately from the file being checked. |
| POSIX shell | A command interpreter that supports the shell syntax used in the examples. |
| Parts per million | Integer millionths of a quantity; 1,000,000 represents the complete quantity. |
| Provenance | Information identifying the source and generation method of reference files. |
| Priority class | A job field that selects dispatch order among eligible waiting jobs. |
| Preemption | Interrupting running work to give its capacity to another job. |
| Queue | Durable jobs waiting for a worker stage. |
| Rebuild | Replace a reader result index from validated public result files. |
| Reference exchange | A versioned collection of generated public files for independent clients and verification. |
| Receipt | Confirmation that a complete job was committed to the queue. |
| Relative path | A path interpreted from the current directory. |
| Repository | The project's version-controlled source files and history. |
| Result index | A reader-owned collection derived from authoritative published result files. |
| Result | Authoritative scores or an explicit terminal failure for a job. |
| SHA-256 | The cryptographic hash function used for file and configuration identity. |
| Retry | A new attempt after an explicitly reported failure, subject to a durable per-stage allowance. |
| Reservation | A dispatch slot assigned to the lowest priority class when eligible work is waiting. |
| Scenario | The initial vehicle state, goal, and obstacle behavior. |
| Semi-implicit Euler | A step method that updates speed before it uses that speed to update position. |
| SQLite | The embedded database used for local queue state and accepted output bytes. |
| Snapshot | A consistent view of queue or job state at one read transaction. |
| Stage | The simulation or analysis portion of a queued job. |
| Standard error | The output stream used for error messages. |
| Standard library | Packages supplied with the Go toolchain. |
| Standard output | The output stream used for command results and help. |
| Static check | A source-code check that does not run the application. |
| STE | Simplified Technical English, the writing method defined by ASD-STE100. |
| Symbolic link | A filesystem entry that points to another path. |
| Synthetic data | Data created for an example or test without copying private records. |
| Swept contact | Contact detected anywhere between two successive recorded positions. |
| Temporary file | A file created for a short operation and removed afterward. |
| Tick | One fixed step of simulation time. |
| Trace | The simulator's in-memory sequence of records and its reason for stopping. |
| Transaction | Database changes committed together or rolled back together. |
| Write-ahead log | SQLite files that preserve committed changes before they reach the main database file. |
| Worker pool | A bounded group of concurrent workers assigned to one stage. |
| Unix milliseconds | Milliseconds since 1970-01-01 at 00:00:00 UTC. |
| Toolchain | Programs that compile, test, and inspect source code. |
