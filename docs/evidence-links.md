# Interruption evidence links

`link-evidence` records one immutable, observational link between an existing
`TASK-N` card and an interruption receipt. It does not change a card, claim,
review result, audit log, or task status. The stored record contains only the
task identity, receipt event identity and digest, optional inert reference,
classification reason/certainty, and classifier identity metadata. It never
stores a payload or conversation transcript and never opens `--receipt-ref`.

The input is strict JSON with `schema_version: 1` and
`contract: "agent-interruption-v1"`. Its `event.id` must equal
`classification.event_id`; an optional `event.task_id` must identify the same
task. `reason` is one of `provider_policy`, `model_refusal`, `approval_denied`,
`user_excluded`, `environment_blocked`, `check_failed`, `normal_stop`, or
`unknown`. `certainty` is `confirmed`, `suspected`, or `unknown`.
`identity_source` is `host_configuration`, `runtime_reported`, or `unknown`.
Unknown reason requires unknown certainty. Confirmed `provider_policy` or
`approval_denied` requires the matching captured `payload.error.code` of
`cyber_policy` or `PermissionDenied`. This checks receipt consistency and does
not attest to the authenticity of its author or decide task acceptance.

`evidence-links --json` returns an object with a `links` array. An omitted
`event.task_id` permits an operator to bind an existing TASK explicitly; a
present task ID must match. References are inert metadata, limited to 4096
bytes, and should point to a retained receipt or audit event.

Verify the SHA-256 of the exact receipt bytes before registration:

```sh
taskchain-task-manager link-evidence receipt.json --dir ./tasks --task-id TASK-1 \
  --sha256 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef --json
taskchain-task-manager evidence-links --dir ./tasks --task-id TASK-1 --json
```

Links are stored under `.task-manager-context/evidence-links/` using a hash of
the event ID. Repeating identical registration is unchanged; another link for
the same task and event ID fails. Board locking and pending-transition checks
apply to both commands.
