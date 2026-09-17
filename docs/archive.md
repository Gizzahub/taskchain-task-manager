# Archive cards

`archive` moves a card into `_archive/<zone>/...`, preserving its module,
category path and filename. It requires an exact source SHA-256, request ID,
owner and an explicit rules file. No timestamp is inserted automatically.

```yaml
schema-version: 1
archive-admission:
  fields:
    review: review-result
    evidence: review-proof
    resolution: disposition
    promoted-to: promoted
    children: child-ids
  accepted-reviews: [pass, conditional, waived]
```

Field names are explicit mappings, not implicit metadata conventions.
Task/backlog admission requires done status, an accepted review and evidence.
Plans require completed child IDs; issues require a resolution and completed
promotion references. This dialect does not resolve titles or body-table links.

```sh
taskchain-task-manager archive --dir tasks --id TASK-1 \
  --source done/TASK-1.md --rules archive.yaml --owner developer \
  --request-id <32-lowercase-hex> --expected-sha256 <64-lowercase-hex> \
  --adopt --json
```

The first operation requires `--adopt` after **all writers** have been upgraded.
Protocol 3 is permanent; never delete its journals to downgrade a board.
Ordinary operations stop while an archive is pending. Retry exactly the same
request with `--resume` after its operation was recorded. If adoption itself
was interrupted before a request was recorded, retry with `--adopt` instead.
Never automatically delete a lock left by an interrupted process: establish
that its owner has terminated before applying an operator-approved recovery.

Normal archival of a workflow-done TASK preserves dependency completion only
while the current archived card matches its recorded identity, path and bytes.
The receipt is not proof that implementation or tests were independently verified.
Existing targets are never overwritten, including byte-identical files.
A held claim requires its exact owner/token; archiving does not release it.

`--operation supersede` changes the frontmatter status to `superseded`.
`--operation force --assertion <reason>` bypasses normal review admission but
preserves source bytes. Neither operation publishes dependency completion.
Rules, request ID, source hash and all other request fields must be preserved
for a retry, including after a stdout failure. JSON success has schemaVersion 1.

Existing archives use the separate [legacy adoption command](legacy-archive.md),
which requires explicit operator approval to publish dependency completion.
Clone rebinding and local-to-shared archive namespace migration are not yet
supported. Scope-changing operations are rejected before
rewriting those bindings. Public release/cutover approval remains separate.
