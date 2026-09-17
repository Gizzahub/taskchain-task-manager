package taskstore

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

const maxModuleActivationBytes = 4 << 20

// Empty Original with mode zero means absence, never an empty ledger file.
// Target bytes and both hashes remain in the enclosing immutable plan.
type moduleIDAdoption struct {
	Original     []byte `json:"original"`
	OriginalMode uint32 `json:"originalMode"`
}

func validateModuleAdoptionShape(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 2 || fields["original"] == nil || fields["originalMode"] == nil || string(fields["original"]) == "null" || string(fields["originalMode"]) == "null" {
		return errors.New("module adoption requires exact original bytes and mode")
	}
	var value moduleIDAdoption
	if err := requireJSONString(fields["original"], "original"); err != nil {
		return err
	}
	return decodeExact(raw, &value)
}

func validateModuleInventory(r *os.Root, policy boardpolicy.Policy, journal transitionJournal, plan policyActivationPlan) error {
	if plan.ModuleAdoption == nil {
		return nil
	}
	target, err := decodeIDs(plan.IDTarget)
	if err != nil {
		return err
	}
	entries, err := listLockedWithPolicy(r, "", policy)
	if err != nil {
		return err
	}
	claims, err := loadClaims(r, entries)
	if err != nil {
		return err
	}
	observed, err := observedIDsWithRecords(entries, target, nil, claims, journal)
	if err != nil {
		return err
	}
	if !containsAllIDs(target.Reserved, observed.Reserved) {
		return errors.New("module adoption target omits frozen inventory IDs")
	}
	return nil
}

func validateModuleIDPlan(plan policyActivationPlan, shared bool) error {
	a := plan.ModuleAdoption
	if a == nil || a.Original == nil || len(a.Original) > maxIDsBytes || a.OriginalMode&^uint32(0777) != 0 || moduleOriginalHash(a.Original) != plan.OriginalIDs {
		return errors.New("invalid original module ID ledger binding")
	}
	// optionalPolicyHash distinguishes nil; the explicit empty byte string also
	// denotes absence for this field, unlike every actual ledger encoding.
	return validateModuleIDLedgers(plan, shared)
}

func moduleOriginalHash(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	return bytesDigest(raw)
}

func validateModuleIDLedgers(plan policyActivationPlan, shared bool) error {
	a := plan.ModuleAdoption
	target, err := decodeIDs(plan.IDTarget)
	if err != nil || bytesDigest(plan.IDTarget) != plan.TargetIDs || (shared && target.SchemaVersion != 3) || (!shared && (target.SchemaVersion != 2 || target.Namespace != "")) {
		return errors.New("invalid module ID target ledger")
	}
	if len(a.Original) == 0 {
		if a.OriginalMode != 0 || plan.OriginalIDs != "" {
			return errors.New("absent original module IDs require absent hash and mode")
		}
		return nil
	}
	original, err := decodeIDs(a.Original)
	if err != nil || (original.SchemaVersion == 3 && original.Namespace != target.Namespace) {
		return errors.New("invalid original module ID namespace")
	}
	if !containsAllIDs(target.Reserved, original.Reserved) {
		return errors.New("module adoption cannot remove reserved IDs")
	}
	return nil
}

func containsAllIDs(have, need []string) bool {
	set := make(map[string]bool, len(have))
	for _, id := range have {
		set[id] = true
	}
	for _, id := range need {
		if !set[id] {
			return false
		}
	}
	return true
}

func validateModuleScopeChange(previous, next boardpolicy.Policy, adopt bool) error {
	old, target := previous.Modules(), next.Modules()
	for _, module := range old {
		if !next.IsModule(module) {
			return errors.New("module removal or rename requires a separate migration")
		}
	}
	if len(target) != len(old) && !adopt {
		return errors.New("module scope expansion requires explicit adopt-modules")
	}
	if adopt && len(target) == 0 {
		return errors.New("adopt-modules requires a declared module scope")
	}
	return nil
}

func moduleSnapshotLedger(r *os.Root, adoption *moduleIDAdoption) (idLedger, fs.FileMode, error) {
	ledger, err := loadIDs(r)
	if errors.Is(err, fs.ErrNotExist) && adoption != nil && len(adoption.Original) == 0 {
		return idLedger{SchemaVersion: 2, Reserved: []string{}}, 0644, nil
	}
	if err != nil {
		return ledger, 0, err
	}
	info, err := r.Lstat(idsFile)
	if err != nil {
		return ledger, 0, err
	}
	if info.Mode()&^os.ModePerm != 0 {
		return ledger, 0, errors.New("unsafe ID ledger mode")
	}
	if adoption != nil {
		want := fs.FileMode(adoption.OriginalMode)
		if len(adoption.Original) == 0 {
			want = 0644
		}
		if info.Mode() != want {
			return ledger, 0, errors.New("module adoption ID ledger mode changed")
		}
	}
	return ledger, info.Mode(), nil
}
