package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

func validatePolicyAuthorityShape(raw json.RawMessage) error {
	if err := validateObjectShape(raw, []string{"authorityId", "scope", "namespace"}, nil); err != nil {
		return err
	}
	var binding policyAuthorityBinding
	if err := decodeExact(raw, &binding); err != nil {
		return err
	}
	return validatePolicyAuthority(binding)
}

func validatePolicyAuthority(binding policyAuthorityBinding) error {
	if !sharedHex32.MatchString(binding.AuthorityID) {
		return errors.New("invalid policy authority ID")
	}
	switch binding.Scope {
	case "local":
		if binding.Namespace != "" {
			return errors.New("local policy authority cannot bind a namespace")
		}
	case "shared":
		if !sharedHex32.MatchString(binding.Namespace) {
			return errors.New("shared policy authority requires a namespace")
		}
	default:
		return errors.New("invalid policy authority scope")
	}
	return nil
}

// Local completion is checked even for a local-only board. Comparing the owner
// path prevents copied tracked receipts from silently enrolling a new worktree.
// Snapshot and HEAD belong to activation recovery, not ongoing admission.
func verifyPolicyActivation(r *os.Root, j transitionJournal) error {
	state, err := loadPolicyActivation(r)
	if errors.Is(err, fs.ErrNotExist) && j.SchemaVersion < 3 {
		return nil
	}
	if err != nil {
		return errors.Join(errors.New("policy activation binding unavailable; restore its original state"), err)
	}
	if state.Phase != "completed" {
		return errors.New("policy activation is pending; explicit policy recovery required")
	}
	if (j.SchemaVersion != 3 && j.SchemaVersion != 4) || j.PolicyAuthority == nil {
		return errors.New("policy activation journal binding missing; restore journal")
	}
	binding := *j.PolicyAuthority
	if err := validatePolicyAuthority(binding); err != nil {
		return err
	}
	if binding.AuthorityID != state.AuthorityID || binding.Scope != state.Scope || binding.Namespace != state.Namespace || j.PolicyDigest != state.Digest {
		return errors.New("policy activation authority mismatch")
	}
	if state.Revision != nil && (j.SchemaVersion != 4 || !bytes.Equal(j.PolicyHistory[state.Revision.PreviousDigest], state.Revision.PreviousCanonical)) {
		return errors.New("policy revision lost its historical journal binding; restore original history")
	}
	owner, err := filepath.EvalSymlinks(r.Name())
	if err != nil {
		return err
	}
	owner, err = filepath.Abs(owner)
	if err != nil {
		return err
	}
	if owner != state.Plan.Root {
		return errors.New("policy activation belongs to another board; explicit join required")
	}
	if _, err := loadIDs(r); err != nil {
		return errors.Join(errors.New("policy activation requires its original ID ledger; restore it"), err)
	}
	return verifyBoardHandle(r, owner)
}

// This is an identity check only. It neither adopts a markerless worktree nor
// validates completed task receipts against the board's current graph.
func (s *sharedSession) verifyPolicyAuthority(r *os.Root, j transitionJournal) error {
	if err := s.verifyStorageBinding(j); err != nil {
		return err
	}
	if err := verifyPolicyActivation(r, j); err != nil {
		return err
	}
	var authority *sharedPolicyAuthority
	if s != nil && s.state != nil {
		authority = s.state.Policy
	}
	if authority == nil {
		if j.PolicyAuthority != nil && j.PolicyAuthority.Scope == "shared" {
			return errors.New("shared policy authority missing; restore common state")
		}
		return nil
	}
	if authority.Phase != "active" {
		return errors.New("shared policy activation is pending; explicit recovery required")
	}
	if j.SchemaVersion == 4 && s.state.PolicyRevisionProtocol != 1 {
		return errors.New("shared policy history lost its permanent revision protocol barrier")
	}
	binding := j.PolicyAuthority
	if (j.SchemaVersion != 3 && j.SchemaVersion != 4) || binding == nil || binding.Scope != "shared" || binding.AuthorityID != authority.AuthorityID || binding.Namespace != s.state.NamespaceID || j.PolicyDigest != authority.Digest {
		return errors.New("board has not joined the shared policy authority; explicit join required")
	}
	state, err := loadPolicyActivation(r)
	if err != nil {
		return err
	}
	if !bytes.Equal(state.Canonical, authority.Canonical) {
		return errors.New("shared policy canonical bytes mismatch")
	}
	ledger, err := loadIDs(r)
	if err != nil {
		return errors.Join(errors.New("shared policy requires its original ID ledger"), err)
	}
	if ledger.SchemaVersion != 3 || ledger.Namespace != binding.Namespace {
		return errors.New("shared policy ID ledger binding mismatch")
	}
	return nil
}
