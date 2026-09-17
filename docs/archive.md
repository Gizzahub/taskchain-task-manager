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
New operations adopt protocol 4, including boards previously using protocol 3.
Finish any existing protocol 3 pending request before adopting protocol 4.
These markers are permanent; never delete journals to downgrade a board.
Shared protocol 4 reservations carry one pending record and exact journal hashes,
not a second copy of the receipt history. Recovery requires the original local
journal; missing history must be restored, not regenerated. The journal limit
remains 8 MiB.
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
Protocol 5 boards use a completed capacity receipt and a schema-2 archive.
Local-to-shared activation may bind a one-time namespace-only rebind: its
common/policy plan records only exact hashes and an inert artifact digest, not
archive contents. The capacity receipt and payload are provenance, never
activation authority. A pending plan accepts only its saved original or target
archive bytes and mode; once completed, later valid archival writes are normal.
Foreign nonempty namespaces remain refused. Public release/cutover approval
remains separate.
