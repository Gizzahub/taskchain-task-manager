# Opt-in command execution timing

Timing records are optional observations of one completed `task run-*` invocation.
They do not change command stdout, exit status, lifecycle receipts, locks or board state.
Help and other commands are not observed. Telemetry failures add a bounded stderr
diagnostic and preserve the original command result.

```sh
TASKCHAIN_EXECUTION_TIMING=1 \
TASKCHAIN_EXECUTION_TIMING_CONTEXT='{"session_id":"example-session","tool_call_id":"example-call","relation_type":"launches"}' \
taskchain-task-manager task run-list --json
```

`TASKCHAIN_EXECUTION_TIMING=1` enables observation; unset or other values disable it.
The optional context is an inline JSON object containing only `session_id`, `turn_id`,
`tool_call_id`, `task_id`, `repository_id`, `relation_type`, and `retry_of`.
Tool relations require session and call IDs; retries require a previous run ID.
IDs are bounded UTF-8 labels without surrounding whitespace or control characters.
Unknown/duplicate context keys and malformed values are rejected by the observer.
Explicit empty/null values and invalid UTF-8 are rejected. Put only correlation
identifiers in these fields; their content is supplied by the caller.
Missing context stays unlinked; time and command resemblance never imply a relation.

Records use `task-manager.execution-timing/v1` and include a fresh attempt ID, UTC
start/end, observed command exit code and status, native monotonic nanoseconds,
an opaque process clock-domain ID, and the allowlisted context. Exit 0 is success;
nonzero exits are failure. They are not guessed to be timeout or cancellation.
Whole execution-generation elapsed time and unfinished processes are not measured.
Both UTC boundaries are required; missing completion never becomes a year-1 timestamp.

Sidecars live under the Git common directory's compatibility ledger at
`ce/task-runtime/v1/execution-timing/v1/<sha256(run_id)>.json`. Legacy ledger files
are not modified. Complete files are published atomically without overwriting
different content, including concurrent attempts. New sidecar directories/files
are private; existing ledger permissions stay unchanged.
Observed symlinks in the bound ledger/sidecar locations are rejected. This is not
a guarantee against a hostile local owner concurrently replacing ancestors.

Records never contain argv, working-directory paths, environment contents,
stdout/stderr, prompts, responses, reasoning or credentials. A timing artifact
does not prove task completion or authorize a board transition. Current CE/DVA
readers do not automatically import this format; consumer support and live monitoring
are separate work. No service, collector or daemon is started.
