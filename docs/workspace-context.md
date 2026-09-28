# Workspace context lookup

`workspace-context MANIFEST --json` reads cards from the exact repositories and
boards named by the manifest. It does not discover repositories or create a
lock. The input is strict JSON:

```json
{
  "schemaVersion": 1,
  "repositories": [
    {"repositoryId": "product", "root": "/work/product", "board": "tasks"}
  ],
  "cardIds": ["TASK-1", "ISSUE-7"]
}
```

Repository IDs are bounded ASCII identifiers. Roots are absolute directory
locators and `board` is one clean, repository-relative directory. The command
rejects unsafe or missing roots and boards, duplicate IDs, duplicate canonical
Git roots, invalid card IDs, duplicate numeric card identities, unknown fields,
trailing JSON, and manifests over 256 KiB. It accepts at most 32 repositories,
256 query IDs, 4,096 cards, 8,192 filesystem entries, and 64 MiB of regular
files per board.

Each query returns `missing`, `found`, or `ambiguous`. Numeric identity means
`TASK-01` and `TASK-1` address the same card; the requested spelling is retained
in `requestedId`. An ambiguous result retains every match and sorts matches by
repository ID and board-relative path. Output contains `card.View`, the
repository ID, and a board-relative `/` path. It never includes an absolute
root, card source, prompt, or agent decision.

The lookup reads a board and compares a bounded file fingerprint before and
after the scan. An observed change fails the complete request. This is a
per-board consistency check; it does not claim one atomic point in time across
repositories or detect a change that is restored before either fingerprint.
The read-only projection validates pending transitions, board policy, cards and
claims, but does not acquire the writer lock or run the session's shared-state
writer coordination; callers must treat a concurrent shared-state change as a
retry condition.
