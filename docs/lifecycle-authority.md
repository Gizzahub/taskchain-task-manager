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
