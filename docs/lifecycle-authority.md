# Lifecycle authority preparation

The internal canonical-repository resolver is inactive preparation. No CLI or
runtime mutation uses it. Existing runtime schema support and config resolution
remain unchanged. It does not read config bytes, validate ACLs or grant authority.

The resolver queries Git's top-level, Git directory, common directory and NUL
worktree inventory. It selects exactly one non-bare primary whose Git directory
is the common directory, independent of directory name, branch or inventory order.
Ordinary primary and linked checkouts are supported by this inactive resolver.
Separate Git metadata is unavailable when Git's inventory reports the metadata
directory instead of the actual primary checkout. The resolver never guesses a
primary from the common directory's parent. Missing, prunable, inconsistent or bare
entries, query errors and multiple primary identities are unavailable. Redirecting
Git environment variables and ambiguous newline paths are rejected.

Every non-primary runtime-config entry, including an identical copy or dangling
symlink, is rejected before config interpretation. Nothing is removed or repaired.
The result pins caller, primary and common directory identities and inventory;
revalidation rejects replacement or topology drift. This is an observation, not
an atomic filesystem snapshot or a security proof. Symlinks are resolved for Git
identity comparison; secure no-follow config access is still required separately.

Before any production wiring, a protected reader must verify descriptor identity,
ancestors, owner, mode, hardlink count and platform ACLs, decode the versioned
authority strictly, and revalidate under the existing mutation lock. Publisher,
all-mutator guards, legacy caller refusal and finish recovery must be implemented
and verified together before activation. There is no installation or writer
transition interface in this delivery.

## Inactive protected reader contract

The next internal reader accepts only the staged schema-6 profile below. It is
not connected to `Service.config()`, a CLI, a mutation or a provider. A successful
read is a bounded observation, not a writer grant, durability proof or atomic
snapshot. Legacy CLI behavior is unchanged; this reader rejects every other
schema and never falls back to a linked/cwd config. CE schema 4/5 features are not
implicitly adopted. Source compatibility must be rechecked before live wiring.

The caller and primary paths must be canonical absolute paths. The reader walks
from `/` using descriptor-relative no-follow opens, pins every directory and the
regular config file, and rewalks/rechecks identities before returning. Ancestors
must belong to root or the current user (effective UID from `geteuid`) and must not be writable by other users
(except root-owned sticky temporary directories). `.ce` must belong to the current
user with exact mode 0700; its config must have exact mode 0600 and one hardlink.
No directory/file is created, repaired, chmodded or removed. Concurrent replacement,
unsafe paths, read/stat/close/ACL failure or an unsupported platform is unavailable.
Git caller/primary/common identity and worktree inventory are revalidated too.

Darwin with cgo is the initial protected-read platform, subject to native ACL
regressions. ACL inspection uses each pinned directory descriptor from `/` through the primary
root and `.ce`, and the pinned config descriptor. Each object is checked against
its own filesystem owner; “current user” means effective UID (`geteuid`). A
successful `fstatx_np` plus `filesec_query_property(FILESEC_ACL)` distinguishes
explicit ACL absence from query failure. A failed ACL getter is never treated
as absence merely because errno is ENOENT. Deny entries and allow
entries naming the exact filesystem owner are permitted; any other allow entry,
including an empty/inherit-only one, is outside this profile. Unknown entries,
owner UUID mapping failure or ACL query failure are unavailable. This proves
neither authentication nor defense against a hostile process with the same UID.
Darwin without cgo and other platforms compile but protected reads are unavailable.

Config input is UTF-8 and at most 64 KiB, including comments and whitespace. Both
stat size and an actual limit+1 read enforce this bound. Read errors, short/change
observations and close errors return no partial observation. Invalid syntax or
unsupported profile is invalid; errors do not quote config bytes. File/directory
sync belongs to the later locked grant protocol and is not performed here.

The staged profile requires a single YAML mapping with exactly these fields:

```yaml
schema-version: 6
repository-id: synthetic
worktree-roots:
  synthetic-host: /synthetic/worktrees/synthetic
integration-provider: gz-git
integration-network-policy: no-fetch
lifecycle-authority:
  version: 1
  writer: none
  generation: "0123456789abcdef0123456789abcdef"
```

`execution-administrators` is optional and retains the existing exact actor/host/
kind rules. All other fields are required and non-null. Root keys, nested keys
and host identities must be strings; mappings reject duplicate keys. Aliases,
anchors, merge keys, multiple documents and unknown fields are rejected. Schema
and authority versions must be untagged plain integer spellings `6` and `1`. Writer is
exactly `ce`, `task-manager` or `none`; generation is a single-quoted or double-quoted
YAML string of exactly 32 lowercase hexadecimal characters. Explicit tags, plain
scalars and literal/folded block presentations are rejected for generation.
Repository and host identities use canonical lowercase kebab-case. Host roots
must be clean absolute non-root paths ending in the repository ID. This syntax
check is not runtime worktree-placement authorization. Provider is exactly
`gz-git` and this initial profile requires explicit `no-fetch`.

