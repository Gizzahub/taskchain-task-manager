# Schema 5 file-role reader contract

This document defines the public reader wire. It defines no activation, writer
admission, deletion, installation, or migration behaviour.
Schema 5 remains reader-only until those separate acceptance boundaries exist.

## Policy shape

`schema-version: 5` retains the schema 4 `board-policy` fields and requires
`modules` plus the complete `file-roles` mapping. Every role is a root-level
Markdown file. Nested paths and unlisted names are rejected.

```yaml
schema-version: 5
board-policy:
  modules: []
  file-roles:
    enabled: true
    projections:
      - path: README.md
        owner: task-indexer
        source-revision: v7
        format: live-card-paths-v1
    guidance:
      - path: AGENTS.md
        owner: task-guide
        source-revision: v7
        content-sha256: bdd46033e46f010d83272a618cc8e69d4d5731b2d542dfbf3559cca58562bb36
    templates:
      - path: TEMPLATE.md
        owner: task-template
        source-revision: v7
        content-sha256: 091383d792d4bda1a2cb221af44f61767fc067ff77624f296195996439c72707
```

`path`, `owner`, and `source-revision` are nonempty UTF-8 strings of at most
128 bytes, have no surrounding whitespace, and contain no control character.
`content-sha256` is exactly 64 lowercase hexadecimal characters. It names the
exact regular-file bytes of guidance or template content; no newline or
encoding normalisation occurs. `format` is required for a projection and is
the closed enum `live-card-paths-v1`. A projection has no content digest.

The allowed paths are: projection `README.md` or `INDEX.md`; guidance
`AGENTS.md` or `CLAUDE.md`; template `TEMPLATE.md`. Each path appears once
across all role lists. Empty lists are valid, and all three lists are required.
`enabled: true` is required. Projection objects have exactly `path`, `owner`,
`source-revision`, and `format`; guidance/template objects have exactly
`path`, `owner`, `source-revision`, and `content-sha256`. Unknown, duplicate,
or cross-kind fields are rejected.

Canonical JSON orders role groups as `projections`, `guidance`, `templates`.
Within every group it sorts entries by UTF-8 byte order of `path`; projection
objects order keys `path`, `owner`, `source-revision`, `format`, and content
objects order `path`, `owner`, `source-revision`, `content-sha256`. Schemas
1–4 retain their existing canonical bytes and digests.

## Projection bytes

`live-card-paths-v1` owns only the bytes between these exact marker lines.
Marker metadata values are compact Go `encoding/json` strings: its default
HTML escaping is retained (`<`, `>`, `&`, U+2028 and U+2029 are escaped),
while ordinary non-ASCII Unicode and slash stay literal. The fields appear in this order: `format`, `owner`, then
`source-revision`. Each marker line ends in one LF byte.

```text
<!-- task-manager:projection-begin format="live-card-paths-v1" owner="task-indexer" source-revision="v7" -->
- todo/001-example.md
<!-- task-manager:projection-end format="live-card-paths-v1" owner="task-indexer" source-revision="v7" -->
```

Every displayed line is LF-terminated in the exact fixture; the fenced block
does not display a literal `\n` sequence.

Its inventory contains every regular Markdown candidate path in a non-archived
workflow, kind, module, or category location, including `done`. It excludes
`archive`, `_archive`, `evidence`, `.ce`, engine-private paths, and declared
non-card roles. Candidate paths sort by UTF-8 bytes. The inventory does not
read card bodies, titles, statuses, or IDs.

For an identity containing a quote, the marker uses its JSON escape, for
example `owner="team\"alpha"`. The begin and end markers occur exactly once,
with begin preceding end, at full LF-terminated lines. A marker-shaped prefix
with missing, reordered, duplicate, or mismatched metadata is malformed.
The list is zero or more LF-split lines whose bytes begin `- ` and whose rest
round-trips as the path; `- ` inside a path is allowed. A path is valid UTF-8,
clean slash-relative, has no empty, `.`, or `..` segment, backslash, Unicode
control character, CR, LF, or leading/trailing whitespace, and contains neither
`<!-- task-manager:projection-` nor `-->`. An empty list has no bytes between
the two marker lines.
Bytes outside the markers are manual content and remain untouched.

Projection files are at most 1 MiB. Guidance and templates are at most 64 KiB.
Missing content, digest mismatch, malformed markers, list drift, size overflow,
and a stable board entry or declared leaf that is a symlink/non-regular shape
are structural findings (exit 1).
For board-readiness lint, gate, and preflight, permission/read failure, active
policy decoding, an initial root symlink/non-directory, root instability, or a
changed no-follow open are unavailable errors (exit 2). This does not alter the
existing `validate-policy` parse-error envelope or its exit behaviour. Readers
use a pinned board root and root-only leaf no-follow; this is neither an atomic
snapshot nor an all-ancestor guarantee. Existing CLI output envelopes stay
unchanged. Metadata asserts declared provenance only: it does not authenticate a remote source.
Durable lineage enforcement is deferred.

The public golden canonical JSON fixtures contain no trailing LF; each expected
digest is the SHA-256 of those exact bytes.
