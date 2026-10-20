package roleprotocol

import (
	"errors"
	"fmt"
)

const (
	maxRoleBytes        = 1 << 20
	maxRoleBindingBytes = 4 << 10
)

// Role is the §14.5 closed persistent role object carried by journal5 and
// activation4. Every digest it stores is recomputed on decode.
type Role struct {
	History              History
	HistoryDigest        string
	CurrentBindingDigest string
	Support              Support
	SupportDigest        string
	Adoption             Ack
	AdoptionDigest       string
}

// ParseRole strictly decodes a standalone role object.
func ParseRole(raw []byte) (Role, error) {
	n, err := parseJSON(raw, maxRoleBytes, "role")
	if err != nil {
		return Role{}, err
	}
	return decodeRole(n)
}

func decodeRole(n *node) (Role, error) {
	if n.size > maxRoleBytes {
		return Role{}, fmt.Errorf("role exceeds %d bytes", maxRoleBytes)
	}
	f, err := fields(n, "role", "protocol", "history", "historyDigest", "currentBindingDigest", "support",
		"supportDigest", "adoption", "adoptionDigest")
	if err != nil {
		return Role{}, err
	}
	if err := exactInt(f["protocol"], "role.protocol", 1); err != nil {
		return Role{}, err
	}
	var r Role
	if r.History, err = decodeHistory(f["history"], HistoryBound); err != nil {
		return Role{}, err
	}
	if r.Support, err = decodeSupport(f["support"]); err != nil {
		return Role{}, err
	}
	if r.Adoption, err = decodeAck(f["adoption"]); err != nil {
		return Role{}, err
	}
	for _, d := range []struct {
		key string
		out *string
	}{{"historyDigest", &r.HistoryDigest}, {"currentBindingDigest", &r.CurrentBindingDigest},
		{"supportDigest", &r.SupportDigest}, {"adoptionDigest", &r.AdoptionDigest}} {
		if *d.out, err = digestValue(f[d.key], "role."+d.key); err != nil {
			return Role{}, err
		}
	}
	if err := r.Validate(); err != nil {
		return Role{}, err
	}
	return r, nil
}

// Validate recomputes every digest and requires the current binding, its
// selected profile, the support's accepted profiles and the adoption's target
// and support to agree.
func (r Role) Validate() error {
	if err := r.History.Validate(HistoryBound); err != nil {
		return err
	}
	if got, err := r.History.Digest(); err != nil || got != r.HistoryDigest {
		return errors.New("role.historyDigest does not match role.history")
	}
	if got, err := r.Support.Digest(); err != nil || got != r.SupportDigest {
		return errors.New("role.supportDigest does not match role.support")
	}
	if got, err := r.Adoption.Digest(); err != nil || got != r.AdoptionDigest {
		return errors.New("role.adoptionDigest does not match role.adoption")
	}
	binding, ok := r.History.Bindings[r.CurrentBindingDigest]
	if !ok || binding.Kind != BindingSelected {
		return errors.New("role current binding must be a selected binding in history")
	}
	selector, err := canonicalSelector(r.History.Selections[*binding.SelectionDigest])
	if err != nil {
		return fmt.Errorf("role current selection: %w", err)
	}
	if !r.Support.Accepts(selector) {
		return fmt.Errorf("role support does not accept profile %s", selector.Profile())
	}
	t := r.Adoption.Target
	switch {
	case t.PolicyDigest != binding.PolicyDigest || t.SelectionDigest != *binding.SelectionDigest ||
		t.BindingDigest != r.CurrentBindingDigest || t.HistoryDigest != r.HistoryDigest:
		return errors.New("role.adoption.target differs from the role's current state")
	case r.Adoption.SupportDigest != r.SupportDigest:
		return errors.New("role.adoption.supportDigest differs from role.supportDigest")
	}
	return nil
}

// PolicyDigest is the canonical policy digest of the current binding.
func (r Role) PolicyDigest() string { return r.History.Bindings[r.CurrentBindingDigest].PolicyDigest }

// CheckActivePolicy requires the owner's active policy to be the one the
// current binding binds.
func (r Role) CheckActivePolicy(activePolicyDigest string) error {
	return r.History.ValidateCurrent(r.CurrentBindingDigest, activePolicyDigest, true)
}

