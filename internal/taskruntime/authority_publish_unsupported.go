//go:build !darwin || !cgo

package taskruntime

import "context"

func (*Service) publishPinnedAuthority(context.Context, canonicalConfigHandle, authorityPublicationInput, *ownerTransitionScope, *ownedMutationLock, canonicalAuthorityDeclaration, canonicalReadHook, *authorityPublicationResult) error {
	return errCanonicalUnavailable
}
