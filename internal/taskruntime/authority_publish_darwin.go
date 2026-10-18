//go:build darwin && cgo

package taskruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func (s *Service) publishPinnedAuthority(ctx context.Context, h canonicalConfigHandle, in authorityPublicationInput, scope *ownerTransitionScope, lock *ownedMutationLock, target canonicalAuthorityDeclaration, hook canonicalReadHook, result *authorityPublicationResult) (err error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	name := fmt.Sprintf(".task-runtime.publish-%x", nonce)
	parentFD := int(h.parent.Fd())
	fd, err := unix.Openat(parentFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	var original unix.Stat_t
	statErr := unix.Fstat(fd, &original)
	renamed := false
	defer func() {
		err = errors.Join(err, file.Close(), canonicalReadPoint(hook, "publication-temp-close"))
		if !renamed {
			// A replacement is a retained residual, never this operation's trash.
			e := canonicalReadPoint(hook, "publication-before-cleanup")
			var st unix.Stat_t
			if e == nil {
				e = unix.Fstatat(parentFD, name, &st, unix.AT_SYMLINK_NOFOLLOW)
			}
			if e == nil {
				if statErr != nil {
					e = errors.New("temp identity unavailable")
				} else {
					if original.Dev != st.Dev || original.Ino != st.Ino {
						e = errors.New("temp identity changed")
					}
				}
			}
			if e == nil {
				e = unix.Unlinkat(parentFD, name, 0)
			}
			if e != nil {
				err = errors.Join(err, fmt.Errorf("publication temp retained in canonical .ce at %s: %w", name, e))
			}
		}
	}()
	if statErr != nil {
		return statErr
	}
	if err := checkCanonicalFD(fd, 2, hook); err != nil {
		return err
	}
	if err := canonicalReadPoint(hook, "publication-temp-created"); err != nil {
		return err
	}
	if n, err := file.Write(in.targetRaw); err != nil {
		return err
	} else if n != len(in.targetRaw) {
		return io.ErrShortWrite
	}
	if err := canonicalReadPoint(hook, "publication-temp-write"); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := canonicalReadPoint(hook, "publication-temp-sync"); err != nil {
		return err
	}
	if err := checkCanonicalFD(fd, 2, hook); err != nil {
		return err
	}
	if err := canonicalReadPoint(hook, "publication-before-rename"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := scope.validate(ctx, s, in); err != nil {
		return err
	}
	if err := checkAuthorityQuiescence(ctx, lock, scope.controller); err != nil {
		return err
	}
	if err := recheckCanonicalConfigPath(h.repository.primary.root, h.config, in.expectedRaw); err != nil {
		return fmt.Errorf("%w: %v", errAuthorityPublicationConflict, err)
	}
	// Ensure the named source is still our validated descriptor.
	var tempStat, fdStat unix.Stat_t
	if unix.Fstatat(parentFD, name, &tempStat, unix.AT_SYMLINK_NOFOLLOW) != nil || unix.Fstat(fd, &fdStat) != nil || !canonicalStableFile(tempStat, fdStat) {
		return errAuthorityPublicationConflict
	}
	if err := unix.Renameat(parentFD, name, parentFD, "task-runtime.yaml"); err != nil {
		return err
	}
	renamed = true
	result.status = "publication-unverified"
	if err := canonicalReadPoint(hook, "publication-renamed"); err != nil {
		return err
	}
	result.status = "durability-unknown"
	if err := h.parent.Sync(); err != nil {
		return err
	}
	if err := canonicalReadPoint(hook, "publication-directory-sync"); err != nil {
		return err
	}
	result.status = "publication-unverified"
	// Reopen through the protected reader, proving same temp inode, exact
	// bytes/tuple and all canonical ancestor/owner/ACL invariants after rename.
	err = withPublicationConfigPath(h.repository, hook, func(current canonicalConfigHandle) error {
		a, e1 := file.Stat()
		b, e2 := current.config.Stat()
		if e1 != nil || e2 != nil || !os.SameFile(a, b) || !bytes.Equal(current.raw, in.targetRaw) || current.declaration.Authority != target.Authority {
			return errAuthorityPublicationConflict
		}
		if err := syncPublicationCurrent(current, hook); err != nil {
			return err
		}
		if err := lock.validate(); err != nil {
			return err
		}
		return scope.validate(ctx, s, in)
	})
	if err != nil {
		return err
	}
	result.status = "published"
	result.observation = canonicalAuthorityObservation{in.repository, append([]byte(nil), in.targetRaw...), target}
	return nil
}