The result retains exact original bytes alongside parsed values. Neither parsing
arbitrary bytes nor a serialized/imported result can establish current authority.
Production integration still requires the existing mutation lock, pinned file/
directory sync, authority CAS, all-mutator guards and old-caller/finish recovery.

## Inactive locked boundary

An internal, inactive callback boundary wraps the protected
schema-6 read. `withCanonicalConfig` keeps the validated config descriptor and
its owner-private `.ce` directory descriptor open for one synchronous callback.
`withAuthorityMutation` performs a side-effect-free precheck, obtains the
existing `mutation.lock` once, and delegates to
`withLockedCanonicalAuthority`, which never acquires or releases that lock.

Only an exact `ce` or `task-manager` writer match can receive the
process-local `authorityLease`; `none`, a foreign writer, malformed schema or
legacy input is rejected before runtime state or a lock is created. The lease
is tied to the canonical repository, raw descriptor, expected writer and the
owned lock's bytes and filesystem identity. It expires at callback return and
detects replacement of either the config pathname or mutation lock. Config and
parent directory `Sync` errors, callback errors and owned-lock release errors
remain visible. A replaced lock pathname is never removed.

Owned release uses an exclusive atomic rename into a random operation-private
name in the same existing lock directory, then verifies the original inode and
bytes before removing that staging entry. A mismatching file is restored without
overwriting another owner, or retained with its exact residual path in the error.
The `.mutation.lock.release-*` names are transient lock retirement, not a new
lock domain or metadata format. Future activation must treat any retained entry
as unresolved; this inactive delivery does not add a cleanup command. Platforms
or filesystems without the required exclusive rename refuse and retain the lock.
This protocol protects cooperating callers competing at `mutation.lock`; it does
not authenticate against a hostile same-UID process modifying private staging.

This boundary has no CLI, provider, ledger, publisher, new lock domain or
legacy transition. Existing service mutators do not call it, so delivery of
this code does not activate authority or change normal runtime behavior.

## Inactive owner-scoped publication

The internal `publishCanonicalAuthority` operation has no production scope
constructor, CLI, installer or ordinary mutator caller. Only tests mint a
synthetic controller on a new temporary repository. Publication is not activation
or a grant for a subsequent mutation. Support remains Darwin with cgo; other
platforms refuse at the protected descriptor boundary.

An owner scope binds the exact canonical repository/inventory, approved target
bytes and source/artifact identity. A new transition requires an externally owned
launch freeze and completed drain before acquiring the existing mutation lock.
Under that lock, the controller must positively confirm known/drained callers,
clear board pending/protection state and clear cleanup/legacy operations. Missing
knowledge refuses; filesystem names are not guessed into an owner verdict. The
runtime independently refuses nonterminal executions, unresolved receipts and
every latest FINISHING claim regardless of process liveness. There is no waiting,
TTL reclamation or forced drain under this lock. Production acceptance must supply
the separately reviewed owner controller and complete caller coverage.

Publication compares the expected repository, complete original bytes and
authority tuple. Protected legacy schema 2/3 with explicit `no-fetch` alone can
bootstrap; schema 4/5 or malformed schema 6 never become legacy-absent. Targets
use strict schema 6 and a fresh 128-bit random generation per normal revision;
the owner must generate and retain the same target bytes/generation for retries.
A changed normal revision cannot reuse its previous generation.

Before old-value CAS or new-transition quiescence, an exact current target can
be synced and rechecked as `already-published`. A confirmation-only scope cannot
publish a different target or bypass approval/repository safety. New publication
uses a unique no-follow, exclusive 0600 temporary file under the pinned `.ce`
descriptor, validates owner/ACL/link identity, syncs it and atomically renames it.
The old target and canonical path are rechecked immediately before rename. After
rename, protected reopen must find the same inode, exact target bytes/tuple and
safe ancestors, then sync/recheck file and directory.

Before-rename errors preserve the original (`failed-before-publish`). After
rename, sync errors are durability-unknown and failed validation/close/release
is publication-unverified; no success observation is returned. No automatic
rollback, writer fallback, journal, outcome store, receipt or provider call is
added. Only an identity-matching owned temporary file is removed; replacements
are retained with their residual name. The controller retains freeze ownership
on failure and must report/recover its phase; this operation never unfreezes it.
Cooperating callers are the threat model, not hostile same-UID authentication.

The descriptor/security query follows the public filesec APIs; Apple's
[ACL wrapper](https://github.com/apple-oss-distributions/Libc/blob/main/posix1e/acl_file.c)
conflates missing ACL properties with a null ACL result. The protected reader
queries property presence explicitly after successful security-stat instead.
