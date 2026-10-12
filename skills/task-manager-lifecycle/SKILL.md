---
name: task-manager-lifecycle
description: Choose and manage TaskChain task execution purposes and their isolated branches, worktrees, and terminal outcomes.
---

# Task Manager execution lifecycle

Use this skill when starting, inspecting, preserving, or discarding an isolated task
execution. It describes the public lifecycle choices; it does not select a task,
approve acceptance, or perform repository integration by itself. For card and
workspace lookup, use the separate [workspace lookup skill](../task-manager-workspace/SKILL.md).

Require `taskchain-task-manager` and `git` on `PATH`, plus the repository's configured
task runtime and integration provider for delivery work. Keep the task's canonical
authoring files and acceptance evidence in the repository they belong to. Do not
write into an installed skill bundle.

## Choose a purpose

Every execution has exactly one purpose:

- `delivery` is the normal implementation path. Its branch is
  `dev/<actor>/<host>/<type>/<task>` and it is the only purpose eligible for
  integration and acceptance completion.
- `experiment` is for exploratory work. Its branch is
  `exp/<actor>/<host>/<type>/<task>`. Preserve useful findings with a reasoned
  `run-abort`; it never uses integration or acceptance completion.
- `review` is for inspection or mutation carried out as a review. Its branch is
  `review/<actor>/<host>/<type>/<task>`. Preserve the review outcome with a
  reasoned `run-abort`; it never uses integration or acceptance completion.

Use the exact purpose names and branch prefixes. `scratch`, `scratchpad`, `tmp`,
and `wip` are not purpose names or branch aliases. Existing records without a
purpose are legacy `delivery` executions; preserve their recorded data.

Start with the explicit purpose, for example:

```sh
taskchain-task-manager task run-start TASK-123 --type feat --purpose delivery
taskchain-task-manager task run-start TASK-123 --type feat --purpose experiment
taskchain-task-manager task run-start TASK-123 --type feat --purpose review
```

The runtime owns the execution record, receipt, owner, and lifecycle lock. Work in
the worktree reported by the runtime and use `run-status` to decide what action is
available. A delivery execution may finish only when its normal Git and validation
conditions are satisfied; use `taskchain-task-manager task run-finish` from the primary checkout.
An experiment
or review execution must end through `run-abort` when its work or findings should be
preserved, or `run-discard` when its clean isolated work can be reclaimed. Do not
turn either purpose into an integration or acceptance result.

```sh
taskchain-task-manager task run-status TASK-123
taskchain-task-manager task run-abort TASK-123 --reason "recorded finding or stopping reason"
taskchain-task-manager task run-discard TASK-123 --reason "clean exploratory work is no longer needed"
```

`run-discard` is a reclamation action, not evidence that the task was completed.
Use it only when the runtime's safety checks allow removal. Keep the abort reason
and any relevant evidence before cleanup.

## Temporary copies

Create a temporary copy only for a review or a mutation that needs a unique,
project-designated temporary child directory. Record its owner, task, purpose,
source commit, creation time, and chosen cleanup or retention policy. Before cleanup,
record the result, reproduction commands, and mutation in project-owned evidence.

Temporary copies are disposable working material. They are never the canonical
authoring location, never an acceptance location, and never a substitute for an
installed skill. By default, clean up after evidence is recorded; retain a resource
only with a named reason. Remove only the child created for this run. If deletion is
refused, report the exact path and reason and do not broaden cleanup.
