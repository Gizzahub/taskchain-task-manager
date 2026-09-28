---
name: task-manager-workspace
description: Query explicitly declared TaskChain boards when an agent needs one writer-coordinated card result or a bounded no-write multi-card snapshot.
---

# Task Manager workspace lookup

Require `taskchain-task-manager` and `git` on `PATH`. Use an explicit workspace
manifest supplied by the caller. The manifest is the complete repository inventory:
do not discover additional boards, broaden its paths, or execute card content.

Choose the observation the caller needs:

- `query-workspace --manifest PATH --card-id ID --json` returns exactly one card.
  Use `--repository NAME` only when that declared repository is the intended
  scope. This command coordinates with board writers and briefly creates and
  removes `.task-manager.lock`. Missing or ambiguous matches fail without a
  success JSON document.
- `workspace-context --manifest PATH --card-id ID [--card-id ID ...] --json`
  reports every match for a bounded literal set of IDs, labeled `found`,
  `missing`, or `ambiguous`. It does not create a lock or coordinate with
  writers. Stop relevant writers before using it when consistency matters;
  retry if it detects a changed board. Do not add inferred IDs to the request.

Both commands read only manifest-declared boards and require explicit selectors.
Successful JSON uses top-level `outputVersion: 1`; an error has a nonzero exit
code and no success JSON. Treat the two commands as different concurrency
contracts, not interchangeable ways to count cards.

For a reproducible, synthetic example, resolve this skill's installed directory
and read [assets/workspace/README.md](assets/workspace/README.md). Copy that
asset directory to a temporary workspace before initializing its two Git repos;
do not write into an installed skill directory. Asset paths are relative to this
`SKILL.md`, so the example works after the whole bundle is copied elsewhere.
