# Inactive declaration reader and wire compatibility

`internal/declaration` is an opt-in library for local, synthetic declaration
fixtures. No command, producer, declaration store, runtime admission, hook,
provider invocation, installation, or writer cutover is enabled. Existing run
ledgers, receipts, timing records and evidence contexts keep their bytes and
meaning. Reading a declaration never creates an execution or resumes its owner.

## Local wire v1

The new schema is `task-manager.declaration/v1`. Its vocabulary is separate from
the execution ledger and `task-manager.execution-timing/v1`. All keys are exact
and case-sensitive. Every field below is required unless marked optional. No
field accepts null; optional absence is preserved without inserting defaults.
Unknown keys, duplicate keys at any depth, unsupported schemes/versions, trailing
JSON, invalid UTF-8, replacement characters and unpaired UTF-16 escapes fail.

| Object | Fields |
|---|---|
| record | `schema`, `id`, `occurred_at`, `repository`, `runtime`, `subject`, `privacy`; optional `execution`, `scope`, `predecessor` |
| repository | `scheme: repository-id/v1`, writer-owned `id`, protected local `common_dir`; optional `catalog_alias` |
| runtime | `name`, `provenance`; `native` requires `session_id` and excludes `unavailable_reason`; `unavailable` requires the reason and excludes the ID |
| subject | optional `task`, optional already-redacted `topic`; `{}` represents a topic-only declaration with topic omitted for privacy |
| privacy | `version: topic-redaction/v1`, `state`; `omitted` excludes topic and requires `reason`; `redacted` requires topic and optionally carries reason |
| execution | `scheme: execution-snapshot/v1`, `snapshot` |
| snapshot | `task`, `type`, `purpose`, `source`, `owner`, `branch`, `worktree`, `startedAt` |
| owner | `actor`, `host`, `kind: agent` or `human` |
| scope | `reads`, `writes`, `contracts` arrays, each required even when empty |
| read | relative `path`, optional immutable hexadecimal `commit` (40 or 64 characters) |

IDs/reasons/topic are bounded to 256 UTF-8 bytes, runtime names to 64, branch to
1024, paths to 4096, each array to 128 items, nesting to 16 and total input to
64 KiB. Strings must be nonempty and trimmed, with no Unicode control/format
characters. Local absolute paths are clean in the current OS; scope paths use
clean relative slash segments, without traversal, backslashes or colons. No
Unicode, path or identity normalization is inferred. Timestamps are nonzero
RFC3339Nano strings ending in `Z`; their original spelling remains part of the
payload. Predecessor cannot equal this declaration's ID. See the synthetic
`internal/declaration/testdata/` examples for the complete shape.

## Legacy generation and advisory assessment

The legacy execution reference contains **all** of task/type/purpose/source/
owner(actor,host,kind)/branch/worktree/startedAt. `updatedAt`, a PID, invocation
`run_id` and a provider's opaque execution ID are not substitutes for generation.
Legacy omitted purpose is represented explicitly as `"purpose":""` in the new
snapshot; it is not rewritten to delivery. No fields are added to old JSON.

`Assess` compares the complete validated snapshot with a caller-supplied current
observation. Repository scheme/id/common-dir must match; catalog aliases never
establish identity or bridge machines. Matching, different-generation, terminal
and unavailable describe the current observation only. They do not grant resume,
takeover, exclusive session ownership or proof that the session performed work.

Unbound input yields `declared-unbound`. A referenced execution's historical
provenance stays `unknown` in this inactive reader, even when the tuple matches.
An input cannot award itself `bound-at-write`. Establishing and preserving that
historical grade requires a separately reviewed trusted-writer adapter under the
existing runtime mutation boundary. This library neither verifies nor rewrites
past audit records and never backfills a current task into them. A caller must
not represent advisory comparison as a completed writer-side bind operation.

## Retransmission and timing compatibility

`SamePayload` and `Retransmission` compare validated immutable typed records,
including schema, producer occurrence time, optional presence, topic privacy,
complete execution reference, predecessor and array order. Whitespace and key
order are irrelevant. Same ID/same payload is `duplicate`; same ID/different
payload is `conflict`; different IDs are `distinct`. No append, overwrite,
exactly-once persistence, hashing, collection timestamp or transport attempt is
implemented. Topic/scope/generation changes need a new declaration ID and can
reference the predecessor.

`task-manager.execution-timing/v1` and its `execution_evidence_v1` allowlist are
unchanged. A run ID denotes a completed invocation attempt, not a lifecycle
generation. Old timing/evidence can still be read by their existing readers;
they have no declaration link and must remain connection-unknown. The evidence
v1 reader rejects a `declaration_id` extension. The declaration reader rejects
legacy timing/evidence as declaration input. New audit/timing references use the separate `task-manager.declaration-link/v1`
envelope and `DecodeLink`; v1 timing/evidence is not extended or downcast. No
audit link producer is enabled here.

The link fields are required `schema`, `event_id`, `occurred_at`, `repository`,
`runtime`, `declaration_id`, and optional `invocation`. Repository/runtime have
the same exact shape and bounds as declarations. Event/declaration/run IDs are
bounded to 256 bytes and occurrence time follows the same timestamp rule.
`invocation`, when present, requires exactly `schema` (currently
`task-manager.execution-timing/v1`) and `run_id`. Absent invocation is an audit
reference without a timing association; null is invalid. No raw legacy metadata,
topic, tool input or transport fields are accepted. The shared strict parser
enforces the same 64-KiB, depth, key, type and duplicate bounds on links.

`LinkRetransmission` compares the complete immutable envelope by event ID, with
the same duplicate/conflict/distinct semantics. A matching declaration/run ID is
only a producer-supplied reference: it establishes neither bound-at-write nor
generation, completed invocation success, provenance, or a causal join. Each
target still needs its own reader and trusted provenance check. Legacy producers
remain supported by their original readers and connection-unknown. Old evidence
readers and timing validation reject this new envelope; unsupported link schemas
are refused by the new reader. Synthetic link/session/generation fixtures exercise
these compatibility and negative cases without rewriting prior records.

## Privacy and file boundary

The decoder consumes already-redacted topic text only. It is not a secret detector
and cannot prove a producer's sanitization claim. Producers must omit uncertain
topics before serialization, logging, hashing or storage, retaining only privacy
state/version/reason. Do not pass raw conversation or tool input. Errors never
echo rejected values, unknown keys, paths or bytes. There is no raw-topic digest,
automatic logging or export API. A future default exporter must omit topic,
absolute paths and personal data and cannot export authority for import.

`ReadFile(root, name)` reads only one basename from an explicitly selected,
existing owner-private collection (0700 directory, 0600 regular file, current
effective UID). Symlink roots/leaves, unknown owner, broad permissions and special
files fail closed. The opened root confines subsequent access; descriptor/stat
identity checks reject replacement during open. The caller owns selection of
the trusted root, including its ancestors. The collection is never discovered
from a ledger, created, chmodded, migrated or repaired. Unsupported owner/ACL
platforms refuse file reads; byte decoding remains available for fixtures.

The legacy execution store's existing permissions are **not** evidence that a
declaration collection is safe. The reader must not be used to change those
permissions. There is no live writer, TTL, retention cleanup, file deletion,
cross-store transaction or automatic recovery. Writer activation, ownership
validation, retention, rollback and consumer E2E remain separate work.