// SourceTuple is the role-bound source tuple of this role under authorityID.
func (r Role) SourceTuple(authorityID string) (SourceTuple, error) {
	support, adoption := r.SupportDigest, r.AdoptionDigest
	t := SourceTuple{Kind: SourceBound, AuthorityID: authorityID, PolicyDigest: r.PolicyDigest(),
		CurrentBindingDigest: r.CurrentBindingDigest, HistoryDigest: r.HistoryDigest,
		SupportDigest: &support, AdoptionDigest: &adoption}
	if err := t.Validate(); err != nil {
		return SourceTuple{}, err
	}
	return t, nil
}

// RoleBinding is the IDs5 roleBinding object.
type RoleBinding struct {
	AuthorityID          string
	CurrentBindingDigest string
	HistoryDigest        string
	SupportDigest        string
	AdoptionDigest       string
}

// ParseRoleBinding strictly decodes a standalone IDs5 roleBinding.
func ParseRoleBinding(raw []byte) (RoleBinding, error) {
	n, err := parseJSON(raw, maxRoleBindingBytes, "roleBinding")
	if err != nil {
		return RoleBinding{}, err
	}
	f, err := fields(n, "roleBinding", "protocol", "authorityId", "currentBindingDigest", "historyDigest",
		"supportDigest", "adoptionDigest")
	if err != nil {
		return RoleBinding{}, err
	}
	if err := exactInt(f["protocol"], "roleBinding.protocol", 1); err != nil {
		return RoleBinding{}, err
	}
	var b RoleBinding
	if b.AuthorityID, err = stringValue(f["authorityId"], "roleBinding.authorityId"); err != nil {
		return RoleBinding{}, err
	}
	if !isAuthorityID(b.AuthorityID) {
		return RoleBinding{}, errors.New("roleBinding.authorityId must be lowercase hex32")
	}
	for _, d := range []struct {
		key string
		out *string
	}{{"currentBindingDigest", &b.CurrentBindingDigest}, {"historyDigest", &b.HistoryDigest},
		{"supportDigest", &b.SupportDigest}, {"adoptionDigest", &b.AdoptionDigest}} {
		if *d.out, err = digestValue(f[d.key], "roleBinding."+d.key); err != nil {
			return RoleBinding{}, err
		}
	}
	return b, nil
}

// Matches requires IDs5 to bind the same authority and role as the journal.
func (b RoleBinding) Matches(authorityID string, r Role) error {
	if b.AuthorityID != authorityID || b.CurrentBindingDigest != r.CurrentBindingDigest ||
		b.HistoryDigest != r.HistoryDigest || b.SupportDigest != r.SupportDigest || b.AdoptionDigest != r.AdoptionDigest {
		return errors.New("IDs5 roleBinding differs from the role it must bind")
	}
	return nil
}

// ValidateRevision applies the §14.5 revision rule: a new hex32 authority,
// an adoption bound to exactly the source, and at least one of policy,
// current binding or support changed. An all-equal revision is a noop and
// creates no generation. The legacy previousDigest!=targetDigest rule is not
// reused here.
func ValidateRevision(source SourceTuple, targetAuthorityID string, target Role) error {
	if err := source.Validate(); err != nil {
		return fmt.Errorf("revision source: %w", err)
	}
	// Role fields are exported, so a caller can hand over an unvalidated
	// value whose missing binding reads as an empty policy digest; validate
	// the whole target first so the comparisons below cannot fail open.
	if err := target.Validate(); err != nil {
		return fmt.Errorf("revision target: %w", err)
	}
	if !isAuthorityID(targetAuthorityID) || targetAuthorityID == source.AuthorityID {
		return errors.New("revision target authorityId must be a new lowercase hex32")
	}
	if !target.Adoption.Source.Equal(source) {
		return errors.New("revision adoption.source differs from the revision source")
	}
	if source.PolicyDigest == target.PolicyDigest() && source.CurrentBindingDigest == target.CurrentBindingDigest &&
		source.SupportDigest != nil && *source.SupportDigest == target.SupportDigest {
		return errors.New("revision changes nothing: noop refused")
	}
	return nil
}
