# Adopt an existing archived card

`adopt-legacy-archive` records an existing archived card without moving or
rewriting it. It does not infer that the product observed its previous workflow.
The operator supplies the exact card ID, relative path, current SHA-256,
permission bits, and a statement explaining the adoption.

```sh
taskchain-task-manager adopt-legacy-archive --dir tasks --id TASK-1 \
  --source _archive/done/TASK-1.md --rules archive.yaml --owner developer \
  --request-id <32-lowercase-hex> --expected-sha256 <64-lowercase-hex> \
  --expected-mode 0644 --assertion 'Reviewed the legacy completion evidence' \
  --approve-completion --adopt --json
```

The archive rules file uses the [archive rules format](archive.md). An assertion
is an operator statement, not authentication or independent quality verification.
This operation requires a previously reserved ID; reserving that ID does not
itself approve completion. Existing held claims still require their owner/token.

Without `--approve-completion`, adoption does not publish dependency completion.
With it, only a non-superseded TASK may receive a `legacy-completion` binding.
This is distinct from a normal archive's observed `workflow-done` provenance.
No flag silently upgrades an earlier adoption: reusing a request ID with changed
approval or mode is a conflict. Do not use this command to supersede or restore
an existing archive operation.

All writers must support storage protocol 4 before `--adopt`, including an
upgrade from protocol 3. Existing protocol 3 pending requests retain their
original recovery format and must finish before upgrade. A pending
operation blocks ordinary board operations. Retry with exactly the same inputs;
use `--resume` once the operation is recorded, or `--adopt` if initial protocol
adoption stopped before recording it. Preserve journals and locks. Never remove
a lock automatically or guess that its owner is dead.

A failed stdout write may follow a successful durable adoption; repeat the exact
request instead of creating a new one. Recovery does not recreate missing cards
or accept changed card contents or permissions.

Adoption is bound to this board and namespace. Copying a journal to another
board does not transfer ownership. Clone/join and local-to-shared rebinding
remain separate operations, not an effect of this command.
