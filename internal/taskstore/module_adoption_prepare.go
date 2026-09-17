package taskstore

import (
	"context"
	"errors"
	"io/fs"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
)

func prepareModuleIDs(r *os.Root, policy boardpolicy.Policy, journal transitionJournal, binding policyAuthorityBinding) (*moduleIDAdoption, []byte, error) {
	raw, err := boundedSnapshotFile(r, idsFile, maxIDsBytes)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	a := &moduleIDAdoption{Original: []byte{}}
	ledger := idLedger{SchemaVersion: 2, Reserved: []string{}}
	if err == nil {
		ledger, err = decodeIDs(raw)
		if err != nil {
			return nil, nil, err
		}
		info, err := r.Lstat(idsFile)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&^os.ModePerm != 0 {
			return nil, nil, errors.Join(errors.New("unsafe original module ID ledger"), err)
		}
		a.Original, a.OriginalMode = raw, uint32(info.Mode().Perm())
	}
	if ledger.SchemaVersion == 3 && ledger.Namespace != binding.Namespace {
		return nil, nil, errors.New("module adoption cannot replace a shared ID namespace")
	}
	entries, err := listLockedWithPolicy(r, "", policy)
	if err != nil {
		return nil, nil, err
	}
	claims, err := loadClaims(r, entries)
	if err != nil {
		return nil, nil, err
	}
	ledger, err = observedIDsWithRecords(entries, ledger, nil, claims, journal)
	if err != nil {
		return nil, nil, err
	}
	if binding.Scope == "shared" {
		ledger.SchemaVersion, ledger.Namespace = 3, binding.Namespace
	} else {
		ledger.SchemaVersion, ledger.Namespace = 2, ""
		location, err := githistory.LocateBoard(context.Background(), r.Name())
		if err != nil {
			return nil, nil, err
		}
		if location != nil {
			history, err := githistory.Scan(context.Background(), location.Repository, location.Board)
			if err != nil {
				return nil, nil, err
			}
			ledger.Reserved = unionIDs(ledger.Reserved, history.IDs)
		}
	}
	target, err := ledgerBytes(ledger)
	return a, target, err
}
