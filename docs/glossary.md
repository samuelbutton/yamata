# Glossary

Use each term with the meaning below.
Add necessary technical terms when a change introduces them.

| Term | Meaning |
| --- | --- |
| Absolute path | A path that starts at the filesystem root. |
| Analysis | One scoring operation on a saved bag with a complete metric configuration. |
| Analysis ID | A hash identifying an execution, bag, and complete analysis configuration. |
| Attempt | One worker attempt to complete a job. |
| Bag | A JSON Lines file with a header and ordered simulation records. |
| Binary | The executable program produced by the Go build command. |
| Checkout | A local copy of the repository files. |
| Coast | Move with zero commanded acceleration. |
| CLI | Command-line interface: commands and options entered in a shell. |
| Current directory | The directory from which the shell starts a command. |
| Data directory | A directory reserved for files created by Yamata. |
| Content hash | A SHA-256 digest calculated from specified bytes. |
| Controller | A built-in policy that selects the vehicle's acceleration or braking command. |
| Correlation ID | An optional caller identifier that passes through unchanged. |
| Event | An immutable record of an execution transition. |
| Exchange directory | The filesystem boundary containing job, event, bag, and result files. |
| Execution | One identified simulation run. |
| Exit code | A number that reports command success or failure to the shell. |
| Go | The programming language and toolchain used to build Yamata. |
| GNU Make | The build tool that runs targets defined in `Makefile`. |
| Job | Complete instructions for a run or an analysis of a saved bag. |
| JSON | JavaScript Object Notation, the structured format used by exchange documents. |
| JSON Lines | A format with one complete JSON value per line. |
| JSON Schema | A standard for defining and validating the structure of JSON values. |
| Metric | A named, versioned calculation on a bag. |
| Module | A group of Go packages with a module path and a declared Go version. |
| Package | Go source files compiled together under one package name. |
| Permission mask | A process setting that can remove permissions from newly created files or directories. |
| POSIX shell | A command interpreter that supports the shell syntax used in the examples. |
| Relative path | A path interpreted from the current directory. |
| Repository | The project's version-controlled source files and history. |
| Result | Authoritative scores or an explicit terminal failure for a job. |
| SHA-256 | The cryptographic hash function used for file and configuration identity. |
| Scenario | The initial vehicle state, goal, and obstacle behavior. |
| Semi-implicit Euler | A step method that updates speed before it uses that speed to update position. |
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
| Unix milliseconds | Milliseconds since 1970-01-01 at 00:00:00 UTC. |
| Toolchain | Programs that compile, test, and inspect source code. |
