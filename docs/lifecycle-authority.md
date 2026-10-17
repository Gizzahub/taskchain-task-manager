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
and authority versions must be exact integer spellings `6` and `1`. Writer is
exactly `ce`, `task-manager` or `none`; generation is a YAML string of exactly
32 lowercase hexadecimal characters. Numeric-looking generations must be quoted.
Repository and host identities use canonical lowercase kebab-case. Host roots
must be clean absolute non-root paths ending in the repository ID. This syntax
check is not runtime worktree-placement authorization. Provider is exactly
`gz-git` and this initial profile requires explicit `no-fetch`.

The result retains exact original bytes alongside parsed values. Neither parsing
arbitrary bytes nor a serialized/imported result can establish current authority.
Production integration still requires the existing mutation lock, pinned file/
directory sync, authority CAS, all-mutator guards and old-caller/finish recovery.

The descriptor/security query follows the public filesec APIs; Apple's
[ACL wrapper](https://github.com/apple-oss-distributions/Libc/blob/main/posix1e/acl_file.c)
conflates missing ACL properties with a null ACL result. The protected reader
queries property presence explicitly after successful security-stat instead.
